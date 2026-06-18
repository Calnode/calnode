package handler

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/calnode/calnode/internal/mailer"
	"github.com/calnode/calnode/internal/uid"
)

const inviteDuration = 7 * 24 * time.Hour

func hashInviteToken(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

// CreateInvite handles POST /v1/invites — admin generates an invite link for an email.
func (h *Handler) CreateInvite(w http.ResponseWriter, r *http.Request) {
	admin, ok := userFromContext(r.Context())
	if !ok || !admin.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if req.Email == "" {
		h.writeError(w, http.StatusBadRequest, "email is required")
		return
	}

	// Reject if a user with this email already exists.
	var exists int
	h.db.QueryRowContext(r.Context(), //nolint:errcheck
		`SELECT COUNT(*) FROM users WHERE email = ?`, req.Email).Scan(&exists)
	if exists > 0 {
		h.writeError(w, http.StatusConflict, "a user with this email already exists")
		return
	}

	id, inviteURL, expiresAt, err := h.issueInvite(r.Context(), req.Email, admin.Name, admin.ID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "invite: issue", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.writeJSON(w, http.StatusCreated, map[string]any{
		"id":         id,
		"email":      req.Email,
		"invite_url": inviteURL,
		"expires_at": expiresAt,
		"email_sent": h.isEmailEnabled(),
		"note":       "This link expires in 7 days and is locked to " + req.Email + ". It cannot be used by anyone else.",
	})
}

// issueInvite invalidates any unused invite for email, mints a fresh token, emails
// the link when SMTP is configured, and returns the invite URL + expiry. Shared by
// CreateInvite and ResendInvite so the two never diverge.
func (h *Handler) issueInvite(ctx context.Context, email, adminName, adminID string) (id, inviteURL, expiresAt string, err error) {
	// Only one live link per email.
	h.db.ExecContext(ctx, //nolint:errcheck
		`DELETE FROM invite_tokens WHERE email = ? AND used_at IS NULL`, email)

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", "", err
	}
	token := hex.EncodeToString(raw)
	id = uid.New()
	expiresAt = time.Now().UTC().Add(inviteDuration).Format(time.RFC3339)
	if _, err := h.db.ExecContext(ctx, `
		INSERT INTO invite_tokens (id, email, token_hash, created_by, expires_at)
		VALUES (?, ?, ?, ?, ?)`,
		id, email, hashInviteToken(token), adminID, expiresAt); err != nil {
		return "", "", "", err
	}

	inviteURL = h.baseURL + "/admin/invite/" + token
	if h.isEmailEnabled() {
		_ = h.mailer.Send(ctx, mailer.Message{
			To:      []string{email},
			Subject: "You've been invited to Calnode",
			Text: "You've been invited to join Calnode by " + adminName + ".\n\n" +
				"Click the link below to set up your account. The link expires in 7 days " +
				"and is locked to this email address.\n\n" + inviteURL + "\n\n" +
				"If you weren't expecting this invite, you can safely ignore this email.",
		})
	}
	return id, inviteURL, expiresAt, nil
}

// ResendInvite handles POST /v1/invites/{id}/resend — admin re-issues a pending
// invite with a fresh token + expiry and re-sends the email. The original token
// can't be recovered (only its hash is stored), so resend mints a new link.
func (h *Handler) ResendInvite(w http.ResponseWriter, r *http.Request) {
	admin, ok := userFromContext(r.Context())
	if !ok || !admin.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
		return
	}
	id := r.PathValue("id")

	var email string
	err := h.db.QueryRowContext(r.Context(),
		`SELECT email FROM invite_tokens WHERE id = ? AND used_at IS NULL`, id).Scan(&email)
	if errors.Is(err, sql.ErrNoRows) {
		h.writeError(w, http.StatusNotFound, "invite not found or already used")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "resend invite: lookup", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	newID, inviteURL, expiresAt, err := h.issueInvite(r.Context(), email, admin.Name, admin.ID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "resend invite: issue", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"id":         newID,
		"email":      email,
		"invite_url": inviteURL,
		"expires_at": expiresAt,
		"email_sent": h.isEmailEnabled(),
	})
}

// ListInvites handles GET /v1/invites — returns all pending (unused, non-expired) invites.
func (h *Handler) ListInvites(w http.ResponseWriter, r *http.Request) {
	admin, ok := userFromContext(r.Context())
	if !ok || !admin.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	rows, err := h.db.QueryContext(r.Context(), `
		SELECT id, email, expires_at, created_by
		FROM invite_tokens
		WHERE used_at IS NULL AND expires_at > ?
		ORDER BY expires_at ASC`, now)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list invites: query", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	type invite struct {
		ID        string `json:"id"`
		Email     string `json:"email"`
		ExpiresAt string `json:"expires_at"`
		CreatedBy string `json:"created_by"`
	}
	var out []invite
	for rows.Next() {
		var inv invite
		if err := rows.Scan(&inv.ID, &inv.Email, &inv.ExpiresAt, &inv.CreatedBy); err != nil {
			continue
		}
		out = append(out, inv)
	}
	if out == nil {
		out = []invite{}
	}
	h.writeJSON(w, http.StatusOK, out)
}

// RevokeInvite handles DELETE /v1/invites/{id} — admin cancels a pending invite.
func (h *Handler) RevokeInvite(w http.ResponseWriter, r *http.Request) {
	admin, ok := userFromContext(r.Context())
	if !ok || !admin.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
		return
	}
	id := r.PathValue("id")
	res, err := h.db.ExecContext(r.Context(),
		`DELETE FROM invite_tokens WHERE id = ? AND used_at IS NULL`, id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "revoke invite: delete", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		h.writeError(w, http.StatusNotFound, "invite not found or already used")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// GetInvite handles GET /v1/invites/{token} — public. Validates the token and
// returns the locked email so the claim form can pre-fill it.
func (h *Handler) GetInvite(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	tokenHash := hashInviteToken(token)
	now := time.Now().UTC().Format(time.RFC3339)

	var id, email string
	err := h.db.QueryRowContext(r.Context(), `
		SELECT id, email FROM invite_tokens
		WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?`,
		tokenHash, now).Scan(&id, &email)
	if err != nil {
		h.writeError(w, http.StatusNotFound, "invite not found or expired")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"id": id, "email": email})
}

// ClaimInvite handles POST /v1/invites/{token}/claim — public. Creates the user
// account from a valid invite token.
func (h *Handler) ClaimInvite(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	tokenHash := hashInviteToken(token)
	now := time.Now().UTC().Format(time.RFC3339)

	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var req struct {
		Name     string `json:"name"`
		Password string `json:"password"`
		Timezone string `json:"timezone"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || req.Password == "" {
		h.writeError(w, http.StatusBadRequest, "name and password are required")
		return
	}
	if msg := validatePassword(req.Password); msg != "" {
		h.writeError(w, http.StatusBadRequest, msg)
		return
	}
	if req.Timezone == "" {
		req.Timezone = "UTC"
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "claim invite: begin tx", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback() //nolint:errcheck

	var inviteID, email string
	if err := tx.QueryRowContext(r.Context(), `
		SELECT id, email FROM invite_tokens
		WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?`,
		tokenHash, now).Scan(&inviteID, &email); err != nil {
		h.writeError(w, http.StatusNotFound, "invite not found or expired")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "claim invite: bcrypt", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	userID := uid.New()
	if _, err := tx.ExecContext(r.Context(), `
		INSERT INTO users (id, email, name, iana_timezone, is_admin, email_login, password_hash)
		VALUES (?, ?, ?, ?, 0, 1, ?)`,
		userID, email, req.Name, req.Timezone, string(hash)); err != nil {
		h.logger.ErrorContext(r.Context(), "claim invite: insert user", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if _, err := tx.ExecContext(r.Context(),
		`UPDATE invite_tokens SET used_at = ? WHERE id = ?`, now, inviteID); err != nil {
		h.logger.ErrorContext(r.Context(), "claim invite: mark used", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err := tx.Commit(); err != nil {
		h.logger.ErrorContext(r.Context(), "claim invite: commit", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err := h.createSession(r.Context(), w, userID); err != nil {
		h.logger.ErrorContext(r.Context(), "claim invite: create session", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.writeJSON(w, http.StatusCreated, map[string]any{
		"user_id": userID,
		"email":   email,
		"name":    req.Name,
	})
}
