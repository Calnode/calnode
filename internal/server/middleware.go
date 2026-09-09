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

// clientIPKey carries the client IP resolved by TrustClientIP. Absent unless
// TRUSTED_PROXY_CIDRS is configured, in which case remoteIP falls back to the peer.
const clientIPKey contextKey = "client_ip"

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

// Flush keeps this wrapper transparent to streaming handlers (SSE): embedding the
// http.ResponseWriter interface does not promote Flush, so without this the underlying
// flusher would be hidden and a streamed response (e.g. the booking assistant) would be
// buffered until the handler returns.
func (rw *responseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
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

// RateLimit returns middleware that allows limit requests per period per remote IP.
// Exceeding the limit returns 429 with a Retry-After header. The IP is the TCP remote
// address only — see remoteIP for why proxy headers are never trusted here.
//
// The body is deliberately fixed English, even though the public booking form renders it
// verbatim in its error slot. Translating it needs a locale, which this middleware has no
// good source for (no DB, so no operator fallback-locale, and these requests carry no
// ?lang=) — and RateLimit also guards login/token/invite/avatar/settings, where a
// translated body would put a stray non-English string into the untranslated admin SPA and
// make machine consumers' 429s vary by Accept-Language. A split constructor to thread a
// locale through only the public limiters was tried and reverted: too much shared-middleware
// surface for one string on an abuse path a booker reaches only by hammering the endpoint.
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

// remoteIP returns the IP a per-IP limit keys on.
//
// By default that is the TCP-level remote address, stripped of its port, and the
// forwarded headers are ignored: without a configured trusted-proxy allowlist those
// headers can be forged by any client and would bypass the rate limit entirely.
// Operators behind a reverse proxy should strip proxy headers at the proxy and rely on
// the TCP address the proxy connects with. See audit/claims.yaml's
// rate-limit-keys-on-tcp-source-address claim, recorded specifically because a prior
// Layer 2 audit pass mistook this deliberate behavior for a spoofable gap.
//
// When TRUSTED_PROXY_CIDRS is set, TrustClientIP has already resolved the client IP for
// this request and left it in the context — see resolveClientIP for what that means and
// what it deliberately does not do. This function reads that value when it is there, so
// the untrusted-peer path stays byte-for-byte the old behaviour.
func remoteIP(r *http.Request) string {
	if ip, ok := r.Context().Value(clientIPKey).(string); ok && ip != "" {
		return ip
	}
	return peerIP(r)
}

// peerIP returns the TCP-level remote address, stripped of its port. This is the only
// value in a request that a client cannot choose.
func peerIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ParseTrustedProxies parses TRUSTED_PROXY_CIDRS entries. A bare address is accepted and
// treated as a single-host range (/32 or /128), because "10.0.0.7" is what an operator
// naming one proxy will write.
//
// Every parseable entry is returned even when others fail, and the error names the ones
// that did not: one typo should cost that hop's trust, not the whole list's. The caller
// is expected to log the error — an unparsed entry means that proxy's headers are NOT
// believed, which degrades to per-proxy rate limiting rather than to trusting a forgery.
func ParseTrustedProxies(entries []string) ([]*net.IPNet, error) {
	var nets []*net.IPNet
	var bad []string
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if _, n, err := net.ParseCIDR(e); err == nil {
			nets = append(nets, n)
			continue
		}
		if ip := net.ParseIP(e); ip != nil {
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			nets = append(nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		bad = append(bad, e)
	}
	if len(bad) > 0 {
		return nets, fmt.Errorf("not a CIDR or IP address: %s", strings.Join(bad, ", "))
	}
	return nets, nil
}

// TrustClientIP returns middleware that resolves the client IP once per request and
// stores it in the request context for remoteIP to read. With no trusted proxies it is a
// pass-through, so an instance that has not configured any is unchanged.
//
// It is middleware rather than a package-level setting because the trust list belongs to
// one server instance: a mutable global would leak between instances in a test binary
// and would make "which requests are affected" unanswerable from the wiring.
func TrustClientIP(trusted []*net.IPNet) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if len(trusted) == 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), clientIPKey, resolveClientIP(r, trusted))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// resolveClientIP applies the forwarded headers, but only for a peer inside a trusted
// CIDR. A header from an untrusted peer is never read at all — that is the whole point,
// and it is why the default (no trusted proxies) cannot be weakened by a header.
//
// For a trusted peer the answer comes from X-Forwarded-For, walked RIGHT TO LEFT past
// trusted hops, returning the first untrusted address. ⛔ Not the leftmost entry: the
// left of that header is whatever the original client sent, so a client that pre-seeds
// "X-Forwarded-For: 1.2.3.4" gets it prepended and preserved by every well-behaved
// proxy. The rightmost non-trusted hop is the last address a trusted proxy actually
// observed, which is the only one in the header that anything vouched for. The peer is
// the answer whenever the header is absent or unusable.
//
// ⛔ Single-value vendor headers (CF-Connecting-IP, X-Real-IP, True-Client-IP) are NOT
// consulted, and that is deliberate rather than an omission. Trusting one means
// trusting it from every peer in the list, and the list is a list of *networks* rather
// than a list of CDNs: an ordinary reverse proxy inside it forwards whatever headers
// the client sent, so a client could name its own rate-limit bucket by sending one. The
// header carries nothing that says which hop set it, so there is no way to tell the
// value a CDN wrote from the value a visitor typed.
//
// Nothing is lost by leaving them out. A CDN sets X-Forwarded-For as well, and its own
// ranges belong in TRUSTED_PROXY_CIDRS anyway, so the walk steps over its edge address
// and lands on the visitor. That is the same answer the vendor header would have given,
// reached without having to believe a header for a reason the code cannot check.
//
// A hop that does not parse ends the walk and falls back to the peer rather than being
// skipped. Skipping it would let a client inject one malformed entry to push the walk
// past the real hop and onto a value it chose. Note this also means a proxy that appends
// "ip:port" (rather than a bare address) reads as malformed and lands on the peer, which
// is the safe direction to be wrong in.
func resolveClientIP(r *http.Request, trusted []*net.IPNet) string {
	peer := peerIP(r)
	if !ipInAny(peer, trusted) {
		return peer
	}

	// ⛔ Values, not Get. A header may arrive as several field lines, and Get returns
	// only the FIRST. A client that sends its own "X-Forwarded-For: 1.2.3.4" followed by
	// a proxy that ADDS a line rather than appending to the existing one leaves two
	// lines, and Get would hand the walk the client's line alone — no trusted hop in it,
	// so the walk returns the client's chosen address on its first step. RFC 9110 says
	// repeated field lines are equivalent to one comma-joined value in the order
	// received, so joining them is both correct and what the walk already assumes.
	if xff := strings.Join(r.Header.Values("X-Forwarded-For"), ","); xff != "" {
		hops := strings.Split(xff, ",")
		for i := len(hops) - 1; i >= 0; i-- {
			ip := net.ParseIP(strings.TrimSpace(hops[i]))
			if ip == nil {
				break
			}
			if ipInAny(ip.String(), trusted) {
				continue // a trusted hop of our own; keep walking left
			}
			return ip.String()
		}
	}

	return peer
}

// ipInAny reports whether ip (a textual address) falls inside any of nets.
func ipInAny(ip string, nets []*net.IPNet) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, n := range nets {
		if n.Contains(parsed) {
			return true
		}
	}
	return false
}
