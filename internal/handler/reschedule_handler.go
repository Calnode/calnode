package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/mailer"
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
	if newStart.Before(time.Now()) {
		h.writeError(w, http.StatusBadRequest, "start_at cannot be in the past")
		return
	}

	// Load booking + event type in one query to check ownership and get duration.
	var hostID, startStr, endStr, status, etSlug, etID string
	var durMins int
	err = h.db.QueryRowContext(r.Context(), `
		SELECT b.host_id, b.start_at, b.end_at, b.status, et.duration_minutes, et.slug, et.id
		FROM bookings b JOIN event_types et ON et.id = b.event_type_id
		WHERE b.id = ?`, id).
		Scan(&hostID, &startStr, &endStr, &status, &durMins, &etSlug, &etID)
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
	capturedEtID := etID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		d, err := h.loadCancellationData(ctx, &bCopy)
		if err != nil {
			h.logger.Error("reschedule booking: load email data", "error", err, "booking_id", bCopy.ID)
			return
		}
		d.BaseURL = h.publicURL()
		d.PreviousStartAt = prevStart
		d.PreviousEndAt = prevEnd

		// Move the calendar event(s) to the new time (all hosts, for Group bookings).
		h.moveCalendarEvents(ctx, bCopy.ID, bCopy.StartAt, bCopy.EndAt)

		if tok, err := h.bookingSvc.RotateManageToken(ctx, bCopy.ID); err == nil {
			d.ManageURL = h.publicURL() + "/manage/" + tok
		}

		prefs := allOnPrefs
		if p, err := h.loadHostPrefs(ctx, bCopy.HostID); err != nil {
			h.logger.Error("reschedule booking: load host prefs", "error", err, "booking_id", bCopy.ID)
		} else {
			prefs = p
		}
		var msgNote, subjNote sql.NullString
		_ = h.db.QueryRowContext(ctx, `SELECT msg_reschedule, subj_reschedule FROM event_types WHERE id = ?`, capturedEtID).
			Scan(&msgNote, &subjNote)
		if msgNote.Valid {
			d.CustomNote = msgNote.String
		}
		if subjNote.Valid {
			d.SubjectOverride = subjNote.String
		}
		d.AttachICS = h.noGoogleDestination(ctx, bCopy.HostID)
		d.ICSSequence = int(bCopy.UpdatedAt.Unix())
		if prefs.NotifyReschedule {
			if err := mailer.SendRescheduleToAttendee(ctx, h.mailer, d); err != nil {
				h.logger.Error("reschedule booking: send email (attendee)", "error", err, "booking_id", bCopy.ID)
			}
		}
		if prefs.NotifyHostReschedule {
			if err := mailer.SendRescheduleToHost(ctx, h.mailer, d); err != nil {
				h.logger.Error("reschedule booking: send email (host)", "error", err, "booking_id", bCopy.ID)
			}
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

		if err := h.replaceReminderJobs(ctx, bCopy.ID, capturedEtID, bCopy.StartAt); err != nil {
			h.logger.Error("reschedule booking: replace reminder jobs", "error", err, "booking_id", bCopy.ID)
		}
	}()
}

