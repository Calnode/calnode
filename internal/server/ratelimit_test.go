package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiter_allowsUpToLimit(t *testing.T) {
	rl := &rateLimiter{
		windows: make(map[string]*rlWindow),
		limit:   3,
		period:  time.Minute,
	}
	for i := range 3 {
		if !rl.allow("192.0.2.1") {
			t.Fatalf("request %d should be allowed (under limit)", i+1)
		}
	}
}

func TestRateLimiter_blocksOverLimit(t *testing.T) {
	rl := &rateLimiter{
		windows: make(map[string]*rlWindow),
		limit:   3,
		period:  time.Minute,
	}
	for range 3 {
		rl.allow("192.0.2.1")
	}
	if rl.allow("192.0.2.1") {
		t.Error("4th request should be blocked")
	}
}

func TestRateLimiter_separateKeysAreIndependent(t *testing.T) {
	rl := &rateLimiter{
		windows: make(map[string]*rlWindow),
		limit:   1,
		period:  time.Minute,
	}
	rl.allow("192.0.2.1")
	if !rl.allow("192.0.2.2") {
		t.Error("different IP should have its own counter")
	}
}

func TestRateLimiter_windowResets(t *testing.T) {
	rl := &rateLimiter{
		windows: make(map[string]*rlWindow),
		limit:   1,
		period:  time.Millisecond,
	}
	rl.allow("192.0.2.1")
	if rl.allow("192.0.2.1") {
		t.Fatal("second request should be blocked within window")
	}
	time.Sleep(2 * time.Millisecond)
	if !rl.allow("192.0.2.1") {
		t.Error("request after window expiry should be allowed")
	}
}

func TestRateLimit_returns429AfterLimit(t *testing.T) {
	handler := RateLimit(2, time.Minute)(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	makeReq := func() int {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "192.0.2.5:1234"
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec.Code
	}

	if got := makeReq(); got != http.StatusOK {
		t.Fatalf("request 1: got %d; want 200", got)
	}
	if got := makeReq(); got != http.StatusOK {
		t.Fatalf("request 2: got %d; want 200", got)
	}
	if got := makeReq(); got != http.StatusTooManyRequests {
		t.Errorf("request 3: got %d; want 429", got)
	}
}

func TestRateLimit_retryAfterHeader(t *testing.T) {
	handler := RateLimit(1, time.Minute)(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	makeReq := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "192.0.2.6:1234"
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec
	}

	makeReq() // consume the single allowed request
	rec := makeReq()
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("got %d; want 429", rec.Code)
	}
	if ra := rec.Header().Get("Retry-After"); ra != "60" {
		t.Errorf("Retry-After = %q; want 60", ra)
	}
}

func TestRemoteIP_xRealIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Real-IP", "203.0.113.1")
	req.RemoteAddr = "127.0.0.1:1234"
	if got := remoteIP(req); got != "203.0.113.1" {
		t.Errorf("remoteIP = %q; want 203.0.113.1", got)
	}
}

func TestRemoteIP_xForwardedFor(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.2, 10.0.0.1")
	req.RemoteAddr = "127.0.0.1:1234"
	if got := remoteIP(req); got != "203.0.113.2" {
		t.Errorf("remoteIP = %q; want 203.0.113.2", got)
	}
}

func TestRemoteIP_fallback(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.3:9999"
	if got := remoteIP(req); got != "203.0.113.3" {
		t.Errorf("remoteIP = %q; want 203.0.113.3", got)
	}
}
