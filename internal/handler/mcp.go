package handler

import (
	"context"
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
	all, err := h.bookingSvc.ListAll(ctx)
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
