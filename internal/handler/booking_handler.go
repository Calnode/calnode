package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/gcal"
	"github.com/calnode/calnode/internal/mailer"
	"github.com/calnode/calnode/internal/uid"
	"github.com/calnode/calnode/internal/webhook"
)

type bookingJSON struct {
	ID                 string `json:"id"`
	EventTypeID        string `json:"event_type_id"`
	HostID             string `json:"host_id"`
	StartAt            string `json:"start_at"`
	EndAt              string `json:"end_at"`
	Status             string `json:"status"`
	CancellationReason string `json:"cancellation_reason,omitempty"`
	LocationValue      string `json:"location_value,omitempty"`
	CreatedAt          string `json:"created_at"`
	UpdatedAt          string `json:"updated_at"`
}

func toBookingJSON(b *booking.Booking) bookingJSON {
	return bookingJSON{
		ID:                 b.ID,
		EventTypeID:        b.EventTypeID,
		HostID:             b.HostID,
		StartAt:            b.StartAt.UTC().Format(time.RFC3339),
		EndAt:              b.EndAt.UTC().Format(time.RFC3339),
		Status:             b.Status,
		CancellationReason: b.CancellationReason,
		LocationValue:      b.LocationValue,
		CreatedAt:          b.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:          b.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// CreateBooking handles POST /v1/bookings (public — no auth required).
// The caller provides the event type slug and a start time selected from /slots.
// end_at is computed from event_type.duration_minutes.
//
// Phase 1: host is always event_type.user_id. Team routing (round_robin,
// collective with multiple hosts) will be added when team CRUD lands.
func (h *Handler) CreateBooking(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)

	var req struct {
		EventTypeSlug string `json:"event_type_slug"`
		StartAt       string `json:"start_at"`
		Name          string `json:"name"`
		Email         string `json:"email"`
		Timezone      string `json:"timezone"`
		Answers       []struct {
			QuestionID string `json:"question_id"`
			Value      string `json:"value"`
		} `json:"answers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.EventTypeSlug == "" || req.StartAt == "" || req.Name == "" || req.Email == "" {
		h.writeError(w, http.StatusBadRequest, "event_type_slug, start_at, name, and email are required")
		return
	}
	if req.Timezone == "" {
		req.Timezone = "UTC"
	}

	startAt, err := time.Parse(time.RFC3339, req.StartAt)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "start_at must be RFC3339 (e.g. 2026-06-15T09:00:00Z)")
		return
	}

	// Look up the event type.
	var et struct {
		ID              string
		UserID          string
		Name            string
		DurationMinutes int
		LocationValue   *string
		IsActive        int
	}
	err = h.db.QueryRowContext(r.Context(), `
		SELECT id, user_id, name, duration_minutes, location_value, is_active
		FROM event_types WHERE slug = ?`, req.EventTypeSlug).
		Scan(&et.ID, &et.UserID, &et.Name, &et.DurationMinutes, &et.LocationValue, &et.IsActive)
	if err != nil {
		h.writeError(w, http.StatusNotFound, "event type not found")
		return
	}
	if et.IsActive == 0 {
		h.writeError(w, http.StatusNotFound, "event type not found")
		return
	}

	// Validate intake question answers.
	answers, err := h.validateAnswers(w, r, et.ID, req.Answers)
	if err != nil {
		return // validateAnswers already wrote the error response
	}

	endAt := startAt.UTC().Add(time.Duration(et.DurationMinutes) * time.Minute)

	locValue := ""
	if et.LocationValue != nil {
		locValue = *et.LocationValue
	}

	b, err := h.bookingSvc.Create(r.Context(), booking.CreateParams{
		EventTypeID:   et.ID,
		HostIDs:       []string{et.UserID},
		StartAt:       startAt.UTC(),
		EndAt:         endAt,
		LocationValue: locValue,
		Organizer: booking.Attendee{
			Name:         req.Name,
			Email:        req.Email,
			IANATimezone: req.Timezone,
		},
		Answers: answers,
	})
	if err != nil {
		if errors.Is(err, booking.ErrDoubleBooked) {
			h.writeError(w, http.StatusConflict, "this slot is no longer available")
			return
		}
		// A question was deleted between validateAnswers and the INSERT — return a
		// clean 422 rather than leaking a generic 500 for an FK constraint failure.
		if isForeignKeyViolation(err) {
			h.writeError(w, http.StatusUnprocessableEntity, "one or more questions are no longer available")
			return
		}
		h.logger.ErrorContext(r.Context(), "create booking", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.writeJSON(w, http.StatusCreated, toBookingJSON(b))

	// Send confirmation emails in the background — the booking is committed; a
	// mail failure must not roll it back or change the HTTP response.
	bData := mailer.BookingData{
		BookingID:         b.ID,
		EventTypeName:     et.Name,
		EventTypeSlug:     req.EventTypeSlug,
		OrganizerName:     req.Name,
		OrganizerEmail:    req.Email,
		OrganizerTimezone: req.Timezone,
		StartAt:           b.StartAt,
		EndAt:             b.EndAt,
		LocationValue:     b.LocationValue,
		BaseURL:           h.baseURL,
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := h.loadHostIntoData(ctx, et.UserID, &bData); err != nil {
			h.logger.Error("booking confirmation: load host", "error", err, "booking_id", b.ID)
			return
		}
		if tok, err := h.bookingSvc.IssueManageToken(ctx, b.ID); err != nil {
			h.logger.Error("issue manage token", "error", err, "booking_id", b.ID)
		} else {
			bData.ManageURL = h.baseURL + "/manage/" + tok
		}
		// Create Google Calendar event and persist the event ID for later cancellation.
		if h.gcal != nil {
			eventID, err := h.gcal.CreateEvent(ctx, et.UserID, gcal.CreateEventParams{
				Summary:        et.Name + " with " + req.Name,
				Description:    "Booking ID: " + b.ID,
				Start:          b.StartAt,
				End:            b.EndAt,
				OrganizerName:  req.Name,
				OrganizerEmail: req.Email,
			})
			if err != nil {
				h.logger.Error("create gcal event", "error", err, "booking_id", b.ID)
			} else if eventID != "" {
				if _, err := h.db.ExecContext(ctx,
					`UPDATE bookings SET external_event_id = ? WHERE id = ?`,
					eventID, b.ID); err != nil {
					h.logger.Error("save gcal event id", "error", err, "booking_id", b.ID)
				}
			}
		}
		prefs := allOnPrefs
		if p, err := h.loadHostPrefs(ctx, et.UserID); err != nil {
			h.logger.Error("booking confirmation: load host prefs", "error", err, "booking_id", b.ID)
		} else {
			prefs = p
		}
		var msgNote sql.NullString
		_ = h.db.QueryRowContext(ctx, `SELECT msg_confirmation FROM event_types WHERE id = ?`, b.EventTypeID).
			Scan(&msgNote)
		if msgNote.Valid {
			bData.CustomNote = msgNote.String
		}
		if prefs.NotifyConfirmation {
			if err := mailer.SendConfirmationToAttendee(ctx, h.mailer, bData); err != nil {
				h.logger.Error("booking confirmation email (attendee)", "error", err, "booking_id", b.ID)
			}
		}
		if prefs.NotifyHostBooking {
			if err := mailer.SendConfirmationToHost(ctx, h.mailer, bData); err != nil {
				h.logger.Error("booking confirmation email (host)", "error", err, "booking_id", b.ID)
			}
		}
		if err := h.webhookSvc.Enqueue(ctx, "booking.created", webhook.BookingPayload{
			ID:            b.ID,
			EventTypeSlug: req.EventTypeSlug,
			HostID:        et.UserID,
			StartAt:       b.StartAt.UTC().Format(time.RFC3339),
			EndAt:         b.EndAt.UTC().Format(time.RFC3339),
			Status:        b.Status,
			LocationValue: b.LocationValue,
			CreatedAt:     b.CreatedAt.UTC().Format(time.RFC3339),
		}); err != nil {
			h.logger.Error("enqueue booking.created webhook", "error", err, "booking_id", b.ID)
		}
		if err := h.enqueueBookingReminders(ctx, b.EventTypeID, b.ID, b.StartAt); err != nil {
			h.logger.Error("enqueue reminders", "error", err, "booking_id", b.ID)
		}
	}()
}

// GetBooking handles GET /v1/bookings/{id} (public — accessible with just the booking ID).
func (h *Handler) GetBooking(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	b, err := h.bookingSvc.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, booking.ErrNotFound) {
			h.writeError(w, http.StatusNotFound, "booking not found")
			return
		}
		h.logger.ErrorContext(r.Context(), "get booking", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, toBookingJSON(b))
}

// ListBookings handles GET /v1/bookings (admin — lists bookings for the current user).
func (h *Handler) ListBookings(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	bookings, err := h.bookingSvc.ListByHost(r.Context(), user.ID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list bookings", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	items := make([]bookingJSON, len(bookings))
	for i := range bookings {
		items[i] = toBookingJSON(&bookings[i])
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// CancelBooking handles POST /v1/bookings/{id}/cancel (admin).
func (h *Handler) CancelBooking(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	id := r.PathValue("id")
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)

	var req struct {
		Reason string `json:"reason"`
	}
	// Ignore decode errors — reason is optional.
	_ = json.NewDecoder(r.Body).Decode(&req)

	if err := h.bookingSvc.Cancel(r.Context(), user.ID, id, req.Reason); err != nil {
		switch {
		case errors.Is(err, booking.ErrNotFound):
			h.writeError(w, http.StatusNotFound, "booking not found")
		case errors.Is(err, booking.ErrAlreadyCancelled):
			h.writeError(w, http.StatusConflict, "booking already cancelled")
		default:
			h.logger.ErrorContext(r.Context(), "cancel booking", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
		}
		return
	}

	b, err := h.bookingSvc.Get(r.Context(), id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "fetch cancelled booking", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, toBookingJSON(b))

	// Cancel the Google Calendar event and send cancellation emails in the background.
	bCopy := b
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		// Cancel the GCal event if one was created at booking time.
		if h.gcal != nil {
			var extEventID sql.NullString
			if err := h.db.QueryRowContext(ctx,
				`SELECT external_event_id FROM bookings WHERE id = ?`, bCopy.ID).
				Scan(&extEventID); err != nil && !errors.Is(err, sql.ErrNoRows) {
				h.logger.Error("fetch external_event_id", "error", err, "booking_id", bCopy.ID)
			} else if extEventID.Valid && extEventID.String != "" {
				if err := h.gcal.CancelEvent(ctx, bCopy.HostID, extEventID.String); err != nil {
					h.logger.Error("cancel gcal event", "error", err, "booking_id", bCopy.ID)
				}
			}
		}
		d, err := h.loadCancellationData(ctx, bCopy)
		if err != nil {
			h.logger.Error("booking cancellation: load data", "error", err, "booking_id", bCopy.ID)
			return
		}
		d.BaseURL = h.baseURL
		prefs := allOnPrefs
		if p, err := h.loadHostPrefs(ctx, bCopy.HostID); err != nil {
			h.logger.Error("booking cancellation: load host prefs", "error", err, "booking_id", bCopy.ID)
		} else {
			prefs = p
		}
		var msgNote sql.NullString
		_ = h.db.QueryRowContext(ctx, `SELECT msg_cancellation FROM event_types WHERE id = ?`, bCopy.EventTypeID).
			Scan(&msgNote)
		if msgNote.Valid {
			d.CustomNote = msgNote.String
		}
		if prefs.NotifyCancellation {
			if err := mailer.SendCancellationToAttendee(ctx, h.mailer, d); err != nil {
				h.logger.Error("booking cancellation email (attendee)", "error", err, "booking_id", bCopy.ID)
			}
		}
		if prefs.NotifyHostCancel {
			if err := mailer.SendCancellationToHost(ctx, h.mailer, d); err != nil {
				h.logger.Error("booking cancellation email (host)", "error", err, "booking_id", bCopy.ID)
			}
		}
		if err := h.webhookSvc.Enqueue(ctx, "booking.cancelled", webhook.BookingPayload{
			ID:                 bCopy.ID,
			EventTypeSlug:      d.EventTypeSlug,
			HostID:             bCopy.HostID,
			StartAt:            bCopy.StartAt.UTC().Format(time.RFC3339),
			EndAt:              bCopy.EndAt.UTC().Format(time.RFC3339),
			Status:             bCopy.Status,
			CancellationReason: bCopy.CancellationReason,
			LocationValue:      bCopy.LocationValue,
			CreatedAt:          bCopy.CreatedAt.UTC().Format(time.RFC3339),
		}); err != nil {
			h.logger.Error("enqueue booking.cancelled webhook", "error", err, "booking_id", bCopy.ID)
		}
	}()
}

// loadHostIntoData fills HostName and HostEmail in d from the users table.
func (h *Handler) loadHostIntoData(ctx context.Context, hostID string, d *mailer.BookingData) error {
	return h.db.QueryRowContext(ctx,
		`SELECT name, email FROM users WHERE id = ?`, hostID).
		Scan(&d.HostName, &d.HostEmail)
}

// loadCancellationData assembles all fields needed for cancellation emails.
func (h *Handler) loadCancellationData(ctx context.Context, b *booking.Booking) (mailer.BookingData, error) {
	var d mailer.BookingData
	d.BookingID = b.ID
	d.StartAt = b.StartAt
	d.EndAt = b.EndAt
	d.LocationValue = b.LocationValue
	d.CancellationReason = b.CancellationReason

	// Event type name + slug and host name + email in one join.
	err := h.db.QueryRowContext(ctx, `
		SELECT et.name, et.slug, u.name, u.email
		FROM event_types et JOIN users u ON u.id = et.user_id
		WHERE et.id = ?`, b.EventTypeID).
		Scan(&d.EventTypeName, &d.EventTypeSlug, &d.HostName, &d.HostEmail)
	if err != nil {
		return d, fmt.Errorf("load event/host: %w", err)
	}

	// Organizer attendee.
	_ = h.db.QueryRowContext(ctx, `
		SELECT name, email, iana_timezone
		FROM booking_attendees WHERE booking_id = ? AND is_organizer = 1`, b.ID).
		Scan(&d.OrganizerName, &d.OrganizerEmail, &d.OrganizerTimezone)

	return d, nil
}

// hostPrefs holds the notification preference booleans for a host user.
type hostPrefs struct {
	NotifyConfirmation   bool
	NotifyCancellation   bool
	NotifyReschedule     bool
	NotifyReminder       bool
	NotifyHostBooking    bool
	NotifyHostCancel     bool
	NotifyHostReschedule bool
}

// allOnPrefs is a safe default used when loadHostPrefs fails.
var allOnPrefs = hostPrefs{true, true, true, true, true, true, true}

// loadHostPrefs fetches notification prefs for a user. On error, callers should
// fall back to allOnPrefs so a DB hiccup does not silently suppress emails.
func (h *Handler) loadHostPrefs(ctx context.Context, hostID string) (hostPrefs, error) {
	var p hostPrefs
	var nc, nca, nr, nrm, nhb, nhc, nhr int
	err := h.db.QueryRowContext(ctx, `
		SELECT COALESCE(notify_confirmation,1), COALESCE(notify_cancellation,1),
		       COALESCE(notify_reschedule,1), COALESCE(notify_reminder,1),
		       COALESCE(notify_host_booking,1), COALESCE(notify_host_cancel,1),
		       COALESCE(notify_host_reschedule,1)
		FROM users WHERE id = ?`, hostID).
		Scan(&nc, &nca, &nr, &nrm, &nhb, &nhc, &nhr)
	if err != nil {
		return p, err
	}
	p.NotifyConfirmation, p.NotifyCancellation = nc != 0, nca != 0
	p.NotifyReschedule, p.NotifyReminder = nr != 0, nrm != 0
	p.NotifyHostBooking, p.NotifyHostCancel, p.NotifyHostReschedule = nhb != 0, nhc != 0, nhr != 0
	return p, nil
}

// isForeignKeyViolation reports whether err is a SQLite FOREIGN KEY constraint failure.
func isForeignKeyViolation(err error) bool {
	return strings.Contains(err.Error(), "FOREIGN KEY constraint failed")
}

// enqueueReminder inserts a reminder.send job scheduled hoursBefore hours before startAt.
// If the computed run_at has already passed, the job fires on the next poll cycle.
func (h *Handler) enqueueReminder(ctx context.Context, bookingID string, startAt time.Time, hoursBefore int) error {
	runAt := startAt.UTC().Add(-time.Duration(hoursBefore) * time.Hour)
	now := time.Now().UTC()
	if runAt.Before(now) {
		runAt = now
	}

	payload, err := json.Marshal(map[string]any{"booking_id": bookingID, "hours_before": hoursBefore})
	if err != nil {
		return fmt.Errorf("enqueue reminder: marshal payload: %w", err)
	}

	_, err = h.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO jobs (id, type, payload, run_at, status, attempts, max_attempts)
		VALUES (?, 'reminder.send', ?, ?, 'pending', 0, 3)`,
		uid.New(), string(payload), runAt.Format(time.RFC3339))
	return err
}

// enqueueBookingReminders loads the event type's reminder list and enqueues one job per
// entry. Falls back to a single 24-hour reminder when no explicit list is configured.
func (h *Handler) enqueueBookingReminders(ctx context.Context, etID, bookingID string, startAt time.Time) error {
	rows, err := h.db.QueryContext(ctx,
		`SELECT hours_before FROM event_type_reminders WHERE event_type_id = ? ORDER BY hours_before DESC`, etID)
	if err != nil {
		return fmt.Errorf("enqueue booking reminders: %w", err)
	}
	defer rows.Close()

	var hours []int
	for rows.Next() {
		var hb int
		if err := rows.Scan(&hb); err != nil {
			return fmt.Errorf("enqueue booking reminders: scan: %w", err)
		}
		hours = append(hours, hb)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	if len(hours) == 0 {
		return h.enqueueReminder(ctx, bookingID, startAt, 24)
	}
	for _, hb := range hours {
		if err := h.enqueueReminder(ctx, bookingID, startAt, hb); err != nil {
			return err
		}
	}
	return nil
}

// replaceReminderJobs atomically deletes all non-running reminder jobs for a
// booking and inserts fresh ones based on the event type's configured reminder
// list. Falls back to a single 24-hour reminder when no explicit list is set.
func (h *Handler) replaceReminderJobs(ctx context.Context, bookingID, etID string, newStart time.Time) error {
	// Load the hours list first (read-only, outside the write transaction).
	rows, err := h.db.QueryContext(ctx,
		`SELECT hours_before FROM event_type_reminders WHERE event_type_id = ? ORDER BY hours_before DESC`, etID)
	if err != nil {
		return fmt.Errorf("replace reminder jobs: load reminders: %w", err)
	}
	defer rows.Close()
	var hours []int
	for rows.Next() {
		var hb int
		if err := rows.Scan(&hb); err != nil {
			return fmt.Errorf("replace reminder jobs: scan: %w", err)
		}
		hours = append(hours, hb)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(hours) == 0 {
		hours = []int{24}
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("replace reminder jobs: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx, `
		DELETE FROM jobs
		WHERE type = 'reminder.send'
		  AND json_extract(payload, '$.booking_id') = ?
		  AND status != 'running'`, bookingID); err != nil {
		return fmt.Errorf("replace reminder jobs: delete: %w", err)
	}

	now := time.Now().UTC()
	for _, hb := range hours {
		runAt := newStart.UTC().Add(-time.Duration(hb) * time.Hour)
		if runAt.Before(now) {
			runAt = now
		}
		payload, err := json.Marshal(map[string]any{"booking_id": bookingID, "hours_before": hb})
		if err != nil {
			return fmt.Errorf("replace reminder jobs: marshal payload: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO jobs (id, type, payload, run_at, status, attempts, max_attempts)
			VALUES (?, 'reminder.send', ?, ?, 'pending', 0, 3)`,
			uid.New(), string(payload), runAt.Format(time.RFC3339)); err != nil {
			return fmt.Errorf("replace reminder jobs: insert: %w", err)
		}
	}
	return tx.Commit()
}
