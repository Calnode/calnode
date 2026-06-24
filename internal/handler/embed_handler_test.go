package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The embed widget script and the stylesheet it pulls are linked WITHOUT a version
// query from third-party host pages, so they must (a) carry a content-hash ETag and
// (b) answer If-None-Match with a 304 — that's what lets a redeploy propagate within
// minutes instead of being pinned for the full max-age.
func TestEmbedJS_etagRevalidation(t *testing.T) {
	h := &Handler{}

	rec := httptest.NewRecorder()
	h.EmbedJS(rec, httptest.NewRequest(http.MethodGet, "/embed.js", nil))
	res := rec.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("first GET: status = %d, want 200", res.StatusCode)
	}
	etag := res.Header.Get("ETag")
	if etag == "" || !strings.HasPrefix(etag, `"`) {
		t.Fatalf("missing/!quoted ETag: %q", etag)
	}
	if cc := res.Header.Get("Cache-Control"); !strings.Contains(cc, "max-age=300") || !strings.Contains(cc, "must-revalidate") {
		t.Errorf("Cache-Control = %q, want max-age=300 + must-revalidate", cc)
	}
	if res.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Error("embed.js must be CORS-public")
	}

	// A conditional request with the matching ETag returns 304 (no bytes re-shipped).
	req := httptest.NewRequest(http.MethodGet, "/embed.js", nil)
	req.Header.Set("If-None-Match", etag)
	rec2 := httptest.NewRecorder()
	h.EmbedJS(rec2, req)
	if rec2.Result().StatusCode != http.StatusNotModified {
		t.Fatalf("conditional GET: status = %d, want 304", rec2.Result().StatusCode)
	}
	if rec2.Body.Len() != 0 {
		t.Errorf("304 should have empty body, got %d bytes", rec2.Body.Len())
	}
}

func TestBookingCSS_cacheModes(t *testing.T) {
	h := &Handler{}

	// Unversioned: short cache + revalidate + ETag.
	rec := httptest.NewRecorder()
	h.BookingCSS(rec, httptest.NewRequest(http.MethodGet, "/booking.css", nil))
	res := rec.Result()
	if res.Header.Get("ETag") == "" {
		t.Error("unversioned booking.css must carry an ETag")
	}
	if cc := res.Header.Get("Cache-Control"); !strings.Contains(cc, "must-revalidate") {
		t.Errorf("unversioned Cache-Control = %q, want must-revalidate", cc)
	}

	// Versioned (?v=…): immutable long cache.
	rec2 := httptest.NewRecorder()
	h.BookingCSS(rec2, httptest.NewRequest(http.MethodGet, "/booking.css?v="+bookingCSSVersion, nil))
	if cc := rec2.Result().Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("versioned Cache-Control = %q, want immutable", cc)
	}
}
