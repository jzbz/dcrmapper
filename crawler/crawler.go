package crawler

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/decred/dcrd/chaincfg/v3"
	"github.com/decred/dcrd/peer/v3"
	"github.com/decred/dcrd/wire"
)

const (
	// staleTimeout is the time in which a host is considered stale.
	staleTimeout = time.Minute * 15

	// dumpAddressInterval is the interval used to dump the address cache to
	// disk for future use.
	dumpAddressInterval = time.Minute * 30

	// peersFilename is the name of the file.
	peersFilename = "nodes.json"

	// nodeTimeout is the time to wait for each peer action - dial, verack and
	// getaddr.
	nodeTimeout = time.Second * 10

	// crawlInterval is how often the full crawl + geolocation cycle runs.
	crawlInterval = time.Minute * 5

	// maxConcurrentChecks caps the number of peers contacted simultaneously
	// during a crawl.
	maxConcurrentChecks = 1000
)

type Manager struct {
	mtx sync.RWMutex

	netParams *chaincfg.Params
	nodes     map[string]*Node
	goodNodes []string
	peersFile string
}

func New(homeDir string, params *chaincfg.Params, seedPeer []string) (*Manager, error) {
	dataDir := filepath.Join(homeDir, params.Name)
	err := os.MkdirAll(dataDir, 0700)
	if err != nil {
		return nil, err
	}
	amgr := &Manager{
		netParams: params,
		nodes:     make(map[string]*Node),
		peersFile: filepath.Join(dataDir, peersFilename),
	}

	var seedIPs []net.IP
	for _, s := range seedPeer {
		seedIPs = append(seedIPs, net.ParseIP(s))
	}

	err = amgr.deserializePeers()
	if err != nil {
		log.Printf("Failed to parse file %s: %v", amgr.peersFile, err)
		// if it is invalid we nuke the old one unconditionally.
		err = os.Remove(amgr.peersFile)
		if err != nil {
			log.Printf("Failed to remove corrupt peers file %s: %v",
				amgr.peersFile, err)
		}
	}

	amgr.AddAddresses(seedIPs)

	// Initialize good list.
	now := time.Now()
	for k, node := range amgr.nodes {
		if now.Sub(node.LastSuccess) < staleTimeout {
			node.good = true
			amgr.goodNodes = append(amgr.goodNodes, k)
		}
	}

	log.Printf("Initialized with %d nodes, %d good", len(amgr.nodes), len(amgr.goodNodes))

	return amgr, nil
}

func (m *Manager) Start(ctx context.Context, shutdownWg *sync.WaitGroup) {
	shutdownWg.Add(2)

	// Crawl loop. The first crawl runs immediately and in the background so the
	// web server can start serving right away.
	go func() {
		defer shutdownWg.Done()
		ticker := time.NewTicker(crawlInterval)
		defer ticker.Stop()
		for {
			m.checkNodes(ctx)
			m.geoIP(ctx)

			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()

	// Persistence loop. Periodically dumps the address cache to disk and does a
	// final save on shutdown.
	go func() {
		defer shutdownWg.Done()
		ticker := time.NewTicker(dumpAddressInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				m.savePeers()
			case <-ctx.Done():
				m.savePeers()
				return
			}
		}
	}()
}

func (m *Manager) testPeer(ctx context.Context, ip string) {
	onaddr := make(chan struct{}, 1)
	verack := make(chan struct{}, 1)

	config := peer.Config{
		UserAgentName:    "decred-mapper",
		UserAgentVersion: "0.0.1",
		Net:              m.netParams.Net,
		DisableRelayTx:   true,

		Listeners: peer.MessageListeners{
			OnAddr: func(p *peer.Peer, msg *wire.MsgAddr) {
				n := make([]net.IP, 0, len(msg.AddrList))
				for _, addr := range msg.AddrList {
					n = append(n, addr.IP)
				}
				added := m.AddAddresses(n)
				if added > 0 {
					log.Printf("Received %v new addresses from peer %s",
						added, p.Addr())
				}

				// Non-blocking: peers may send multiple addr messages, and a
				// blocked send here would wedge the peer's message-handling
				// goroutine forever once nothing is receiving.
				select {
				case onaddr <- struct{}{}:
				default:
				}
			},
			OnVerAck: func(_ *peer.Peer, _ *wire.MsgVerAck) {
				select {
				case verack <- struct{}{}:
				default:
				}
			},
		},
	}

	host := net.JoinHostPort(ip, m.netParams.DefaultPort)
	p, err := peer.NewOutboundPeer(&config, host)
	if err != nil {
		m.Bad(ip, "outbound peer error", err)
		return
	}

	ctxTimeout, cancel := context.WithTimeout(ctx, nodeTimeout)
	defer cancel()
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctxTimeout, "tcp", p.Addr())
	if err != nil {
		m.Bad(ip, "dial timeout error", err)
		return
	}
	p.AssociateConnection(conn)
	defer p.Disconnect()

	// Wait for the verack message.
	select {
	case <-verack:
		m.Good(p)
		// Ask peer for some addresses.
		p.QueueMessage(wire.NewMsgGetAddr(), nil)
	case <-time.After(nodeTimeout):
		m.Bad(ip, "verack timeout", nil)
		return
	case <-ctx.Done():
		// App shutting down.
		return
	}

	select {
	case <-onaddr:
	case <-time.After(nodeTimeout):
	case <-ctx.Done():
	}
}

func (m *Manager) checkNodes(ctx context.Context) {
	for {
		ips := m.StaleAddresses()
		if len(ips) == 0 {
			log.Println("No stale addresses")
			return
		}

		log.Printf("Checking %d stale addresses", len(ips))

		// Test peers concurrently, capped by a semaphore.
		sem := make(chan struct{}, maxConcurrentChecks)
		var wg sync.WaitGroup
		for _, ip := range ips {
			select {
			case <-ctx.Done():
				wg.Wait()
				return
			case sem <- struct{}{}:
			}

			wg.Add(1)
			go func(ip string) {
				defer wg.Done()
				defer func() { <-sem }()
				m.testPeer(ctx, ip)
			}(ip)
		}
		wg.Wait()

		m.mtx.RLock()
		total, good := len(m.nodes), len(m.goodNodes)
		m.mtx.RUnlock()
		log.Printf("Done checking %d addresses, %d good", total, good)
	}
}

func (m *Manager) AddAddresses(addrs []net.IP) int {
	var count int

	m.mtx.Lock()
	for _, addr := range addrs {
		if !isRoutable(addr) {
			continue
		}
		addrStr := addr.String()

		_, exists := m.nodes[addrStr]
		if exists {
			continue
		}
		node := Node{
			IP: addr,
		}
		m.nodes[addrStr] = &node

		count++
	}
	m.mtx.Unlock()

	return count
}

// StaleAddresses returns IPs that need to be tested again.
func (m *Manager) StaleAddresses() []string {
	now := time.Now()

	m.mtx.RLock()
	addrs := make([]string, 0, len(m.nodes))
	for _, node := range m.nodes {
		if now.Sub(node.LastAttempt) < staleTimeout {
			continue
		}

		addrs = append(addrs, node.IP.String())
	}
	m.mtx.RUnlock()

	return addrs
}

func (m *Manager) Bad(ip, reason string, err error) {
	m.mtx.Lock()
	defer m.mtx.Unlock()

	node, ok := m.nodes[ip]
	if !ok {
		return
	}
	node.LastAttempt = time.Now()

	// Only nodes currently in the good set need to be removed from it.
	if !node.good {
		return
	}
	node.good = false

	if i := slices.Index(m.goodNodes, ip); i != -1 {
		m.goodNodes[i] = m.goodNodes[len(m.goodNodes)-1]
		m.goodNodes = m.goodNodes[:len(m.goodNodes)-1]
	}
	log.Printf("Removed bad peer, reason: %q, IP %s, err: %v\n", reason, ip, err)
}

func (m *Manager) Good(p *peer.Peer) {
	m.mtx.Lock()
	defer m.mtx.Unlock()

	peerIP := p.NA().IP.String()

	node, exists := m.nodes[peerIP]
	if !exists {
		// Should be impossible since we only dial IPs from the map, but a
		// panic here would take down the entire service from a background
		// goroutine.
		log.Printf("Good called for unknown peer %s", peerIP)
		return
	}

	now := time.Now()
	node.ProtocolVersion = p.ProtocolVersion()
	node.Services = p.Services()
	node.UserAgent = p.UserAgent()
	node.LastSuccess = now
	node.LastAttempt = now

	// Add to the good set if not already a member.
	if !node.good {
		node.good = true
		m.goodNodes = append(m.goodNodes, peerIP)
	}
}

func (m *Manager) AllGoodNodes() []*Node {
	m.mtx.RLock()
	defer m.mtx.RUnlock()
	goodNodes := make([]*Node, 0, len(m.goodNodes))
	for _, ip := range m.goodNodes {
		goodNodes = append(goodNodes, m.nodes[ip])
	}
	return goodNodes
}

func (m *Manager) GetNode(ip string) (*Node, bool, bool) {
	m.mtx.RLock()
	defer m.mtx.RUnlock()

	node, ok := m.nodes[ip]
	if !ok {
		return nil, false, false
	}

	return node, node.good, true
}

type Count struct {
	Value      string
	Count      int
	AbsPercent int
	RelPercent int
}

type Summary struct {
	IP4           int
	IP6           int
	GoodCount     int
	UserAgents    []Count
	AS            []Count
	CountryCounts []Count
}

func (m *Manager) GetSummary() Summary {
	m.mtx.RLock()
	defer m.mtx.RUnlock()

	total := len(m.goodNodes)

	var ip4, ip6 int
	ua := make(map[string]int)
	as := make(map[string]int)
	country := make(map[string]int)

	for _, ip := range m.goodNodes {
		node := m.nodes[ip]
		if node.IP.To4() != nil {
			ip4++
		} else {
			ip6++
		}

		if node.GeoData != nil {
			country[node.GeoData.Country]++
			as[node.GeoData.ASName]++
		}

		ua[node.UserAgent]++
	}

	// absPercent returns v as a percentage of the total good nodes, guarding
	// against division by zero when there are none.
	absPercent := func(v int) int {
		if total == 0 {
			return 0
		}
		return 100 * v / total
	}

	// Sort UserAgents by count, descending.
	sortedUA := make([]Count, 0, len(ua))
	for k, v := range ua {
		sortedUA = append(sortedUA, Count{Value: k, Count: v})
	}
	slices.SortFunc(sortedUA, func(a, b Count) int { return cmp.Compare(b.Count, a.Count) })

	// Sort AS by count, descending.
	sortedAS := make([]Count, 0, len(as))
	for k, v := range as {
		sortedAS = append(sortedAS, Count{
			Value:      k,
			Count:      v,
			AbsPercent: absPercent(v),
		})
	}
	slices.SortFunc(sortedAS, func(a, b Count) int { return cmp.Compare(b.Count, a.Count) })

	// Sort Countries by count, descending. RelPercent is relative to the most
	// populous country so the largest bar fills the row.
	var maxCountries int
	for _, v := range country {
		maxCountries = max(maxCountries, v)
	}
	sortedCountry := make([]Count, 0, len(country))
	for k, v := range country {
		c := Count{
			Value:      k,
			Count:      v,
			AbsPercent: absPercent(v),
		}
		if maxCountries > 0 {
			c.RelPercent = 100 * v / maxCountries
		}
		sortedCountry = append(sortedCountry, c)
	}
	slices.SortFunc(sortedCountry, func(a, b Count) int { return cmp.Compare(b.Count, a.Count) })

	return Summary{
		IP4:           ip4,
		IP6:           ip6,
		GoodCount:     total,
		UserAgents:    sortedUA,
		AS:            sortedAS,
		CountryCounts: sortedCountry,
	}
}

func (m *Manager) PageOfNodes(first, last int) (int, []*Node) {
	m.mtx.RLock()
	defer m.mtx.RUnlock()

	count := len(m.goodNodes)

	// Clamp the requested range so pages beyond the end return an empty
	// slice rather than panicking.
	first = min(max(first, 0), count)
	last = min(max(last, first), count)

	keys := m.goodNodes[first:last]
	toReturn := make([]*Node, 0, len(keys))
	for _, key := range keys {
		toReturn = append(toReturn, m.nodes[key])
	}

	return count, toReturn
}

func (m *Manager) deserializePeers() error {
	filePath := m.peersFile
	_, err := os.Stat(filePath)
	if os.IsNotExist(err) {
		return nil
	}
	r, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("%s error opening file: %w", filePath, err)
	}
	defer r.Close()

	var nodes map[string]*Node
	dec := json.NewDecoder(r)
	err = dec.Decode(&nodes)
	if err != nil {
		return fmt.Errorf("error reading %s: %w", filePath, err)
	}

	l := len(nodes)

	m.mtx.Lock()
	m.nodes = nodes
	m.mtx.Unlock()

	log.Printf("%d nodes loaded from %s", l, filePath)
	return nil
}

func (m *Manager) savePeers() {
	m.mtx.RLock()
	defer m.mtx.RUnlock()

	// Write temporary peers file and then move it into place.
	tmpfile := m.peersFile + ".new"
	w, err := os.Create(tmpfile)
	if err != nil {
		log.Printf("Error opening file %s: %v", tmpfile, err)
		return
	}
	enc := json.NewEncoder(w)
	if err := enc.Encode(&m.nodes); err != nil {
		log.Printf("Failed to encode file %s: %v", tmpfile, err)
		return
	}
	if err := w.Close(); err != nil {
		log.Printf("Error closing file %s: %v", tmpfile, err)
		return
	}
	if err := os.Rename(tmpfile, m.peersFile); err != nil {
		log.Printf("Error writing file %s: %v", m.peersFile, err)
		return
	}

	log.Printf("%d nodes saved to %s", len(m.nodes), m.peersFile)
}
