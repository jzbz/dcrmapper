package crawler

import (
	"net"
	"time"

	"github.com/decred/dcrd/wire"
)

type Node struct {
	Services        wire.ServiceFlag
	LastAttempt     time.Time
	LastSuccess     time.Time
	ProtocolVersion uint32
	IP              net.IP
	// Onion is the v3 .onion hostname for nodes reachable only over Tor. It is
	// empty for clearnet nodes, which are identified by IP instead. A node is
	// one or the other, never both.
	Onion     string
	UserAgent string
	GeoData   *GeoData

	// good tracks whether the node is currently in the good set. It mirrors
	// membership of Manager.goodNodes so that membership tests are O(1) rather
	// than requiring a linear scan. Being unexported it is never serialized; it
	// is recomputed from LastSuccess on startup.
	good bool
}

// IsOnion reports whether the node is a Tor v3 onion service (reachable only
// through a SOCKS proxy) rather than a clearnet IP node.
func (n *Node) IsOnion() bool { return n.Onion != "" }

// Address returns the node's canonical address: its .onion hostname for onion
// nodes, otherwise its IP. This is also the key under which the node is stored,
// so it is stable for the life of the node. Exposed to templates for display
// and to build the /node link.
func (n *Node) Address() string {
	if n.Onion != "" {
		return n.Onion
	}
	return n.IP.String()
}

type GeoData struct {
	City      string
	Region    string
	Country   string
	Continent string
	Org       string
	ISP       string
	AS        string
	ASName    string
	Lat       float32
	Lon       float32
}
