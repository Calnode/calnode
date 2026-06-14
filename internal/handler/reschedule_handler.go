package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/mailer"
	"github.com/calnode/calnode/internal/uid"
	"github.com/calnode/calnode/internal/webhook"
)

// RescheduleBooking handles PATCH /v1/bookings/{id}/reschedule (admin — host only).
// Body: {"start_at":"<RFC3339>"}
// end_at is computed from the event type's duration_minutes.
func (h *Handler) RescheduleBooking(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	id := r.PathValue("id")
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)

	var req struct {
		StartAt string `json:"start_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.StartAt == "" {
		h.writeError(w, http.StatusBadRequest, "start_at is required")
		return
	}
	newStart, err := time.Parse(time.RFC3339, req.StartAt)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "start_at must be RFC3339 (e.g. 2026-06-15T09:00:00Z)")
		return
	}

	// Load booking + event type in one query to check ownership and get duration.
	var hostID, startStr, endStr, status, etSlug string
	var durMins int
	err = h.db.QueryRowContext(r.Context(), `
		SELECT b.host_id, b.start_at, b.end_at, b.status, et.duration_minutes, et.slug
		FROM bookings b JOIN event_types et ON et.id = b.event_type_id
		WHERE b.id = ?`, id).
		Scan(&hostID, &startStr, &endStr, &status, &durMins, &etSlug)
	if errors.Is(err, sql.ErrNoRows) {
		h.writeError(w, http.StatusNotFound, "booking not found")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "reschedule booking: load", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	// Return 404 for unauthorized access to avoid leaking booking IDs.
	if hostID != user.ID {
		h.writeError(w, http.StatusNotFound, "booking not found")
		return
	}
	if status == "cancelled" {
		h.writeError(w, http.StatusConflict, "this booking has been cancelled")
		return
	}

	previousStart, err := time.Parse(time.RFC3339Nano, startStr)
	if err != nil {
		// Fallback for RFC3339 without nanoseconds.
		previousStart, err = time.Parse(time.RFC3339, startStr)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "reschedule booking: parse start_at", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}
	previousEnd, err := time.Parse(time.RFC3339Nano, endStr)
	if err != nil {
		previousEnd, err = time.Parse(time.RFC3339, endStr)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "reschedule booking: parse end_at", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	newEnd := newStart.Add(time.Duration(durMins) * time.Minute)

	updated, err := h.bookingSvc.Reschedule(r.Context(), id, newStart, newEnd)
	if errors.Is(err, booking.ErrDoubleBooked) {
		h.writeError(w, http.StatusConflict, "that time slot is no longer available")
		return
	}
	if errors.Is(err, booking.ErrAlreadyCancelled) {
		h.writeError(w, http.StatusConflict, "this booking has been cancelled")
		return
	}
	if errors.Is(err, booking.ErrNotFound) {
		h.writeError(w, http.StatusNotFound, "booking not found")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "reschedule booking: update", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.writeJSON(w, http.StatusOK, toBookingJSON(updated))

	bCopy := *updated
	prevStart := previousStart
	prevEnd := previousEnd
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		d, err := h.loadCancellationData(ctx, &bCopy)
		if err != nil {
			h.logger.Error("reschedule booking: load email data", "error", err, "booking_id", bCopy.ID)
			return
		}
		d.BaseURL = h.baseURL
		d.PreviousStartAt = prevStart
		d.PreviousEndAt = prevEnd

		if tok, err := h.bookingSvc.RotateManageToken(ctx, bCopy.ID); err == nil {
			d.ManageURL = h.baseURL + "/manage/" + tok
		}

		if err := mailer.SendReschedule(ctx, h.mailer, d); err != nil {
			h.logger.Error("reschedule booking: send email", "error", err, "booking_id", bCopy.ID)
		}

		if h.webhookSvc != nil {
			if err := h.webhookSvc.Enqueue(ctx, "booking.rescheduled", webhook.BookingPayload{
				ID:              bCopy.ID,
				EventTypeSlug:   etSlug,
				HostID:          bCopy.HostID,
				StartAt:         bCopy.StartAt.UTC().Format(time.RFC3339),
				EndAt:           bCopy.EndAt.UTC().Format(time.RFC3339),
				Status:          bCopy.Status,
				LocationValue:   bCopy.LocationValue,
				CreatedAt:       bCopy.CreatedAt.UTC().Format(time.RFC3339),
				PreviousStartAt: prevStart.UTC().Format(time.RFC3339),
				PreviousEndAt:   prevEnd.UTC().Format(time.RFC3339),
			}); err != nil {
				h.logger.Error("reschedule booking: enqueue webhook", "error", err, "booking_id", bCopy.ID)
			}
		}

		if err := h.rescheduleReminderJob(ctx, bCopy.ID, bCopy.StartAt); err != nil {
			h.logger.Error("reschedule booking: update reminder job", "error", err, "booking_id", bCopy.ID)
		}
	}()
}

// rescheduleReminderJob reschedules the reminder.send job for bookingID to fire
// 24 hours before newStart. Uses an UPSERT so it handles all job states:
//   - no job yet → INSERT
//   - pending/done/failed → UPDATE run_at and reset to pending
//   - running → WHERE clause excludes it; in-flight send uses current DB data
//     (which already reflects the new booking time) so the email is correct
func (h *Handler) rescheduleReminderJob(ctx context.Context, bookingID string, newStart time.Time) error {
	runAt := newStart.UTC().Add(-24 * time.Hour)
	now := time.Now().UTC()
	if runAt.Before(now) {
		runAt = now
	}

	payload, err := json.Marshal(map[string]string{"booking_id": bookingID})
	if err != nil {
		return fmt.Errorf("reschedule reminder job: marshal payload: %w", err)
	}

	_, err = h.db.ExecContext(ctx, `
		INSERT INTO jobs (id, type, payload, run_at, status, attempts, max_attempts)
		VALUES (?, 'reminder.send', ?, ?, 'pending', 0, 3)
		ON CONFLICT(type, payload) DO UPDATE SET
			run_at   = excluded.run_at,
			status   = 'pending',
			attempts = 0
		WHERE jobs.status != 'running'`,
		uid.New(), string(payload), runAt.Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("reschedule reminder job: upsert: %w", err)
	}
	return nil
}
