package netutil_test

import (
	"net"
	"testing"

	"github.com/calnode/calnode/internal/netutil"
)

func TestIsPrivateIP(t *testing.T) {
	cases := []struct {
		ip      string
		private bool
	}{
		// Loopback
		{"127.0.0.1", true},
		{"::1", true},
		// Unspecified
		{"0.0.0.0", true},
		// Link-local
		{"169.254.1.1", true},
		{"fe80::1", true},
		// RFC 1918
		{"10.0.0.1", true},
		{"10.255.255.255", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"192.168.1.1", true},
		// "This" network — routes to loopback on Linux (RFC 1122)
		{"0.1.2.3", true},
		// CGNAT (RFC 6598)
		{"100.64.0.1", true},
		{"100.127.255.255", true},
		// IPv6 ULA (RFC 4193)
		{"fc00::1", true},
		{"fd00::1", true},
		// Teredo (2001::/32) — encodes IPv4 inside IPv6
		{"2001:0000::1", true},
		// 6to4 (2002::/16) — encodes IPv4 inside IPv6
		{"2002:7f00:0001::1", true},
		// Public addresses — must NOT be blocked
		{"1.1.1.1", false},
		{"8.8.8.8", false},
		{"93.184.216.34", false}, // example.com
		{"2001:4860:4860::8888", false},
	}
	for _, tc := range cases {
		ip := net.ParseIP(tc.ip)
		if ip == nil {
			t.Fatalf("invalid test IP %q", tc.ip)
		}
		got := netutil.IsPrivateIP(ip)
		if got != tc.private {
			t.Errorf("IsPrivateIP(%q) = %v; want %v", tc.ip, got, tc.private)
		}
	}
}
