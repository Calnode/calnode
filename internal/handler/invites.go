package handler

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

	// Invalidate any existing unused invite for this email so there's only one live link.
	h.db.ExecContext(r.Context(), //nolint:errcheck
		`DELETE FROM invite_tokens WHERE email = ? AND used_at IS NULL`, req.Email)

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		h.logger.ErrorContext(r.Context(), "invite: rand", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	token := hex.EncodeToString(raw)
	tokenHash := hashInviteToken(token)
	id := uid.New()
	expiresAt := time.Now().UTC().Add(inviteDuration).Format(time.RFC3339)

	if _, err := h.db.ExecContext(r.Context(), `
		INSERT INTO invite_tokens (id, email, token_hash, created_by, expires_at)
		VALUES (?, ?, ?, ?, ?)`,
		id, req.Email, tokenHash, admin.ID, expiresAt); err != nil {
		h.logger.ErrorContext(r.Context(), "invite: insert", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	inviteURL := h.baseURL + "/admin/invite/" + token

	// If SMTP is configured, send the invite email automatically.
	if h.isEmailEnabled() {
		_ = h.mailer.Send(r.Context(), mailer.Message{
			To:      []string{req.Email},
			Subject: "You've been invited to Calnode",
			Text: "You've been invited to join Calnode by " + admin.Name + ".\n\n" +
				"Click the link below to set up your account. The link expires in 7 days " +
				"and is locked to this email address.\n\n" + inviteURL + "\n\n" +
				"If you weren't expecting this invite, you can safely ignore this email.",
		})
	}

	h.writeJSON(w, http.StatusCreated, map[string]any{
		"id":           id,
		"email":        req.Email,
		"invite_url":   inviteURL,
		"expires_at":   expiresAt,
		"email_sent":   h.isEmailEnabled(),
		"note":         "This link expires in 7 days and is locked to " + req.Email + ". It cannot be used by anyone else.",
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
