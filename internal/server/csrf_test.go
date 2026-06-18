package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// runSameOrigin sends r through SameOriginCheck and returns (statusCode, nextCalled).
func runSameOrigin(r *http.Request) (int, bool) {
	called := false
	h := SameOriginCheck(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec.Code, called
}

func csrfReq(method, origin string, withCookie bool) *http.Request {
	r := httptest.NewRequest(method, "http://app.example.com/v1/teams", nil)
	r.Host = "app.example.com"
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if withCookie {
		r.AddCookie(&http.Cookie{Name: "calnode_session", Value: "sess"})
	}
	return r
}

func TestSameOrigin_blocksCrossOriginCookieWrite(t *testing.T) {
	code, called := runSameOrigin(csrfReq(http.MethodPost, "http://evil.example.net", true))
	if code != http.StatusForbidden {
		t.Errorf("status = %d; want 403", code)
	}
	if called {
		t.Error("next handler should not run for a blocked cross-origin write")
	}
}

func TestSameOrigin_allowsSameOriginCookieWrite(t *testing.T) {
	code, called := runSameOrigin(csrfReq(http.MethodPost, "http://app.example.com", true))
	if code != http.StatusOK || !called {
		t.Errorf("same-origin write should pass: status=%d called=%v", code, called)
	}
}

func TestSameOrigin_allowsWriteWithoutSessionCookie(t *testing.T) {
	// Public booking POST / API-key clients carry no session cookie — never blocked.
	code, called := runSameOrigin(csrfReq(http.MethodPost, "http://evil.example.net", false))
	if code != http.StatusOK || !called {
		t.Errorf("no-cookie write should pass: status=%d called=%v", code, called)
	}
}

func TestSameOrigin_allowsGetEvenCrossOrigin(t *testing.T) {
	code, called := runSameOrigin(csrfReq(http.MethodGet, "http://evil.example.net", true))
	if code != http.StatusOK || !called {
		t.Errorf("GET should pass regardless of origin: status=%d called=%v", code, called)
	}
}

func TestSameOrigin_allowsWhenNoOriginOrReferer(t *testing.T) {
	// Neither header present → can't determine cross-origin; SameSite=Lax is the guard.
	code, called := runSameOrigin(csrfReq(http.MethodPost, "", true))
	if code != http.StatusOK || !called {
		t.Errorf("missing Origin/Referer should pass: status=%d called=%v", code, called)
	}
}

func TestSameOrigin_fallsBackToReferer(t *testing.T) {
	r := csrfReq(http.MethodDelete, "", true) // no Origin
	r.Header.Set("Referer", "http://evil.example.net/some/page")
	code, called := runSameOrigin(r)
	if code != http.StatusForbidden || called {
		t.Errorf("cross-origin Referer should block: status=%d called=%v", code, called)
	}
}
