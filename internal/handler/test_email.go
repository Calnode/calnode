package handler

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/mailer"
)

// SendTestEmail handles POST /v1/event-types/{slug}/test-email.
// It sends a sample email to the authenticated host so they can preview
// how the custom message looks inside the real email template.
func (h *Handler) SendTestEmail(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	slug := r.PathValue("slug")
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)

	var req struct {
		Type string `json:"type"` // confirmation | cancellation | reschedule | reminder
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	validTypes := map[string]bool{
		"confirmation": true, "cancellation": true,
		"reschedule": true, "reminder": true,
	}
	if !validTypes[req.Type] {
		h.writeError(w, http.StatusBadRequest, "type must be one of: confirmation, cancellation, reschedule, reminder")
		return
	}

	// Load the event type and its custom messages (verifies ownership first).
	var etName string
	var durationMinutes int
	var locVal, msgConf, msgCancel, msgResched, msgRemind sql.NullString
	err := h.db.QueryRowContext(r.Context(), `
		SELECT name, duration_minutes, location_value,
		       msg_confirmation, msg_cancellation, msg_reschedule, msg_reminder
		FROM event_types
		WHERE slug = ? AND user_id = ?`, slug, user.ID).
		Scan(&etName, &durationMinutes, &locVal,
			&msgConf, &msgCancel, &msgResched, &msgRemind)
	if err == sql.ErrNoRows {
		h.writeError(w, http.StatusNotFound, "event type not found")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "test email: load event type", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Check email is configured after verifying ownership so 404 takes precedence.
	if !h.isEmailEnabled() {
		h.writeError(w, http.StatusServiceUnavailable, "Email is not configured on this server — add SMTP settings to enable sending")
		return
	}

	// Pick the custom note for the requested email type.
	var customNote string
	switch req.Type {
	case "confirmation":
		if msgConf.Valid {
			customNote = msgConf.String
		}
	case "cancellation":
		if msgCancel.Valid {
			customNote = msgCancel.String
		}
	case "reschedule":
		if msgResched.Valid {
			customNote = msgResched.String
		}
	case "reminder":
		if msgRemind.Valid {
			customNote = msgRemind.String
		}
	}

	// Build placeholder booking data. The sample date is tomorrow at 14:00 UTC.
	if durationMinutes < 1 {
		durationMinutes = 30
	}
	start := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, 1).Add(14 * time.Hour)
	end := start.Add(time.Duration(durationMinutes) * time.Minute)

	d := mailer.BookingData{
		BookingID:        "preview-test",
		EventTypeName:    etName,
		EventTypeSlug:    slug,
		HostName:         user.Name,
		HostEmail:        user.Email,
		OrganizerName:    "Alex Johnson",
		OrganizerEmail:   user.Email, // send to host so they can review it
		OrganizerTimezone: user.IANATZ,
		StartAt:          start,
		EndAt:            end,
		PreviousStartAt:  start.AddDate(0, 0, -1), // for reschedule preview
		PreviousEndAt:    end.AddDate(0, 0, -1),
		LocationValue:    locVal.String,
		BaseURL:          h.publicURL(),
		CustomNote:       customNote,
	}

	subject, body, _ := mailer.RenderBody(req.Type, d)
	if strings.HasPrefix(body, "(template render error:") {
		h.logger.ErrorContext(r.Context(), "test email: template render failed", "body", body)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := h.mailer.Send(r.Context(), mailer.Message{
		To:      []string{user.Email},
		Subject: "[TEST] " + subject,
		Text:    body,
	}); err != nil {
		h.logger.ErrorContext(r.Context(), "test email: send", "error", err)
		h.writeError(w, http.StatusInternalServerError, "failed to send test email")
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]any{
		"sent": true,
		"to":   user.Email,
	})
}
