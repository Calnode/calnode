package server

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// serveThroughTrust runs one request through TrustClientIP and reports what remoteIP
// resolved to inside the handler — the value a rate limiter would key on.
func serveThroughTrust(t *testing.T, cidrs []string, peer string, headers map[string]string) string {
	t.Helper()
	trusted, err := ParseTrustedProxies(cidrs)
	if err != nil {
		t.Fatalf("ParseTrustedProxies(%v): %v", cidrs, err)
	}
	var got string
	h := TrustClientIP(trusted)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = remoteIP(r)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = peer
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	h.ServeHTTP(httptest.NewRecorder(), req)
	return got
}

// The default. An untrusted peer's forwarded headers are not read at all, so the spoof
// keys on the connection the spoofer actually made.
func TestTrustClientIP_untrustedPeerIgnoresSpoofedHeaders(t *testing.T) {
	got := serveThroughTrust(t, []string{"10.0.0.0/8"}, "203.0.113.9:1234", map[string]string{
		"X-Forwarded-For":  "198.51.100.1",
		"CF-Connecting-IP": "198.51.100.2",
		"X-Real-IP":        "198.51.100.3",
	})
	if got != "203.0.113.9" {
		t.Errorf("client IP = %q; want the peer 203.0.113.9", got)
	}
}

// With no trusted proxies configured at all, TrustClientIP is a pass-through and the
// behaviour is exactly what it was before the setting existed.
func TestTrustClientIP_noTrustedProxiesIsUnchanged(t *testing.T) {
	got := serveThroughTrust(t, nil, "10.0.0.5:1234", map[string]string{
		"X-Forwarded-For": "198.51.100.1",
	})
	if got != "10.0.0.5" {
		t.Errorf("client IP = %q; want the peer 10.0.0.5", got)
	}
}

// ⛔ Single-value vendor headers are ignored even from a TRUSTED peer, and this is the
// case that says why. Trusting one means trusting it from every network in the list,
// and an ordinary reverse proxy in that list forwards whatever headers the client sent.
// If CF-Connecting-IP were preferred, the client below would have chosen its own
// rate-limit key by sending one header, which is exactly the spoof the right-to-left
// walk exists to prevent.
//
// The chain is what is believed: 10.0.0.9 is one of ours, so the walk steps over it and
// stops at 198.51.100.7, the address our outermost proxy actually observed.
func TestTrustClientIP_ignoresVendorHeadersFromATrustedPeer(t *testing.T) {
	for _, header := range []string{"CF-Connecting-IP", "X-Real-IP", "True-Client-IP"} {
		t.Run(header, func(t *testing.T) {
			got := serveThroughTrust(t, []string{"10.0.0.0/8"}, "10.0.0.5:1234", map[string]string{
				header:            "1.2.3.4",
				"X-Forwarded-For": "198.51.100.7, 10.0.0.9",
			})
			if got != "198.51.100.7" {
				t.Errorf("client IP = %q; want 198.51.100.7 from the chain, never %s", got, header)
			}
		})
	}
}

// And with no X-Forwarded-For to fall back on, a vendor header still buys the client
// nothing: the answer is the peer, not the address the client named.
func TestTrustClientIP_vendorHeaderAloneFallsBackToThePeer(t *testing.T) {
	got := serveThroughTrust(t, []string{"10.0.0.0/8"}, "10.0.0.5:1234",
		map[string]string{"CF-Connecting-IP": "1.2.3.4"})
	if got != "10.0.0.5" {
		t.Errorf("client IP = %q; want the peer 10.0.0.5", got)
	}
}

// ⛔ X-Forwarded-For can arrive as SEVERAL field lines, and Header.Get returns only the
// first one.
//
// A client that sends its own X-Forwarded-For, in front of a proxy that ADDS a line
// rather than appending to the existing one, produces exactly this: line 1 is the
// client's, line 2 is the proxy's. Reading only line 1 hands the walk a chain with no
// trusted hop in it, so it returns the client's chosen address on the first step and the
// client has named its own rate-limit bucket. RFC 9110 makes repeated field lines
// equivalent to one comma-joined value in order, which is what the walk needs.
//
// This uses Add rather than the shared helper on purpose: the helper calls Set, which
// collapses everything to one line and cannot express the case.
func TestTrustClientIP_joinsRepeatedForwardedForLines(t *testing.T) {
	trusted, err := ParseTrustedProxies([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("ParseTrustedProxies: %v", err)
	}
	var got string
	h := TrustClientIP(trusted)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = remoteIP(r)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.5:1234"
	req.Header.Add("X-Forwarded-For", "1.2.3.4")                // the client's own line
	req.Header.Add("X-Forwarded-For", "198.51.100.7, 10.0.0.9") // what our proxies observed
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got != "198.51.100.7" {
		t.Errorf("client IP = %q; want 198.51.100.7 — with only the first field line read, "+
			"the client's own 1.2.3.4 becomes the rate-limit key", got)
	}
}

// Two of our own hops appended themselves; the walk goes right to left past both and
// stops at the address the outermost trusted proxy actually saw.
func TestTrustClientIP_walksTrustedChainRightToLeft(t *testing.T) {
	got := serveThroughTrust(t, []string{"10.0.0.0/8", "192.168.0.0/16"}, "10.0.0.5:1234",
		map[string]string{"X-Forwarded-For": "198.51.100.7, 192.168.1.1, 10.0.0.9"})
	if got != "198.51.100.7" {
		t.Errorf("client IP = %q; want 198.51.100.7", got)
	}
}

// The leftmost entry is client-supplied. A client that pre-seeds the header must not be
// able to choose its own key by putting a value to the left of the real one.
func TestTrustClientIP_ignoresEntriesLeftOfTheRealHop(t *testing.T) {
	got := serveThroughTrust(t, []string{"10.0.0.0/8"}, "10.0.0.5:1234",
		map[string]string{"X-Forwarded-For": "1.2.3.4, 198.51.100.7, 10.0.0.9"})
	if got != "198.51.100.7" {
		t.Errorf("client IP = %q; want 198.51.100.7 (not the client-seeded 1.2.3.4)", got)
	}
}

func TestTrustClientIP_malformedHeaderFallsBackToPeer(t *testing.T) {
	cases := map[string]string{
		"garbage":            "not-an-ip",
		"empty entries":      " , ,",
		"address with port":  "198.51.100.7:443",
		"malformed left hop": "not-an-ip, 10.0.0.9",
	}
	for name, xff := range cases {
		t.Run(name, func(t *testing.T) {
			got := serveThroughTrust(t, []string{"10.0.0.0/8"}, "10.0.0.5:1234",
				map[string]string{"X-Forwarded-For": xff})
			if got != "10.0.0.5" {
				t.Errorf("client IP = %q; want the peer 10.0.0.5", got)
			}
		})
	}
}

// A chain of nothing but our own proxies has no client address in it. The peer is the
// only honest answer left.
func TestTrustClientIP_allHopsTrustedFallsBackToPeer(t *testing.T) {
	got := serveThroughTrust(t, []string{"10.0.0.0/8"}, "10.0.0.5:1234",
		map[string]string{"X-Forwarded-For": "10.0.0.7, 10.0.0.9"})
	if got != "10.0.0.5" {
		t.Errorf("client IP = %q; want the peer 10.0.0.5", got)
	}
}

func TestTrustClientIP_ipv6(t *testing.T) {
	// The proxies sit in 2001:db8:0::/48; the client is one prefix over, so the walk
	// skips the trusted hop and stops on it.
	got := serveThroughTrust(t, []string{"2001:db8:0::/48"}, "[2001:db8::1]:1234",
		map[string]string{"X-Forwarded-For": "2001:db8:1::5, 2001:db8::9"})
	if got != "2001:db8:1::5" {
		t.Errorf("client IP = %q; want 2001:db8:1::5", got)
	}
}

func TestParseTrustedProxies(t *testing.T) {
	// A bare address is what an operator naming one proxy writes.
	nets, err := ParseTrustedProxies([]string{"10.0.0.7", "192.168.0.0/16", "2001:db8::1"})
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if len(nets) != 3 {
		t.Fatalf("parsed %d entries; want 3", len(nets))
	}
	if !nets[0].Contains(net.ParseIP("10.0.0.7")) || nets[0].Contains(net.ParseIP("10.0.0.8")) {
		t.Errorf("bare IPv4 %v should be exactly one host", nets[0])
	}
	if !nets[2].Contains(net.ParseIP("2001:db8::1")) || nets[2].Contains(net.ParseIP("2001:db8::2")) {
		t.Errorf("bare IPv6 %v should be exactly one host", nets[2])
	}
}

// One typo costs that hop's trust, not the whole list's — and it is reported rather than
// swallowed, because an operator who thinks a proxy is trusted and is wrong gets shared
// rate-limit buckets with no explanation.
func TestParseTrustedProxies_reportsBadEntriesAndKeepsTheRest(t *testing.T) {
	nets, err := ParseTrustedProxies([]string{"10.0.0.0/8", "10.0.0.0/99", "nonsense"})
	if err == nil {
		t.Fatal("err = nil; want the bad entries named")
	}
	if len(nets) != 1 {
		t.Errorf("parsed %d entries; want the 1 good one kept", len(nets))
	}
}
