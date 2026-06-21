package crawler

import (
	"net"
	"testing"
)

// newTestManager builds a Manager whose good set is the given nodes and
// publishes the snapshot, as a crawl cycle would.
func newTestManager(good []*Node) *Manager {
	m := &Manager{nodes: make(map[string]*Node)}
	for _, n := range good {
		n.good = true
		key := n.Address()
		m.nodes[key] = n
		m.goodNodes = append(m.goodNodes, key)
	}
	m.rebuildSnapshot()
	return m
}

func geoNode(ip, country, asName string) *Node {
	return &Node{
		IP:      net.ParseIP(ip),
		GeoData: &GeoData{Country: country, ASName: asName},
	}
}

func TestGetSummaryEmpty(t *testing.T) {
	// A manager with no good nodes must not panic (no divide-by-zero) and
	// returns a zero-value summary.
	m := newTestManager(nil)
	s := m.GetSummary()
	if s.GoodCount != 0 || len(s.CountryCounts) != 0 || len(s.AS) != 0 {
		t.Fatalf("expected empty summary, got %+v", s)
	}
}

func TestGetSummaryCounts(t *testing.T) {
	m := newTestManager([]*Node{
		geoNode("8.8.8.8", "United States", "Google"),
		geoNode("1.1.1.1", "United States", "Cloudflare"),
		geoNode("2606:4700::1", "Germany", "Cloudflare"),
		{IP: net.ParseIP("9.9.9.9")}, // good but no geo data yet
	})
	s := m.GetSummary()

	if s.GoodCount != 4 {
		t.Errorf("GoodCount = %d, want 4", s.GoodCount)
	}
	if s.IP4 != 3 || s.IP6 != 1 {
		t.Errorf("IP4/IP6 = %d/%d, want 3/1", s.IP4, s.IP6)
	}

	// Countries sorted by count descending: US (2) before Germany (1).
	if len(s.CountryCounts) != 2 {
		t.Fatalf("CountryCounts len = %d, want 2", len(s.CountryCounts))
	}
	us := s.CountryCounts[0]
	if us.Value != "United States" || us.Count != 2 {
		t.Errorf("top country = %s (%d), want United States (2)", us.Value, us.Count)
	}
	// AbsPercent is relative to the 4 good nodes; RelPercent to the most
	// populous country (US itself => 100).
	if us.AbsPercent != 50 {
		t.Errorf("US AbsPercent = %d, want 50", us.AbsPercent)
	}
	if us.RelPercent != 100 {
		t.Errorf("US RelPercent = %d, want 100", us.RelPercent)
	}
}

func TestSummaryTieBreak(t *testing.T) {
	// Equal counts must order deterministically by value (ascending), so the
	// displayed list does not reshuffle between crawl cycles.
	m := newTestManager([]*Node{
		geoNode("1.1.1.1", "Zedland", "X"),
		geoNode("2.2.2.2", "Andorra", "Y"),
	})
	cc := m.GetSummary().CountryCounts
	if len(cc) != 2 {
		t.Fatalf("want 2 countries, got %d", len(cc))
	}
	if cc[0].Value != "Andorra" || cc[1].Value != "Zedland" {
		t.Errorf("tie-break order = [%q, %q], want [Andorra, Zedland]", cc[0].Value, cc[1].Value)
	}
}

func TestPageOfNodesClamping(t *testing.T) {
	m := newTestManager([]*Node{
		geoNode("10.0.0.5", "", ""), // not routable, but PageOfNodes doesn't filter
		geoNode("8.8.8.8", "", ""),
		geoNode("1.1.1.1", "", ""),
		geoNode("9.9.9.9", "", ""),
		geoNode("4.4.4.4", "", ""),
	})

	tests := []struct {
		name        string
		first, last int
		wantCount   int
		wantLen     int
	}{
		{"first page", 0, 2, 5, 2},
		{"partial last page", 4, 14, 5, 1},
		{"entirely past end", 10, 20, 5, 0},
		{"negative first clamps to 0", -5, 2, 5, 2},
		{"last before first clamps", 3, 1, 5, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			count, nodes := m.PageOfNodes(tc.first, tc.last)
			if count != tc.wantCount {
				t.Errorf("count = %d, want %d", count, tc.wantCount)
			}
			if len(nodes) != tc.wantLen {
				t.Errorf("len = %d, want %d", len(nodes), tc.wantLen)
			}
		})
	}
}

func TestSnapshotStableOrder(t *testing.T) {
	// Nodes are inserted out of order; the snapshot must sort them by IP so
	// pagination is consistent between requests.
	m := newTestManager([]*Node{
		geoNode("9.9.9.9", "", ""),
		geoNode("1.1.1.1", "", ""),
		geoNode("8.8.8.8", "", ""),
	})
	_, nodes := m.PageOfNodes(0, 3)
	got := make([]string, len(nodes))
	for i, n := range nodes {
		got[i] = n.IP.String()
	}
	want := []string{"1.1.1.1", "8.8.8.8", "9.9.9.9"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}
