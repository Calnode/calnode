package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func corsReq(t *testing.T, mw func(http.HandlerFunc) http.HandlerFunc, method, origin string) *httptest.ResponseRecorder {
	t.Helper()
	called := false
	h := mw(func(w http.ResponseWriter, _ *http.Request) { called = true; w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest(method, "/v1/event-types/x/slots", nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	if method == http.MethodOptions && called {
		t.Error("preflight OPTIONS should not reach the wrapped handler")
	}
	return rec
}

func TestPublicCORS_allowAny(t *testing.T) {
	rec := corsReq(t, PublicCORS(nil), http.MethodGet, "https://customer.example")
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("ACAO = %q; want *", got)
	}
}

func TestPublicCORS_allowlistMatch(t *testing.T) {
	mw := PublicCORS([]string{"https://acme.com", "https://www.acme.com/"})
	rec := corsReq(t, mw, http.MethodGet, "https://acme.com")
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://acme.com" {
		t.Errorf("ACAO = %q; want reflected origin", got)
	}
	if rec.Header().Get("Vary") == "" {
		t.Error("Vary: Origin should be set when reflecting a specific origin")
	}
}

func TestPublicCORS_allowlistNonMatch(t *testing.T) {
	mw := PublicCORS([]string{"https://acme.com"})
	rec := corsReq(t, mw, http.MethodGet, "https://evil.example")
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("ACAO = %q; want empty (origin not allowed)", got)
	}
}

func TestPublicCORS_preflight(t *testing.T) {
	rec := corsReq(t, PublicCORS(nil), http.MethodOptions, "https://customer.example")
	if rec.Code != http.StatusNoContent {
		t.Errorf("preflight status = %d; want 204", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Error("preflight should advertise Allow-Methods")
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("preflight should carry ACAO")
	}
}

func TestPublicCORS_neverAllowsCredentials(t *testing.T) {
	rec := corsReq(t, PublicCORS([]string{"https://acme.com"}), http.MethodGet, "https://acme.com")
	if rec.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Error("Allow-Credentials must never be set on public CORS")
	}
}
