package netutil_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/calnode/calnode/internal/netutil"
)

func mustCIDRs(t *testing.T, ss ...string) []*net.IPNet {
	t.Helper()
	var out []*net.IPNet
	for _, s := range ss {
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		out = append(out, n)
	}
	return out
}

func TestResolveSafeAllowing(t *testing.T) {
	ctx := context.Background()
	// Deliberately over-broad: an operator listing the metadata range, or everything,
	// must still not reach the metadata service.
	allowed := mustCIDRs(t, "10.200.50.0/24", "127.0.0.0/8", "169.254.0.0/16", "fe80::/10", "fd00::/8", "0.0.0.0/0")
	cases := []struct {
		host string
		ok   bool
	}{
		{"10.200.50.7", true},      // the listed Docker network
		{"127.0.0.1", true},        // listed loopback
		{"10.0.0.1", true},         // covered by 0.0.0.0/0
		{"8.8.8.8", true},          // public, always allowed
		{"169.254.169.254", false}, // metadata: listed, still refused
		{"169.254.1.1", false},     // link-local
		{"fe80::1", false},         // IPv6 link-local
		{"fd00:ec2::254", false},   // AWS IPv6 metadata, inside listed fd00::/8
		{"0.0.0.0", false},         // unspecified
	}
	for _, tc := range cases {
		_, err := netutil.ResolveSafeAllowing(ctx, tc.host, allowed)
		if (err == nil) != tc.ok {
			t.Errorf("ResolveSafeAllowing(%q) err = %v; want ok=%v", tc.host, err, tc.ok)
		}
	}
}

// With no list, the behaviour is exactly ResolveSafe's: every private range refused.
func TestResolveSafeAllowing_emptyIsResolveSafe(t *testing.T) {
	ctx := context.Background()
	for _, host := range []string{"10.200.50.7", "127.0.0.1", "192.168.1.1", "169.254.169.254", "fd00::1", "8.8.8.8"} {
		_, errA := netutil.ResolveSafeAllowing(ctx, host, nil)
		_, errB := netutil.ResolveSafe(ctx, host)
		if (errA == nil) != (errB == nil) {
			t.Errorf("%s: ResolveSafeAllowing(nil) err = %v, ResolveSafe err = %v; want the same verdict", host, errA, errB)
		}
	}
	// A list that doesn't cover the address changes nothing for it.
	if _, err := netutil.ResolveSafeAllowing(ctx, "10.0.0.1", mustCIDRs(t, "10.200.50.0/24")); err == nil {
		t.Error("10.0.0.1 allowed by 10.200.50.0/24; want refused")
	}
}

// The dial-time guard applies the same list: a listed private target is reached, an
// unlisted one is refused before any connection is made.
func TestSafeTransportAllowing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "ok") //nolint:errcheck
	}))
	defer srv.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	allowed := &http.Client{Transport: netutil.SafeTransportAllowing(logger, "test", mustCIDRs(t, "127.0.0.0/8"))}
	resp, err := allowed.Get(srv.URL)
	if err != nil {
		t.Fatalf("listed loopback: %v", err)
	}
	resp.Body.Close()

	for name, tr := range map[string]http.RoundTripper{
		"no list":       netutil.SafeTransport(logger, "test"),
		"other network": netutil.SafeTransportAllowing(logger, "test", mustCIDRs(t, "10.200.50.0/24")),
	} {
		if resp, err := (&http.Client{Transport: tr}).Get(srv.URL); err == nil {
			resp.Body.Close()
			t.Errorf("%s: loopback target reached; want refused", name)
		}
	}
}
