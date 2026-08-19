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

// EU AI Act Art. 50(1): the embed widget's "Book by chat" drawer must carry the same
// persistent AI-disclosure notice as the hosted booking page (book.html). embed.js now
// fetches its strings (including "assistant_disclosure") from the same server-side
// locale JSON book.html reads via {{.T}} — see PublicEventType's "i18n" response field
// and internal-docs/i18n-plan.md — so drift is structurally impossible rather than
// something to string-match for. This guards that the *wiring* is actually there: the
// disclosure element pulls from the fetched i18n map (t(this.i18n, 'assistant_disclosure')),
// not a hardcoded literal that could silently diverge again.
func TestEmbedJS_assistantDisclosure(t *testing.T) {
	h := &Handler{}
	rec := httptest.NewRecorder()
	h.EmbedJS(rec, httptest.NewRequest(http.MethodGet, "/embed.js", nil))
	body := rec.Body.String()

	if !strings.Contains(body, `t(this.i18n, 'assistant_disclosure')`) {
		t.Fatal("embed.js disclosure no longer reads from the shared i18n lookup - it may have regressed to a hardcoded literal that can drift from book.html")
	}
	if !strings.Contains(body, `role: 'note'`) && !strings.Contains(body, `'role': 'note'`) {
		t.Error("embed.js disclosure element is missing role: 'note' (accessibility exposure)")
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
