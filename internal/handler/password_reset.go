package handler

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/mailer"
	"golang.org/x/crypto/bcrypt"
)

// resetLinkTTL is longer than the magic-link TTL: a reset email sits in an
// inbox longer than a just-requested login link, and the token is single-use
// and hashed at rest either way.
const resetLinkTTL = time.Hour

// RequestPasswordReset handles POST /v1/auth/password/forgot — emails a one-time
// password-reset link to the address if it belongs to an active account with email
// login enabled. Always responds 200 with the same message (no account enumeration):
// the user lookup happens inline (a single indexed SELECT), but token generation,
// the DB write, and the email send are dispatched to a background goroutine rather
// than awaited, so a timing attacker can't distinguish "no such user" from "found
// user, still emailing" — the same pattern as RequestMagicLink.
func (h *Handler) RequestPasswordReset(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	email := strings.TrimSpace(strings.ToLower(req.Email))
	if email != "" {
		var userID string
		var archivedAt sql.NullString
		var emailLogin int
		err := h.db.QueryRowContext(r.Context(),
			`SELECT id, archived_at, email_login FROM users WHERE email = ?`, email).
			Scan(&userID, &archivedAt, &emailLogin)
		if err == nil && !archivedAt.Valid && emailLogin != 0 {
			go h.sendPasswordReset(context.WithoutCancel(r.Context()), userID, email)
		}
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"message": "If an account with that email exists, a password-reset link is on its way.",
	})
}

// sendPasswordReset generates and stores a token and emails the reset link. Run in a
// background goroutine by RequestPasswordReset so its variable cost (DB write +
// mailer round-trip) never shows up in the HTTP response timing.
func (h *Handler) sendPasswordReset(ctx context.Context, userID, email string) {
	rawBytes := make([]byte, 32)
	if _, err := rand.Read(rawBytes); err != nil {
		h.logger.ErrorContext(ctx, "password reset: rand", "error", err)
		return
	}
	raw := hex.EncodeToString(rawBytes)
	sum := sha256.Sum256([]byte(raw))
	tokenHash := hex.EncodeToString(sum[:])
	expiresAt := time.Now().UTC().Add(resetLinkTTL).Format(time.RFC3339)

	if _, err := h.db.ExecContext(ctx,
		`INSERT INTO password_reset_tokens (token_hash, user_id, expires_at) VALUES (?, ?, ?)`,
		tokenHash, userID, expiresAt); err != nil {
		h.logger.ErrorContext(ctx, "password reset: store token", "error", err)
		return
	}

	link := h.baseURL + "/admin/reset-password?token=" + raw
	if h.mailer != nil {
		if err := h.mailer.Send(ctx, resetPasswordMessage(email, link)); err != nil {
			h.logger.ErrorContext(ctx, "password reset: send email", "error", err, "user_id", userID)
		}
	}
}

// ResetPassword handles POST /v1/auth/password/reset — consumes a reset token and
// sets a new password. The token proves ownership of the email address, so no session
// is required; on success every existing session is revoked and a fresh one is
// created, signing the user straight in (same assurance as VerifyMagicLink).
func (h *Handler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var req struct {
		Token       string `json:"token"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	// Validate the password before touching the token so a weak-password attempt
	// doesn't burn the single-use link.
	if msg := validatePassword(req.NewPassword); msg != "" {
		h.writeError(w, http.StatusBadRequest, msg)
		return
	}
	if req.Token == "" {
		h.writeError(w, http.StatusBadRequest, "reset link is invalid or has expired")
		return
	}
	sum := sha256.Sum256([]byte(req.Token))
	tokenHash := hex.EncodeToString(sum[:])
	now := time.Now().UTC().Format(time.RFC3339)

	// Atomically consume: only succeeds if unused and unexpired (single-use, race-safe).
	res, err := h.db.ExecContext(r.Context(),
		`UPDATE password_reset_tokens SET used_at = ? WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?`,
		now, tokenHash, now)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "password reset: consume", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		h.writeError(w, http.StatusBadRequest, "reset link is invalid or has expired")
		return
	}

	var userID string
	var archivedAt sql.NullString
	var emailLogin int
	if err := h.db.QueryRowContext(r.Context(),
		`SELECT u.id, u.archived_at, u.email_login FROM password_reset_tokens t JOIN users u ON u.id = t.user_id WHERE t.token_hash = ?`,
		tokenHash).Scan(&userID, &archivedAt, &emailLogin); err != nil {
		h.logger.ErrorContext(r.Context(), "password reset: load user", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	// The token was emailed to this address, so saying *why* it can't be used leaks
	// nothing — but an archived or email-login-disabled account must not gain access.
	if archivedAt.Valid || emailLogin == 0 {
		h.writeError(w, http.StatusForbidden, "password reset is not available for this account")
		return
	}

	newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "password reset: bcrypt", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if _, err := h.db.ExecContext(r.Context(),
		`UPDATE users SET password_hash = ? WHERE id = ?`, string(newHash), userID); err != nil {
		h.logger.ErrorContext(r.Context(), "password reset: update", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	// Unauthenticated request, so there is no current session to preserve — revoke
	// everything (a compromised session must not survive the reset), then sign in.
	if _, err := h.db.ExecContext(r.Context(),
		`DELETE FROM sessions WHERE user_id = ?`, userID); err != nil {
		h.logger.ErrorContext(r.Context(), "password reset: revoke sessions", "error", err, "user_id", userID)
	}
	if err := h.createSession(r.Context(), w, userID); err != nil {
		// The password IS reset; only the convenience sign-in failed. The login
		// page bounces to /admin/login without a session, so report success and
		// let the user sign in with the new password.
		h.logger.ErrorContext(r.Context(), "password reset: create session", "error", err)
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// resetPasswordMessage builds the reset-link email (multipart text + minimal HTML).
func resetPasswordMessage(to, link string) mailer.Message {
	return mailer.Message{
		To:      []string{to},
		Subject: "Reset your Calnode password",
		Text: "Click the link below to set a new Calnode password. It expires in 1 hour and can be used once.\n\n" +
			link + "\n\nIf you didn't request this, you can ignore this email — your password stays unchanged.",
		HTML: fmt.Sprintf(`<div style="font-family:-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;color:#111827;line-height:1.5">`+
			`<p>Click the button below to set a new Calnode password. It expires in 1 hour and can be used once.</p>`+
			`<p style="margin:24px 0"><a href="%s" style="background:#111827;color:#fff;text-decoration:none;padding:10px 18px;border-radius:8px;display:inline-block;font-weight:600">Set a new password</a></p>`+
			`<p style="font-size:13px;color:#6b7280">Or paste this link into your browser:<br><a href="%s">%s</a></p>`+
			`<p style="font-size:13px;color:#6b7280">If you didn't request this, you can ignore this email — your password stays unchanged.</p></div>`,
			link, link, link),
	}
}
