package handler

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
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

	// The first user is the workspace owner (and therefore an admin).
	if _, err := tx.ExecContext(r.Context(), `
		INSERT INTO users (id, email, name, iana_timezone, is_admin, is_owner)
		VALUES (?, ?, ?, ?, 1, 1)`,
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
		"is_owner":    user.IsOwner,
		"role":        user.Role(),
		// Notification preferences
		"notify_confirmation":    user.NotifyConfirmation,
		"notify_cancellation":    user.NotifyCancellation,
		"notify_reschedule":      user.NotifyReschedule,
		"notify_reminder":        user.NotifyReminder,
		"notify_host_booking":    user.NotifyHostBooking,
		"notify_host_cancel":     user.NotifyHostCancel,
		"notify_host_reschedule": user.NotifyHostReschedule,
	}
	if user.AvatarURL != "" {
		out["avatar_url"] = user.AvatarURL
	}
	h.writeJSON(w, http.StatusOK, out)
}

// PatchMe handles PATCH /v1/users/me — updates timezone, time_format, week_start, date_format, and notification prefs.
func (h *Handler) PatchMe(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)

	var req struct {
		Name                 *string `json:"name"`
		Timezone             *string `json:"timezone"`
		TimeFormat           *string `json:"time_format"`
		WeekStart            *int    `json:"week_start"`
		DateFormat           *string `json:"date_format"`
		NotifyConfirmation   *bool   `json:"notify_confirmation"`
		NotifyCancellation   *bool   `json:"notify_cancellation"`
		NotifyReschedule     *bool   `json:"notify_reschedule"`
		NotifyReminder       *bool   `json:"notify_reminder"`
		NotifyHostBooking    *bool   `json:"notify_host_booking"`
		NotifyHostCancel     *bool   `json:"notify_host_cancel"`
		NotifyHostReschedule *bool   `json:"notify_host_reschedule"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	current := struct {
		Name                 string
		Timezone             string
		TimeFormat           string
		WeekStart            int
		DateFormat           string
		NotifyConfirmation   bool
		NotifyCancellation   bool
		NotifyReschedule     bool
		NotifyReminder       bool
		NotifyHostBooking    bool
		NotifyHostCancel     bool
		NotifyHostReschedule bool
	}{
		user.Name, user.IANATZ, user.TimeFormat, user.WeekStart, user.DateFormat,
		user.NotifyConfirmation, user.NotifyCancellation, user.NotifyReschedule, user.NotifyReminder,
		user.NotifyHostBooking, user.NotifyHostCancel, user.NotifyHostReschedule,
	}

	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			h.writeError(w, http.StatusBadRequest, "name cannot be empty")
			return
		}
		if len(name) > 200 {
			h.writeError(w, http.StatusBadRequest, "name is too long (max 200 characters)")
			return
		}
		current.Name = name
	}
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
	if req.NotifyConfirmation != nil {
		current.NotifyConfirmation = *req.NotifyConfirmation
	}
	if req.NotifyCancellation != nil {
		current.NotifyCancellation = *req.NotifyCancellation
	}
	if req.NotifyReschedule != nil {
		current.NotifyReschedule = *req.NotifyReschedule
	}
	if req.NotifyReminder != nil {
		current.NotifyReminder = *req.NotifyReminder
	}
	if req.NotifyHostBooking != nil {
		current.NotifyHostBooking = *req.NotifyHostBooking
	}
	if req.NotifyHostCancel != nil {
		current.NotifyHostCancel = *req.NotifyHostCancel
	}
	if req.NotifyHostReschedule != nil {
		current.NotifyHostReschedule = *req.NotifyHostReschedule
	}

	boolToInt := func(b bool) int {
		if b {
			return 1
		}
		return 0
	}

	if _, err := h.db.ExecContext(r.Context(), `
		UPDATE users SET
			name = ?, iana_timezone = ?, time_format = ?, week_start = ?, date_format = ?,
			notify_confirmation = ?, notify_cancellation = ?, notify_reschedule = ?, notify_reminder = ?,
			notify_host_booking = ?, notify_host_cancel = ?, notify_host_reschedule = ?
		WHERE id = ?`,
		current.Name, current.Timezone, current.TimeFormat, current.WeekStart, current.DateFormat,
		boolToInt(current.NotifyConfirmation), boolToInt(current.NotifyCancellation),
		boolToInt(current.NotifyReschedule), boolToInt(current.NotifyReminder),
		boolToInt(current.NotifyHostBooking), boolToInt(current.NotifyHostCancel),
		boolToInt(current.NotifyHostReschedule),
		user.ID); err != nil {
		h.logger.ErrorContext(r.Context(), "patch me", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	out := map[string]any{
		"id":          user.ID,
		"email":       user.Email,
		"name":        current.Name,
		"timezone":    current.Timezone,
		"time_format": current.TimeFormat,
		"week_start":  current.WeekStart,
		"date_format": current.DateFormat,
		"is_admin":    user.IsAdmin,
		"is_owner":    user.IsOwner,
		"role":        user.Role(),
		// Notification preferences
		"notify_confirmation":    current.NotifyConfirmation,
		"notify_cancellation":    current.NotifyCancellation,
		"notify_reschedule":      current.NotifyReschedule,
		"notify_reminder":        current.NotifyReminder,
		"notify_host_booking":    current.NotifyHostBooking,
		"notify_host_cancel":     current.NotifyHostCancel,
		"notify_host_reschedule": current.NotifyHostReschedule,
	}
	if user.AvatarURL != "" {
		out["avatar_url"] = user.AvatarURL
	}
	h.writeJSON(w, http.StatusOK, out)
}
