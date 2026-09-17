package handler

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/calnode/calnode/internal/mailer"
)

// passwordResetTTL is how long an emailed reset link stays usable. It is longer than the
// magic link's 15 minutes only to survive slow delivery: greylisting mail servers commonly
// hold a first message from a new sender for 5 to 15 minutes, and a link that is dead on
// arrival sends the user straight back to the form. It stays at the short end of the usual
// range otherwise, because a reset link is exactly as powerful as a login link.
const passwordResetTTL = 30 * time.Minute

// passwordResetCooldown is the minimum gap between two reset emails for one account.
// The per-IP limit on the route cannot provide this, because a sender with many
// addresses gets many buckets. It matters for more than inbox flooding: every new
// request invalidates the account's outstanding link, so without a cooldown anyone who
// knows the address could keep the owner's links dying faster than they can click them.
// With it, the newest link always survives at least this long.
const passwordResetCooldown = time.Minute

// passwordResetAccepted is the one response the request endpoint ever gives to a
// well-formed body. It must not vary with whether the account exists, is archived, signs
// in with SSO only, is inside its cooldown, or whether email is configured at all.
const passwordResetAccepted = "If that email belongs to an account that signs in with a password, a reset link is on its way."

// passwordResetInvalid is the single answer for every token that cannot be used: unknown,
// expired, already used, superseded by a newer request, or belonging to an account that
// has since been archived or no longer signs in with a password.
const passwordResetInvalid = "This reset link is invalid or has expired. Request a new one."

// resetEligibleUser is the SQL predicate, over users aliased u, for an account that may
// reset its password by email. It is LoginEmail's own condition for a password sign-in to
// be possible (email_login set and a hash present) plus not archived, so the reset can
// never hand a password to an account that could not have used one: an SSO-only member
// gets a password from an admin, not from their inbox. Request, check and confirm all
// read this one definition so the three cannot drift apart.
const resetEligibleUser = `u.archived_at IS NULL AND u.email_login = 1 AND COALESCE(u.password_hash, '') != ''`

func hashPasswordResetToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// RequestPasswordReset handles POST /v1/auth/password-reset/request (public). It answers
// immediately with the same status and body for every well-formed request, and does no
// work that depends on the account before answering: the user lookup, the cooldown check,
// the token write and the email all run in the background. Magic links (magic_link.go)
// look the user up inline and move only the send off the request; this goes one step
// further so there is not even a found-versus-missing query to time.
//
// It is a goroutine rather than a worker job for two reasons. A job's payload is stored,
// so the raw token cannot travel in it, and minting inside the job would mean enqueueing
// for every address typed, known or not, which writes attacker-chosen strings into a
// jobs table that nothing purges (and that Litestream, where configured, replicates
// offsite). Enqueueing only for known accounts would put an account-dependent write back
// on the request path.
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
	// isEmailEnabled is instance configuration, not a property of the account, so
	// branching on it here tells a caller nothing they could not read from
	// GET /v1/auth/status.
	if email != "" && h.isEmailEnabled() {
		ctx := context.WithoutCancel(r.Context())
		h.resetSends.Go(func() { h.sendPasswordReset(ctx, email) })
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"message": passwordResetAccepted,
	})
}

// sendPasswordReset mints a reset token for email's account and mails the link, when the
// account is eligible and outside its cooldown. Every other outcome is silent by design:
// the requester has already been answered, and logging an unknown address would copy
// attacker-supplied strings into the logs.
func (h *Handler) sendPasswordReset(ctx context.Context, email string) {
	var userID string
	err := h.db.QueryRowContext(ctx,
		`SELECT u.id FROM users u WHERE u.email = ? AND `+resetEligibleUser, email).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return
	}
	if err != nil {
		h.logger.ErrorContext(ctx, "password reset: look up user", "error", err)
		return
	}

	rawBytes := make([]byte, 32)
	if _, err := rand.Read(rawBytes); err != nil {
		h.logger.ErrorContext(ctx, "password reset: rand", "error", err)
		return
	}
	raw := hex.EncodeToString(rawBytes)
	now := time.Now().UTC()

	// Cooldown check, invalidation and insert in one transaction. With the single SQLite
	// connection that serialises two concurrent requests for the same account, so both
	// cannot pass the cooldown and mint.
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.logger.ErrorContext(ctx, "password reset: begin tx", "error", err)
		return
	}
	defer tx.Rollback() //nolint:errcheck

	var recent int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM password_reset_tokens WHERE user_id = ? AND created_at > ?`,
		userID, now.Add(-passwordResetCooldown).Format(time.RFC3339)).Scan(&recent); err != nil {
		h.logger.ErrorContext(ctx, "password reset: cooldown check", "error", err)
		return
	}
	if recent > 0 {
		h.logger.InfoContext(ctx, "password reset: skipped, a link was sent within the cooldown",
			"user_id", userID, "cooldown", passwordResetCooldown.String())
		return
	}
	// Only one live link per account: a new request supersedes the old one, the same
	// rule issueInvite applies to invites.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM password_reset_tokens WHERE user_id = ? AND used_at IS NULL`, userID); err != nil {
		h.logger.ErrorContext(ctx, "password reset: invalidate older links", "error", err)
		return
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO password_reset_tokens (token_hash, user_id, expires_at, created_at) VALUES (?, ?, ?, ?)`,
		hashPasswordResetToken(raw), userID,
		now.Add(passwordResetTTL).Format(time.RFC3339), now.Format(time.RFC3339)); err != nil {
		h.logger.ErrorContext(ctx, "password reset: store token", "error", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.logger.ErrorContext(ctx, "password reset: commit", "error", err)
		return
	}

	// The link is built from the configured BASE_URL and never from anything in the
	// request. A reset link assembled from the Host header lets whoever sends the request
	// choose the domain the victim's token is delivered to.
	//
	// The token travels in the fragment, unlike invites (path) and magic links (query).
	// A fragment is never sent to the server, so it stays out of reverse-proxy access
	// logs, and it is never included in a Referer, including the one on the admin app's
	// font request. The reset page reads it client-side and strips it from the address bar.
	link := h.baseURL + "/admin/reset-password#token=" + raw
	if err := h.mailer.Send(ctx, passwordResetMessage(email, link)); err != nil {
		h.logger.ErrorContext(ctx, "password reset: send email", "error", err, "user_id", userID)
	}
}

// passwordResetTokenRequest is the body of the check and confirm endpoints. The token is
// posted rather than put in a URL for the same reason the email link carries it in a
// fragment: nothing that logs request lines ever sees it.
type passwordResetTokenRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// CheckPasswordReset handles POST /v1/auth/password-reset/check (public). It reports
// whether a token can still be used, without consuming it, and returns the account's
// email so the reset page can say whose password is being changed and give password
// managers a username to save the new password against. The email reveals nothing the
// token holder cannot already take: the token alone is enough to set the password.
func (h *Handler) CheckPasswordReset(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var req passwordResetTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	email, _, err := h.lookupPasswordResetToken(r.Context(), strings.TrimSpace(req.Token))
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			h.logger.ErrorContext(r.Context(), "password reset check: lookup", "error", err)
		}
		h.writeError(w, http.StatusNotFound, passwordResetInvalid)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"email": email})
}

// lookupPasswordResetToken returns the email and user id behind a usable token, or
// sql.ErrNoRows when there is none (unknown, expired, used, or the account no longer
// qualifies).
func (h *Handler) lookupPasswordResetToken(ctx context.Context, raw string) (email, userID string, err error) {
	if raw == "" {
		return "", "", sql.ErrNoRows
	}
	err = h.db.QueryRowContext(ctx, `
		SELECT u.email, u.id FROM password_reset_tokens t
		JOIN users u ON u.id = t.user_id
		WHERE t.token_hash = ? AND t.used_at IS NULL AND t.expires_at > ? AND `+resetEligibleUser,
		hashPasswordResetToken(raw), time.Now().UTC().Format(time.RFC3339)).Scan(&email, &userID)
	return email, userID, err
}

// ConfirmPasswordReset handles POST /v1/auth/password-reset/confirm (public). It consumes
// the token, sets the new password, revokes every session the account holds, and then
// signs the caller in, as claiming an invite and following a magic link both do.
//
// Order matters in three places:
//   - The password policy is checked before anything else, so a rejected password does
//     not spend the link.
//   - bcrypt runs before the transaction. At DefaultCost it takes long enough that
//     holding the single SQLite connection across it would stall every other request.
//     A cheap non-consuming lookup comes first so an unusable token never costs a hash.
//   - The token is consumed by a conditional UPDATE inside the transaction, and that
//     UPDATE is the authority: two concurrent confirms can both pass the lookup, and
//     exactly one of them will see a row affected.
func (h *Handler) ConfirmPasswordReset(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var req passwordResetTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	token := strings.TrimSpace(req.Token)
	if msg := validatePassword(req.Password); msg != "" {
		h.writeError(w, http.StatusBadRequest, msg)
		return
	}
	if _, _, err := h.lookupPasswordResetToken(r.Context(), token); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			h.logger.ErrorContext(r.Context(), "password reset confirm: lookup", "error", err)
		}
		h.writeError(w, http.StatusNotFound, passwordResetInvalid)
		return
	}

	newHash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "password reset confirm: bcrypt", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	tokenHash := hashPasswordResetToken(token)
	now := time.Now().UTC().Format(time.RFC3339)

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "password reset confirm: begin tx", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback() //nolint:errcheck

	res, err := tx.ExecContext(r.Context(),
		`UPDATE password_reset_tokens SET used_at = ? WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?`,
		now, tokenHash, now)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "password reset confirm: consume token", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		h.writeError(w, http.StatusNotFound, passwordResetInvalid) // lost a race, or expired since the lookup
		return
	}

	var userID string
	if err := tx.QueryRowContext(r.Context(),
		`SELECT user_id FROM password_reset_tokens WHERE token_hash = ?`, tokenHash).Scan(&userID); err != nil {
		h.logger.ErrorContext(r.Context(), "password reset confirm: load user", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// The eligibility predicate is re-applied at the write, not only at the lookup above,
	// so an account archived (or moved off password sign-in) during the bcrypt cannot slip
	// through. Refusing rolls the consume back: like a session, a link is judged against
	// the account as it is when used, which is how archiving works everywhere else.
	res, err = tx.ExecContext(r.Context(),
		`UPDATE users AS u SET password_hash = ? WHERE u.id = ? AND `+resetEligibleUser,
		string(newHash), userID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "password reset confirm: update password", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		h.writeError(w, http.StatusNotFound, passwordResetInvalid)
		return
	}

	// Revoke every session, as AdminSetPassword does. A reset is what someone does when
	// they believe their password is known to someone else, and a stolen session cookie
	// must not outlive that. Any other outstanding link for the account dies too.
	if _, err := tx.ExecContext(r.Context(), `DELETE FROM sessions WHERE user_id = ?`, userID); err != nil {
		h.logger.ErrorContext(r.Context(), "password reset confirm: revoke sessions", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if _, err := tx.ExecContext(r.Context(),
		`DELETE FROM password_reset_tokens WHERE user_id = ? AND token_hash != ?`, userID, tokenHash); err != nil {
		h.logger.ErrorContext(r.Context(), "password reset confirm: invalidate other links", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := tx.Commit(); err != nil {
		h.logger.ErrorContext(r.Context(), "password reset confirm: commit", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err := h.createSession(r.Context(), w, userID); err != nil {
		// The password is already changed; the user can sign in with it normally.
		h.logger.ErrorContext(r.Context(), "password reset confirm: create session", "error", err)
		h.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "signed_in": false})
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "signed_in": true})
}

// passwordResetMessage builds the reset email (plain text plus minimal HTML), in the same
// form as magicLinkMessage. English only, like every other message addressed to a member
// rather than a booker: the admin app is not translated (ARCHITECTURE §23).
func passwordResetMessage(to, link string) mailer.Message {
	minutes := int(passwordResetTTL.Minutes())
	escaped := html.EscapeString(link)
	return mailer.Message{
		To:      []string{to},
		Subject: "Reset your Calnode password",
		Text: fmt.Sprintf("Someone asked to reset the password for the Calnode account %s. "+
			"Click the link below to choose a new one. It expires in %d minutes and can be used once.\n\n"+
			"%s\n\nIf you didn't ask for this, you can ignore this email. Your password has not been changed.",
			to, minutes, link),
		HTML: fmt.Sprintf(`<div style="font-family:-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;color:#111827;line-height:1.5">`+
			`<p>Someone asked to reset the password for the Calnode account %s. Click the button below to choose a new one. It expires in %d minutes and can be used once.</p>`+
			`<p style="margin:24px 0"><a href="%s" style="background:#111827;color:#fff;text-decoration:none;padding:10px 18px;border-radius:8px;display:inline-block;font-weight:600">Reset password</a></p>`+
			`<p style="font-size:13px;color:#6b7280">Or paste this link into your browser:<br><a href="%s">%s</a></p>`+
			`<p style="font-size:13px;color:#6b7280">If you didn't ask for this, you can ignore this email. Your password has not been changed.</p></div>`,
			html.EscapeString(to), minutes, escaped, escaped, escaped),
	}
}
