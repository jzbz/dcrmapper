package crawler

import (
	"net"
	"strings"
	"testing"

	"github.com/decred/dcrd/chaincfg/v3"
	"github.com/decred/go-socks/socks"
)

// onionHost builds a shape-valid v3 .onion host from a single repeated base32
// character. isOnionV3 checks shape, not the embedded checksum, so this is
// sufficient for tests.
func onionHost(c byte) string {
	return strings.Repeat(string(c), onionV3HostLen) + ".onion"
}

func TestIsOnionV3(t *testing.T) {
	valid := onionHost('a')
	tests := []struct {
		name string
		host string
		want bool
	}{
		{"valid v3", valid, true},
		{"uppercase normalised", strings.ToUpper(valid), true},
		{"v2 length", strings.Repeat("a", 16) + ".onion", false},
		{"too short", strings.Repeat("a", 55) + ".onion", false},
		{"too long", strings.Repeat("a", 57) + ".onion", false},
		{"no suffix", strings.Repeat("a", 56), false},
		{"bad base32 char (1)", strings.Repeat("1", 56) + ".onion", false},
		{"bad base32 char (0)", strings.Repeat("0", 56) + ".onion", false},
		{"empty", "", false},
		{"plain ip", "1.2.3.4", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isOnionV3(tc.host); got != tc.want {
				t.Errorf("isOnionV3(%q) = %v, want %v", tc.host, got, tc.want)
			}
		})
	}
}

func TestAddOnionAddresses(t *testing.T) {
	m := &Manager{nodes: make(map[string]*Node)}
	host := onionHost('a')

	added := m.AddOnionAddresses([]string{
		host,                         // valid
		host + ":9108",               // same host with port -> dedup to host
		strings.ToUpper(host),        // same host, different case -> dedup
		"  " + onionHost('b') + "  ", // valid, needs trimming
		"not-an-onion.com",           // invalid, skipped
		"",                           // empty, skipped
	})

	if added != 2 {
		t.Fatalf("added = %d, want 2", added)
	}

	node, ok := m.nodes[host]
	if !ok {
		t.Fatalf("node %q not stored", host)
	}
	if !node.IsOnion() || node.Onion != host {
		t.Errorf("node.Onion = %q, IsOnion = %v; want %q, true", node.Onion, node.IsOnion(), host)
	}
	if node.Address() != host {
		t.Errorf("node.Address() = %q, want %q", node.Address(), host)
	}

	// Re-adding an existing host is a no-op.
	if again := m.AddOnionAddresses([]string{host}); again != 0 {
		t.Errorf("re-add count = %d, want 0", again)
	}
}

func TestStaleTargetsOnion(t *testing.T) {
	host := onionHost('a')

	// Without a proxy, onion nodes are unreachable and must be skipped.
	noProxy := &Manager{nodes: make(map[string]*Node), netParams: chaincfg.MainNetParams()}
	noProxy.nodes[host] = &Node{Onion: host}
	if got := noProxy.staleTargets(); len(got) != 0 {
		t.Fatalf("staleTargets without proxy = %d targets, want 0", len(got))
	}

	// With a proxy, the onion node yields an onion target dialed on the default
	// port.
	withProxy := &Manager{
		nodes:     map[string]*Node{host: {Onion: host}},
		netParams: chaincfg.MainNetParams(),
		proxy:     &socks.Proxy{Addr: "127.0.0.1:9150"},
	}
	got := withProxy.staleTargets()
	if len(got) != 1 {
		t.Fatalf("staleTargets with proxy = %d targets, want 1", len(got))
	}
	tgt := got[0]
	wantDial := net.JoinHostPort(host, chaincfg.MainNetParams().DefaultPort)
	if !tgt.onion || tgt.key != host || tgt.dialAddr != wantDial {
		t.Errorf("target = %+v, want {key:%q dialAddr:%q onion:true}", tgt, host, wantDial)
	}
}

func TestSummaryCountsOnion(t *testing.T) {
	m := newTestManager([]*Node{
		geoNode("8.8.8.8", "United States", "Google"),
		{IP: net.ParseIP("2606:4700::1")},
		{Onion: onionHost('a')},
		{Onion: onionHost('b')},
	})
	s := m.GetSummary()

	if s.Onion != 2 {
		t.Errorf("Onion = %d, want 2", s.Onion)
	}
	if s.IP4 != 1 || s.IP6 != 1 {
		t.Errorf("IP4/IP6 = %d/%d, want 1/1", s.IP4, s.IP6)
	}
	if s.GoodCount != 4 {
		t.Errorf("GoodCount = %d, want 4", s.GoodCount)
	}
}
