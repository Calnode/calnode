package handler

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"time"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/i18n"
	"github.com/calnode/calnode/internal/slots"
)

func invitationTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Lookup never logs the credential. Terminal records are readable only to show
// a safe state; neither their metadata nor their booking-management credentials
// are exposed by the consumed invitation token.
func (h *Handler) invitationByToken(ctx context.Context, token string) (*schedulingInvitation, error) {
	if len(token) != 64 {
		return nil, sql.ErrNoRows
	}
	if _, err := hex.DecodeString(token); err != nil {
		return nil, sql.ErrNoRows
	}
	var id, creator string
	if err := h.db.QueryRowContext(ctx, `SELECT i.id,i.created_by FROM scheduling_invitation_tokens t JOIN scheduling_invitations i ON i.id=t.invitation_id WHERE t.token_hash=?`, invitationTokenHash(token)).Scan(&id, &creator); err != nil {
		return nil, err
	}
	return h.loadSchedulingInvitation(ctx, id, creator)
}

func invitationHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
}

func invitationState(inv *schedulingInvitation) string {
	if inv.Status == "booked" {
		return "This invitation has already been booked. Use the booking-management link in your confirmation."
	}
	if inv.Status == "cancelled" {
		return "This invitation has been cancelled."
	}
	if inv.Status == "expired" || !inv.ExpiresAt.After(time.Now()) {
		return "This invitation has expired."
	}
	return "This invitation is currently unavailable. Contact the sender."
}

func (h *Handler) GetPublicInvitationSlots(w http.ResponseWriter, r *http.Request) {
	invitationHeaders(w)
	inv, err := h.invitationByToken(r.Context(), r.PathValue("token"))
	if err != nil {
		h.writeError(w, 404, "Invalid invitation link.")
		return
	}
	schedule, err := h.invitationSchedulingContext(r.Context(), inv)
	if err != nil {
		h.writeError(w, 410, invitationState(inv))
		return
	}
	h.writeInvitationSlots(w, r, schedule)
}

func (h *Handler) writeInvitationSlots(w http.ResponseWriter, r *http.Request, schedule schedulingContext) {
	// Calendar clients ask for a month at a time. Bound past ranges too: the
	// shared event-type parser only caps the upper bound, which would otherwise
	// allow a bearer to request a walk starting centuries before today.
	if value := r.URL.Query().Get("from"); value != "" {
		from, err := time.Parse("2006-01-02", value)
		if err != nil || from.Before(time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -31)) {
			h.writeError(w, 400, "from must be a calendar date within the last 31 days or in the future")
			return
		}
	}
	res, err := h.computeSlotsForContext(r.Context(), schedule, r.URL.Query().Get("tz"), r.URL.Query().Get("from"), r.URL.Query().Get("to"), slotsWanted{NoticeGap: true})
	if err != nil {
		if errors.Is(err, errInvalidTimezone) || errors.Is(err, errBadDateRange) {
			h.writeError(w, 400, err.Error())
		} else {
			h.writeError(w, 503, "Availability could not be checked. Try again shortly.")
		}
		return
	}
	body := map[string]any{"slots": res.Slots, "hosts": res.Hosts}
	// Credential pages must not fetch remote avatars.
	for _, host := range res.Hosts {
		host["avatar_url"] = credentialImage(host["avatar_url"])
	}
	if res.Degraded {
		body["degraded"] = true
	}
	if res.MinNoticeMinutes > 0 {
		body["min_notice"] = map[string]any{"minutes": res.MinNoticeMinutes, "dates": res.MinNoticeDates}
	}
	h.writeJSON(w, 200, body)
}

// recheckSchedulingContext uses the same generator for grid, hours, window,
// duration, notice and buffers. It can run on the booking transaction; all
// external calendar calls have already finished before this local recheck.
func recheckSchedulingContext(ctx context.Context, source dbQuerier, schedule schedulingContext, start time.Time) (map[string]bool, error) {
	var active, notice, future int
	if err := source.QueryRowContext(ctx, `SELECT is_active,min_notice_minutes,max_future_days FROM event_types WHERE id=? AND archived_at IS NULL`, schedule.EventTypeID).Scan(&active, &notice, &future); err != nil || active == 0 {
		return nil, errSlotUnavailable
	}
	schedule.Event.MinNoticeMinutes = max(schedule.Event.MinNoticeMinutes, notice)
	if future > 0 && (schedule.Event.MaxFutureDays == 0 || future < schedule.Event.MaxFutureDays) {
		schedule.Event.MaxFutureDays = future
	}
	day := start.UTC().Truncate(24 * time.Hour)
	free := map[string]bool{}
	var gates []slots.HostAvailability
	for _, host := range schedule.Hosts {
		var archived sql.NullString
		if err := source.QueryRowContext(ctx, `SELECT archived_at FROM users WHERE id=?`, host.UserID).Scan(&archived); err != nil {
			return nil, errSlotUnavailable
		}
		if archived.Valid {
			if host.Role == "required" {
				return nil, errSlotUnavailable
			}
			continue
		}
		loc, rules, overrides, err := loadHostScheduleFrom(ctx, source, host.UserID, schedule.EventTypeID)
		if err != nil {
			return nil, err
		}
		ha := slots.HostAvailability{HostID: host.UserID, Role: host.Role, Location: loc, Rules: rules, Overrides: overrides}
		busyFrom, busyTo := schedulingBusyRange(schedule.Event, day.AddDate(0, 0, -1), day.AddDate(0, 0, 1))
		rows, err := source.QueryContext(ctx, `SELECT b.start_at,b.end_at FROM bookings b JOIN booking_hosts bh ON bh.booking_id=b.id WHERE bh.user_id=? AND b.status!='cancelled' AND b.id!=? AND julianday(b.end_at)>julianday(?) AND julianday(b.start_at)<julianday(?)`, host.UserID, schedule.ExcludeBookingID, busyFrom.Format(time.RFC3339), busyTo.Format(time.RFC3339))
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var ss, es string
			if err := rows.Scan(&ss, &es); err != nil {
				rows.Close()
				return nil, err
			}
			s, e1 := time.Parse(time.RFC3339Nano, ss)
			e, e2 := time.Parse(time.RFC3339Nano, es)
			if e1 != nil || e2 != nil {
				rows.Close()
				return nil, errSlotUnavailable
			}
			ha.Busy = append(ha.Busy, slots.Interval{Start: s, End: e})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		single := schedule.Event
		single.RoutingMode = "fixed"
		offered, err := slots.Generate(slots.Request{Event: single, Hosts: []slots.HostAvailability{ha}, DateFrom: day.AddDate(0, 0, -1), DateTo: day.AddDate(0, 0, 1), BookerTZ: time.UTC, Now: time.Now().UTC(), AllowedWindow: schedule.AllowedWindow})
		if err != nil {
			return nil, err
		}
		for _, slot := range offered {
			if slot.Start.Equal(start) {
				free[host.UserID] = true
				break
			}
		}
		if host.Role != "optional" {
			gates = append(gates, ha)
		}
	}
	offered, err := slots.Generate(slots.Request{Event: schedule.Event, Hosts: gates, DateFrom: day.AddDate(0, 0, -1), DateTo: day.AddDate(0, 0, 1), BookerTZ: time.UTC, Now: time.Now().UTC(), AllowedWindow: schedule.AllowedWindow})
	if err != nil {
		return nil, err
	}
	for _, slot := range offered {
		if slot.Start.Equal(start) {
			return free, nil
		}
	}
	return nil, errSlotUnavailable
}

func (h *Handler) CreateInvitationBooking(w http.ResponseWriter, r *http.Request) {
	invitationHeaders(w)
	inv, err := h.invitationByToken(r.Context(), r.PathValue("token"))
	if err != nil {
		h.writeError(w, 404, "Invalid invitation link.")
		return
	}
	schedule, err := h.invitationSchedulingContext(r.Context(), inv)
	if err != nil {
		h.writeError(w, 410, invitationState(inv))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req struct {
		StartAt  time.Time `json:"start_at"`
		Timezone string    `json:"timezone"`
		Answers  []struct {
			QuestionID string `json:"question_id"`
			Value      string `json:"value"`
		} `json:"answers"`
		Language string `json:"language"`
		Honeypot string `json:"hp_extra"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || req.StartAt.IsZero() {
		h.writeError(w, 400, "Submit start_at, timezone and intake answers only.")
		return
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		h.writeError(w, 400, "invalid JSON")
		return
	}
	if req.Honeypot != "" {
		h.writeError(w, 400, "invalid request")
		return
	}
	if req.Timezone == "" {
		req.Timezone = "UTC"
	}
	if _, err := time.LoadLocation(req.Timezone); err != nil {
		h.writeError(w, 400, "invalid timezone")
		return
	}
	loc := h.resolveLocale(r)
	if chosen := i18n.Get(req.Language); chosen != nil {
		loc = chosen
	}
	rawAnswers := make([]booking.Answer, 0, len(req.Answers))
	for _, answer := range req.Answers {
		rawAnswers = append(rawAnswers, booking.Answer{QuestionID: answer.QuestionID, Value: answer.Value})
	}
	answers, err := h.validateAnswersCore(r.Context(), inv.EventTypeID, rawAnswers, loc)
	if err != nil {
		h.writeError(w, 400, err.Error())
		return
	}
	var name, slug, delivery string
	var cap int
	if err := h.db.QueryRowContext(r.Context(), `SELECT name,slug,invite_delivery,max_active_bookings FROM event_types WHERE id=?`, inv.EventTypeID).Scan(&name, &slug, &delivery, &cap); err != nil {
		h.writeError(w, 410, "Invitation unavailable.")
		return
	}
	candidates, required, optional := resolveBookingHostPool(schedule.Hosts, schedule.Event.RoutingMode)
	end := req.StartAt.UTC().Add(time.Duration(inv.DurationMinutes) * time.Minute)
	if _, err := recheckSchedulingContext(r.Context(), h.db, schedule, req.StartAt.UTC()); err != nil {
		h.invitationBookingError(w, err)
		return
	}
	et := &bookableEventType{ID: inv.EventTypeID, RoutingMode: inv.RoutingMode, BufferBeforeMinutes: inv.BufferBeforeMinutes, BufferAfterMinutes: inv.BufferAfterMinutes}
	candidates, optional, err = h.calendarFreeHosts(r.Context(), et, candidates, required, optional, req.StartAt.UTC(), end)
	if err != nil {
		h.invitationBookingError(w, err)
		return
	}
	p := booking.CreateParams{
		EventTypeID:         inv.EventTypeID,
		HostIDs:             candidates,
		RequiredHosts:       required,
		OptionalHosts:       optional,
		RoutingMode:         inv.RoutingMode,
		RRStrategy:          inv.RRStrategy,
		StartAt:             req.StartAt.UTC(),
		EndAt:               end,
		LocationType:        inv.LocationType,
		LocationValue:       inv.LocationValue,
		InviteDelivery:      delivery,
		MaxActivePerInvitee: cap,
		MaxBookingsPerHour:  maxBookingsPerEmailPerHour,
		Organizer: booking.Attendee{
			Name:         inv.Recipient.Name,
			Email:        inv.Recipient.Email,
			IANATimezone: req.Timezone,
			Locale:       loc.Code,
		},
		Answers:                answers,
		SchedulingInvitationID: inv.ID,
		InvitationTokenHash:    invitationTokenHash(r.PathValue("token")),
		BufferBeforeMinutes:    inv.BufferBeforeMinutes,
		BufferAfterMinutes:     inv.BufferAfterMinutes,
	}
	p.ValidateTx = func(ctx context.Context, tx *sql.Tx, p *booking.CreateParams) error {
		free, err := recheckSchedulingContext(ctx, tx, schedule, p.StartAt)
		if err != nil {
			return err
		}
		p.HostIDs = slices.DeleteFunc(p.HostIDs, func(id string) bool { return !free[id] })
		p.OptionalHosts = slices.DeleteFunc(p.OptionalHosts, func(id string) bool { return !free[id] })
		return nil
	}
	b, err := h.bookingSvc.Create(r.Context(), p)
	if err != nil {
		h.invitationBookingError(w, err)
		return
	}
	go h.dispatchBookingConfirmation(b, bookingConfirmationInput{EventTypeName: name, EventTypeSlug: slug, LocationType: inv.LocationType, OrganizerName: inv.Recipient.Name, OrganizerEmail: inv.Recipient.Email, OrganizerTimezone: req.Timezone, OrganizerLocale: loc.Code}) // #nosec G118 -- existing best-effort booking side effects use their own context
	h.writeJSON(w, 201, map[string]any{"id": b.ID, "start_at": b.StartAt, "end_at": b.EndAt, "status": b.Status})
}

func (h *Handler) invitationBookingError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, booking.ErrInvitationUnavailable):
		h.writeError(w, 410, "This invitation is no longer available.")
	case errors.Is(err, errSlotUnavailable), errors.Is(err, booking.ErrDoubleBooked), errors.Is(err, errNoHostAvailable):
		h.writeError(w, 409, "This slot is no longer available. Choose another time.")
	case errors.Is(err, booking.ErrEmailThrottled):
		h.writeError(w, 429, "Too many bookings. Try again later.")
	case errors.Is(err, booking.ErrBookingLimitReached):
		h.writeError(w, 409, "The active booking limit has been reached.")
	default:
		h.writeError(w, 503, "Booking could not be completed. Try again shortly.")
	}
}
