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

// requireAuth wraps a handler with API-key authentication.
// Accepts the key via X-API-Key header or Authorization: Bearer <key>.
// RequireAuth wraps a handler with API-key authentication.
func (h *Handler) RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := extractAPIKey(r)
		if key == "" {
			h.writeError(w, http.StatusUnauthorized, "missing API key")
			return
		}

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

		// Best-effort last_used_at stamp — don't fail the request if it errors.
		now := time.Now().UTC().Format(time.RFC3339Nano)
		_, _ = h.db.ExecContext(r.Context(),
			`UPDATE api_keys SET last_used_at = ? WHERE id = ?`, now, keyID)

		next(w, r.WithContext(context.WithValue(r.Context(), ctxKeyUser, user)))
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
