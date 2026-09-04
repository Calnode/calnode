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

// replicaBucket extracts the bucket (or Azure container) name from a LITESTREAM_REPLICA_URL.
//
// Litestream replicates to more than S3 — `gcs://`, `abs://` and `file://` are all valid — so
// the scheme is stripped generically rather than by trimming a literal "s3://". Trimming only
// s3 left every other scheme's URL intact, and the first '/' then found was the one inside
// "://": a `gcs://my-bucket/calnode` replica reported its bucket as "gcs:".
//
// Whatever is left after the scheme is the authority, up to the first path separator. For
// `file:///var/lib/calnode` that is empty, which is the honest answer — a file replica has no
// bucket — and for the empty string it stays empty, so callers can still tell "not configured"
// from "configured with no bucket".
func replicaBucket(replica string) string {
	rest := replica
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+len("://"):]
	}
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

// GetStorageSettings handles GET /v1/settings/storage (admin).
func (h *Handler) GetStorageSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	replica := os.Getenv("LITESTREAM_REPLICA_URL")
	_, recReady := recordingStorage()
	h.writeJSON(w, http.StatusOK, map[string]any{
		"backups_configured":       replica != "",
		"backups_bucket":           replicaBucket(replica),
		"backups_endpoint":         os.Getenv("LITESTREAM_ENDPOINT"),
		"recordings_enabled":       h.recordingsEnabled(r.Context()),
		"recordings_storage_ready": recReady,
		"recordings_prefix":        "recordings/",
	})
}

// PatchStorageSettings handles PATCH /v1/settings/storage (admin) — toggles meeting recording.
func (h *Handler) PatchStorageSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if h.demoMode {
		h.writeError(w, http.StatusServiceUnavailable, "not available in the demo")
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
