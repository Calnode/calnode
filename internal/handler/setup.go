package handler

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/calnode/calnode/internal/uid"
)

// Setup handles POST /v1/setup — creates the first user and API key.
// Returns 409 if the workspace is already configured.
func (h *Handler) Setup(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	var req struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Timezone string `json:"timezone"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Name == "" || req.Email == "" {
		h.writeError(w, http.StatusBadRequest, "name and email are required")
		return
	}
	if req.Timezone == "" {
		req.Timezone = "UTC"
	}
	if _, err := time.LoadLocation(req.Timezone); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid timezone: "+req.Timezone)
		return
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		h.logger.ErrorContext(r.Context(), "setup: rand", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	plainKey := "cno_" + hex.EncodeToString(raw)
	keyHash := hashAPIKey(plainKey)

	userID := uid.New()
	keyID := uid.New()
	now := time.Now().UTC().Format(time.RFC3339Nano)

	// Begin the transaction before the existence check so that the check and
	// the inserts are atomic. With the single-connection pool, BeginTx
	// serialises all Setup callers at the DB level.
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "setup: begin tx", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback() //nolint:errcheck

	var count int
	if err := tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		h.logger.ErrorContext(r.Context(), "setup: count users", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if count > 0 {
		h.writeError(w, http.StatusConflict, "workspace already configured")
		return
	}

	if _, err := tx.ExecContext(r.Context(), `
		INSERT INTO users (id, email, name, iana_timezone, is_admin)
		VALUES (?, ?, ?, ?, 1)`,
		userID, req.Email, req.Name, req.Timezone); err != nil {
		h.logger.ErrorContext(r.Context(), "setup: insert user", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if _, err := tx.ExecContext(r.Context(), `
		INSERT INTO api_keys (id, user_id, name, key_hash, created_at)
		VALUES (?, ?, 'default', ?, ?)`,
		keyID, userID, keyHash, now); err != nil {
		h.logger.ErrorContext(r.Context(), "setup: insert api key", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := tx.Commit(); err != nil {
		h.logger.ErrorContext(r.Context(), "setup: commit", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.writeJSON(w, http.StatusCreated, map[string]any{
		"api_key": plainKey,
		"user_id": userID,
		"note":    "save this API key — it will not be shown again",
	})
}

// GetMe handles GET /v1/users/me.
func (h *Handler) GetMe(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	h.writeJSON(w, http.StatusOK, map[string]any{
		"id":       user.ID,
		"email":    user.Email,
		"name":     user.Name,
		"timezone": user.IANATZ,
		"is_admin": user.IsAdmin,
	})
}
