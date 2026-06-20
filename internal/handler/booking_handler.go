package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/mailer"
	"github.com/calnode/calnode/internal/uid"
	"github.com/calnode/calnode/internal/webhook"
)

// maxBookingsPerEmailPerHour caps how many bookings one email address can create
// across the workspace in a rolling hour — a per-identity backstop to the per-IP
// rate limit, for the openly-public booking page.
const maxBookingsPerEmailPerHour = 10

// onlineMeetingLocation reports whether an event-type location wants the calendar
// provider to auto-create a native online meeting. The connected provider mints
// whatever it supports — Google Meet for Google, Microsoft Teams for Microsoft.
func onlineMeetingLocation(locType string) bool {
	return locType == "google_meet" || locType == "teams"
}

type attendeeJSON struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type bookingJSON struct {
	ID                 string         `json:"id"`
	EventTypeID        string         `json:"event_type_id"`
	EventTypeSlug      string         `json:"event_type_slug,omitempty"`
	HostID             string         `json:"host_id"`
	HostName           string         `json:"host_name,omitempty"` // set in the admin "All bookings" view
	StartAt            string         `json:"start_at"`
	EndAt              string         `json:"end_at"`
	Status             string         `json:"status"`
	CancellationReason string         `json:"cancellation_reason,omitempty"`
	LocationValue      string         `json:"location_value,omitempty"`
	CreatedAt          string         `json:"created_at"`
	UpdatedAt          string         `json:"updated_at"`
	Attendees          []attendeeJSON `json:"attendees,omitempty"`
	Hosts              []hostBrief    `json:"hosts,omitempty"` // assigned host(s) for display; set on the public create response
}

// hostBrief is an assigned host's identity for the booking-confirmation screen.
type hostBrief struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url,omitempty"`
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

// noGoogleDestination reports whether the given host has no Google destination
// calendar — the gate for attaching Calnode's own iCalendar invite to an email.
// When the host *has* a Google destination, Google already delivers a native
// invite (the booker is added as an attendee; the host owns the event), so our
// .ics would duplicate it. gc==nil (calendar feature off) ⇒ no destination ⇒ attach.
// On a lookup error we report false (don't attach): a missing .ics — recipients
// still have the add-to-calendar links — beats risking a duplicate invite.
//
// This is the single gate for the whole .ics feature. It is Google-specific today;
// when a provider that also auto-invites attendees is added (e.g. Microsoft Graph),
// this must return false for *its* destinations too, or those users get duplicate
// invites. Plain CalDAV (no iTIP scheduling) does NOT auto-invite, so an .ics is
// still wanted there — i.e. the right future shape is "no destination whose provider
// auto-delivers invites", not just "no Google".
func (h *Handler) noGoogleDestination(ctx context.Context, hostID string) bool {
	gc := h.getCal()
	if gc == nil {
		return true
	}
	has, err := gc.HasDestination(ctx, hostID)
	if err != nil {
		return false
	}
	return !has
}

// CreateBooking handles POST /v1/bookings (public — no auth required).
// The caller provides the event type slug and a start time selected from /slots.
// end_at is computed from event_type.duration_minutes.
//
// Phase 1: host is always event_type.user_id. Team routing (round_robin,
// collective with multiple hosts) will be added when team CRUD lands.
func (h *Handler) CreateBooking(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)

	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "could not read request body")
		return
	}

	var req struct {
		EventTypeSlug string `json:"event_type_slug"`
		StartAt       string `json:"start_at"`
		Name          string `json:"name"`
		Email         string `json:"email"`
		Timezone      string `json:"timezone"`
		Company       string `json:"company"` // honeypot: a hidden form field; must stay empty
		Answers       []struct {
			QuestionID string `json:"question_id"`
			Value      string `json:"value"`
		} `json:"answers"`
	}
	if err := json.Unmarshal(rawBody, &req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	// Honeypot: a field hidden from humans on the booking form. A non-empty value
	// means an automated submission — reject with a generic error.
	if strings.TrimSpace(req.Company) != "" {
		h.logger.InfoContext(r.Context(), "booking rejected: honeypot filled")
		h.writeError(w, http.StatusBadRequest, "invalid submission")
		return
	}

	// Idempotency-Key (optional): a client — e.g. an automation agent retrying a
	// timed-out request — may resend a booking POST with the same key. We reserve
	// the key now; the original response is replayed on a repeat, and the key is
	// released on any failure path (via the deferred cleanup below) so a retry
	// after an error can still proceed. The public booking page does not send a
	// key, so this path is inert for normal bookings.
	idemKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	idemDone := false
	if idemKey != "" {
		if len(idemKey) > 255 {
			h.writeError(w, http.StatusBadRequest, "Idempotency-Key must be at most 255 characters")
			return
		}
		reqHash := idemHash(rawBody)
		rec, replay, err := h.claimIdempotencyKey(r.Context(), idemKey, reqHash)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "create booking: claim idempotency key", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if replay {
			if rec.StatusCode == 0 {
				h.writeError(w, http.StatusConflict, "a request with this Idempotency-Key is still being processed")
				return
			}
			if rec.RequestHash != reqHash {
				h.writeError(w, http.StatusUnprocessableEntity, "Idempotency-Key was already used with a different request body")
				return
			}
			w.Header().Set("Idempotency-Replayed", "true")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(rec.StatusCode)
			_, _ = w.Write([]byte(rec.ResponseBody))
			return
		}
		// We own the key now. Release it on any non-success exit so the caller can
		// retry; the success path sets idemDone after storing the response.
		defer func() {
			if !idemDone {
				h.releaseIdempotencyKey(context.Background(), idemKey)
			}
		}()
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
		ID                string
		UserID            string
		Name              string
		DurationMinutes   int
		LocationType      string
		LocationValue     *string
		RoutingMode       string
		RRStrategy        string
		IsActive          int
		MaxActiveBookings int
	}
	err = h.db.QueryRowContext(r.Context(), `
		SELECT id, user_id, name, duration_minutes, location_type, location_value, routing_mode, rr_strategy, is_active, max_active_bookings
		FROM event_types WHERE slug = ?`, req.EventTypeSlug).
		Scan(&et.ID, &et.UserID, &et.Name, &et.DurationMinutes, &et.LocationType, &et.LocationValue, &et.RoutingMode, &et.RRStrategy, &et.IsActive, &et.MaxActiveBookings)
	if err != nil {
		h.writeError(w, http.StatusNotFound, "event type not found")
		return
	}
	if et.IsActive == 0 {
		h.writeError(w, http.StatusNotFound, "event type not found")
		return
	}

	// Per-email throttle: cap how many bookings one email can create across the
	// workspace in a rolling hour, independent of IP — backstops the per-IP rate
	// limit against a single identity spamming via rotating IPs. Counts cancelled
	// bookings too, so book/cancel/rebook churn is bounded. A query error is logged,
	// not fatal (don't block a legit booking on a transient read failure).
	{
		windowStart := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
		var recent int
		if err := h.db.QueryRowContext(r.Context(), `
			SELECT COUNT(*) FROM bookings b
			JOIN booking_attendees a ON a.booking_id = b.id AND a.is_organizer = 1
			WHERE a.email = ? COLLATE NOCASE AND b.created_at > ?`,
			req.Email, windowStart).Scan(&recent); err != nil {
			h.logger.ErrorContext(r.Context(), "create booking: per-email throttle", "error", err)
		} else if recent >= maxBookingsPerEmailPerHour {
			h.writeError(w, http.StatusTooManyRequests,
				"Too many bookings from this email address recently. Please try again later.")
			return
		}
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

	// Resolve candidate hosts by routing mode: rotation hosts for round-robin,
	// required hosts otherwise. Archived hosts are excluded by resolveEventTypeHosts.
	hosts, err := h.resolveEventTypeHosts(r.Context(), et.ID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "create booking: resolve hosts", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	var candidates, required, optional []string
	for _, hh := range hosts {
		if et.RoutingMode == "round_robin" {
			// rotation = the pool one is picked from; required = fixed hosts who
			// always attend; optional = join if free.
			switch hh.Role {
			case "rotation":
				candidates = append(candidates, hh.UserID)
			case "required":
				required = append(required, hh.UserID)
			case "optional":
				optional = append(optional, hh.UserID)
			}
		} else {
			// fixed + collective: required hosts must attend; optional join if free.
			switch hh.Role {
			case "required":
				candidates = append(candidates, hh.UserID)
			case "optional":
				optional = append(optional, hh.UserID)
			}
		}
	}
	if len(candidates) == 0 {
		// No active host can take this booking (e.g. the configured host was archived).
		h.writeError(w, http.StatusConflict, "this slot is no longer available")
		return
	}

	b, err := h.bookingSvc.Create(r.Context(), booking.CreateParams{
		EventTypeID:   et.ID,
		HostIDs:       candidates,
		RoutingMode:   et.RoutingMode,
		RRStrategy:    et.RRStrategy,
		RequiredHosts: required,
		OptionalHosts: optional,
		StartAt:       startAt.UTC(),
		EndAt:         endAt,
		LocationValue: locValue,
		Organizer: booking.Attendee{
			Name:         req.Name,
			Email:        req.Email,
			IANATimezone: req.Timezone,
		},
		Answers:             answers,
		MaxActivePerInvitee: et.MaxActiveBookings,
	})
	if err != nil {
		if errors.Is(err, booking.ErrDoubleBooked) {
			h.writeError(w, http.StatusConflict, "this slot is no longer available")
			return
		}
		// Not 409 — the booking page treats 409 as "slot taken". Use 422 so its
		// generic error branch surfaces this message verbatim to the invitee.
		if errors.Is(err, booking.ErrBookingLimitReached) {
			h.writeError(w, http.StatusUnprocessableEntity,
				"You already have the maximum number of upcoming bookings for this event. Please cancel an existing booking or wait until one has passed before booking again.")
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

	bj := toBookingJSON(b)
	for _, hd := range h.displayHostsForBooking(r.Context(), b.ID) {
		bj.Hosts = append(bj.Hosts, hostBrief{ID: hd.ID, Name: hd.Name, AvatarURL: hd.AvatarURL})
	}
	respBody, err := json.Marshal(bj)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "create booking: marshal response", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if idemKey != "" {
		// Persist the response so a retry with this key replays it verbatim. The
		// booking is already committed; a failure storing the key must not change
		// the outcome, so we mark it done either way (a stale pending row, if the
		// UPDATE failed, is purged by the worker's TTL sweep).
		if err := h.finishIdempotencyKey(r.Context(), idemKey, http.StatusCreated, respBody, b.ID); err != nil {
			h.logger.ErrorContext(r.Context(), "create booking: store idempotency response", "error", err)
		}
		idemDone = true
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write(respBody)

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
		BaseURL:           h.publicURL(),
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		h.applyBranding(ctx, &bData)
		// Every assigned host attends (Group books several; round-robin/Normal one).
		hosts, err := h.assignedHosts(ctx, b.ID)
		if err != nil || len(hosts) == 0 {
			h.logger.Error("booking confirmation: load assigned hosts", "error", err, "booking_id", b.ID)
			// Fall back to the primary host so notifications still go out.
			fb := assignedHost{UserID: b.HostID, IsPrimary: true}
			_ = h.db.QueryRowContext(ctx, `SELECT name, email FROM users WHERE id = ?`, b.HostID).
				Scan(&fb.Name, &fb.Email)
			hosts = []assignedHost{fb}
		}
		if tok, err := h.bookingSvc.IssueManageToken(ctx, b.ID); err != nil {
			h.logger.Error("issue manage token", "error", err, "booking_id", b.ID)
		} else {
			bData.ManageURL = h.publicURL() + "/manage/" + tok
		}
		var msgNote, subjNote sql.NullString
		_ = h.db.QueryRowContext(ctx, `SELECT msg_confirmation, subj_confirmation FROM event_types WHERE id = ?`, b.EventTypeID).
			Scan(&msgNote, &subjNote)
		if msgNote.Valid {
			bData.CustomNote = msgNote.String
		}
		if subjNote.Valid {
			bData.SubjectOverride = subjNote.String
		}

		gc := h.getCal()
		// For online-meeting event types (Google Meet / Teams), the primary host's
		// event mints the meeting link via their connected provider; the link is
		// captured, stored on the booking, surfaced in emails, and passed to any
		// secondary hosts' events as their location.
		wantMeet := onlineMeetingLocation(et.LocationType)
		meetURL := ""
		var primaryPrefs hostPrefs = allOnPrefs
		for _, host := range hosts {
			// Create a calendar event on each host's connected calendar and record
			// the per-host event ID so it can be cancelled later. The primary's id
			// also lives on the booking row for back-compat.
			if gc != nil {
				eventID, link, err := gc.CreateEvent(ctx, host.UserID, calendar.CreateEventParams{
					Summary:        et.Name + " with " + req.Name,
					Description:    "Booking ID: " + b.ID,
					Location:       meetURL, // empty until the primary creates it; secondary hosts get the link
					Start:          b.StartAt,
					End:            b.EndAt,
					OrganizerName:  req.Name,
					OrganizerEmail: req.Email,
					AddMeet:        wantMeet && host.IsPrimary,
				})
				if err != nil {
					h.logger.Error("create gcal event", "error", err, "booking_id", b.ID, "host", host.UserID)
					h.nudgeCalendarReconcile() // heal this missing event on a later sweep
				} else if eventID != "" {
					if _, err := h.db.ExecContext(ctx,
						`UPDATE booking_hosts SET external_event_id = ? WHERE booking_id = ? AND user_id = ?`,
						eventID, b.ID, host.UserID); err != nil {
						h.logger.Error("save host gcal event id", "error", err, "booking_id", b.ID)
					}
					if host.IsPrimary {
						if _, err := h.db.ExecContext(ctx,
							`UPDATE bookings SET external_event_id = ? WHERE id = ?`, eventID, b.ID); err != nil {
							h.logger.Error("save gcal event id", "error", err, "booking_id", b.ID)
						}
						// Persist the generated Meet link so the confirmation emails (sent
						// below), the booking record, and the manage page show it. bData
						// feeds the emails; updating it here reaches every send that follows.
						if link != "" {
							meetURL = link
							bData.LocationValue = link
							if _, err := h.db.ExecContext(ctx,
								`UPDATE bookings SET location_value = ? WHERE id = ?`, link, b.ID); err != nil {
								h.logger.Error("save meet link", "error", err, "booking_id", b.ID)
							}
						}
					}
				}
			}

			prefs := allOnPrefs
			if p, err := h.loadHostPrefs(ctx, host.UserID); err != nil {
				h.logger.Error("booking confirmation: load host prefs", "error", err, "booking_id", b.ID, "host", host.UserID)
			} else {
				prefs = p
			}
			if host.IsPrimary {
				primaryPrefs = prefs
			}
			if prefs.NotifyHostBooking {
				hd := bData
				hd.HostName, hd.HostEmail = host.Name, host.Email
				hd.AttachICS = h.noGoogleDestination(ctx, host.UserID) // per-host: their own calendar
				hd.ICSSequence = int(b.UpdatedAt.Unix())
				if err := mailer.SendConfirmationToHost(ctx, h.mailer, hd); err != nil {
					h.logger.Error("booking confirmation email (host)", "error", err, "booking_id", b.ID, "host", host.UserID)
				}
			}
		}

		// Attendee confirmation, once. "With:" names the primary host; gated on the
		// primary host's notification preference (matches prior behaviour).
		bData.HostName, bData.HostEmail = primaryHost(hosts).Name, primaryHost(hosts).Email
		bData.AttachICS = h.noGoogleDestination(ctx, b.HostID)
		bData.ICSSequence = int(b.UpdatedAt.Unix())
		if primaryPrefs.NotifyConfirmation {
			if err := mailer.SendConfirmationToAttendee(ctx, h.mailer, bData); err != nil {
				h.logger.Error("booking confirmation email (attendee)", "error", err, "booking_id", b.ID)
			}
		}
		if err := h.webhookSvc.Enqueue(ctx, "booking.created", webhook.BookingPayload{
			ID:            b.ID,
			EventTypeSlug: req.EventTypeSlug,
			HostID:        b.HostID,
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
// Returns bookings enriched with event_type_slug and the organizer attendee.
func (h *Handler) ListBookings(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())

	// Members see only bookings they host. Admins/owners may request the whole
	// workspace with ?scope=all (for team oversight); the scope is ignored for
	// non-admins so it can't be used to escalate visibility.
	allScope := r.URL.Query().Get("scope") == "all" && user.IsAdmin
	var bookings []booking.Booking
	var err error
	if allScope {
		bookings, err = h.bookingSvc.ListAll(r.Context())
	} else {
		bookings, err = h.bookingSvc.ListByHost(r.Context(), user.ID)
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list bookings", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	items := make([]bookingJSON, len(bookings))
	idxByID := make(map[string]int, len(bookings))
	for i := range bookings {
		items[i] = toBookingJSON(&bookings[i])
		items[i].Attendees = []attendeeJSON{} // ensure non-null in JSON
		idxByID[bookings[i].ID] = i
	}

	if len(items) == 0 {
		h.writeJSON(w, http.StatusOK, map[string]any{"items": items})
		return
	}

	// Build IN-clause args (booking IDs).
	ids := make([]any, len(bookings))
	for i, b := range bookings {
		ids[i] = b.ID
	}
	ph := strings.Repeat("?,", len(ids))
	ph = ph[:len(ph)-1]

	// Fetch event type slugs via a single JOIN.
	etRows, err := h.db.QueryContext(r.Context(),
		`SELECT b.id, COALESCE(et.slug, '') FROM bookings b
		 LEFT JOIN event_types et ON et.id = b.event_type_id
		 WHERE b.id IN (`+ph+`)`, ids...)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list bookings: slugs", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer etRows.Close()
	for etRows.Next() {
		var bid, slug string
		if err := etRows.Scan(&bid, &slug); err != nil {
			continue
		}
		if i, ok := idxByID[bid]; ok {
			items[i].EventTypeSlug = slug
		}
	}
	if err := etRows.Err(); err != nil {
		h.logger.ErrorContext(r.Context(), "list bookings: slugs scan", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// In the admin "All bookings" view, label each row with its host's name so the
	// admin can tell whose booking it is.
	if allScope {
		hRows, err := h.db.QueryContext(r.Context(),
			`SELECT b.id, COALESCE(u.name, '') FROM bookings b
			 LEFT JOIN users u ON u.id = b.host_id
			 WHERE b.id IN (`+ph+`)`, ids...)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "list bookings: host names", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		defer hRows.Close()
		for hRows.Next() {
			var bid, name string
			if err := hRows.Scan(&bid, &name); err != nil {
				continue
			}
			if i, ok := idxByID[bid]; ok {
				items[i].HostName = name
			}
		}
	}

	// Fetch organizer attendee for each booking.
	aRows, err := h.db.QueryContext(r.Context(),
		`SELECT booking_id, name, email FROM booking_attendees
		 WHERE booking_id IN (`+ph+`) AND is_organizer = 1`, ids...)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list bookings: attendees", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer aRows.Close()
	for aRows.Next() {
		var bid, name, email string
		if err := aRows.Scan(&bid, &name, &email); err != nil {
			continue
		}
		if i, ok := idxByID[bid]; ok {
			items[i].Attendees = append(items[i].Attendees, attendeeJSON{Name: name, Email: email})
		}
	}
	if err := aRows.Err(); err != nil {
		h.logger.ErrorContext(r.Context(), "list bookings: attendees scan", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
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

	// Admins (and the owner) may cancel any booking — needed to resolve a
	// departing member's meetings. Non-admin hosts can cancel only their own.
	var cancelErr error
	if user.IsAdmin {
		cancelErr = h.bookingSvc.CancelByID(r.Context(), id, req.Reason)
	} else {
		cancelErr = h.bookingSvc.Cancel(r.Context(), user.ID, id, req.Reason)
	}
	if err := cancelErr; err != nil {
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

	// Cancel calendar events and send cancellation emails in the background.
	go h.cancelSideEffects(*b)
}

// cancelSideEffects removes every assigned host's calendar event, notifies each
// host, emails the attendee, and fires the webhook for a cancelled booking. Runs
// in its own context so it can be launched in a goroutine after the response is
// written. Shared by the admin (CancelBooking) and manage-link (CancelByToken)
// cancel paths so both fan out across all hosts (Group bookings).
func (h *Handler) cancelSideEffects(b booking.Booking) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	d, err := h.loadCancellationData(ctx, &b)
	if err != nil {
		h.logger.Error("booking cancellation: load data", "error", err, "booking_id", b.ID)
		return
	}
	d.BaseURL = h.publicURL()
	h.applyBranding(ctx, &d)
	var msgNote, subjNote sql.NullString
	_ = h.db.QueryRowContext(ctx, `SELECT msg_cancellation, subj_cancellation FROM event_types WHERE id = ?`, b.EventTypeID).
		Scan(&msgNote, &subjNote)
	if msgNote.Valid {
		d.CustomNote = msgNote.String
	}
	if subjNote.Valid {
		d.SubjectOverride = subjNote.String
	}

	// Cancel each assigned host's calendar event and notify each host. Group
	// bookings put the meeting on several calendars; remove them all. Read the
	// host list fully first — the inner CancelEvent/loadHostPrefs queries can't run
	// while a cursor holds the single DB connection (would deadlock).
	gc := h.getCal()
	var primaryPrefs = allOnPrefs
	hosts, hErr := h.assignedHosts(ctx, b.ID)
	if hErr != nil {
		h.logger.Error("booking cancellation: load hosts", "error", hErr, "booking_id", b.ID)
	}
	for _, host := range hosts {
		if gc != nil && host.ExternalEventID != "" {
			if err := gc.CancelEvent(ctx, host.UserID, host.ExternalEventID); err != nil {
				h.logger.Error("cancel gcal event", "error", err, "booking_id", b.ID, "host", host.UserID)
				h.nudgeCalendarReconcile() // event still on the calendar — heal on a later sweep
			} else {
				// Event removed — clear the id (matching the reconciler) so it isn't
				// re-processed and so own-event exclusion, which keys on a non-empty
				// external_event_id, stops treating this slot as ours.
				if _, err := h.db.ExecContext(ctx,
					`UPDATE booking_hosts SET external_event_id = NULL WHERE booking_id = ? AND user_id = ?`,
					b.ID, host.UserID); err != nil {
					h.logger.Error("clear cancelled event id", "error", err, "booking_id", b.ID, "host", host.UserID)
				}
				if host.IsPrimary {
					if _, err := h.db.ExecContext(ctx,
						`UPDATE bookings SET external_event_id = NULL WHERE id = ?`, b.ID); err != nil {
						h.logger.Error("clear cancelled booking event id", "error", err, "booking_id", b.ID)
					}
				}
			}
		}
		prefs := allOnPrefs
		if p, err := h.loadHostPrefs(ctx, host.UserID); err == nil {
			prefs = p
		}
		if host.IsPrimary {
			primaryPrefs = prefs
			d.HostName, d.HostEmail = host.Name, host.Email // attendee "With:" = primary host, not owner
		}
		if prefs.NotifyHostCancel {
			hd := d
			hd.HostName, hd.HostEmail = host.Name, host.Email
			hd.AttachICS = h.noGoogleDestination(ctx, host.UserID) // per-host: their own calendar
			hd.ICSSequence = int(b.UpdatedAt.Unix())
			if err := mailer.SendCancellationToHost(ctx, h.mailer, hd); err != nil {
				h.logger.Error("booking cancellation email (host)", "error", err, "booking_id", b.ID, "host", host.UserID)
			}
		}
	}
	d.AttachICS = h.noGoogleDestination(ctx, b.HostID)
	d.ICSSequence = int(b.UpdatedAt.Unix())
	if primaryPrefs.NotifyCancellation {
		if err := mailer.SendCancellationToAttendee(ctx, h.mailer, d); err != nil {
			h.logger.Error("booking cancellation email (attendee)", "error", err, "booking_id", b.ID)
		}
	}
	if err := h.webhookSvc.Enqueue(ctx, "booking.cancelled", webhook.BookingPayload{
		ID:                 b.ID,
		EventTypeSlug:      d.EventTypeSlug,
		HostID:             b.HostID,
		StartAt:            b.StartAt.UTC().Format(time.RFC3339),
		EndAt:              b.EndAt.UTC().Format(time.RFC3339),
		Status:             b.Status,
		CancellationReason: b.CancellationReason,
		LocationValue:      b.LocationValue,
		CreatedAt:          b.CreatedAt.UTC().Format(time.RFC3339),
	}); err != nil {
		h.logger.Error("enqueue booking.cancelled webhook", "error", err, "booking_id", b.ID)
	}
}

// moveCalendarEvents moves every assigned host's calendar event to a new
// start/end after a reschedule. Best-effort; a failure leaves that host's event
// at the old time (logged). Group bookings move all hosts' events.
func (h *Handler) moveCalendarEvents(ctx context.Context, bookingID string, start, end time.Time) {
	gc := h.getCal()
	if gc == nil {
		return
	}
	hosts, err := h.assignedHosts(ctx, bookingID)
	if err != nil {
		h.logger.Error("reschedule: load hosts for calendar move", "error", err, "booking_id", bookingID)
		return
	}
	for _, host := range hosts {
		if host.ExternalEventID == "" {
			continue
		}
		if err := gc.UpdateEvent(ctx, host.UserID, host.ExternalEventID, start, end); err != nil {
			// The event is now at the wrong time — flag it so the reconciler re-applies
			// the move on a later sweep (drift can't be inferred from booking state).
			h.logger.Error("reschedule: move gcal event", "error", err, "booking_id", bookingID, "host", host.UserID)
			h.setNeedsSync(ctx, bookingID, host.UserID, true)
			h.nudgeCalendarReconcile()
		} else {
			// Succeeded — clear any flag a previous failed move may have left.
			h.setNeedsSync(ctx, bookingID, host.UserID, false)
		}
	}
}

// setNeedsSync marks (or clears) a host's calendar event as out-of-sync with the
// booking's time. Best-effort; a failure here just delays self-healing.
func (h *Handler) setNeedsSync(ctx context.Context, bookingID, userID string, dirty bool) {
	v := 0
	if dirty {
		v = 1
	}
	if _, err := h.db.ExecContext(ctx,
		`UPDATE booking_hosts SET needs_sync = ? WHERE booking_id = ? AND user_id = ?`,
		v, bookingID, userID); err != nil {
		h.logger.Error("set needs_sync", "error", err, "booking_id", bookingID, "host", userID)
	}
}

// assignedHost is one host attending a booking (from booking_hosts).
type assignedHost struct {
	UserID          string
	Name            string
	Email           string
	IsPrimary       bool
	ExternalEventID string // per-host calendar event id, if one was created
}

// assignedHosts returns every host attending a booking, primary first. Group
// bookings have several; round-robin/Normal have one. The full result is read
// into a slice before returning so callers can run further queries per host —
// the DB pool is capped at one connection, so holding an open cursor while
// querying inside the loop would deadlock.
func (h *Handler) assignedHosts(ctx context.Context, bookingID string) ([]assignedHost, error) {
	rows, err := h.db.QueryContext(ctx, `
		SELECT bh.user_id, u.name, u.email, bh.is_primary, COALESCE(bh.external_event_id, '')
		FROM booking_hosts bh JOIN users u ON u.id = bh.user_id
		WHERE bh.booking_id = ?
		ORDER BY bh.is_primary DESC, u.name ASC`, bookingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []assignedHost
	for rows.Next() {
		var a assignedHost
		var primary int
		if err := rows.Scan(&a.UserID, &a.Name, &a.Email, &primary, &a.ExternalEventID); err != nil {
			return nil, err
		}
		a.IsPrimary = primary != 0
		out = append(out, a)
	}
	return out, rows.Err()
}

// displayHostsForBooking returns the assigned host(s) of a booking for display
// (primary first), with name + avatar — used by the manage page and the create
// confirmation response so they show who's actually attending, not the owner.
func (h *Handler) displayHostsForBooking(ctx context.Context, bookingID string) []hostDisplay {
	rows, err := h.db.QueryContext(ctx, `
		SELECT bh.user_id, u.name, COALESCE(u.avatar_url, '')
		FROM booking_hosts bh JOIN users u ON u.id = bh.user_id
		WHERE bh.booking_id = ?
		ORDER BY bh.is_primary DESC, u.name ASC`, bookingID)
	if err != nil {
		h.logger.ErrorContext(ctx, "display hosts for booking", "error", err, "booking_id", bookingID)
		return nil
	}
	defer rows.Close()
	var out []hostDisplay
	for rows.Next() {
		var hd hostDisplay
		if err := rows.Scan(&hd.ID, &hd.Name, &hd.AvatarURL); err != nil {
			continue
		}
		hd.Initial = firstRune(hd.Name)
		out = append(out, hd)
	}
	return out
}

// primaryHost returns the primary host (the one flagged is_primary, else the
// first). hosts must be non-empty.
func primaryHost(hosts []assignedHost) assignedHost {
	for _, a := range hosts {
		if a.IsPrimary {
			return a
		}
	}
	return hosts[0]
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
