package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
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

// RateLimit returns middleware that allows limit requests per period per remote IP.
// Exceeding the limit returns 429 with a Retry-After header. The IP is taken
// from X-Real-IP or X-Forwarded-For when present (set by a trusted reverse
// proxy), falling back to the TCP remote address.
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

// remoteIP returns the best-available client IP. It trusts X-Real-IP and
// X-Forwarded-For only when set — operators behind a load balancer should
// configure it to strip these headers from untrusted clients.
func remoteIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		// Take only the first (leftmost) address.
		for i := range len(ip) {
			if ip[i] == ',' {
				return ip[:i]
			}
		}
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
