package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"
)

type contextKey string

const ctxKeyUser contextKey = "user"

// AuthUser is the authenticated caller stored in request context.
type AuthUser struct {
	ID      string
	Email   string
	Name    string
	IANATZ  string
	IsAdmin bool
}

func userFromContext(ctx context.Context) (AuthUser, bool) {
	u, ok := ctx.Value(ctxKeyUser).(AuthUser)
	return u, ok
}

// RequireAuth wraps a handler with authentication. It accepts either:
//   - An API key via X-API-Key header or Authorization: Bearer (for programmatic/MCP callers)
//   - A session cookie set by Google OAuth login (for the admin UI browser sessions)
//
// If a key header is present but invalid, the request is rejected immediately
// (no session fallback) to prevent confused-deputy attacks.
func (h *Handler) RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// --- API key path ---
		if key := extractAPIKey(r); key != "" {
			hash := hashAPIKey(key)
			var user AuthUser
			var keyID string
			err := h.db.QueryRowContext(r.Context(), `
				SELECT ak.id, u.id, u.email, u.name, u.iana_timezone, u.is_admin
				FROM api_keys ak JOIN users u ON u.id = ak.user_id
				WHERE ak.key_hash = ?`, hash).
				Scan(&keyID, &user.ID, &user.Email, &user.Name, &user.IANATZ, &user.IsAdmin)
			if err != nil {
				h.writeError(w, http.StatusUnauthorized, "invalid API key")
				return
			}
			now := time.Now().UTC().Format(time.RFC3339Nano)
			_, _ = h.db.ExecContext(r.Context(),
				`UPDATE api_keys SET last_used_at = ? WHERE id = ?`, now, keyID)
			next(w, r.WithContext(context.WithValue(r.Context(), ctxKeyUser, user)))
			return
		}

		// --- Session cookie path (admin browser UI) ---
		if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
			now := time.Now().UTC().Format(time.RFC3339)
			var user AuthUser
			if err := h.db.QueryRowContext(r.Context(), `
				SELECT u.id, u.email, u.name, u.iana_timezone, u.is_admin
				FROM sessions s
				JOIN users u ON u.id = s.user_id
				WHERE s.id = ? AND s.expires_at > ?`,
				cookie.Value, now).
				Scan(&user.ID, &user.Email, &user.Name, &user.IANATZ, &user.IsAdmin); err == nil {
				next(w, r.WithContext(context.WithValue(r.Context(), ctxKeyUser, user)))
				return
			}
		}

		h.writeError(w, http.StatusUnauthorized, "authentication required")
	}
}

func extractAPIKey(r *http.Request) string {
	if k := r.Header.Get("X-API-Key"); k != "" {
		return k
	}
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

func hashAPIKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}
