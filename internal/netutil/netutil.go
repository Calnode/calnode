package netutil

import (
	"context"
	"fmt"
	"net"
)

// ResolveSafe resolves host and returns its addresses, or an error if it fails to
// resolve, resolves to zero addresses, or resolves to any private/loopback/
// link-local/CGNAT/ULA address (see IsPrivateIP) — the shared SSRF check used both
// when an operator saves a webhook URL and when the worker actually dials it.
// Callers that go on to make a real connection should dial one of the returned
// addresses directly (not re-resolve the hostname) to avoid a DNS-rebinding gap
// between this check and the connection.
func ResolveSafe(ctx context.Context, host string) ([]net.IPAddr, error) {
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve %q: %w", host, err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("%q resolved to no addresses", host)
	}
	for _, a := range addrs {
		if IsPrivateIP(a.IP) {
			return nil, fmt.Errorf("%q resolved to a private or loopback address", host)
		}
	}
	return addrs, nil
}

// IsPrivateIP reports whether ip is a loopback, unspecified, link-local, or
// private-use address. Used to block SSRF-prone webhook delivery targets.
func IsPrivateIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		inPrivateRange(ip)
}

func inPrivateRange(ip net.IP) bool {
	for _, cidr := range privateRanges {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// privateRanges lists IPv4/IPv6 blocks that must never be reachable as
// webhook delivery targets: RFC 1918, CGNAT (RFC 6598), IPv6 ULA (RFC 4193).
var privateRanges []*net.IPNet

func init() {
	for _, s := range []string{
		"0.0.0.0/8", // "This" network; routes to loopback on Linux
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"100.64.0.0/10", // CGNAT (RFC 6598)
		"fc00::/7",      // IPv6 ULA (RFC 4193)
		"2001::/32",     // Teredo; encodes IPv4 inside IPv6
		"2002::/16",     // 6to4; encodes IPv4 inside IPv6
	} {
		_, cidr, err := net.ParseCIDR(s)
		if err != nil {
			panic("netutil: invalid CIDR " + s + ": " + err.Error())
		}
		privateRanges = append(privateRanges, cidr)
	}
}
