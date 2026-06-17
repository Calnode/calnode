package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/gcal"
	"github.com/calnode/calnode/internal/mailer"
	"github.com/calnode/calnode/internal/webhook"
)

// ListUserUpcomingBookings handles GET /v1/users/{id}/upcoming-bookings (admin).
// Returns the member's upcoming, non-cancelled bookings as host — the list that
// drives the "resolve meetings" step before archiving.
func (h *Handler) ListUserUpcomingBookings(w http.ResponseWriter, r *http.Request) {
	actor, ok := userFromContext(r.Context())
	if !ok || !actor.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
		return
	}
	hostID := r.PathValue("id")
	now := time.Now().UTC().Format(time.RFC3339Nano)

	rows, err := h.db.QueryContext(r.Context(), `
		SELECT b.id, b.start_at, b.end_at, et.name, et.slug,
		       COALESCE(a.name,''), COALESCE(a.email,'')
		FROM bookings b
		JOIN event_types et ON et.id = b.event_type_id
		LEFT JOIN booking_attendees a ON a.booking_id = b.id AND a.is_organizer = 1
		WHERE b.host_id = ? AND b.status != 'cancelled' AND b.end_at > ?
		ORDER BY b.start_at ASC`, hostID, now)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list upcoming bookings: query", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	type row struct {
		ID            string `json:"id"`
		StartAt       string `json:"start_at"`
		EndAt         string `json:"end_at"`
		EventTypeName string `json:"event_type_name"`
		EventTypeSlug string `json:"event_type_slug"`
		AttendeeName  string `json:"attendee_name"`
		AttendeeEmail string `json:"attendee_email"`
	}
	out := []row{}
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.ID, &x.StartAt, &x.EndAt, &x.EventTypeName, &x.EventTypeSlug,
			&x.AttendeeName, &x.AttendeeEmail); err != nil {
			continue
		}
		out = append(out, x)
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// ReassignBooking handles POST /v1/bookings/{id}/reassign (admin).
// Body: {"host_id":"<userId>"}. Moves the booking to another active host,
// checking the new host is free at that time, then (async) moves the Google
// Calendar event and notifies the attendee and the new host.
func (h *Handler) ReassignBooking(w http.ResponseWriter, r *http.Request) {
	actor, ok := userFromContext(r.Context())
	if !ok || !actor.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
		return
	}
	id := r.PathValue("id")
	r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
	var req struct {
		HostID string `json:"host_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.HostID == "" {
		h.writeError(w, http.StatusBadRequest, "host_id is required")
		return
	}

	// The new host must exist and be active (not archived).
	var dummy int
	err := h.db.QueryRowContext(r.Context(),
		`SELECT 1 FROM users WHERE id = ? AND archived_at IS NULL`, req.HostID).Scan(&dummy)
	if errors.Is(err, sql.ErrNoRows) {
		h.writeError(w, http.StatusBadRequest, "new host not found or archived")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "reassign: validate host", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Capture the old host + calendar event + summary fields before the move.
	var oldHostID, extEventID, etName, orgName, orgEmail string
	err = h.db.QueryRowContext(r.Context(), `
		SELECT b.host_id, COALESCE(b.external_event_id,''), et.name,
		       COALESCE(a.name,''), COALESCE(a.email,'')
		FROM bookings b
		JOIN event_types et ON et.id = b.event_type_id
		LEFT JOIN booking_attendees a ON a.booking_id = b.id AND a.is_organizer = 1
		WHERE b.id = ?`, id).
		Scan(&oldHostID, &extEventID, &etName, &orgName, &orgEmail)
	if errors.Is(err, sql.ErrNoRows) {
		h.writeError(w, http.StatusNotFound, "booking not found")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "reassign: load booking", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	updated, err := h.bookingSvc.ReassignHost(r.Context(), id, req.HostID)
	if errors.Is(err, booking.ErrDoubleBooked) {
		h.writeError(w, http.StatusConflict, "the chosen host already has a booking at that time")
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
		h.logger.ErrorContext(r.Context(), "reassign: update", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Keep the multi-host record in sync — the primary host row follows the
	// booking's host_id so later cancel/notify fan-out targets the right person.
	// (Reassign is single-host; the primary row is the one being moved.)
	if _, err := h.db.ExecContext(r.Context(),
		`UPDATE booking_hosts SET user_id = ?, external_event_id = NULL WHERE booking_id = ? AND is_primary = 1`,
		req.HostID, updated.ID); err != nil {
		h.logger.ErrorContext(r.Context(), "reassign: sync booking_hosts", "error", err)
	}

	h.writeJSON(w, http.StatusOK, toBookingJSON(updated))

	// Side effects: move the calendar event and notify attendee + new host.
	bCopy := *updated
	newHostID := req.HostID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// Move the Google Calendar event: remove from the old host, recreate on
		// the new host, and persist the new event ID (clearing it if recreation
		// produced nothing, e.g. the new host has no destination calendar).
		if gc := h.getGCal(); gc != nil {
			if extEventID != "" {
				if err := gc.CancelEvent(ctx, oldHostID, extEventID); err != nil {
					h.logger.Error("reassign: delete old calendar event", "error", err, "booking_id", bCopy.ID)
				}
			}
			newEventID, err := gc.CreateEvent(ctx, newHostID, gcal.CreateEventParams{
				Summary:        etName + " with " + orgName,
				Description:    "Booking ID: " + bCopy.ID,
				Start:          bCopy.StartAt,
				End:            bCopy.EndAt,
				OrganizerName:  orgName,
				OrganizerEmail: orgEmail,
			})
			if err != nil {
				h.logger.Error("reassign: create new calendar event", "error", err, "booking_id", bCopy.ID)
			} else {
				if _, err := h.db.ExecContext(ctx,
					`UPDATE bookings SET external_event_id = ? WHERE id = ?`, newEventID, bCopy.ID); err != nil {
					h.logger.Error("reassign: persist new event id", "error", err, "booking_id", bCopy.ID)
				}
				if _, err := h.db.ExecContext(ctx,
					`UPDATE booking_hosts SET external_event_id = ? WHERE booking_id = ? AND is_primary = 1`,
					newEventID, bCopy.ID); err != nil {
					h.logger.Error("reassign: persist host event id", "error", err, "booking_id", bCopy.ID)
				}
			}
		}

		// Notify the attendee (their host changed) and the new host, reusing the
		// confirmation templates. Host details are overridden to the new host.
		d, err := h.loadCancellationData(ctx, &bCopy)
		if err != nil {
			h.logger.Error("reassign: load email data", "error", err, "booking_id", bCopy.ID)
			return
		}
		d.BaseURL = h.publicURL()
		if err := h.loadHostIntoData(ctx, newHostID, &d); err != nil {
			h.logger.Error("reassign: load new host", "error", err, "booking_id", bCopy.ID)
		}

		prefs := allOnPrefs
		if p, err := h.loadHostPrefs(ctx, newHostID); err == nil {
			prefs = p
		}
		if prefs.NotifyConfirmation {
			if err := mailer.SendConfirmationToAttendee(ctx, h.mailer, d); err != nil {
				h.logger.Error("reassign: email attendee", "error", err, "booking_id", bCopy.ID)
			}
		}
		if prefs.NotifyHostBooking {
			if err := mailer.SendConfirmationToHost(ctx, h.mailer, d); err != nil {
				h.logger.Error("reassign: email new host", "error", err, "booking_id", bCopy.ID)
			}
		}

		if h.webhookSvc != nil {
			if err := h.webhookSvc.Enqueue(ctx, "booking.rescheduled", webhook.BookingPayload{
				ID:            bCopy.ID,
				EventTypeSlug: d.EventTypeSlug,
				HostID:        newHostID,
				StartAt:       bCopy.StartAt.UTC().Format(time.RFC3339),
				EndAt:         bCopy.EndAt.UTC().Format(time.RFC3339),
				Status:        bCopy.Status,
				LocationValue: bCopy.LocationValue,
				CreatedAt:     bCopy.CreatedAt.UTC().Format(time.RFC3339),
			}); err != nil {
				h.logger.Error("reassign: enqueue webhook", "error", err, "booking_id", bCopy.ID)
			}
		}
	}()
}
