package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/calnode/calnode/internal/booking"
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
		DurationMinutes int
		LocationValue   *string
		IsActive        int
	}
	err = h.db.QueryRowContext(r.Context(), `
		SELECT id, user_id, duration_minutes, location_value, is_active
		FROM event_types WHERE slug = ?`, req.EventTypeSlug).
		Scan(&et.ID, &et.UserID, &et.DurationMinutes, &et.LocationValue, &et.IsActive)
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
}
