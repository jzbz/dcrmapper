package crawler

import (
	"net"
	"testing"
)

func TestIsRoutable(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		// Public, routable addresses.
		{"8.8.8.8", true},
		{"1.1.1.1", true},
		{"2606:4700:4700::1111", true},

		// RFC1918 private IPv4.
		{"10.0.0.1", false},
		{"172.16.5.4", false},
		{"192.168.1.1", false},

		// Other non-routable IPv4.
		{"0.0.0.0", false},
		{"127.0.0.1", false},
		{"169.254.10.10", false}, // link-local
		{"224.0.0.1", false},     // multicast

		// Non-routable IPv6.
		{"::", false},      // unspecified
		{"::1", false},     // loopback
		{"fe80::1", false}, // link-local
		{"fc00::1", false}, // unique local (RFC4193)
		{"2002::1", false}, // 6to4 (RFC3964)
		{"ff02::1", false}, // multicast
	}

	for _, tc := range tests {
		ip := net.ParseIP(tc.ip)
		if ip == nil {
			t.Fatalf("bad test IP %q", tc.ip)
		}
		if got := isRoutable(ip); got != tc.want {
			t.Errorf("isRoutable(%s) = %v, want %v", tc.ip, got, tc.want)
		}
	}

	// A nil IP must never be considered routable.
	if isRoutable(nil) {
		t.Error("isRoutable(nil) = true, want false")
	}
}
