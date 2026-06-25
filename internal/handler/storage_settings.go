package handler

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
)

// The Storage settings page surfaces both object-storage uses in one place: the Litestream DB
// backups (configured via environment, so read-only here) and meeting recordings (which reuse
// the same bucket under a recordings/ prefix). The only editable knob is the recording toggle.

// GetStorageSettings handles GET /v1/settings/storage (admin).
func (h *Handler) GetStorageSettings(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok || !user.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
		return
	}
	replica := os.Getenv("LITESTREAM_REPLICA_URL")
	bucket := strings.TrimPrefix(replica, "s3://")
	if i := strings.IndexByte(bucket, '/'); i >= 0 {
		bucket = bucket[:i]
	}
	_, recReady := recordingStorage()
	h.writeJSON(w, http.StatusOK, map[string]any{
		"backups_configured":       replica != "",
		"backups_bucket":           bucket,
		"backups_endpoint":         os.Getenv("LITESTREAM_ENDPOINT"),
		"recordings_enabled":       h.recordingsEnabled(r.Context()),
		"recordings_storage_ready": recReady,
		"recordings_prefix":        "recordings/",
	})
}

// PatchStorageSettings handles PATCH /v1/settings/storage (admin) — toggles meeting recording.
func (h *Handler) PatchStorageSettings(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok || !user.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var req struct {
		RecordingsEnabled bool `json:"recordings_enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	v := 0
	if req.RecordingsEnabled {
		v = 1
	}
	if _, err := h.db.ExecContext(r.Context(),
		`UPDATE server_settings SET recordings_enabled = ?, updated_at = datetime('now') WHERE id = 1`, v); err != nil {
		h.logger.ErrorContext(r.Context(), "storage settings: update", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.GetStorageSettings(w, r)
}
