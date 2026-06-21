package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type contextKey string

const requestIDKey contextKey = "request_id"

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			id = hex.EncodeToString(b)
		}
		w.Header().Set("X-Request-Id", id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func Logging(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rw, r)

		reqID, _ := r.Context().Value(requestIDKey).(string)
		logger.InfoContext(r.Context(), "request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"remote_addr", r.RemoteAddr,
			"request_id", reqID,
		)
	})
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(status int) {
	rw.status = status
	rw.ResponseWriter.WriteHeader(status)
}

// SameOriginCheck is CSRF defense-in-depth layered on the session cookie's
// SameSite=Lax: for a state-changing request that carries the admin session cookie,
// it rejects the request when a present Origin (or, as a fallback, Referer) header
// names a different host than the one the request was sent to.
//
// Scope is deliberately narrow — it only fires when the `calnode_session` cookie is
// present, so the public booking POST, API-key clients, and manage-token actions
// (none of which carry that cookie) are untouched, as are all GET/HEAD requests.
// When neither Origin nor Referer is present it allows the request: SameSite=Lax is
// the primary guard, and non-browser clients legitimately omit both. The comparison
// is against the request Host header, so a reverse proxy must forward the original
// Host (the common default).
func SameOriginCheck(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isStateChanging(r.Method) {
			if _, err := r.Cookie("calnode_session"); err == nil {
				if src := requestOriginHost(r); src != "" && !strings.EqualFold(src, r.Host) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusForbidden)
					fmt.Fprint(w, `{"error":"cross-origin request blocked"}`)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isStateChanging(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// requestOriginHost returns the host[:port] of the request's Origin header, or its
// Referer host as a fallback, or "" if neither is present/usable (a "null" Origin
// from an opaque/sandboxed context counts as absent here).
func requestOriginHost(r *http.Request) string {
	if o := r.Header.Get("Origin"); o != "" && o != "null" {
		if u, err := url.Parse(o); err == nil && u.Host != "" {
			return u.Host
		}
	}
	if ref := r.Header.Get("Referer"); ref != "" {
		if u, err := url.Parse(ref); err == nil && u.Host != "" {
			return u.Host
		}
	}
	return ""
}

// RateLimit returns middleware that allows limit requests per period per remote IP.
// Exceeding the limit returns 429 with a Retry-After header. The IP is taken
// from X-Real-IP or X-Forwarded-For when present (set by a trusted reverse
// proxy), falling back to the TCP remote address.
// PublicCORS wraps a public, unauthenticated endpoint to allow cross-origin browser
// access (for the embeddable booking widget). allowedOrigins empty ⇒ any origin
// (`*`); otherwise only a request whose Origin is in the list gets an
// Access-Control-Allow-Origin header (others are blocked browser-side). Credentials
// are never permitted — these endpoints carry no session — so a malicious page can't
// ride a logged-in admin's cookie. Note CORS only constrains browsers; it is not an
// access-control boundary (the routes are rate-limited regardless). Handles the
// OPTIONS preflight itself (returns 204).
func PublicCORS(allowedOrigins []string) func(http.HandlerFunc) http.HandlerFunc {
	allowAny := len(allowedOrigins) == 0
	allowed := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowed[strings.ToLower(strings.TrimRight(o, "/"))] = true
	}
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if origin := r.Header.Get("Origin"); origin != "" {
				if allowAny {
					w.Header().Set("Access-Control-Allow-Origin", "*")
				} else if allowed[strings.ToLower(strings.TrimRight(origin, "/"))] {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Add("Vary", "Origin")
				}
			}
			if r.Method == http.MethodOptions {
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Idempotency-Key")
				w.Header().Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next(w, r)
		}
	}
}

func RateLimit(limit int, period time.Duration) func(http.HandlerFunc) http.HandlerFunc {
	rl := &rateLimiter{
		windows: make(map[string]*rlWindow),
		limit:   limit,
		period:  period,
	}
	go rl.cleanup()
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			ip := remoteIP(r)
			if !rl.allow(ip) {
				w.Header().Set("Retry-After", fmt.Sprintf("%d", int(period.Seconds())))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				fmt.Fprint(w, `{"error":"rate limit exceeded"}`)
				return
			}
			next(w, r)
		}
	}
}

type rlWindow struct {
	count     int
	expiresAt time.Time
}

type rateLimiter struct {
	mu      sync.Mutex
	windows map[string]*rlWindow
	limit   int
	period  time.Duration
}

func (rl *rateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	w, ok := rl.windows[key]
	if !ok || now.After(w.expiresAt) {
		rl.windows[key] = &rlWindow{count: 1, expiresAt: now.Add(rl.period)}
		return true
	}
	if w.count >= rl.limit {
		return false
	}
	w.count++
	return true
}

// cleanup removes expired windows every period to bound memory usage.
func (rl *rateLimiter) cleanup() {
	for range time.Tick(rl.period) {
		rl.mu.Lock()
		now := time.Now()
		for k, w := range rl.windows {
			if now.After(w.expiresAt) {
				delete(rl.windows, k)
			}
		}
		rl.mu.Unlock()
	}
}

// remoteIP returns the TCP-level remote address, stripped of its port.
// X-Real-IP and X-Forwarded-For are intentionally ignored: without a
// configured trusted-proxy allowlist, those headers can be forged by any
// client and would bypass the rate limit entirely. Operators behind a reverse
// proxy should strip proxy headers at the proxy level and rely on the TCP
// address the proxy connects with.
func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
