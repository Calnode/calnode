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
	ID         string
	Email      string
	Name       string
	IANATZ     string
	TimeFormat string // "12h" or "24h"
	WeekStart  int    // 0=Sunday, 1=Monday
	DateFormat string // "dmy", "mdy", or "ymd"
	AvatarURL  string
	IsAdmin    bool

	// Notification preferences (all default true).
	NotifyConfirmation   bool
	NotifyCancellation   bool
	NotifyReschedule     bool
	NotifyReminder       bool
	NotifyHostBooking    bool
	NotifyHostCancel     bool
	NotifyHostReschedule bool
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
			var nc, nca, nr, nrm, nhb, nhc, nhr int
			err := h.db.QueryRowContext(r.Context(), `
				SELECT ak.id, u.id, u.email, u.name, u.iana_timezone, u.time_format, u.week_start, u.date_format, COALESCE(u.avatar_url,''), u.is_admin,
				       COALESCE(u.notify_confirmation,1), COALESCE(u.notify_cancellation,1), COALESCE(u.notify_reschedule,1), COALESCE(u.notify_reminder,1),
				       COALESCE(u.notify_host_booking,1), COALESCE(u.notify_host_cancel,1), COALESCE(u.notify_host_reschedule,1)
				FROM api_keys ak JOIN users u ON u.id = ak.user_id
				WHERE ak.key_hash = ?`, hash).
				Scan(&keyID, &user.ID, &user.Email, &user.Name, &user.IANATZ, &user.TimeFormat, &user.WeekStart, &user.DateFormat, &user.AvatarURL, &user.IsAdmin,
					&nc, &nca, &nr, &nrm, &nhb, &nhc, &nhr)
			user.NotifyConfirmation, user.NotifyCancellation, user.NotifyReschedule, user.NotifyReminder = nc != 0, nca != 0, nr != 0, nrm != 0
			user.NotifyHostBooking, user.NotifyHostCancel, user.NotifyHostReschedule = nhb != 0, nhc != 0, nhr != 0
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
			var nc, nca, nr, nrm, nhb, nhc, nhr int
			if err := h.db.QueryRowContext(r.Context(), `
				SELECT u.id, u.email, u.name, u.iana_timezone, u.time_format, u.week_start, u.date_format, COALESCE(u.avatar_url,''), u.is_admin,
				       COALESCE(u.notify_confirmation,1), COALESCE(u.notify_cancellation,1), COALESCE(u.notify_reschedule,1), COALESCE(u.notify_reminder,1),
				       COALESCE(u.notify_host_booking,1), COALESCE(u.notify_host_cancel,1), COALESCE(u.notify_host_reschedule,1)
				FROM sessions s
				JOIN users u ON u.id = s.user_id
				WHERE s.id = ? AND s.expires_at > ?`,
				cookie.Value, now).
				Scan(&user.ID, &user.Email, &user.Name, &user.IANATZ, &user.TimeFormat, &user.WeekStart, &user.DateFormat, &user.AvatarURL, &user.IsAdmin,
					&nc, &nca, &nr, &nrm, &nhb, &nhc, &nhr); err == nil {
				user.NotifyConfirmation, user.NotifyCancellation, user.NotifyReschedule, user.NotifyReminder = nc != 0, nca != 0, nr != 0, nrm != 0
				user.NotifyHostBooking, user.NotifyHostCancel, user.NotifyHostReschedule = nhb != 0, nhc != 0, nhr != 0
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
