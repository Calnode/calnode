package handler

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"

	"github.com/calnode/calnode/internal/uid"
)

// newOAuthState generates a CSRF state token, sets it as a short-lived cookie, and
// returns it for inclusion in the provider's authorize URL. Shared by the Google
// and Microsoft sign-in flows.
func (h *Handler) newOAuthState(w http.ResponseWriter) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	state := hex.EncodeToString(b)
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- HttpOnly/SameSite/Secure are all set; Secure is h.secureCookie (dynamic on BASE_URL scheme), which gosec's static check can't verify
		Name:     stateCookieName,
		Value:    state,
		Path:     "/",
		MaxAge:   int(stateDuration.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   h.secureCookie,
	})
	return state, nil
}

// verifyOAuthState checks the ?state param against the state cookie and clears the
// cookie regardless (single use). Returns true when they match.
func (h *Handler) verifyOAuthState(w http.ResponseWriter, r *http.Request) bool {
	c, err := r.Cookie(stateCookieName)
	ok := err == nil && c.Value != "" && r.URL.Query().Get("state") == c.Value
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- HttpOnly/SameSite/Secure are all set; Secure is h.secureCookie (dynamic on BASE_URL scheme), which gosec's static check can't verify
		Name:     stateCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   h.secureCookie,
	})
	return ok
}

// finishOAuthLogin resolves an OAuth-verified email to an existing, non-archived
// user and starts a session. When allowedSignupDomains is empty, self-registration
// is blocked and unknown emails redirect to /admin/login?error=no_account. When
// allowedSignupDomains is configured, matching domains auto-provision a "member"
// user and non-matching domains are rejected with 403 Forbidden.
func (h *Handler) finishOAuthLogin(w http.ResponseWriter, r *http.Request, email string) {
	h.finishOAuthLoginWithJITEmail(w, r, email, email)
}

func (h *Handler) finishOAuthLoginWithJITEmail(w http.ResponseWriter, r *http.Request, email, jitEmail string) {
	email = strings.ToLower(strings.TrimSpace(email))
	jitEmail = strings.ToLower(strings.TrimSpace(jitEmail))
	var userID string
	var archivedAt sql.NullString
	err := h.db.QueryRowContext(r.Context(),
		`SELECT id, archived_at FROM users WHERE email = ?`, email).Scan(&userID, &archivedAt)
	if errors.Is(err, sql.ErrNoRows) {
		// Never JIT-provision a non-owner member before the workspace has been claimed;
		// otherwise /v1/auth/claim would see userCount > 0 and lock out the first owner.
		var userCount int
		if cntErr := h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM users`).Scan(&userCount); cntErr != nil {
			h.logger.ErrorContext(r.Context(), "auth: count users", "error", cntErr)
			http.Redirect(w, r, "/admin/login?error=session", http.StatusFound)
			return
		}
		if userCount == 0 {
			h.logger.WarnContext(r.Context(), "auth: instance unclaimed, skipping JIT provisioning", "email", email)
			http.Redirect(w, r, "/admin/login?error=no_account", http.StatusFound)
			return
		}
		allowed := h.getAllowedSignupDomains()
		if len(allowed) == 0 {
			h.logger.WarnContext(r.Context(), "auth: no account for email", "email", email)
			http.Redirect(w, r, "/admin/login?error=no_account", http.StatusFound)
			return
		}
		if !emailDomainAllowed(jitEmail, allowed) {
			h.logger.WarnContext(r.Context(), "auth: email domain not in allowed signup domains", "email", jitEmail)
			h.writeError(w, http.StatusForbidden, "email domain is not allowed")
			return
		}
		email = jitEmail
		userID = uid.New()
		name, _, _ := strings.Cut(email, "@")
		if name == "" {
			name = email
		}
		if _, insErr := h.db.ExecContext(r.Context(), `
			INSERT INTO users (id, email, name, iana_timezone, is_admin, is_owner, email_login)
			VALUES (?, ?, ?, 'UTC', 0, 0, 0)`,
			userID, email, name); insErr != nil {
			// Handle concurrent login race if another request inserted the same email first.
			if reErr := h.db.QueryRowContext(r.Context(),
				`SELECT id, archived_at FROM users WHERE email = ?`, email).Scan(&userID, &archivedAt); reErr != nil {
				h.logger.ErrorContext(r.Context(), "auth: auto-provision user", "email", email, "error", insErr)
				http.Redirect(w, r, "/admin/login?error=session", http.StatusFound)
				return
			}
		} else {
			h.logger.InfoContext(r.Context(), "auth: auto-provisioned member user", "user_id", userID, "email", email)
		}
	} else if err != nil {
		h.logger.ErrorContext(r.Context(), "auth: lookup user", "email", email, "error", err)
		http.Redirect(w, r, "/admin/login?error=session", http.StatusFound)
		return
	}
	if archivedAt.Valid {
		http.Redirect(w, r, "/admin/login?error=archived", http.StatusFound)
		return
	}
	if err := h.createSession(r.Context(), w, userID); err != nil {
		h.logger.ErrorContext(r.Context(), "auth: create session", "error", err)
		http.Redirect(w, r, "/admin/login?error=session", http.StatusFound)
		return
	}
	// If this login was initiated by an MCP "Connect" flow, return to /oauth/authorize
	// (the consent step) rather than the admin home.
	if dest, ok := h.consumeOAuthReturn(w, r); ok {
		http.Redirect(w, r, dest, http.StatusFound) // #nosec G710 -- dest is validated by safeLocalPath (only "/oauth/authorize", never "//") both when set and when consumed; gosec's taint analysis can't trace through that check
		return
	}
	http.Redirect(w, r, "/admin", http.StatusFound)
}

func emailDomainAllowed(email string, allowed []string) bool {
	_, domain, ok := strings.Cut(strings.ToLower(strings.TrimSpace(email)), "@")
	if !ok || domain == "" {
		return false
	}
	for _, a := range allowed {
		if domain == a {
			return true
		}
	}
	return false
}
