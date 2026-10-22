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
// It sends a sample email to the caller so they can preview how the custom message
// looks inside the real email template. When an admin previews someone else's event
// type, the sample still names the event type's owner as the host (that is what a
// booker would see), but it goes to the admin who asked for it, never to the owner.
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

	// Load the event type, its owner and its custom messages (verifies the caller may
	// edit it first).
	var ownerID, ownerName, ownerEmail, etName string
	var durationMinutes int
	var locVal, msgConf, msgCancel, msgResched, msgRemind sql.NullString
	var subjConf, subjCancel, subjResched, subjRemind sql.NullString
	err := h.db.QueryRowContext(r.Context(), `
		SELECT et.user_id, u.name, u.email,
		       et.name, et.duration_minutes, et.location_value,
		       et.msg_confirmation, et.msg_cancellation, et.msg_reschedule, et.msg_reminder,
		       et.subj_confirmation, et.subj_cancellation, et.subj_reschedule, et.subj_reminder
		FROM event_types et
		JOIN users u ON u.id = et.user_id
		WHERE et.slug = ?`, slug).
		Scan(&ownerID, &ownerName, &ownerEmail, &etName, &durationMinutes, &locVal,
			&msgConf, &msgCancel, &msgResched, &msgRemind,
			&subjConf, &subjCancel, &subjResched, &subjRemind)
	if err == sql.ErrNoRows || (err == nil && !canManageEventType(user, ownerID)) {
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

	// Pick the custom note + subject for the requested email type. NullString.String
	// is "" when unset, which is exactly the "use the default" sentinel.
	var customNote, customSubject string
	switch req.Type {
	case "confirmation":
		customNote, customSubject = msgConf.String, subjConf.String
	case "cancellation":
		customNote, customSubject = msgCancel.String, subjCancel.String
	case "reschedule":
		customNote, customSubject = msgResched.String, subjResched.String
	case "reminder":
		customNote, customSubject = msgRemind.String, subjRemind.String
	}

	// Build placeholder booking data. The sample date is tomorrow at 14:00 UTC.
	if durationMinutes < 1 {
		durationMinutes = 30
	}
	start := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, 1).Add(14 * time.Hour)
	end := start.Add(time.Duration(durationMinutes) * time.Minute)

	d := mailer.BookingData{
		BookingID:         "preview-test",
		EventTypeName:     etName,
		EventTypeSlug:     slug,
		HostName:          ownerName,
		HostEmail:         ownerEmail,
		OrganizerName:     "Alex Johnson",
		OrganizerEmail:    user.Email, // send to host so they can review it
		OrganizerTimezone: user.IANATZ,
		StartAt:           start,
		EndAt:             end,
		PreviousStartAt:   start.AddDate(0, 0, -1), // for reschedule preview
		PreviousEndAt:     end.AddDate(0, 0, -1),
		LocationValue:     locVal.String,
		BaseURL:           h.publicURL(),
		CustomNote:        customNote,
		SubjectOverride:   customSubject,
	}
	h.applyBranding(r.Context(), &d)

	subject, body, html, _ := mailer.RenderBody(req.Type, d)
	if strings.HasPrefix(body, "(template render error:") {
		h.logger.ErrorContext(r.Context(), "test email: template render failed", "body", body)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := h.mailer.Send(r.Context(), mailer.Message{
		To:      []string{user.Email},
		Subject: "[TEST] " + subject,
		Text:    body,
		HTML:    html,
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
