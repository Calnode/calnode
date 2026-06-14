package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/mailer"
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
	})
	if err != nil {
		if errors.Is(err, booking.ErrDoubleBooked) {
			h.writeError(w, http.StatusConflict, "this slot is no longer available")
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
		if err := mailer.SendConfirmation(ctx, h.mailer, bData); err != nil {
			h.logger.Error("booking confirmation email", "error", err, "booking_id", b.ID)
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

	// Send cancellation emails in the background.
	bCopy := b
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		d, err := h.loadCancellationData(ctx, bCopy)
		if err != nil {
			h.logger.Error("booking cancellation: load data", "error", err, "booking_id", bCopy.ID)
			return
		}
		d.BaseURL = h.baseURL
		if err := mailer.SendCancellation(ctx, h.mailer, d); err != nil {
			h.logger.Error("booking cancellation email", "error", err, "booking_id", bCopy.ID)
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
