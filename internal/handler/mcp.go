package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/buildinfo"
)

// MCPServer builds the Model Context Protocol server exposing Calnode's booking
// operations as typed tools (PRD §11). The same server instance is served over both
// transports: stdio (the `calnode mcp` subcommand, for local agents) and Streamable
// HTTP (mounted at POST /mcp behind API-key auth, for remote agents). Tools call the
// same internal services the REST handlers use — no parallel code path.
func (h *Handler) MCPServer() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{
		Name:    "calnode",
		Title:   "Calnode booking",
		Version: buildinfo.Get().Version,
	}, nil)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_event_types",
		Description: "List the bookable event types in this workspace (active and public). Returns each type's slug (use it as event_type_id in other tools), name, duration, and location.",
	}, h.mcpListEventTypes)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_event_type",
		Description: "Get full details for one event type by slug: duration, location, hosts, and especially its intake QUESTIONS (which are required, and any allowed options). Call this before create_booking so you can supply the required answers.",
	}, h.mcpGetEventType)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_available_slots",
		Description: "List bookable time slots for an event type over a date range, in the given timezone. Returns slot start/end as RFC3339 timestamps. Use a slot's exact start as slot_start in create_booking.",
	}, h.mcpGetAvailableSlots)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_booking",
		Description: "Fetch a single booking by its id, returning its event type, host, start/end times, status, and location.",
	}, h.mcpGetBooking)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_bookings",
		Description: "List bookings in this workspace, newest first, with optional filters (status, date range, event type, host). Times are RFC3339 in UTC.",
	}, h.mcpListBookings)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "create_booking",
		Description: "Book a slot on an event type. slot_start must be an exact start from get_available_slots. Answers the event type's required intake questions if any. Returns the created booking (with its id and assigned host).",
	}, h.mcpCreateBooking)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "reschedule_booking",
		Description: "Move an existing booking to a new start time (RFC3339). The duration is preserved. Fails if the new slot is taken or the booking is cancelled.",
	}, h.mcpRescheduleBooking)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "cancel_booking",
		Description: "Cancel a booking by id, with an optional reason. Removes the calendar event(s) and notifies attendee and hosts.",
	}, h.mcpCancelBooking)

	return s
}

// ── list_event_types ─────────────────────────────────────────────────────────

type listEventTypesIn struct{}

type eventTypeBrief struct {
	ID              string `json:"id" jsonschema:"the event type's slug — pass this as event_type_id to other tools"`
	Name            string `json:"name"`
	DurationMinutes int    `json:"duration_minutes"`
	LocationType    string `json:"location_type"`
	Description     string `json:"description,omitempty"`
}

type listEventTypesOut struct {
	EventTypes []eventTypeBrief `json:"event_types"`
}

func (h *Handler) mcpListEventTypes(ctx context.Context, _ *mcp.CallToolRequest, _ listEventTypesIn) (*mcp.CallToolResult, listEventTypesOut, error) {
	rows, err := h.db.QueryContext(ctx, `
		SELECT slug, name, duration_minutes, location_type, COALESCE(description, '')
		FROM event_types
		WHERE is_active = 1 AND is_public = 1
		ORDER BY name`)
	if err != nil {
		return nil, listEventTypesOut{}, fmt.Errorf("list event types: %w", err)
	}
	defer rows.Close()
	out := listEventTypesOut{EventTypes: []eventTypeBrief{}}
	for rows.Next() {
		var e eventTypeBrief
		if err := rows.Scan(&e.ID, &e.Name, &e.DurationMinutes, &e.LocationType, &e.Description); err != nil {
			return nil, listEventTypesOut{}, fmt.Errorf("list event types: scan: %w", err)
		}
		out.EventTypes = append(out.EventTypes, e)
	}
	return nil, out, rows.Err()
}

// ── get_event_type ───────────────────────────────────────────────────────────

type eventTypeQuestion struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	Type     string   `json:"type"`
	Options  []string `json:"options,omitempty"`
	Required bool     `json:"required"`
}

type eventTypeHostBrief struct {
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url,omitempty"`
}

type getEventTypeIn struct {
	EventTypeID string `json:"event_type_id" jsonschema:"the event type slug (from list_event_types)"`
}

type getEventTypeOut struct {
	ID              string               `json:"id"`
	Name            string               `json:"name"`
	Description     string               `json:"description,omitempty"`
	DurationMinutes int                  `json:"duration_minutes"`
	LocationType    string               `json:"location_type"`
	Hosts           []eventTypeHostBrief `json:"hosts,omitempty"`
	Questions       []eventTypeQuestion  `json:"questions"`
}

func (h *Handler) mcpGetEventType(ctx context.Context, _ *mcp.CallToolRequest, in getEventTypeIn) (*mcp.CallToolResult, getEventTypeOut, error) {
	out := getEventTypeOut{ID: in.EventTypeID, Questions: []eventTypeQuestion{}}
	var etID string
	var isActive int
	err := h.db.QueryRowContext(ctx, `
		SELECT id, name, COALESCE(description, ''), duration_minutes, location_type, is_active
		FROM event_types WHERE slug = ?`, in.EventTypeID).
		Scan(&etID, &out.Name, &out.Description, &out.DurationMinutes, &out.LocationType, &isActive)
	if err != nil || isActive == 0 {
		return nil, getEventTypeOut{}, fmt.Errorf("event type not found: %s", in.EventTypeID)
	}

	// Intake questions, in form order. Materialize fully before the host queries below
	// — the single-connection pool can't run a second query while this cursor is open.
	rows, err := h.db.QueryContext(ctx, `
		SELECT id, label, type, COALESCE(options, ''), required
		FROM event_type_questions WHERE event_type_id = ? ORDER BY position, id`, etID)
	if err != nil {
		return nil, getEventTypeOut{}, fmt.Errorf("load questions: %w", err)
	}
	func() {
		defer rows.Close()
		for rows.Next() {
			var q eventTypeQuestion
			var opts string
			var req int
			if err := rows.Scan(&q.ID, &q.Label, &q.Type, &opts, &req); err != nil {
				continue
			}
			q.Required = req != 0
			if opts != "" {
				_ = json.Unmarshal([]byte(opts), &q.Options)
			}
			out.Questions = append(out.Questions, q)
		}
	}()

	// Configured hosts (display names), so an agent knows who the booking is with.
	if hosts, err := h.resolveEventTypeHosts(ctx, etID); err == nil {
		ids := make([]string, 0, len(hosts))
		for _, hh := range hosts {
			ids = append(ids, hh.UserID)
		}
		dm := h.hostDisplayMap(ctx, ids)
		for _, id := range ids {
			if d, ok := dm[id]; ok {
				out.Hosts = append(out.Hosts, eventTypeHostBrief{Name: d["name"], AvatarURL: d["avatar_url"]})
			}
		}
	}
	return nil, out, nil
}

// ── get_available_slots ──────────────────────────────────────────────────────

type getSlotsIn struct {
	EventTypeID string `json:"event_type_id" jsonschema:"the event type slug (from list_event_types)"`
	DateFrom    string `json:"date_from,omitempty" jsonschema:"start date YYYY-MM-DD; defaults to today"`
	DateTo      string `json:"date_to,omitempty" jsonschema:"end date YYYY-MM-DD; defaults to the event type's max future window"`
	Timezone    string `json:"timezone,omitempty" jsonschema:"IANA timezone the returned times are expressed in (e.g. Pacific/Auckland); defaults to UTC"`
}

type getSlotsOut struct {
	Slots []slotJSON `json:"slots"`
}

func (h *Handler) mcpGetAvailableSlots(ctx context.Context, _ *mcp.CallToolRequest, in getSlotsIn) (*mcp.CallToolResult, getSlotsOut, error) {
	out, _, err := h.computeSlots(ctx, in.EventTypeID, in.Timezone, in.DateFrom, in.DateTo)
	if err != nil {
		// The sentinel errors (not found / invalid timezone / bad range) are already
		// human-readable; surface them as the tool error.
		return nil, getSlotsOut{}, err
	}
	return nil, getSlotsOut{Slots: out}, nil
}

// ── get_booking ──────────────────────────────────────────────────────────────

type getBookingIn struct {
	BookingID string `json:"booking_id" jsonschema:"the booking's id (as returned by create_booking or list_bookings)"`
}

func (h *Handler) mcpGetBooking(ctx context.Context, _ *mcp.CallToolRequest, in getBookingIn) (*mcp.CallToolResult, bookingJSON, error) {
	b, err := h.bookingSvc.Get(ctx, in.BookingID)
	if err != nil {
		if errors.Is(err, booking.ErrNotFound) {
			return nil, bookingJSON{}, fmt.Errorf("booking not found: %s", in.BookingID)
		}
		return nil, bookingJSON{}, fmt.Errorf("get booking: %w", err)
	}
	// A member may only read bookings they host; don't reveal others' bookings.
	if userID, fullAccess := mcpCallerScope(ctx); !fullAccess && !h.userHostsBooking(ctx, userID, b.ID) {
		return nil, bookingJSON{}, fmt.Errorf("booking not found: %s", in.BookingID)
	}
	out := toBookingJSON(b)
	out.EventTypeSlug = h.slugForEventTypeID(ctx, b.EventTypeID)
	return nil, out, nil
}

// ── list_bookings ────────────────────────────────────────────────────────────

type listBookingsIn struct {
	Status      string `json:"status,omitempty" jsonschema:"filter by status: confirmed, cancelled, or rescheduled"`
	DateFrom    string `json:"date_from,omitempty" jsonschema:"only bookings starting on/after this date (YYYY-MM-DD, UTC)"`
	DateTo      string `json:"date_to,omitempty" jsonschema:"only bookings starting on/before this date (YYYY-MM-DD, UTC)"`
	EventTypeID string `json:"event_type_id,omitempty" jsonschema:"filter to this event type slug"`
	HostID      string `json:"host_id,omitempty" jsonschema:"filter to this host's user id"`
}

type listBookingsOut struct {
	Bookings []bookingJSON `json:"bookings"`
}

func (h *Handler) mcpListBookings(ctx context.Context, _ *mcp.CallToolRequest, in listBookingsIn) (*mcp.CallToolResult, listBookingsOut, error) {
	// Members see only bookings they host; admins/owner (and the local stdio operator)
	// see the whole workspace.
	userID, fullAccess := mcpCallerScope(ctx)
	var all []booking.Booking
	var err error
	if fullAccess {
		all, err = h.bookingSvc.ListAll(ctx)
	} else {
		all, err = h.bookingSvc.ListByHost(ctx, userID)
	}
	if err != nil {
		return nil, listBookingsOut{}, fmt.Errorf("list bookings: %w", err)
	}

	// Map event-type ids ⇄ slugs once, so we can both honour an event_type_id (slug)
	// filter and stamp each result with its slug.
	slugByID, idBySlug := h.eventTypeSlugMaps(ctx)
	var filterETID string
	if in.EventTypeID != "" {
		filterETID = idBySlug[in.EventTypeID]
		if filterETID == "" {
			return nil, listBookingsOut{Bookings: []bookingJSON{}}, nil // unknown slug → no matches
		}
	}

	var from, to time.Time
	if in.DateFrom != "" {
		if t, err := time.Parse("2006-01-02", in.DateFrom); err == nil {
			from = t
		} else {
			return nil, listBookingsOut{}, fmt.Errorf("date_from must be YYYY-MM-DD")
		}
	}
	if in.DateTo != "" {
		if t, err := time.Parse("2006-01-02", in.DateTo); err == nil {
			to = t.AddDate(0, 0, 1) // inclusive of the whole day
		} else {
			return nil, listBookingsOut{}, fmt.Errorf("date_to must be YYYY-MM-DD")
		}
	}

	out := listBookingsOut{Bookings: []bookingJSON{}}
	for i := range all {
		b := &all[i]
		if in.Status != "" && b.Status != in.Status {
			continue
		}
		if filterETID != "" && b.EventTypeID != filterETID {
			continue
		}
		if in.HostID != "" && b.HostID != in.HostID {
			continue
		}
		if !from.IsZero() && b.StartAt.Before(from) {
			continue
		}
		if !to.IsZero() && !b.StartAt.Before(to) {
			continue
		}
		bj := toBookingJSON(b)
		bj.EventTypeSlug = slugByID[b.EventTypeID]
		out.Bookings = append(out.Bookings, bj)
	}
	return nil, out, nil
}

// eventTypeSlugMaps returns id→slug and slug→id maps for all event types. Used by the
// MCP booking tools to translate between the internal id stored on bookings and the
// slug the tools expose as event_type_id.
func (h *Handler) eventTypeSlugMaps(ctx context.Context) (slugByID, idBySlug map[string]string) {
	slugByID, idBySlug = map[string]string{}, map[string]string{}
	rows, err := h.db.QueryContext(ctx, `SELECT id, slug FROM event_types`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id, slug string
		if err := rows.Scan(&id, &slug); err != nil {
			continue
		}
		slugByID[id], idBySlug[slug] = slug, id
	}
	return
}

func (h *Handler) slugForEventTypeID(ctx context.Context, id string) string {
	var slug string
	_ = h.db.QueryRowContext(ctx, `SELECT slug FROM event_types WHERE id = ?`, id).Scan(&slug)
	return slug
}

// ── create_booking ───────────────────────────────────────────────────────────

type mcpAnswer struct {
	QuestionID string `json:"question_id"`
	Value      string `json:"value"`
}

type createBookingIn struct {
	EventTypeID   string      `json:"event_type_id" jsonschema:"the event type slug (from list_event_types)"`
	SlotStart     string      `json:"slot_start" jsonschema:"the slot's exact start as RFC3339, taken from get_available_slots"`
	AttendeeName  string      `json:"attendee_name"`
	AttendeeEmail string      `json:"attendee_email"`
	Timezone      string      `json:"timezone,omitempty" jsonschema:"attendee IANA timezone; defaults to UTC"`
	Answers       []mcpAnswer `json:"answers,omitempty" jsonschema:"answers to the event type's intake questions, if any are required"`
}

func (h *Handler) mcpCreateBooking(ctx context.Context, _ *mcp.CallToolRequest, in createBookingIn) (*mcp.CallToolResult, bookingJSON, error) {
	if in.EventTypeID == "" || in.SlotStart == "" || in.AttendeeName == "" || in.AttendeeEmail == "" {
		return nil, bookingJSON{}, fmt.Errorf("event_type_id, slot_start, attendee_name, and attendee_email are required")
	}
	tz := in.Timezone
	if tz == "" {
		tz = "UTC"
	}
	startAt, err := time.Parse(time.RFC3339, in.SlotStart)
	if err != nil {
		return nil, bookingJSON{}, fmt.Errorf("slot_start must be RFC3339 (e.g. 2026-06-15T09:00:00Z)")
	}

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
	}
	err = h.db.QueryRowContext(ctx, `
		SELECT id, name, duration_minutes, location_type, location_value, routing_mode, rr_strategy, is_active, max_active_bookings
		FROM event_types WHERE slug = ?`, in.EventTypeID).
		Scan(&et.ID, &et.Name, &et.DurationMinutes, &et.LocationType, &et.LocationValue, &et.RoutingMode, &et.RRStrategy, &et.IsActive, &et.MaxActiveBookings)
	if err != nil || et.IsActive == 0 {
		return nil, bookingJSON{}, fmt.Errorf("event type not found: %s", in.EventTypeID)
	}

	raw := make([]booking.Answer, len(in.Answers))
	for i, a := range in.Answers {
		raw[i] = booking.Answer{QuestionID: a.QuestionID, Value: a.Value}
	}
	answers, err := h.validateAnswersCore(ctx, et.ID, raw)
	if err != nil {
		return nil, bookingJSON{}, err // *answerError messages are human-readable
	}

	endAt := startAt.UTC().Add(time.Duration(et.DurationMinutes) * time.Minute)
	locValue := ""
	if et.LocationValue != nil {
		locValue = *et.LocationValue
	}

	hosts, err := h.resolveEventTypeHosts(ctx, et.ID)
	if err != nil {
		return nil, bookingJSON{}, fmt.Errorf("resolve event-type hosts: %w", err)
	}
	candidates, required, optional := resolveBookingHostPool(hosts, et.RoutingMode)
	if len(candidates) == 0 {
		return nil, bookingJSON{}, fmt.Errorf("this slot is no longer available: no active host can take it")
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
		Organizer:           booking.Attendee{Name: in.AttendeeName, Email: in.AttendeeEmail, IANATimezone: tz},
		Answers:             answers,
		MaxActivePerInvitee: et.MaxActiveBookings,
	})
	if err != nil {
		switch {
		case errors.Is(err, booking.ErrDoubleBooked):
			return nil, bookingJSON{}, fmt.Errorf("this slot is no longer available")
		case errors.Is(err, booking.ErrBookingLimitReached):
			return nil, bookingJSON{}, fmt.Errorf("the attendee already holds the maximum number of upcoming bookings for this event")
		default:
			return nil, bookingJSON{}, fmt.Errorf("create booking: %w", err)
		}
	}

	out := toBookingJSON(b)
	out.EventTypeSlug = in.EventTypeID
	for _, hd := range h.displayHostsForBooking(ctx, b.ID) {
		out.Hosts = append(out.Hosts, hostBrief{ID: hd.ID, Name: hd.Name, AvatarURL: hd.AvatarURL})
	}
	// Calendar events, confirmation emails, webhook, reminders — the same shared path
	// the REST handler uses. The booking is committed; side-effect failures are logged.
	go h.dispatchBookingConfirmation(b, bookingConfirmationInput{
		EventTypeName:     et.Name,
		EventTypeSlug:     in.EventTypeID,
		LocationType:      et.LocationType,
		OrganizerName:     in.AttendeeName,
		OrganizerEmail:    in.AttendeeEmail,
		OrganizerTimezone: tz,
	})
	return nil, out, nil
}

// ── reschedule_booking ───────────────────────────────────────────────────────

type rescheduleBookingIn struct {
	BookingID    string `json:"booking_id"`
	NewSlotStart string `json:"new_slot_start" jsonschema:"the new start as RFC3339; the duration is preserved"`
}

func (h *Handler) mcpRescheduleBooking(ctx context.Context, _ *mcp.CallToolRequest, in rescheduleBookingIn) (*mcp.CallToolResult, bookingJSON, error) {
	if in.BookingID == "" || in.NewSlotStart == "" {
		return nil, bookingJSON{}, fmt.Errorf("booking_id and new_slot_start are required")
	}
	newStart, err := time.Parse(time.RFC3339, in.NewSlotStart)
	if err != nil {
		return nil, bookingJSON{}, fmt.Errorf("new_slot_start must be RFC3339")
	}
	b, err := h.bookingSvc.Get(ctx, in.BookingID)
	if err != nil {
		if errors.Is(err, booking.ErrNotFound) {
			return nil, bookingJSON{}, fmt.Errorf("booking not found: %s", in.BookingID)
		}
		return nil, bookingJSON{}, fmt.Errorf("get booking: %w", err)
	}
	// Members may reschedule only bookings they primarily host (matching cancel).
	if userID, fullAccess := mcpCallerScope(ctx); !fullAccess && b.HostID != userID {
		return nil, bookingJSON{}, fmt.Errorf("booking not found: %s", in.BookingID)
	}
	var durMins int
	if err := h.db.QueryRowContext(ctx, `SELECT duration_minutes FROM event_types WHERE id = ?`, b.EventTypeID).Scan(&durMins); err != nil {
		return nil, bookingJSON{}, fmt.Errorf("load duration: %w", err)
	}
	previousStart, previousEnd := b.StartAt, b.EndAt
	updated, err := h.bookingSvc.Reschedule(ctx, b.ID, newStart, newStart.Add(time.Duration(durMins)*time.Minute))
	if err != nil {
		switch {
		case errors.Is(err, booking.ErrDoubleBooked):
			return nil, bookingJSON{}, fmt.Errorf("that time slot is no longer available")
		case errors.Is(err, booking.ErrAlreadyCancelled):
			return nil, bookingJSON{}, fmt.Errorf("this booking has been cancelled")
		case errors.Is(err, booking.ErrNotFound):
			return nil, bookingJSON{}, fmt.Errorf("booking not found: %s", in.BookingID)
		default:
			return nil, bookingJSON{}, fmt.Errorf("reschedule: %w", err)
		}
	}
	out := toBookingJSON(updated)
	out.EventTypeSlug = h.slugForEventTypeID(ctx, updated.EventTypeID)
	go h.rescheduleSideEffects(*updated, b.EventTypeID, previousStart, previousEnd)
	return nil, out, nil
}

// ── cancel_booking ───────────────────────────────────────────────────────────

type cancelBookingIn struct {
	BookingID string `json:"booking_id"`
	Reason    string `json:"reason,omitempty"`
}

func (h *Handler) mcpCancelBooking(ctx context.Context, _ *mcp.CallToolRequest, in cancelBookingIn) (*mcp.CallToolResult, bookingJSON, error) {
	if in.BookingID == "" {
		return nil, bookingJSON{}, fmt.Errorf("booking_id is required")
	}
	// Admins/owner (and the local stdio operator) may cancel any booking; a member may
	// cancel only one they primarily host (Cancel enforces host_id, returning ErrNotFound
	// otherwise — so a member can't probe or cancel others' bookings).
	userID, fullAccess := mcpCallerScope(ctx)
	var cancelErr error
	if fullAccess {
		cancelErr = h.bookingSvc.CancelByID(ctx, in.BookingID, in.Reason)
	} else {
		cancelErr = h.bookingSvc.Cancel(ctx, userID, in.BookingID, in.Reason)
	}
	if err := cancelErr; err != nil {
		switch {
		case errors.Is(err, booking.ErrNotFound):
			return nil, bookingJSON{}, fmt.Errorf("booking not found: %s", in.BookingID)
		case errors.Is(err, booking.ErrAlreadyCancelled):
			return nil, bookingJSON{}, fmt.Errorf("booking already cancelled")
		default:
			return nil, bookingJSON{}, fmt.Errorf("cancel booking: %w", err)
		}
	}
	b, err := h.bookingSvc.Get(ctx, in.BookingID)
	if err != nil {
		return nil, bookingJSON{}, fmt.Errorf("fetch cancelled booking: %w", err)
	}
	out := toBookingJSON(b)
	out.EventTypeSlug = h.slugForEventTypeID(ctx, b.EventTypeID)
	go h.cancelSideEffects(*b)
	return nil, out, nil
}
