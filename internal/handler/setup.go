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
	out := map[string]any{
		"id":          user.ID,
		"email":       user.Email,
		"name":        user.Name,
		"timezone":    user.IANATZ,
		"time_format": user.TimeFormat,
		"week_start":  user.WeekStart,
		"date_format": user.DateFormat,
		"is_admin":    user.IsAdmin,
	}
	if user.AvatarURL != "" {
		out["avatar_url"] = user.AvatarURL
	}
	h.writeJSON(w, http.StatusOK, out)
}

// PatchMe handles PATCH /v1/users/me — updates timezone, time_format, week_start, date_format.
func (h *Handler) PatchMe(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)

	var req struct {
		Timezone   *string `json:"timezone"`
		TimeFormat *string `json:"time_format"`
		WeekStart  *int    `json:"week_start"`
		DateFormat *string `json:"date_format"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	current := struct {
		Timezone   string
		TimeFormat string
		WeekStart  int
		DateFormat string
	}{user.IANATZ, user.TimeFormat, user.WeekStart, user.DateFormat}

	if req.Timezone != nil {
		if _, err := time.LoadLocation(*req.Timezone); err != nil {
			h.writeError(w, http.StatusBadRequest, "invalid timezone: "+*req.Timezone)
			return
		}
		current.Timezone = *req.Timezone
	}
	if req.TimeFormat != nil {
		if *req.TimeFormat != "12h" && *req.TimeFormat != "24h" {
			h.writeError(w, http.StatusBadRequest, "time_format must be '12h' or '24h'")
			return
		}
		current.TimeFormat = *req.TimeFormat
	}
	if req.WeekStart != nil {
		if *req.WeekStart < 0 || *req.WeekStart > 6 {
			h.writeError(w, http.StatusBadRequest, "week_start must be 0 (Sunday) through 6 (Saturday)")
			return
		}
		current.WeekStart = *req.WeekStart
	}
	if req.DateFormat != nil {
		switch *req.DateFormat {
		case "dmy", "mdy", "ymd":
			current.DateFormat = *req.DateFormat
		default:
			h.writeError(w, http.StatusBadRequest, "date_format must be 'dmy', 'mdy', or 'ymd'")
			return
		}
	}

	if _, err := h.db.ExecContext(r.Context(), `
		UPDATE users SET iana_timezone = ?, time_format = ?, week_start = ?, date_format = ? WHERE id = ?`,
		current.Timezone, current.TimeFormat, current.WeekStart, current.DateFormat, user.ID); err != nil {
		h.logger.ErrorContext(r.Context(), "patch me", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	out := map[string]any{
		"id":          user.ID,
		"email":       user.Email,
		"name":        user.Name,
		"timezone":    current.Timezone,
		"time_format": current.TimeFormat,
		"week_start":  current.WeekStart,
		"date_format": current.DateFormat,
		"is_admin":    user.IsAdmin,
	}
	if user.AvatarURL != "" {
		out["avatar_url"] = user.AvatarURL
	}
	h.writeJSON(w, http.StatusOK, out)
}
