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
)

const magicLinkTTL = 15 * time.Minute

// RequestMagicLink handles POST /v1/auth/magic-link/request — emails a one-time login link to
// the address if it belongs to an active account. Always responds 200 immediately with the same
// message (no account enumeration): the user lookup happens inline (a single indexed SELECT, so
// its own cost is negligible), but token generation, the DB write, and the email send — for a
// real, non-archived user — are dispatched to a background goroutine rather than awaited, so a
// timing attacker can't distinguish "no such user" from "found user, still emailing" by how long
// the request takes. See internal/handler/email_auth.go's dummyHash for the analogous constant-
// time pattern on the password-login path.
func (h *Handler) RequestMagicLink(w http.ResponseWriter, r *http.Request) {
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
		err := h.db.QueryRowContext(r.Context(),
			`SELECT id, archived_at FROM users WHERE email = ?`, email).Scan(&userID, &archivedAt)
		if err == nil && !archivedAt.Valid {
			go h.sendMagicLink(context.WithoutCancel(r.Context()), userID, email)
		}
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"message": "If an account with that email exists, a login link is on its way.",
	})
}

// sendMagicLink generates and stores a token and emails the login link. Run in a background
// goroutine by RequestMagicLink so its variable cost (DB write + mailer round-trip) never shows
// up in the HTTP response timing.
func (h *Handler) sendMagicLink(ctx context.Context, userID, email string) {
	rawBytes := make([]byte, 32)
	if _, err := rand.Read(rawBytes); err != nil {
		h.logger.ErrorContext(ctx, "magic link: rand", "error", err)
		return
	}
	raw := hex.EncodeToString(rawBytes)
	sum := sha256.Sum256([]byte(raw))
	tokenHash := hex.EncodeToString(sum[:])
	expiresAt := time.Now().UTC().Add(magicLinkTTL).Format(time.RFC3339)

	if _, err := h.db.ExecContext(ctx,
		`INSERT INTO magic_link_tokens (token_hash, user_id, expires_at) VALUES (?, ?, ?)`,
		tokenHash, userID, expiresAt); err != nil {
		h.logger.ErrorContext(ctx, "magic link: store token", "error", err)
		return
	}

	link := h.baseURL + "/v1/auth/magic-link/verify?token=" + raw
	if h.mailer != nil {
		if err := h.mailer.Send(ctx, magicLinkMessage(email, link)); err != nil {
			h.logger.ErrorContext(ctx, "magic link: send email", "error", err, "user_id", userID)
		}
	}
}

// VerifyMagicLink handles GET /v1/auth/magic-link/verify?token=… — consumes the token, starts
// a session, and redirects into the admin app. Invalid/expired/used tokens redirect to the
// login page with an error flag.
func (h *Handler) VerifyMagicLink(w http.ResponseWriter, r *http.Request) {
	fail := func() {
		http.Redirect(w, r, h.baseURL+"/admin/login?error=link", http.StatusFound)
	}
	raw := r.URL.Query().Get("token")
	if raw == "" {
		fail()
		return
	}
	sum := sha256.Sum256([]byte(raw))
	tokenHash := hex.EncodeToString(sum[:])
	now := time.Now().UTC().Format(time.RFC3339)

	// Atomically consume: only succeeds if unused and unexpired (single-use, race-safe).
	res, err := h.db.ExecContext(r.Context(),
		`UPDATE magic_link_tokens SET used_at = ? WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?`,
		now, tokenHash, now)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "magic link: consume", "error", err)
		fail()
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		fail() // not found, expired, or already used
		return
	}

	var userID string
	var archivedAt sql.NullString
	if err := h.db.QueryRowContext(r.Context(),
		`SELECT u.id, u.archived_at FROM magic_link_tokens t JOIN users u ON u.id = t.user_id WHERE t.token_hash = ?`,
		tokenHash).Scan(&userID, &archivedAt); err != nil || archivedAt.Valid {
		fail()
		return
	}

	if err := h.createSession(r.Context(), w, userID); err != nil {
		h.logger.ErrorContext(r.Context(), "magic link: create session", "error", err)
		fail()
		return
	}
	http.Redirect(w, r, h.baseURL+"/admin/", http.StatusFound)
}

// magicLinkMessage builds the login-link email (multipart text + minimal HTML).
func magicLinkMessage(to, link string) mailer.Message {
	return mailer.Message{
		To:      []string{to},
		Subject: "Your Calnode login link",
		Text: "Click the link below to sign in to Calnode. It expires in 15 minutes and can be used once.\n\n" +
			link + "\n\nIf you didn't request this, you can ignore this email.",
		HTML: fmt.Sprintf(`<div style="font-family:-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;color:#111827;line-height:1.5">`+
			`<p>Click the button below to sign in to Calnode. It expires in 15 minutes and can be used once.</p>`+
			`<p style="margin:24px 0"><a href="%s" style="background:#111827;color:#fff;text-decoration:none;padding:10px 18px;border-radius:8px;display:inline-block;font-weight:600">Sign in to Calnode</a></p>`+
			`<p style="font-size:13px;color:#6b7280">Or paste this link into your browser:<br><a href="%s">%s</a></p>`+
			`<p style="font-size:13px;color:#6b7280">If you didn't request this, you can ignore this email.</p></div>`,
			link, link, link),
	}
}
