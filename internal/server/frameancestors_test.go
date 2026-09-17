package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/calnode/calnode/frontend"
)

// serveAdminSPA runs a request through the admin SPA handler wrapped exactly as New
// wraps it, and returns the response.
func serveAdminSPA(origins []string, path string) *httptest.ResponseRecorder {
	h := FrameAncestors(origins)(frontend.Handler())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// ⚠️ This pins what /admin/ sends TODAY, which is no frame header at all: the SPA is
// framable by any site unless an operator says otherwise. FRAME_ANCESTORS deliberately
// does not change that when unset — an opt-in setting must not smuggle in a default deny
// — so this test exists to make the next person's change to it deliberate rather than
// incidental. The public booking pages are the ones that carry an unconditional DENY.
func TestAdminSPA_sendsNoFrameHeadersWhenUnset(t *testing.T) {
	for _, path := range []string{"/", "/bookings", "/favicon.svg"} {
		t.Run(path, func(t *testing.T) {
			rec := serveAdminSPA(nil, path)
			if got := rec.Header().Get("Content-Security-Policy"); got != "" {
				t.Errorf("Content-Security-Policy = %q; want it absent", got)
			}
			if got := rec.Header().Get("X-Frame-Options"); got != "" {
				t.Errorf("X-Frame-Options = %q; want it absent", got)
			}
		})
	}
}

func TestAdminSPA_frameAncestorsWhenConfigured(t *testing.T) {
	rec := serveAdminSPA([]string{"https://console.example.test", "'self'"}, "/")

	want := "frame-ancestors https://console.example.test 'self'"
	if got := rec.Header().Get("Content-Security-Policy"); got != want {
		t.Errorf("Content-Security-Policy = %q; want %q", got, want)
	}
	// X-Frame-Options has no allow-list form, and SAMEORIGIN would be honoured instead of
	// the CSP by the browsers that read it, breaking the embedding this enables.
	if got := rec.Header().Get("X-Frame-Options"); got != "" {
		t.Errorf("X-Frame-Options = %q; want it absent alongside frame-ancestors", got)
	}
}

// The SPA fallback route (any client-side path) carries it too, not just the shell.
func TestAdminSPA_frameAncestorsOnSPAFallback(t *testing.T) {
	rec := serveAdminSPA([]string{"'self'"}, "/settings/video")
	if got := rec.Header().Get("Content-Security-Policy"); got != "frame-ancestors 'self'" {
		t.Errorf("Content-Security-Policy = %q; want frame-ancestors 'self'", got)
	}
}
