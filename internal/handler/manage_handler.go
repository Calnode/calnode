package handler

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"time"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/mailer"
	"github.com/calnode/calnode/internal/webhook"
)

//go:embed templates/manage.html
var manageTmplSrc string

var manageTmpl = template.Must(template.New("manage").Parse(manageTmplSrc))

type managePageData struct {
	Token           string
	BookingID       string
	EventTypeName   string
	EventTypeSlug   string
	HostName        string
	DurationLabel   string
	LocationLabel   string
	MaxFutureDays   int
	DurationMinutes int
	CurrentStartISO string // RFC3339 for JS
	OrganizerTZ     string
	Status          string // "confirmed" or "cancelled"
	TokenInvalid    bool   // token not found or expired
}

// ManagePage renders the attendee manage page for a booking (reschedule / cancel).
func (h *Handler) ManagePage(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")

	b, err := h.bookingSvc.ValidateManageToken(r.Context(), token)
	if errors.Is(err, booking.ErrTokenNotFound) {
		h.renderManage(w, r, managePageData{TokenInvalid: true})
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "manage page: validate token", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	var etName, etSlug, locType, locValue string
	var durMins, maxDays int
	var hostName string
	if err := h.db.QueryRowContext(r.Context(), `
		SELECT et.name, et.slug, et.duration_minutes, et.max_future_days,
		       et.location_type, COALESCE(et.location_value,''), u.name
		FROM event_types et JOIN users u ON u.id = et.user_id
		WHERE et.id = ?`, b.EventTypeID).
		Scan(&etName, &etSlug, &durMins, &maxDays, &locType, &locValue, &hostName); err != nil {
		h.logger.ErrorContext(r.Context(), "manage page: load event type", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	var orgTZ string
	_ = h.db.QueryRowContext(r.Context(), `
		SELECT iana_timezone FROM booking_attendees
		WHERE booking_id = ? AND is_organizer = 1`, b.ID).Scan(&orgTZ)
	if orgTZ == "" {
		orgTZ = "UTC"
	}

	data := managePageData{
		Token:           token,
		BookingID:       b.ID,
		EventTypeName:   etName,
		EventTypeSlug:   etSlug,
		HostName:        hostName,
		DurationLabel:   durationLabel(durMins),
		LocationLabel:   locationLabel(locType, locValue),
		MaxFutureDays:   maxDays,
		DurationMinutes: durMins,
		CurrentStartISO: b.StartAt.UTC().Format(time.RFC3339),
		OrganizerTZ:     orgTZ,
		Status:          b.Status,
	}
	h.renderManage(w, r, data)
}

func (h *Handler) renderManage(w http.ResponseWriter, r *http.Request, data managePageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if err := manageTmpl.Execute(w, data); err != nil {
		h.logger.ErrorContext(r.Context(), "manage page: template", "error", err)
	}
}

// RescheduleByToken moves a booking to a new time authenticated by a manage token.
// POST /manage/{token}/reschedule  body: {"start_at":"<RFC3339>"}
func (h *Handler) RescheduleByToken(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
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
		h.writeError(w, http.StatusBadRequest, "start_at must be RFC3339")
		return
	}

	b, err := h.bookingSvc.ValidateManageToken(r.Context(), token)
	if errors.Is(err, booking.ErrTokenNotFound) {
		h.writeError(w, http.StatusNotFound, "manage link not found or expired")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "reschedule: validate token", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	var durMins int
	if err := h.db.QueryRowContext(r.Context(),
		`SELECT duration_minutes FROM event_types WHERE id = ?`, b.EventTypeID).
		Scan(&durMins); err != nil {
		h.logger.ErrorContext(r.Context(), "reschedule: load duration", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	previousStart := b.StartAt
	previousEnd := b.EndAt
	newEnd := newStart.Add(time.Duration(durMins) * time.Minute)

	updated, err := h.bookingSvc.Reschedule(r.Context(), b.ID, newStart, newEnd)
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
		h.logger.ErrorContext(r.Context(), "reschedule: update", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.writeJSON(w, http.StatusOK, toBookingJSON(updated))

	bCopy := *updated
	capturedEtID := b.EventTypeID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		d, err := h.loadCancellationData(ctx, &bCopy)
		if err != nil {
			h.logger.Error("reschedule: load email data", "error", err, "booking_id", bCopy.ID)
			return
		}
		d.BaseURL = h.baseURL
		d.PreviousStartAt = previousStart
		d.PreviousEndAt = previousEnd

		// Rotate the token so the original confirmation-email link is invalidated.
		if tok, err := h.bookingSvc.RotateManageToken(ctx, bCopy.ID); err == nil {
			d.ManageURL = h.baseURL + "/manage/" + tok
		}

		prefs := allOnPrefs
		if p, err := h.loadHostPrefs(ctx, bCopy.HostID); err != nil {
			h.logger.Error("reschedule: load host prefs", "error", err, "booking_id", bCopy.ID)
		} else {
			prefs = p
		}
		var msgNote sql.NullString
		_ = h.db.QueryRowContext(ctx, `SELECT msg_reschedule FROM event_types WHERE id = ?`, capturedEtID).
			Scan(&msgNote)
		if msgNote.Valid {
			d.CustomNote = msgNote.String
		}
		if prefs.NotifyReschedule {
			if err := mailer.SendRescheduleToAttendee(ctx, h.mailer, d); err != nil {
				h.logger.Error("reschedule email (attendee)", "error", err, "booking_id", bCopy.ID)
			}
		}
		if prefs.NotifyHostReschedule {
			if err := mailer.SendRescheduleToHost(ctx, h.mailer, d); err != nil {
				h.logger.Error("reschedule email (host)", "error", err, "booking_id", bCopy.ID)
			}
		}

		if err := h.webhookSvc.Enqueue(ctx, "booking.rescheduled", webhook.BookingPayload{
			ID:              bCopy.ID,
			EventTypeSlug:   d.EventTypeSlug,
			HostID:          bCopy.HostID,
			StartAt:         bCopy.StartAt.UTC().Format(time.RFC3339),
			EndAt:           bCopy.EndAt.UTC().Format(time.RFC3339),
			Status:          bCopy.Status,
			LocationValue:   bCopy.LocationValue,
			CreatedAt:       bCopy.CreatedAt.UTC().Format(time.RFC3339),
			PreviousStartAt: previousStart.UTC().Format(time.RFC3339),
			PreviousEndAt:   previousEnd.UTC().Format(time.RFC3339),
		}); err != nil {
			h.logger.Error("enqueue booking.rescheduled webhook", "error", err, "booking_id", bCopy.ID)
		}

		if err := h.replaceReminderJobs(ctx, bCopy.ID, capturedEtID, bCopy.StartAt); err != nil {
			h.logger.Error("reschedule: replace reminder jobs", "error", err, "booking_id", bCopy.ID)
		}
	}()
}

// CancelByToken cancels a booking authenticated by a manage token.
// POST /manage/{token}/cancel  body: {"reason":"<optional>"}
func (h *Handler) CancelByToken(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)

	var req struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req) // reason is optional; ignore decode errors

	b, err := h.bookingSvc.CancelByToken(r.Context(), token, req.Reason)
	if errors.Is(err, booking.ErrTokenNotFound) {
		h.writeError(w, http.StatusNotFound, "manage link not found or expired")
		return
	}
	if errors.Is(err, booking.ErrAlreadyCancelled) {
		h.writeError(w, http.StatusConflict, "this booking is already cancelled")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "cancel by token", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.writeJSON(w, http.StatusOK, toBookingJSON(b))

	bCopy := *b
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if gc := h.getGCal(); gc != nil {
			var extEventID sql.NullString
			if err := h.db.QueryRowContext(ctx,
				`SELECT external_event_id FROM bookings WHERE id = ?`, bCopy.ID).
				Scan(&extEventID); err != nil && !errors.Is(err, sql.ErrNoRows) {
				h.logger.Error("cancel by token: fetch gcal event id", "error", err, "booking_id", bCopy.ID)
			} else if extEventID.Valid && extEventID.String != "" {
				if err := gc.CancelEvent(ctx, bCopy.HostID, extEventID.String); err != nil {
					h.logger.Error("cancel by token: gcal cancel", "error", err, "booking_id", bCopy.ID)
				}
			}
		}

		d, err := h.loadCancellationData(ctx, &bCopy)
		if err != nil {
			h.logger.Error("cancel by token: load email data", "error", err, "booking_id", bCopy.ID)
			return
		}
		d.BaseURL = h.baseURL

		prefs := allOnPrefs
		if p, err := h.loadHostPrefs(ctx, bCopy.HostID); err != nil {
			h.logger.Error("cancel by token: load host prefs", "error", err, "booking_id", bCopy.ID)
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
				h.logger.Error("cancel by token: email (attendee)", "error", err, "booking_id", bCopy.ID)
			}
		}
		if prefs.NotifyHostCancel {
			if err := mailer.SendCancellationToHost(ctx, h.mailer, d); err != nil {
				h.logger.Error("cancel by token: email (host)", "error", err, "booking_id", bCopy.ID)
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
			h.logger.Error("enqueue booking.cancelled webhook (by token)", "error", err, "booking_id", bCopy.ID)
		}
	}()
}

