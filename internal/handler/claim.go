package handler

import (
	"encoding/json"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/calnode/calnode/internal/uid"
)

// AuthStatus handles GET /v1/auth/status — public endpoint the login page uses
// to determine which auth methods to show and whether the install is claimed.
func (h *Handler) AuthStatus(w http.ResponseWriter, r *http.Request) {
	var userCount int
	if err := h.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM users`).Scan(&userCount); err != nil {
		h.logger.ErrorContext(r.Context(), "auth status: db query", "error", err)
		h.writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}

	providers := []string{}
	if h.getGoogleAuth() != nil {
		providers = append(providers, "google")
	}
	if h.getMicrosoftAuth() != nil {
		providers = append(providers, "microsoft")
	}

	// Best-effort status flag: on query error emailLoginCount simply stays 0 (treated
	// as "no email-login users yet"), which is a safe default for this UI hint.
	var emailLoginCount int
	//nolint:errcheck
	// #nosec G104
	h.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM users WHERE email_login = 1`).Scan(&emailLoginCount)

	h.writeJSON(w, http.StatusOK, map[string]any{
		"claimed":         userCount > 0,
		"email_login":     emailLoginCount > 0,
		"providers":       providers,
		"smtp_configured": h.isEmailEnabled(),
	})
}

// Claim handles POST /v1/auth/claim — creates the first admin user.
// Returns 409 if any users already exist. Atomic under a transaction so two
// simultaneous first-loaders cannot both become admin.
func (h *Handler) Claim(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var req struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Password string `json:"password"`
		Timezone string `json:"timezone"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || req.Email == "" || req.Password == "" {
		h.writeError(w, http.StatusBadRequest, "name, email, and password are required")
		return
	}
	if _, err := mail.ParseAddress(req.Email); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid email address")
		return
	}
	if msg := validatePassword(req.Password); msg != "" {
		h.writeError(w, http.StatusBadRequest, msg)
		return
	}
	if req.Timezone == "" {
		req.Timezone = "UTC"
	}
	if _, err := time.LoadLocation(req.Timezone); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid timezone")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "claim: bcrypt", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "claim: begin tx", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback() //nolint:errcheck

	var count int
	if err := tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		h.logger.ErrorContext(r.Context(), "claim: count users", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if count > 0 {
		h.writeError(w, http.StatusConflict, "this installation has already been claimed — please log in")
		return
	}

	userID := uid.New()
	if _, err := tx.ExecContext(r.Context(), `
		INSERT INTO users (id, email, name, iana_timezone, is_admin, email_login, password_hash)
		VALUES (?, ?, ?, ?, 1, 1, ?)`,
		userID, req.Email, req.Name, req.Timezone, string(hash)); err != nil {
		h.logger.ErrorContext(r.Context(), "claim: insert user", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := tx.Commit(); err != nil {
		h.logger.ErrorContext(r.Context(), "claim: commit", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err := h.createSession(r.Context(), w, userID); err != nil {
		h.logger.ErrorContext(r.Context(), "claim: create session", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.writeJSON(w, http.StatusCreated, map[string]any{
		"user_id": userID,
		"email":   req.Email,
		"name":    req.Name,
	})
}
