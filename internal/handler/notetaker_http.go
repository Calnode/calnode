package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/secret"
)

// GetNotetakerSettings handles GET /v1/settings/notetaker (admin). Never returns the key.
func (h *Handler) GetNotetakerSettings(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok || !user.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
		return
	}
	var enabled int
	var keyEnc string
	err := h.db.QueryRowContext(r.Context(),
		`SELECT COALESCE(notetaker_enabled,0), COALESCE(stt_api_key_enc,'') FROM server_settings WHERE id = 1`).
		Scan(&enabled, &keyEnc)
	if err != nil && err != sql.ErrNoRows {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"enabled":         enabled != 0,
		"stt_api_key_set": keyEnc != "",
	})
}

// PatchNotetakerSettings handles PATCH /v1/settings/notetaker (admin). An empty stt_api_key keeps
// the stored one (use the toggle to turn the feature off).
func (h *Handler) PatchNotetakerSettings(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok || !user.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var req struct {
		Enabled *bool   `json:"enabled"`
		STTKey  *string `json:"stt_api_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Enabled != nil {
		v := 0
		if *req.Enabled {
			v = 1
		}
		if _, err := h.db.ExecContext(r.Context(),
			`UPDATE server_settings SET notetaker_enabled = ?, updated_at = datetime('now') WHERE id = 1`, v); err != nil {
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}
	if req.STTKey != nil {
		if k := strings.TrimSpace(*req.STTKey); k != "" {
			enc, err := secret.Encrypt(h.encKey, k)
			if err != nil {
				h.writeError(w, http.StatusInternalServerError, "internal error")
				return
			}
			if _, err := h.db.ExecContext(r.Context(),
				`UPDATE server_settings SET stt_api_key_enc = ?, updated_at = datetime('now') WHERE id = 1`, enc); err != nil {
				h.writeError(w, http.StatusInternalServerError, "internal error")
				return
			}
		}
	}
	h.GetNotetakerSettings(w, r)
}

// GetBookingNotes handles GET /v1/bookings/{id}/notes (admin) — the AI notes for a booking.
func (h *Handler) GetBookingNotes(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok || !user.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
		return
	}
	var content, status, updatedAt string
	err := h.db.QueryRowContext(r.Context(),
		`SELECT content, status, updated_at FROM notes WHERE booking_id = ?`, r.PathValue("id")).
		Scan(&content, &status, &updatedAt)
	if err == sql.ErrNoRows {
		h.writeJSON(w, http.StatusOK, map[string]any{"exists": false})
		return
	}
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"exists": true, "content": content, "status": status, "updated_at": updatedAt,
	})
}

// RegenerateBookingNotes handles POST /v1/bookings/{id}/notes/regenerate (admin) — re-runs the LLM
// summary over the booking's existing transcript(s). 409 if there's no transcript yet, 424 if no LLM.
func (h *Handler) RegenerateBookingNotes(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok || !user.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
		return
	}
	bookingID := r.PathValue("id")
	var n int
	_ = h.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM transcripts WHERE booking_id = ? AND status = 'complete'`, bookingID).Scan(&n)
	if n == 0 {
		h.writeError(w, http.StatusConflict, "no transcript to summarise yet")
		return
	}
	if h.getLLM() == nil {
		h.writeError(w, http.StatusFailedDependency, "no LLM configured")
		return
	}
	// Run the summary inline (not via the worker job) so the freshly generated notes go straight
	// back in the response — the panel fills in immediately, no refetch. Bounded so a slow model
	// can't hold the request open indefinitely.
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	content, err := h.summarizeBooking(ctx, bookingID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "notetaker: regenerate summary", "error", err, "booking_id", bookingID)
		h.writeError(w, http.StatusBadGateway, "the summary model could not be reached — try again")
		return
	}
	status := "complete"
	if content == "" {
		status = "empty"
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"exists":  content != "",
		"content": content,
		"status":  status,
	})
}

// GetBookingTranscript handles GET /v1/bookings/{id}/transcript (admin) — the raw transcript.
func (h *Handler) GetBookingTranscript(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok || !user.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
		return
	}
	rows, err := h.db.QueryContext(r.Context(),
		`SELECT text FROM transcripts WHERE booking_id = ? AND status = 'complete' ORDER BY created_at`,
		r.PathValue("id"))
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	var parts []string
	for rows.Next() {
		var t string
		if rows.Scan(&t) == nil && t != "" {
			parts = append(parts, t)
		}
	}
	rows.Close()
	h.writeJSON(w, http.StatusOK, map[string]any{
		"exists": len(parts) > 0,
		"text":   strings.Join(parts, "\n\n"),
	})
}
