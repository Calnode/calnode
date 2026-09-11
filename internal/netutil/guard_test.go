package netutil_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calnode/calnode/internal/netutil"
)

// discardLogger keeps the guard's Warn line (which is where the blocked address is
// allowed to appear) out of the test output.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestGuardedTransport_blockedResolutionIsASentinel pins both halves of the contract: the
// refusal is matchable with errors.Is through http.Client's *url.Error wrapper, and the
// address the resolver objected to is NOT in the error a caller would surface.
func TestGuardedTransport_blockedResolutionIsASentinel(t *testing.T) {
	const secret = "10.43.0.1"

	hc := &http.Client{Transport: netutil.GuardedTransport(
		func(_ context.Context, host string) ([]net.IPAddr, error) {
			return nil, fmt.Errorf("%q resolved to a private or loopback address (%s)", host, secret)
		},
		discardLogger(), "test: SSRF block",
	)}

	_, err := hc.Get("http://caldav.example.invalid/dav/")
	if err == nil {
		t.Fatal("the dial succeeded; the guard let a blocked address through")
	}
	if !errors.Is(err, netutil.ErrBlockedAddress) {
		t.Errorf("error = %v; want it to wrap ErrBlockedAddress", err)
	}
	if msg := err.Error(); strings.Contains(msg, secret) {
		t.Errorf("error %q names %q; the resolved address must stay in the log line", msg, secret)
	}
}

// The other half, and the one that says the guard is a guard rather than an outage: a
// resolution the tier permits ends in a real connection. Without this, "every dial fails"
// would satisfy the assertion above.
func TestGuardedTransport_permittedResolutionDials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	host, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatalf("split test server address: %v", err)
	}
	// The stub stands in for "this name is public"; the dial that follows is a real one
	// to the real server, so what is proved is that a permitted resolution ends in a
	// connection rather than in a second refusal.
	hc := &http.Client{Transport: netutil.GuardedTransport(
		func(_ context.Context, _ string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP(host)}}, nil
		},
		discardLogger(), "test: SSRF block",
	)}

	resp, err := hc.Get("http://caldav.public.example:" + port + "/dav/")
	if err != nil {
		t.Fatalf("a permitted address did not connect: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d; want 204", resp.StatusCode)
	}
}

// SafeTransport and MetadataSafeTransport are the two named tiers over the same guard, and
// their refusals must stay matchable the same way — the CalDAV client's error collapse and
// the webhook worker both go through this sentinel.
func TestNamedTransports_refuseWithTheSameSentinel(t *testing.T) {
	for name, rt := range map[string]http.RoundTripper{
		"SafeTransport":         netutil.SafeTransport(discardLogger(), "test: SSRF block"),
		"MetadataSafeTransport": netutil.MetadataSafeTransport(discardLogger(), "test: SSRF block"),
	} {
		t.Run(name, func(t *testing.T) {
			// 169.254.169.254 is cloud metadata: refused by both tiers, and an IP
			// literal needs no DNS, so this is the real production resolver.
			_, err := (&http.Client{Transport: rt}).Get("http://169.254.169.254/")
			if err == nil {
				t.Fatal("cloud metadata was reachable")
			}
			if !errors.Is(err, netutil.ErrBlockedAddress) {
				t.Errorf("error = %v; want it to wrap ErrBlockedAddress", err)
			}
			// http.Client puts the caller's own URL in the *url.Error, which
			// discloses nothing. What must not appear is the resolver's reason.
			msg := strings.ToLower(err.Error())
			for _, leak := range []string{"private", "loopback", "link-local", "metadata"} {
				if strings.Contains(msg, leak) {
					t.Errorf("error %q says %q, which tells the caller WHY it failed", msg, leak)
				}
			}
		})
	}
}
