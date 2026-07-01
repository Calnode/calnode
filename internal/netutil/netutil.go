package netutil

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
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

// ResolveNotMetadata resolves host and rejects it only if it maps to a link-local
// address (169.254.0.0/16 / fe80::/10) — the range every major cloud provider (AWS,
// GCP, Azure, DigitalOcean, Oracle) uses for its instance-metadata service, and which
// is never a legitimate destination for anything Calnode dials. Unlike ResolveSafe,
// this deliberately does NOT block loopback/RFC1918/CGNAT/ULA: CalDAV servers,
// self-hosted LLM runtimes, and self-hosted LiveKit servers are all things an
// operator legitimately points at their own private network or even localhost — a
// self-hostable product can't treat "private network" as inherently suspicious for
// features whose entire purpose is "bring your own server." Cloud metadata, by
// contrast, is never a real chat-completions/CalDAV/LiveKit endpoint for anyone.
func ResolveNotMetadata(ctx context.Context, host string) ([]net.IPAddr, error) {
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve %q: %w", host, err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("%q resolved to no addresses", host)
	}
	for _, a := range addrs {
		if IsLinkLocal(a.IP) {
			return nil, fmt.Errorf("%q resolved to a link-local address (cloud metadata range)", host)
		}
	}
	return addrs, nil
}

// IsLinkLocal reports whether ip is in the link-local range (169.254.0.0/16 /
// fe80::/10) — the range cloud metadata services live in. Exposed separately from
// ResolveNotMetadata for callers doing best-effort validation (e.g. at config-save
// time) that want to treat "definitely metadata" and "failed to resolve right now"
// differently — a save-time DNS hiccup shouldn't block saving a setting the way an
// actual metadata address should.
func IsLinkLocal(ip net.IP) bool {
	return ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}

// CheckHostnameNotMetadata is a best-effort, save-time companion to
// MetadataSafeTransport: it errors only when host definitely resolves to a cloud
// metadata address right now. A resolution failure (offline endpoint, DNS not
// provisioned yet, a transient blip) is not itself an error — it returns nil — because
// the runtime dial-time guard is what actually enforces this once the endpoint is
// used; a save-time DNS hiccup shouldn't block saving a setting. Shared by every
// admin-configurable "bring your own server" field (BYO-LLM endpoint, LiveKit URL).
func CheckHostnameNotMetadata(ctx context.Context, host string) error {
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil
	}
	for _, a := range addrs {
		if IsLinkLocal(a.IP) {
			return fmt.Errorf("%q resolves to a cloud metadata address", host)
		}
	}
	return nil
}

// SafeTransport returns an http.RoundTripper that resolves every dial target through
// ResolveSafe and connects to the resolved IP directly (never re-resolving the
// hostname), closing the DNS-rebinding TOCTOU gap between validation and connection.
// logMsg is the slog message used when a dial is blocked (callers should keep it
// generic — don't disclose the blocked address to whoever configured the target).
// Use this for targets that should never legitimately be private (webhook delivery —
// a third party's receiving endpoint, not infrastructure the operator runs).
func SafeTransport(logger *slog.Logger, logMsg string) http.RoundTripper {
	return dialGuardTransport(ResolveSafe, logger, logMsg)
}

// MetadataSafeTransport is SafeTransport's narrower sibling: it blocks only cloud
// metadata / link-local addresses (see ResolveNotMetadata), for admin-configured
// "bring your own server" targets (CalDAV, the BYO-LLM endpoint, LiveKit) where
// private-network and localhost destinations are an intended, self-hosting use case,
// not a red flag — only the metadata range is universally illegitimate for these.
func MetadataSafeTransport(logger *slog.Logger, logMsg string) http.RoundTripper {
	return dialGuardTransport(ResolveNotMetadata, logger, logMsg)
}

func dialGuardTransport(resolve func(context.Context, string) ([]net.IPAddr, error), logger *slog.Logger, logMsg string) http.RoundTripper {
	baseDialer := &net.Dialer{}
	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, fmt.Errorf("netutil: split addr: %w", err)
			}
			addrs, err := resolve(ctx, host)
			if err != nil {
				logger.Warn(logMsg, "host", host, "error", err)
				return nil, fmt.Errorf("netutil: target resolved to a blocked address")
			}
			return baseDialer.DialContext(ctx, network, net.JoinHostPort(addrs[0].IP.String(), port))
		},
	}
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
