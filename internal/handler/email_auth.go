package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

const maxPasswordLen = 72 // bcrypt silently truncates at 72 bytes

// dummyHash is compared when no user is found so response time is constant
// and an attacker cannot enumerate valid emails via timing differences.
// Must use DefaultCost to match the cost of real stored hashes.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("dummy-constant-string"), bcrypt.DefaultCost)

func validatePassword(p string) string {
	if len(p) < 8 {
		return "password must be at least 8 characters"
	}
	if len(p) > maxPasswordLen {
		return "password must be 72 characters or fewer"
	}
	return ""
}

// LoginEmail handles POST /v1/auth/login/email — email + password authentication.
func (h *Handler) LoginEmail(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if req.Email == "" || req.Password == "" {
		h.writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}

	var userID, storedHash string
	var emailLogin int
	var archivedAt sql.NullString
	err := h.db.QueryRowContext(r.Context(),
		`SELECT id, COALESCE(password_hash,''), email_login, archived_at FROM users WHERE email = ?`,
		req.Email).Scan(&userID, &storedHash, &emailLogin, &archivedAt)

	// Always run bcrypt to prevent user enumeration via timing side-channel.
	hashToCompare := storedHash
	if err != nil || emailLogin == 0 || storedHash == "" {
		hashToCompare = string(dummyHash)
	}
	bcryptErr := bcrypt.CompareHashAndPassword([]byte(hashToCompare), []byte(req.Password))

	if err != nil || emailLogin == 0 || storedHash == "" || bcryptErr != nil {
		h.writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}

	// Credentials are valid — but an archived member can't sign in. Tell them why
	// (only after the password check, so this never leaks archived status to a
	// wrong-password attempt).
	if archivedAt.Valid {
		h.writeError(w, http.StatusForbidden,
			"Your account has been archived. If you think this is an error, please contact your workspace admin.")
		return
	}

	if err := h.createSession(r.Context(), w, userID); err != nil {
		h.logger.ErrorContext(r.Context(), "email login: create session", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ChangePassword handles POST /v1/users/me/password — changes the authenticated
// user's own password. Requires email_login = 1 and the current password.
func (h *Handler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if msg := validatePassword(req.NewPassword); msg != "" {
		h.writeError(w, http.StatusBadRequest, msg)
		return
	}

	var storedHash string
	var emailLogin int
	if err := h.db.QueryRowContext(r.Context(),
		`SELECT COALESCE(password_hash,''), email_login FROM users WHERE id = ?`, user.ID).
		Scan(&storedHash, &emailLogin); err != nil {
		h.logger.ErrorContext(r.Context(), "change password: db query", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if emailLogin == 0 || storedHash == "" {
		h.writeError(w, http.StatusBadRequest, "email login is not enabled for this account")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(req.CurrentPassword)); err != nil {
		h.writeError(w, http.StatusUnauthorized, "current password is incorrect")
		return
	}

	newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "change password: bcrypt", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if _, err := h.db.ExecContext(r.Context(),
		`UPDATE users SET password_hash = ? WHERE id = ?`, string(newHash), user.ID); err != nil {
		h.logger.ErrorContext(r.Context(), "change password: update", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// AdminSetPassword handles POST /v1/users/{id}/password — admin resets another
// user's password without needing the current password. Admins must use
// ChangePassword to change their own password (which requires the current one).
func (h *Handler) AdminSetPassword(w http.ResponseWriter, r *http.Request) {
	admin, ok := userFromContext(r.Context())
	if !ok || !admin.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
		return
	}
	targetID := r.PathValue("id")
	if targetID == "" {
		h.writeError(w, http.StatusBadRequest, "user id is required")
		return
	}
	if targetID == admin.ID {
		h.writeError(w, http.StatusBadRequest, "use POST /v1/users/me/password to change your own password")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if msg := validatePassword(req.Password); msg != "" {
		h.writeError(w, http.StatusBadRequest, msg)
		return
	}

	// Load the target's role so we don't allow a privilege-escalation path: an
	// admin must not be able to reset the owner's (or another admin's) password
	// and then log in as them. Only the owner can reset an admin's password, and
	// the owner's own password is changed only via /v1/users/me/password.
	var targetIsAdmin, targetIsOwner int
	err := h.db.QueryRowContext(r.Context(),
		`SELECT is_admin, is_owner FROM users WHERE id = ?`, targetID).
		Scan(&targetIsAdmin, &targetIsOwner)
	if errors.Is(err, sql.ErrNoRows) {
		h.writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin set password: db query", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if targetIsOwner != 0 {
		h.writeError(w, http.StatusForbidden, "the owner's password can only be changed by the owner")
		return
	}
	if targetIsAdmin != 0 && !admin.IsOwner {
		h.writeError(w, http.StatusForbidden, "only the workspace owner can reset an admin's password")
		return
	}

	newHash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "admin set password: bcrypt", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if _, err := h.db.ExecContext(r.Context(),
		`UPDATE users SET password_hash = ?, email_login = 1 WHERE id = ?`,
		string(newHash), targetID); err != nil {
		h.logger.ErrorContext(r.Context(), "admin set password: update", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
