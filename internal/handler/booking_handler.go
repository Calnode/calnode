package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/mailer"
	"github.com/calnode/calnode/internal/uid"
	"github.com/calnode/calnode/internal/webhook"
	"github.com/calnode/calnode/internal/zoom"
)

// maxBookingsPerEmailPerHour caps how many bookings one email address can create
// across the workspace in a rolling hour — a per-identity backstop to the per-IP
// rate limit, for the openly-public booking page.
const maxBookingsPerEmailPerHour = 10

// paymentStatusForWebhook maps the internal payment_status to the webhook value, treating
// the free-booking sentinel "none" as empty so the omitempty field is dropped for free bookings.
func paymentStatusForWebhook(s string) string {
	if s == "none" {
		return ""
	}
	return s
}

// onlineMeetingLocation reports whether an event-type location is a native online
// meeting type (Google Meet or Microsoft Teams) that may be auto-generated.
func onlineMeetingLocation(locType string) bool {
	return locType == "google_meet" || locType == "teams"
}

// providerMintsPlatform reports whether a connected calendar provider can natively
// auto-generate the meeting link for the given online location type: Google Meet
// only from a Google calendar, Microsoft Teams only from a Microsoft calendar. When
// the chosen platform doesn't match the host's connected provider we must NOT
// fabricate a link of the wrong kind — the organizer's manually-entered link is
// used instead. (Personal Microsoft accounts can't mint Teams-for-Business links;
// that surfaces at runtime as an empty link, which falls back the same way.)
func providerMintsPlatform(locType, provider string) bool {
	switch locType {
	case "google_meet":
		return provider == "google"
	case "teams":
		return provider == "microsoft"
	default:
		return false
	}
}

// platformLabel is the human name for an online location type.
func platformLabel(locType string) string {
	switch locType {
	case "teams":
		return "Microsoft Teams"
	case "google_meet":
		return "Google Meet"
	default:
		return locType
	}
}

// validMeetingLink reports whether v is a plausible join link for an online
// platform (host-based): Teams on teams.microsoft.com / *.teams.microsoft.com /
// teams.live.com / *.teams.microsoft.us; Google Meet on meet.google.com. Used to
// allow saving an online-meeting event type when the organizer can't auto-generate
// the link and must supply one. Non-online types are not validated here.
func validMeetingLink(locType, v string) bool {
	u, err := url.Parse(strings.TrimSpace(v))
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return false
	}
	host := strings.ToLower(u.Host)
	switch locType {
	case "teams":
		return host == "teams.microsoft.com" || strings.HasSuffix(host, ".teams.microsoft.com") ||
			host == "teams.live.com" || host == "teams.microsoft.us" || strings.HasSuffix(host, ".teams.microsoft.us")
	case "google_meet":
		return host == "meet.google.com"
	default:
		return true
	}
}

// validVideoURL validates a manual video-meeting URL: any https URL for the generic
// "link"/"custom_video" types, restricted to the Zoom host family for "zoom" so an
// obviously-wrong link (e.g. a Google Meet URL) pasted into the Zoom field is caught.
func validVideoURL(locType, v string) bool {
	u, err := url.Parse(strings.TrimSpace(v))
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return false
	}
	if locType == "zoom" {
		host := strings.ToLower(u.Host)
		return host == "zoom.us" || strings.HasSuffix(host, ".zoom.us") ||
			host == "zoomgov.com" || strings.HasSuffix(host, ".zoomgov.com")
	}
	return true
}

// validPhone is a lenient phone-number check: at least 6 digits, and only
// phone-ish characters (digits, +, spaces, and - ( ) . plus an x/X extension
// marker). Deliberately not strict E.164 so local formats and extensions pass.
func validPhone(v string) bool {
	v = strings.TrimSpace(v)
	digits := 0
	for _, r := range v {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r == '+' || r == '-' || r == ' ' || r == '(' || r == ')' || r == '.' || r == 'x' || r == 'X':
			// allowed separator / extension marker
		default:
			return false
		}
	}
	return digits >= 6
}

// validateLocation enforces that an event type's location carries usable join info:
// Teams/Meet need an auto-capable connected calendar or a valid manual link;
// Zoom/Video link need a valid https URL; Phone needs a phone number. In-person and
// any other type impose no value requirement. Returns a user-facing error (nil = ok).
func (h *Handler) validateLocation(ctx context.Context, ownerID, locType string, locValue *string) error {
	link := ""
	if locValue != nil {
		link = strings.TrimSpace(*locValue)
	}
	switch locType {
	case "teams", "google_meet":
		if link != "" {
			if !validMeetingLink(locType, link) {
				return fmt.Errorf("enter a valid %s link", platformLabel(locType))
			}
			return nil // a valid manual link always satisfies
		}
		// No link — only allowed when the owner's calendar can auto-generate it.
		if cal := h.getCal(); cal != nil {
			ok, err := cal.CanAutoGenerate(ctx, ownerID, locType)
			if err != nil {
				h.logger.ErrorContext(ctx, "validate location: capability lookup", "error", err, "owner", ownerID)
				return nil // don't block on a transient lookup error; booking-time fallback covers it
			}
			if ok {
				return nil
			}
		}
		if locType == "teams" {
			return fmt.Errorf("connect a Microsoft 365 work account to auto-generate Teams links, or enter a Teams link")
		}
		return fmt.Errorf("connect a Google calendar to auto-generate Meet links, or enter a Google Meet link")
	case "zoom":
		if link != "" {
			if !validVideoURL(locType, link) {
				return fmt.Errorf("enter a valid Zoom link (https://…zoom.us/…)")
			}
			return nil // a valid manual link always satisfies
		}
		// No link — only allowed when the owner has connected their Zoom account, so a
		// real meeting can be auto-minted per booking.
		if zc := h.getZoom(); zc != nil {
			if connected, err := zc.Connected(ctx, ownerID); err == nil && connected {
				return nil
			}
		}
		return fmt.Errorf("connect your Zoom account to auto-generate meeting links, or enter a Zoom link")
	case "livekit":
		// Built-in video: the room is always minted server-side per booking, so there's no
		// manual link (that's what the "Video link" type is for). LiveKit must be configured.
		if h.getLiveKit() != nil {
			return nil
		}
		return fmt.Errorf("configure LiveKit in Settings → Video to host built-in video rooms")
	case "link", "custom_video":
		if !validVideoURL(locType, link) {
			return fmt.Errorf("enter a valid meeting URL (https://…)")
		}
		return nil
	case "phone":
		if !validPhone(link) {
			return fmt.Errorf("enter a valid phone number")
		}
		return nil
	default: // in_person and anything else: no value requirement
		return nil
	}
}

// smartDefaultLocation picks the default location type for a new event type based on
// the owner's connected calendar: Google Meet for Google, Teams for a work Microsoft
// account (both auto-generate, so the event type is bookable with no manual link),
// falling back to Zoom (top of the picker) when nothing auto-capable is connected.
func (h *Handler) smartDefaultLocation(ctx context.Context, ownerID string) string {
	if cal := h.getCal(); cal != nil {
		if connected, provider, err := cal.Connected(ctx, ownerID); err == nil && connected {
			switch provider {
			case "google":
				return "google_meet"
			case "microsoft":
				if ok, _ := cal.CanAutoGenerate(ctx, ownerID, "teams"); ok {
					return "teams"
				}
			}
		}
	}
	return "zoom"
}

// resolveBookingHostPool splits an event type's resolved hosts into the candidate,
// required, and optional pools that booking.Create expects, per routing mode. For
// round_robin: rotation hosts are candidates (one is picked), required hosts always
// attend, optional join if free. For fixed/collective: required hosts are the
// candidates (all must be free), optional join if free. Shared by the REST
// CreateBooking handler and the MCP create_booking tool.
func resolveBookingHostPool(hosts []EventHost, routingMode string) (candidates, required, optional []string) {
	for _, hh := range hosts {
		if routingMode == "round_robin" {
			switch hh.Role {
			case "rotation":
				candidates = append(candidates, hh.UserID)
			case "required":
				required = append(required, hh.UserID)
			case "optional":
				optional = append(optional, hh.UserID)
			}
		} else {
			switch hh.Role {
			case "required":
				candidates = append(candidates, hh.UserID)
			case "optional":
				optional = append(optional, hh.UserID)
			}
		}
	}
	return candidates, required, optional
}

// errNoHostAvailable means no active host can take a booking (e.g. the configured host
// was archived) — surfaced to callers as "this slot is no longer available".
var errNoHostAvailable = errors.New("no active host can take this booking")

// errPaymentRequired is returned by the shared booking core for paid event types, which can
// only be booked through the Stripe Checkout flow on the booking page (agents can't pay).
var errPaymentRequired = errors.New("this event requires payment; please book on the booking page")

// createBookingForSlug is the shared public booking-creation path for one event-type slug:
// look up the (active) event type, validate intake answers, resolve the host pool, persist,
// and fire the side effects (calendar/email/webhook/reminders) in the background. Returns the
// booking. Shared by the MCP create_booking tool and the conversational booking assistant —
// no parallel code path. Errors are raw (errEventTypeNotFound, *answerError,
// booking.ErrDoubleBooked, booking.ErrBookingLimitReached, errNoHostAvailable) for callers to
// map to their own protocol.
func (h *Handler) createBookingForSlug(ctx context.Context, slug string, startAt time.Time, organizer booking.Attendee, rawAnswers []booking.Answer) (*booking.Booking, error) {
	var et struct {
		ID                string
		Name              string
		DurationMinutes   int
		LocationType      string
		LocationValue     *string
		RoutingMode       string
		RRStrategy        string
		IsActive          int
		MaxActiveBookings int
		PriceCents        int
	}
	err := h.db.QueryRowContext(ctx, `
		SELECT id, name, duration_minutes, location_type, location_value, routing_mode, rr_strategy, is_active, max_active_bookings, price_cents
		FROM event_types WHERE slug = ?`, slug).
		Scan(&et.ID, &et.Name, &et.DurationMinutes, &et.LocationType, &et.LocationValue, &et.RoutingMode, &et.RRStrategy, &et.IsActive, &et.MaxActiveBookings, &et.PriceCents)
	if err != nil || et.IsActive == 0 {
		return nil, errEventTypeNotFound
	}
	// Paid events require the Stripe Checkout flow (booking page only) — agents/assistant
	// can't pay, so they can't book a paid event.
	if et.PriceCents > 0 {
		return nil, errPaymentRequired
	}
	answers, err := h.validateAnswersCore(ctx, et.ID, rawAnswers)
	if err != nil {
		return nil, err
	}
	endAt := startAt.UTC().Add(time.Duration(et.DurationMinutes) * time.Minute)
	locValue := ""
	if et.LocationValue != nil {
		locValue = *et.LocationValue
	}
	hosts, err := h.resolveEventTypeHosts(ctx, et.ID)
	if err != nil {
		return nil, fmt.Errorf("resolve event-type hosts: %w", err)
	}
	candidates, required, optional := resolveBookingHostPool(hosts, et.RoutingMode)
	if len(candidates) == 0 {
		return nil, errNoHostAvailable
	}
	b, err := h.bookingSvc.Create(ctx, booking.CreateParams{
		EventTypeID:         et.ID,
		HostIDs:             candidates,
		RoutingMode:         et.RoutingMode,
		RRStrategy:          et.RRStrategy,
		RequiredHosts:       required,
		OptionalHosts:       optional,
		StartAt:             startAt.UTC(),
		EndAt:               endAt,
		LocationValue:       locValue,
		Organizer:           organizer,
		Answers:             answers,
		MaxActivePerInvitee: et.MaxActiveBookings,
	})
	if err != nil {
		return nil, err
	}
	go h.dispatchBookingConfirmation(b, bookingConfirmationInput{
		EventTypeName:     et.Name,
		EventTypeSlug:     slug,
		LocationType:      et.LocationType,
		OrganizerName:     organizer.Name,
		OrganizerEmail:    organizer.Email,
		OrganizerTimezone: organizer.IANATimezone,
	})
	return b, nil
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
	PaymentStatus      string         `json:"payment_status,omitempty" jsonschema:"payment state for paid event types: paid, refunded, or pending; absent for free bookings"`
	AmountPaidCents    int            `json:"amount_paid_cents,omitempty" jsonschema:"amount charged in minor units (e.g. cents); absent for free bookings"`
	AmountPaidCurrency string         `json:"amount_paid_currency,omitempty" jsonschema:"ISO 4217 currency of the charge (lowercase)"`
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
	j := bookingJSON{
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
	// Payment fields are omitted for free bookings (payment_status defaults to 'none').
	if b.PaymentStatus != "" && b.PaymentStatus != "none" {
		j.PaymentStatus = b.PaymentStatus
		j.AmountPaidCents = b.AmountPaidCents
		j.AmountPaidCurrency = b.AmountPaidCurrency
	}
	return j
}

// noConnectedDestination reports whether the given host has no connected destination
// calendar — the gate for attaching Calnode's own iCalendar invite to an email.
// When the host *has* a destination on a provider that auto-invites attendees
// (Google, Microsoft Graph), that provider already delivers a native invite, so our
// .ics would duplicate it. gc==nil (calendar feature off) ⇒ no destination ⇒ attach.
// On a lookup error we report false (don't attach): a missing .ics — recipients
// still have the add-to-calendar links — beats risking a duplicate invite.
//
// This is the single gate for the whole .ics feature. It keys on whether the host's
// destination provider auto-delivers invites: Google and Microsoft Graph do (so our
// .ics would duplicate), but CalDAV (no iTIP scheduling) does NOT — so a CalDAV
// destination still needs our .ics, exactly as a host with no destination does.
func (h *Handler) noConnectedDestination(ctx context.Context, hostID string) bool {
	gc := h.getCal()
	if gc == nil {
		return true
	}
	has, err := gc.HasDestination(ctx, hostID)
	if err != nil {
		return false
	}
	if !has {
		return true // no destination → attach our .ics
	}
	// Has a destination, but if that provider doesn't email guests itself (CalDAV),
	// we must still attach the .ics so the guest gets an invite.
	return !gc.InvitesGuests(ctx, hostID)
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
		PriceCents        int
		Currency          string
	}
	err = h.db.QueryRowContext(r.Context(), `
		SELECT id, user_id, name, duration_minutes, location_type, location_value, routing_mode, rr_strategy, is_active, max_active_bookings, price_cents, currency
		FROM event_types WHERE slug = ?`, req.EventTypeSlug).
		Scan(&et.ID, &et.UserID, &et.Name, &et.DurationMinutes, &et.LocationType, &et.LocationValue, &et.RoutingMode, &et.RRStrategy, &et.IsActive, &et.MaxActiveBookings, &et.PriceCents, &et.Currency)
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
	candidates, required, optional := resolveBookingHostPool(hosts, et.RoutingMode)
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

	// PAID event types: the booking row already holds the slot (status='confirmed'); now
	// send the booker to Stripe Checkout and DEFER all confirmation side-effects until the
	// webhook marks the payment paid. Free event types fall through to the normal path.
	if et.PriceCents > 0 {
		sc := h.getStripe()
		if sc == nil {
			// The price gate requires Stripe at save time, but never strand a charge:
			// release the hold and tell the booker payments are unavailable.
			_ = h.bookingSvc.CancelByID(r.Context(), b.ID, "payments unavailable")
			h.writeError(w, http.StatusServiceUnavailable, "payments are temporarily unavailable")
			return
		}
		checkoutURL, err := h.startBookingCheckout(r.Context(), sc, b.ID, et.PriceCents, et.Currency, et.Name, req.EventTypeSlug, req.Email)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "create booking: start checkout", "error", err, "booking_id", b.ID)
			_ = h.bookingSvc.CancelByID(r.Context(), b.ID, "checkout failed")
			h.writeError(w, http.StatusBadGateway, "could not start payment")
			return
		}
		respBody, _ := json.Marshal(map[string]any{
			"payment_required": true,
			"booking_id":       b.ID,
			"checkout_url":     checkoutURL,
		})
		if idemKey != "" {
			if err := h.finishIdempotencyKey(r.Context(), idemKey, http.StatusOK, respBody, b.ID); err != nil {
				h.logger.ErrorContext(r.Context(), "create booking: store idempotency response", "error", err)
			}
			idemDone = true
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(respBody)
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

	// Run calendar/email/webhook side effects in the background — the booking is
	// committed; a side-effect failure must not roll it back or change the response.
	go h.dispatchBookingConfirmation(b, bookingConfirmationInput{
		EventTypeName:     et.Name,
		EventTypeSlug:     req.EventTypeSlug,
		LocationType:      et.LocationType,
		OrganizerName:     req.Name,
		OrganizerEmail:    req.Email,
		OrganizerTimezone: req.Timezone,
	})
}

// bookingConfirmationInput carries the event-type and organizer details that the
// post-create side effects need but that aren't stored on the booking row.
type bookingConfirmationInput struct {
	EventTypeName     string
	EventTypeSlug     string
	LocationType      string
	OrganizerName     string
	OrganizerEmail    string
	OrganizerTimezone string
}

// dispatchBookingConfirmation runs the post-create side effects for a booking:
// calendar events on each assigned host's connected calendar (auto-generating a
// Meet/Teams link only when the host's provider natively matches the platform),
// confirmation emails to attendee and hosts, the booking.created webhook, and
// reminder scheduling. Intended to run in its own goroutine; every failure is logged,
// never fatal. Shared by the REST CreateBooking handler and the MCP create_booking tool.
func (h *Handler) dispatchBookingConfirmation(b *booking.Booking, in bookingConfirmationInput) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	bData := mailer.BookingData{
		BookingID:         b.ID,
		EventTypeName:     in.EventTypeName,
		EventTypeSlug:     in.EventTypeSlug,
		OrganizerName:     in.OrganizerName,
		OrganizerEmail:    in.OrganizerEmail,
		OrganizerTimezone: in.OrganizerTimezone,
		StartAt:           b.StartAt,
		EndAt:             b.EndAt,
		LocationValue:     b.LocationValue,
		BaseURL:           h.publicURL(),
	}
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
	// For online-meeting event types (Google Meet / Teams) we auto-generate the
	// link ONLY when the primary host's connected calendar natively matches the
	// chosen platform (Meet↔Google, Teams↔Microsoft). Otherwise we never fabricate
	// a link of the wrong kind — the organizer's manually-entered link is used as
	// the location instead. The generated link (if any) is captured, stored on the
	// booking, surfaced in emails, and passed to secondary hosts' events.
	autoGenMeet := false
	if gc != nil && onlineMeetingLocation(in.LocationType) {
		primaryHostID := b.HostID
		for _, host := range hosts {
			if host.IsPrimary {
				primaryHostID = host.UserID
				break
			}
		}
		if _, primaryProvider, perr := gc.Connected(ctx, primaryHostID); perr == nil {
			autoGenMeet = providerMintsPlatform(in.LocationType, primaryProvider)
		} else {
			h.logger.Error("booking confirmation: primary host provider lookup", "error", perr, "booking_id", b.ID)
		}
	}
	// Seed the propagated location with the organizer's manual link when we won't
	// auto-generate, so the calendar events and emails still carry a join link.
	meetURL := ""
	if onlineMeetingLocation(in.LocationType) && !autoGenMeet {
		meetURL = b.LocationValue
	}
	// Zoom is an independent meeting-link provider (not tied to the host's calendar): for a
	// Zoom-located event, default to the organizer's manual link, but if the primary host has
	// connected their own Zoom account, mint a real meeting under it and use that join URL on
	// the calendar events, the booking record, the webhook, and the emails.
	if in.LocationType == "zoom" {
		meetURL = b.LocationValue
		if zc := h.getZoom(); zc != nil {
			primaryHostID := b.HostID
			for _, host := range hosts {
				if host.IsPrimary {
					primaryHostID = host.UserID
					break
				}
			}
			if connected, cerr := zc.Connected(ctx, primaryHostID); cerr != nil {
				h.logger.Error("zoom: connection lookup", "error", cerr, "booking_id", b.ID)
			} else if connected {
				join, mid, zerr := zc.CreateMeeting(ctx, primaryHostID, zoom.MeetingParams{
					Topic:           in.EventTypeName + " with " + in.OrganizerName,
					Start:           b.StartAt,
					DurationMinutes: int(b.EndAt.Sub(b.StartAt).Minutes()),
					Timezone:        in.OrganizerTimezone,
				})
				if zerr != nil {
					h.logger.Error("zoom: create meeting", "error", zerr, "booking_id", b.ID)
				} else {
					meetURL = join
					b.LocationValue = join
					bData.LocationValue = join
					if _, err := h.db.ExecContext(ctx,
						`UPDATE bookings SET location_value = ?, zoom_meeting_id = ? WHERE id = ?`,
						join, mid, b.ID); err != nil {
						h.logger.Error("zoom: save meeting", "error", err, "booking_id", b.ID)
					}
				}
			}
		}
	}
	// LiveKit: instance-level built-in video. Mint a room + an expiring, signed join URL
	// (no per-host connection, no manual link — the room is always generated). If LiveKit was
	// disabled after the event type was created, meetURL stays empty (booking still succeeds).
	if in.LocationType == "livekit" {
		if lk := h.getLiveKit(); lk != nil {
			room := "booking-" + b.ID
			// Valid from now until a bit past the meeting end (late joins / overruns).
			joinURL := lk.BookingJoinURL(h.baseURL, room, b.EndAt.Add(2*time.Hour))
			meetURL = joinURL
			b.LocationValue = joinURL
			bData.LocationValue = joinURL
			if _, err := h.db.ExecContext(ctx,
				`UPDATE bookings SET location_value = ?, livekit_room = ? WHERE id = ?`,
				joinURL, room, b.ID); err != nil {
				h.logger.Error("livekit: save room", "error", err, "booking_id", b.ID)
			}
		}
	}
	var primaryPrefs hostPrefs = allOnPrefs
	for _, host := range hosts {
		// Create a calendar event on each host's connected calendar and record
		// the per-host event ID so it can be cancelled later. The primary's id
		// also lives on the booking row for back-compat.
		if gc != nil {
			eventID, link, err := gc.CreateEvent(ctx, host.UserID, calendar.CreateEventParams{
				Summary:        in.EventTypeName + " with " + in.OrganizerName,
				Description:    "Booking ID: " + b.ID,
				Location:       meetURL, // empty until the primary creates it; secondary hosts get the link
				Start:          b.StartAt,
				End:            b.EndAt,
				OrganizerName:  in.OrganizerName,
				OrganizerEmail: in.OrganizerEmail,
				AddMeet:        autoGenMeet && host.IsPrimary,
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
			hd.AttachICS = h.noConnectedDestination(ctx, host.UserID) // per-host: their own calendar
			hd.ICSSequence = int(b.UpdatedAt.Unix())
			if err := mailer.SendConfirmationToHost(ctx, h.mailer, hd); err != nil {
				h.logger.Error("booking confirmation email (host)", "error", err, "booking_id", b.ID, "host", host.UserID)
			}
		}
	}

	// Attendee confirmation, once. "With:" names the primary host; gated on the
	// primary host's notification preference (matches prior behaviour).
	bData.HostName, bData.HostEmail = primaryHost(hosts).Name, primaryHost(hosts).Email
	bData.AttachICS = h.noConnectedDestination(ctx, b.HostID)
	bData.ICSSequence = int(b.UpdatedAt.Unix())
	if primaryPrefs.NotifyConfirmation {
		if err := mailer.SendConfirmationToAttendee(ctx, h.mailer, bData); err != nil {
			h.logger.Error("booking confirmation email (attendee)", "error", err, "booking_id", b.ID)
		}
	}
	if err := h.webhookSvc.Enqueue(ctx, "booking.created", webhook.BookingPayload{
		ID:                 b.ID,
		EventTypeSlug:      in.EventTypeSlug,
		HostID:             b.HostID,
		StartAt:            b.StartAt.UTC().Format(time.RFC3339),
		EndAt:              b.EndAt.UTC().Format(time.RFC3339),
		Status:             b.Status,
		LocationValue:      b.LocationValue,
		CreatedAt:          b.CreatedAt.UTC().Format(time.RFC3339),
		PaymentStatus:      paymentStatusForWebhook(b.PaymentStatus),
		AmountPaidCents:    b.AmountPaidCents,
		AmountPaidCurrency: b.AmountPaidCurrency,
	}); err != nil {
		h.logger.Error("enqueue booking.created webhook", "error", err, "booking_id", b.ID)
	}
	if err := h.enqueueBookingReminders(ctx, b.EventTypeID, b.ID, b.StartAt); err != nil {
		h.logger.Error("enqueue reminders", "error", err, "booking_id", b.ID)
	}
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

	// Fetch event type slugs via a single JOIN (payment fields already come from toBookingJSON).
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
	// Delete the Zoom meeting this booking minted (if any) — independent of the calendar.
	h.cancelZoomMeeting(ctx, &b)
	// Refund the Stripe payment if this was a paid booking.
	h.refundBookingPayment(ctx, b.ID)
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
			hd.AttachICS = h.noConnectedDestination(ctx, host.UserID) // per-host: their own calendar
			hd.ICSSequence = int(b.UpdatedAt.Unix())
			if err := mailer.SendCancellationToHost(ctx, h.mailer, hd); err != nil {
				h.logger.Error("booking cancellation email (host)", "error", err, "booking_id", b.ID, "host", host.UserID)
			}
		}
	}
	d.AttachICS = h.noConnectedDestination(ctx, b.HostID)
	d.ICSSequence = int(b.UpdatedAt.Unix())
	if primaryPrefs.NotifyCancellation {
		if err := mailer.SendCancellationToAttendee(ctx, h.mailer, d); err != nil {
			h.logger.Error("booking cancellation email (attendee)", "error", err, "booking_id", b.ID)
		}
	}
	// Re-read payment state — refundBookingPayment above may have flipped it to 'refunded'.
	var payStatus, payCur string
	var payAmt int
	_ = h.db.QueryRowContext(ctx,
		`SELECT payment_status, amount_paid_cents, amount_paid_currency FROM bookings WHERE id = ?`, b.ID).
		Scan(&payStatus, &payAmt, &payCur)
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
		PaymentStatus:      paymentStatusForWebhook(payStatus),
		AmountPaidCents:    payAmt,
		AmountPaidCurrency: payCur,
	}); err != nil {
		h.logger.Error("enqueue booking.cancelled webhook", "error", err, "booking_id", b.ID)
	}
}

// rescheduleZoomMeeting updates the booking's Zoom meeting time after a reschedule (the
// join URL is unchanged). No-op when Zoom isn't configured or the booking minted no meeting.
// The meeting lives under the primary host's account (b.HostID).
func (h *Handler) rescheduleZoomMeeting(ctx context.Context, b *booking.Booking) {
	zc := h.getZoom()
	if zc == nil {
		return
	}
	var meetingID string
	if err := h.db.QueryRowContext(ctx,
		`SELECT COALESCE(zoom_meeting_id,'') FROM bookings WHERE id = ?`, b.ID).Scan(&meetingID); err != nil || meetingID == "" {
		return
	}
	dur := int(b.EndAt.Sub(b.StartAt).Minutes())
	// timezone is cosmetic for Zoom (start_time is sent as unambiguous UTC); leave blank.
	if err := zc.UpdateMeeting(ctx, b.HostID, meetingID, b.StartAt, dur, ""); err != nil {
		h.logger.Error("zoom: update meeting on reschedule", "error", err, "booking_id", b.ID)
	}
}

// cancelZoomMeeting deletes the booking's Zoom meeting (if it minted one). No-op when Zoom
// isn't configured or no meeting was created.
func (h *Handler) cancelZoomMeeting(ctx context.Context, b *booking.Booking) {
	zc := h.getZoom()
	if zc == nil {
		return
	}
	var meetingID string
	if err := h.db.QueryRowContext(ctx,
		`SELECT COALESCE(zoom_meeting_id,'') FROM bookings WHERE id = ?`, b.ID).Scan(&meetingID); err != nil || meetingID == "" {
		return
	}
	if err := zc.DeleteMeeting(ctx, b.HostID, meetingID); err != nil {
		h.logger.Error("zoom: delete meeting on cancel", "error", err, "booking_id", b.ID)
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
