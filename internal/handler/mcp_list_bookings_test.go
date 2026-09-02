package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpBookingRow struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	HostID        string `json:"host_id"`
	EventTypeSlug string `json:"event_type_slug"`
}

type mcpListBookingsResult struct {
	Bookings []mcpBookingRow `json:"bookings"`
	Total    int             `json:"total"`
}

func (r mcpListBookingsResult) ids() []string {
	out := make([]string, len(r.Bookings))
	for i, b := range r.Bookings {
		out[i] = b.ID
	}
	return out
}

// callListBookings invokes the MCP tool and decodes its structured result.
func callListBookings(t *testing.T, cs *mcp.ClientSession, args map[string]any) mcpListBookingsResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "list_bookings", Arguments: args,
	})
	if err != nil {
		t.Fatalf("CallTool list_bookings: %v", err)
	}
	if res.IsError {
		t.Fatalf("list_bookings returned an error: %+v", res.Content)
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var out mcpListBookingsResult
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode list_bookings result: %v (raw %s)", err, raw)
	}
	return out
}

// TestMCP_listBookings_statusCancelled is the MCP half of the regression covered in
// booking_filter_test.go. It matters separately because this tool's *own schema*
// documents "cancelled" as an accepted status while the underlying query hardcoded
// `status != 'cancelled'` and the tool filtered on top of that result. An agent asking
// for cancelled bookings was told, with complete confidence, that there were none.
func TestMCP_listBookings_statusCancelled(t *testing.T) {
	h, database, key, ownerID := setupWorkspaceWithDB(t)
	slug, etID := seedEventTypeHTTP(t, h, key)
	seedBooking(t, database, "b-live", etID, ownerID, "2027-07-01T10:00:00Z", "2027-07-01T10:30:00Z", "confirmed")
	seedBooking(t, database, "b-dead", etID, ownerID, "2027-07-02T10:00:00Z", "2027-07-02T10:30:00Z", "cancelled")

	cs := connectMCP(t, h)

	got := callListBookings(t, cs, map[string]any{"status": "cancelled"})
	if len(got.Bookings) != 1 || got.Bookings[0].ID != "b-dead" {
		t.Fatalf("status=cancelled gave %v; want [b-dead]. The tool advertises this "+
			"status but could never return anything for it", got.ids())
	}

	// The default still hides cancelled, so agents that pass no status are unaffected.
	got = callListBookings(t, cs, map[string]any{})
	if len(got.Bookings) != 1 || got.Bookings[0].ID != "b-live" {
		t.Fatalf("default listing = %v, want [b-live]", got.ids())
	}
	if got.Bookings[0].EventTypeSlug != slug {
		t.Errorf("event_type_slug = %q, want %q", got.Bookings[0].EventTypeSlug, slug)
	}
}

// The tool now pages. Total must describe the whole match set, or an agent that reads
// it will conclude the workspace is smaller than it is.
func TestMCP_listBookings_pagesAndReportsTotal(t *testing.T) {
	h, database, key, ownerID := setupWorkspaceWithDB(t)
	_, etID := seedEventTypeHTTP(t, h, key)
	for i := 1; i <= 5; i++ {
		day := fmt.Sprintf("2027-07-%02d", i)
		seedBooking(t, database, fmt.Sprintf("b%d", i), etID, ownerID,
			day+"T10:00:00Z", day+"T10:30:00Z", "confirmed")
	}
	cs := connectMCP(t, h)

	first := callListBookings(t, cs, map[string]any{"limit": 2})
	if len(first.Bookings) != 2 {
		t.Errorf("limit=2 returned %d bookings", len(first.Bookings))
	}
	if first.Total != 5 {
		t.Errorf("total = %d, want 5 - it describes the match set, not the page", first.Total)
	}

	second := callListBookings(t, cs, map[string]any{"limit": 2, "offset": 2})
	if len(second.Bookings) != 2 {
		t.Fatalf("offset page = %v, want 2 bookings", second.ids())
	}
	if second.Bookings[0].ID == first.Bookings[0].ID {
		t.Errorf("offset=2 did not advance: first %v, second %v", first.ids(), second.ids())
	}
}

// Filtering by host must find the meetings a person attends as an assigned host, not
// only the ones where they are bookings.host_id - the same defect the REST filter had.
func TestMCP_listBookings_hostFilterIncludesAssignedHosts(t *testing.T) {
	h, database, key, ownerID := setupWorkspaceWithDB(t)
	if _, err := database.Exec(
		`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u2','u2@example.com','Two','UTC',0)`); err != nil {
		t.Fatal(err)
	}
	_, etID := seedEventTypeHTTP(t, h, key)
	seedBooking(t, database, "b-attended", etID, ownerID, "2027-07-02T10:00:00Z", "2027-07-02T10:30:00Z", "confirmed")
	seedBooking(t, database, "b-unrelated", etID, ownerID, "2027-07-03T10:00:00Z", "2027-07-03T10:30:00Z", "confirmed")
	if _, err := database.Exec(
		`INSERT INTO booking_hosts (id, booking_id, user_id, is_primary) VALUES ('bh1','b-attended','u2',0)`); err != nil {
		t.Fatal(err)
	}

	got := callListBookings(t, connectMCP(t, h), map[string]any{"host_id": "u2"})
	if len(got.Bookings) != 1 || got.Bookings[0].ID != "b-attended" {
		t.Errorf("host_id=u2 gave %v, want [b-attended]", got.ids())
	}
}

// An unknown event-type slug is an empty result, not an error: it is a stale filter,
// not a malformed request.
func TestMCP_listBookings_unknownEventTypeIsEmpty(t *testing.T) {
	h, database, key, ownerID := setupWorkspaceWithDB(t)
	_, etID := seedEventTypeHTTP(t, h, key)
	seedBooking(t, database, "b1", etID, ownerID, "2027-07-01T10:00:00Z", "2027-07-01T10:30:00Z", "confirmed")

	got := callListBookings(t, connectMCP(t, h), map[string]any{"event_type_id": "no-such-slug"})
	if len(got.Bookings) != 0 {
		t.Errorf("unknown slug returned %v, want nothing", got.ids())
	}
}

// A bad status is rejected rather than silently returning an empty list, which an
// agent would otherwise read as "you have no bookings like that".
func TestMCP_listBookings_rejectsUnknownStatus(t *testing.T) {
	h, _, _, _ := setupWorkspaceWithDB(t)
	res, err := connectMCP(t, h).CallTool(context.Background(), &mcp.CallToolParams{
		Name: "list_bookings", Arguments: map[string]any{"status": "banana"},
	})
	if err == nil && !res.IsError {
		t.Error("an unknown status was accepted; want a tool error so the agent can correct it")
	}
}
