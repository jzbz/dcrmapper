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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/decred/dcrd/chaincfg/v3"
	"github.com/decred/dcrd/peer/v3"
	"github.com/decred/dcrd/wire"
	"github.com/decred/go-socks/socks"
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

	// onionTimeout is the per-action timeout for onion peers. Tor circuit
	// construction and rendezvous routinely take far longer than a direct TCP
	// dial, so onion probes get a more generous budget than clearnet ones.
	onionTimeout = time.Second * 30

	// crawlInterval is how often the full crawl + geolocation cycle runs.
	crawlInterval = time.Minute * 5

	// maxConcurrentChecks caps the number of clearnet peers contacted
	// simultaneously during a crawl.
	maxConcurrentChecks = 1000

	// maxConcurrentOnion caps simultaneous onion probes. They all share a
	// single Tor instance, which cannot build anywhere near maxConcurrentChecks
	// circuits at once, so onion dials use their own much smaller budget.
	maxConcurrentOnion = 8
)

type Manager struct {
	mtx sync.RWMutex

	netParams *chaincfg.Params
	nodes     map[string]*Node
	goodNodes []string
	peersFile string

	// proxyAddr is the SOCKS5 proxy address (e.g. arti/tor) used to reach onion
	// peers, and proxy is the dialer built from it. Both are nil/empty when no
	// proxy was configured, in which case onion nodes are never seeded or
	// probed.
	proxyAddr string
	proxy     *socks.Proxy

	// snap holds an immutable, point-in-time view of the crawl results,
	// rebuilt once per crawl cycle and published atomically. HTTP handlers read
	// it without locking and without racing the crawler's writes to Node fields.
	snap atomic.Pointer[snapshot]
}

// snapshot is what the web handlers serve between crawl cycles. Its Node
// pointers reference copies, never live map entries, so readers never observe a
// half-updated node.
type snapshot struct {
	summary Summary
	good    []*Node // good nodes, copied and sorted by IP for stable pagination
}

// New builds a crawl Manager. proxyAddr, when non-empty, is the SOCKS5 proxy
// (arti or tor) used to reach onion peers; onionSeeds are bootstrap v3 .onion
// hosts to probe through it. Onion seeds are ignored when no proxy is given,
// since onion addresses are unreachable without one.
func New(homeDir string, params *chaincfg.Params, seedPeer []string, proxyAddr string, onionSeeds []string) (*Manager, error) {
	dataDir := filepath.Join(homeDir, params.Name)
	err := os.MkdirAll(dataDir, 0700)
	if err != nil {
		return nil, err
	}
	amgr := &Manager{
		netParams: params,
		nodes:     make(map[string]*Node),
		peersFile: filepath.Join(dataDir, peersFilename),
		proxyAddr: proxyAddr,
	}

	if proxyAddr != "" {
		// TorIsolation gives each probe its own circuit so a single slow or
		// hostile onion peer cannot stall or correlate the others.
		amgr.proxy = &socks.Proxy{Addr: proxyAddr, TorIsolation: true}
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

	if amgr.proxy != nil {
		if added := amgr.AddOnionAddresses(onionSeeds); added > 0 {
			log.Printf("Seeded %d onion addresses", added)
		}
	} else if len(onionSeeds) > 0 {
		log.Printf("Ignoring %d onion seed(s): no --proxy configured", len(onionSeeds))
	}

	// Initialize good list.
	now := time.Now()
	for k, node := range amgr.nodes {
		if now.Sub(node.LastSuccess) < staleTimeout {
			node.good = true
			amgr.goodNodes = append(amgr.goodNodes, k)
		}
	}

	log.Printf("Initialized with %d nodes, %d good", len(amgr.nodes), len(amgr.goodNodes))

	amgr.rebuildSnapshot()

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
			m.rebuildSnapshot()

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

func (m *Manager) testPeer(ctx context.Context, t target) {
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

	// Onion dials go through the SOCKS proxy. Setting Proxy stops the peer from
	// advertising a routable local address, and HostToNetAddress lets it accept
	// the non-IP .onion host while building the version message.
	if t.onion {
		config.Proxy = m.proxyAddr
		config.HostToNetAddress = onionHostToNetAddress
	}

	timeout := nodeTimeout
	if t.onion {
		timeout = onionTimeout
	}

	p, err := peer.NewOutboundPeer(&config, t.dialAddr)
	if err != nil {
		m.Bad(t.key, "outbound peer error", err)
		return
	}

	ctxTimeout, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := m.dial(ctxTimeout, t, p.Addr())
	if err != nil {
		m.Bad(t.key, "dial timeout error", err)
		return
	}
	p.AssociateConnection(conn)
	defer p.Disconnect()

	// Wait for the verack message.
	select {
	case <-verack:
		m.Good(t.key, p)
		// Ask peer for some addresses.
		p.QueueMessage(wire.NewMsgGetAddr(), nil)
	case <-time.After(timeout):
		m.Bad(t.key, "verack timeout", nil)
		return
	case <-ctx.Done():
		// App shutting down.
		return
	}

	select {
	case <-onaddr:
	case <-time.After(timeout):
	case <-ctx.Done():
	}
}

// dial opens a connection to the target, routing onion targets through the
// SOCKS proxy and clearnet targets directly.
func (m *Manager) dial(ctx context.Context, t target, addr string) (net.Conn, error) {
	if t.onion {
		return m.proxy.DialContext(ctx, "tcp", addr)
	}
	var dialer net.Dialer
	return dialer.DialContext(ctx, "tcp", addr)
}

func (m *Manager) checkNodes(ctx context.Context) {
	for {
		targets := m.staleTargets()
		if len(targets) == 0 {
			log.Println("No stale addresses")
			return
		}

		log.Printf("Checking %d stale addresses", len(targets))

		// Test peers concurrently. Clearnet and onion probes draw on separate
		// semaphores so the small onion budget (one shared Tor instance) never
		// throttles the much larger clearnet crawl, and vice versa.
		clearSem := make(chan struct{}, maxConcurrentChecks)
		onionSem := make(chan struct{}, maxConcurrentOnion)
		var wg sync.WaitGroup
		for _, t := range targets {
			sem := clearSem
			if t.onion {
				sem = onionSem
			}

			select {
			case <-ctx.Done():
				wg.Wait()
				return
			case sem <- struct{}{}:
			}

			wg.Add(1)
			go func(t target, sem chan struct{}) {
				defer wg.Done()
				defer func() { <-sem }()
				m.testPeer(ctx, t)
			}(t, sem)
		}
		wg.Wait()

		m.mtx.RLock()
		total, good := len(m.nodes), len(m.goodNodes)
		m.mtx.RUnlock()
		log.Printf("Done checking %d addresses, %d good", total, good)
	}
}

func (m *Manager) AddAddresses(addrs []net.IP) int {
	// Filter and stringify outside the lock so the critical section is just the
	// map inserts. Remote peers can send up to ~1000 addresses at once, and many
	// peers call this concurrently during a crawl.
	type candidate struct {
		ip  net.IP
		key string
	}
	candidates := make([]candidate, 0, len(addrs))
	for _, addr := range addrs {
		if isRoutable(addr) {
			candidates = append(candidates, candidate{ip: addr, key: addr.String()})
		}
	}
	if len(candidates) == 0 {
		return 0
	}

	var count int
	m.mtx.Lock()
	for _, c := range candidates {
		if _, exists := m.nodes[c.key]; exists {
			continue
		}
		m.nodes[c.key] = &Node{IP: c.ip}
		count++
	}
	m.mtx.Unlock()

	return count
}

// target is a single node to probe in a crawl cycle. It is resolved from the
// node map under lock so testPeer never has to touch shared state to learn how
// to reach a node.
type target struct {
	key      string // map key, i.e. node.Address()
	dialAddr string // host:port to dial
	onion    bool   // reach via the SOCKS proxy rather than a direct dial
}

// staleTargets returns the nodes that need to be tested again, with the dial
// information needed to reach each one. Onion nodes are skipped when no proxy is
// configured, since they are unreachable without one.
func (m *Manager) staleTargets() []target {
	now := time.Now()

	m.mtx.RLock()
	defer m.mtx.RUnlock()

	targets := make([]target, 0, len(m.nodes))
	for key, node := range m.nodes {
		if now.Sub(node.LastAttempt) < staleTimeout {
			continue
		}

		if node.IsOnion() {
			if m.proxy == nil {
				continue
			}
			targets = append(targets, target{
				key:      key,
				dialAddr: net.JoinHostPort(node.Onion, m.netParams.DefaultPort),
				onion:    true,
			})
			continue
		}

		targets = append(targets, target{
			key:      key,
			dialAddr: net.JoinHostPort(node.IP.String(), m.netParams.DefaultPort),
		})
	}

	return targets
}

// AddOnionAddresses seeds v3 .onion hosts into the node map, keyed by hostname.
// A trailing :port is accepted but ignored; like clearnet nodes, onion nodes
// are dialed on the network's default port. Malformed addresses are skipped. It
// returns the number of new nodes added.
func (m *Manager) AddOnionAddresses(hosts []string) int {
	type candidate struct{ host string }
	candidates := make([]candidate, 0, len(hosts))
	for _, h := range hosts {
		host := strings.ToLower(strings.TrimSpace(h))
		if host == "" {
			continue
		}
		// Tolerate an explicit port; the dial always uses the default port.
		if hostOnly, _, err := net.SplitHostPort(host); err == nil {
			host = hostOnly
		}
		if !isOnionV3(host) {
			log.Printf("Skipping invalid onion seed %q", h)
			continue
		}
		candidates = append(candidates, candidate{host: host})
	}
	if len(candidates) == 0 {
		return 0
	}

	var count int
	m.mtx.Lock()
	for _, c := range candidates {
		if _, exists := m.nodes[c.host]; exists {
			continue
		}
		m.nodes[c.host] = &Node{Onion: c.host}
		count++
	}
	m.mtx.Unlock()

	return count
}

// Bad records a failed probe of the node stored under key (its Address).
func (m *Manager) Bad(key, reason string, err error) {
	m.mtx.Lock()
	defer m.mtx.Unlock()

	node, ok := m.nodes[key]
	if !ok {
		return
	}
	node.LastAttempt = time.Now()

	// Only nodes currently in the good set need to be removed from it.
	if !node.good {
		return
	}
	node.good = false

	if i := slices.Index(m.goodNodes, key); i != -1 {
		m.goodNodes[i] = m.goodNodes[len(m.goodNodes)-1]
		m.goodNodes = m.goodNodes[:len(m.goodNodes)-1]
	}
	log.Printf("Removed bad peer, reason: %q, addr %s, err: %v\n", reason, key, err)
}

// Good records a successful handshake with the node stored under key (its
// Address). key, rather than the peer's advertised address, is authoritative
// because onion peers have only a placeholder NetAddress.
func (m *Manager) Good(key string, p *peer.Peer) {
	m.mtx.Lock()
	defer m.mtx.Unlock()

	node, exists := m.nodes[key]
	if !exists {
		// Should be impossible since we only dial addresses from the map, but a
		// panic here would take down the entire service from a background
		// goroutine.
		log.Printf("Good called for unknown peer %s", key)
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
		m.goodNodes = append(m.goodNodes, key)
	}
}

// AllGoodNodes returns the cached snapshot's good nodes. The slice and its
// elements are immutable; callers must not mutate them.
func (m *Manager) AllGoodNodes() []*Node {
	if s := m.snap.Load(); s != nil {
		return s.good
	}
	return nil
}

// GetNode returns a copy of the live node for ip (so the caller can read it
// without holding the lock or racing the crawler), whether it is currently
// good, and whether it exists at all.
func (m *Manager) GetNode(ip string) (*Node, bool, bool) {
	m.mtx.RLock()
	defer m.mtx.RUnlock()

	node, ok := m.nodes[ip]
	if !ok {
		return nil, false, false
	}

	cp := *node
	return &cp, cp.good, true
}

// rebuildSnapshot recomputes the cached summary and good-node list and publishes
// them atomically. Called once per crawl cycle so HTTP requests never trigger
// the O(n log n) summary build (and never hold the read lock during it).
func (m *Manager) rebuildSnapshot() {
	// Hold the lock only long enough to copy the good nodes; the (more
	// expensive) sort and summary build then run on the immutable copies
	// without blocking the crawler.
	m.mtx.RLock()
	good := make([]*Node, 0, len(m.goodNodes))
	for _, ip := range m.goodNodes {
		cp := *m.nodes[ip] // copy so the snapshot is immutable
		good = append(good, &cp)
	}
	m.mtx.RUnlock()

	// Stable order independent of good-set churn, so pagination is consistent
	// between requests. Sort by Address so onion nodes (which have no IP) order
	// deterministically alongside clearnet ones.
	slices.SortFunc(good, func(a, b *Node) int {
		return strings.Compare(a.Address(), b.Address())
	})

	m.snap.Store(&snapshot{summary: computeSummary(good), good: good})
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
	Onion         int
	GoodCount     int
	UserAgents    []Count
	AS            []Count
	CountryCounts []Count
}

// GetSummary returns the cached summary from the most recent crawl cycle.
func (m *Manager) GetSummary() Summary {
	if s := m.snap.Load(); s != nil {
		return s.summary
	}
	return Summary{}
}

// byCount orders Counts by count descending, breaking ties by value so the
// displayed order is deterministic across crawl cycles.
func byCount(a, b Count) int {
	if c := cmp.Compare(b.Count, a.Count); c != 0 {
		return c
	}
	return cmp.Compare(a.Value, b.Value)
}

// computeSummary builds the summary from an immutable slice of good nodes. It
// takes no lock and does not touch shared state, so the (potentially expensive)
// aggregation and sorting run outside the crawler's critical section.
func computeSummary(good []*Node) Summary {
	total := len(good)

	var ip4, ip6, onion int
	ua := make(map[string]int)
	as := make(map[string]int)
	country := make(map[string]int)

	for _, node := range good {
		switch {
		case node.IsOnion():
			onion++
		case node.IP.To4() != nil:
			ip4++
		default:
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

	sortedUA := make([]Count, 0, len(ua))
	for k, v := range ua {
		sortedUA = append(sortedUA, Count{Value: k, Count: v})
	}
	slices.SortFunc(sortedUA, byCount)

	sortedAS := make([]Count, 0, len(as))
	for k, v := range as {
		sortedAS = append(sortedAS, Count{Value: k, Count: v, AbsPercent: absPercent(v)})
	}
	slices.SortFunc(sortedAS, byCount)

	// RelPercent is relative to the most populous country so the largest bar
	// fills its row.
	var maxCountries int
	for _, v := range country {
		maxCountries = max(maxCountries, v)
	}
	sortedCountry := make([]Count, 0, len(country))
	for k, v := range country {
		c := Count{Value: k, Count: v, AbsPercent: absPercent(v)}
		if maxCountries > 0 {
			c.RelPercent = 100 * v / maxCountries
		}
		sortedCountry = append(sortedCountry, c)
	}
	slices.SortFunc(sortedCountry, byCount)

	return Summary{
		IP4:           ip4,
		IP6:           ip6,
		Onion:         onion,
		GoodCount:     total,
		UserAgents:    sortedUA,
		AS:            sortedAS,
		CountryCounts: sortedCountry,
	}
}

func (m *Manager) PageOfNodes(first, last int) (int, []*Node) {
	var good []*Node
	if s := m.snap.Load(); s != nil {
		good = s.good
	}
	count := len(good)

	// Clamp the requested range so pages beyond the end return an empty
	// slice rather than panicking.
	first = min(max(first, 0), count)
	last = min(max(last, first), count)

	return count, good[first:last]
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
	// Marshal in memory under the lock, then do the slow disk I/O (write +
	// rename) without it, so persistence never stalls the crawler on disk
	// latency.
	m.mtx.RLock()
	data, err := json.Marshal(m.nodes)
	count := len(m.nodes)
	m.mtx.RUnlock()
	if err != nil {
		log.Printf("Failed to encode peers: %v", err)
		return
	}

	// Write a temporary file and then move it into place atomically.
	tmpfile := m.peersFile + ".new"
	if err := os.WriteFile(tmpfile, data, 0600); err != nil {
		log.Printf("Error writing file %s: %v", tmpfile, err)
		return
	}
	if err := os.Rename(tmpfile, m.peersFile); err != nil {
		log.Printf("Error renaming file %s: %v", m.peersFile, err)
		return
	}

	log.Printf("%d nodes saved to %s", count, m.peersFile)
}
