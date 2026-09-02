package handler_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/calnode/calnode/internal/handler"
)

// listResp is the shape GET /v1/bookings returns. counts describe the whole match
// set; items is one page of it.
type listResp struct {
	Items []struct {
		ID            string `json:"id"`
		Status        string `json:"status"`
		HostID        string `json:"host_id"`
		EventTypeSlug string `json:"event_type_slug"`
	} `json:"items"`
	Total  int `json:"total"`
	Counts struct {
		Upcoming int `json:"upcoming"`
		Past     int `json:"past"`
	} `json:"counts"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

func listBookings(t *testing.T, h *handler.Handler, apiKey, query string) listResp {
	t.Helper()
	req := authReq(http.MethodGet, "/v1/bookings"+query, "", apiKey)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ListBookings)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/bookings%s: %d - %s", query, rec.Code, rec.Body.String())
	}
	var out listResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func listIDs(r listResp) []string {
	ids := make([]string, len(r.Items))
	for i, it := range r.Items {
		ids[i] = it.ID
	}
	return ids
}

// seedBooking inserts one booking directly. start/end are RFC3339 UTC.
func seedBooking(t *testing.T, db *sql.DB, id, etID, hostID, start, end, status string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at, status)
		 VALUES (?,?,?,?,?,?)`, id, etID, hostID, start, end, status); err != nil {
		t.Fatalf("seed booking %s: %v", id, err)
	}
}

// TestListBookings_filtersByStatusAndSurfacesCancelled is the regression for a bug
// that made one advertised filter value permanently unusable.
//
// ListByHost/ListAll hardcoded `status != 'cancelled'`, and both the REST list and
// the MCP list_bookings tool filtered on top of that result. So status="cancelled"
// - a value the MCP tool's own schema documents - could never match anything, and
// there was no way at all to see a cancelled booking through either surface. The
// filter now runs in SQL, and an explicit status replaces the default exclusion
// rather than being applied after it.
func TestListBookings_filtersByStatusAndSurfacesCancelled(t *testing.T) {
	h, database, key, ownerID := setupWorkspaceWithDB(t)
	_, etID := seedEventTypeHTTP(t, h, key)
	seedBooking(t, database, "b-ok", etID, ownerID, "2027-07-01T10:00:00Z", "2027-07-01T10:30:00Z", "confirmed")
	seedBooking(t, database, "b-gone", etID, ownerID, "2027-07-02T10:00:00Z", "2027-07-02T10:30:00Z", "cancelled")

	t.Run("default hides cancelled", func(t *testing.T) {
		got := listIDs(listBookings(t, h, key, ""))
		if len(got) != 1 || got[0] != "b-ok" {
			t.Errorf("got %v, want just [b-ok] - the default view must keep excluding cancelled", got)
		}
	})

	t.Run("explicit status=cancelled returns them", func(t *testing.T) {
		got := listIDs(listBookings(t, h, key, "?status=cancelled"))
		if len(got) != 1 || got[0] != "b-gone" {
			t.Errorf("got %v, want [b-gone]; asking for cancelled bookings returned none, "+
				"which is what the hardcoded exclusion used to guarantee", got)
		}
	})

	t.Run("explicit status=confirmed narrows", func(t *testing.T) {
		got := listIDs(listBookings(t, h, key, "?status=confirmed"))
		if len(got) != 1 || got[0] != "b-ok" {
			t.Errorf("got %v, want [b-ok]", got)
		}
	})
}

// TestListBookings_hostFilterMatchesAssignedHosts covers the second half of the same
// class of bug. Visibility has always counted a user as hosting a booking if they are
// the primary host OR an assigned host in booking_hosts (so Group attendees see the
// meetings they're on). The host filter compared bookings.host_id only, so filtering
// to a person hid exactly the meetings they attend but don't lead.
func TestListBookings_hostFilterMatchesAssignedHosts(t *testing.T) {
	h, database, key, ownerID := setupWorkspaceWithDB(t)
	if _, err := database.Exec(
		`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u2','u2@example.com','Two','UTC',0)`); err != nil {
		t.Fatal(err)
	}
	_, etID := seedEventTypeHTTP(t, h, key)

	// u2 leads one booking, merely attends a second, and has nothing to do with a third.
	seedBooking(t, database, "b-led", etID, "u2", "2027-07-01T10:00:00Z", "2027-07-01T10:30:00Z", "confirmed")
	seedBooking(t, database, "b-attended", etID, ownerID, "2027-07-02T10:00:00Z", "2027-07-02T10:30:00Z", "confirmed")
	seedBooking(t, database, "b-unrelated", etID, ownerID, "2027-07-03T10:00:00Z", "2027-07-03T10:30:00Z", "confirmed")
	if _, err := database.Exec(
		`INSERT INTO booking_hosts (id, booking_id, user_id, is_primary) VALUES ('bh1','b-attended','u2',0)`); err != nil {
		t.Fatal(err)
	}

	got := listIDs(listBookings(t, h, key, "?scope=all&host=u2"))
	seen := map[string]bool{}
	for _, id := range got {
		seen[id] = true
	}
	if !seen["b-led"] {
		t.Error("host filter missed the booking u2 leads")
	}
	if !seen["b-attended"] {
		t.Error("host filter missed the booking u2 attends as an assigned host; " +
			"matching only bookings.host_id hides exactly the Group meetings a person is on")
	}
	// Without this the test passes against a build that ignores host= entirely, since
	// scope=all would return everything including the two u2 is on.
	if seen["b-unrelated"] {
		t.Errorf("host filter returned a booking u2 has no part in; got %v", got)
	}
}

// A member must not be able to widen their view with a filter. Filters narrow; the
// viewer gate is applied independently and always.
func TestListBookings_memberCannotWidenWithFilters(t *testing.T) {
	h, database, key, ownerID := setupWorkspaceWithDB(t)
	if _, err := database.Exec(
		`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u2','u2@example.com','Two','UTC',0)`); err != nil {
		t.Fatal(err)
	}
	memberKey := "member-filter-key"
	if _, err := database.Exec(
		`INSERT INTO api_keys (id,user_id,name,key_hash,created_at) VALUES ('k2','u2','t',?,'2024-01-01')`,
		sha256HexForTest(memberKey)); err != nil {
		t.Fatal(err)
	}
	_, etID := seedEventTypeHTTP(t, h, key)
	seedBooking(t, database, "b-someone-elses", etID, ownerID, "2027-07-01T10:00:00Z", "2027-07-01T10:30:00Z", "confirmed")

	for _, q := range []string{"?scope=all", "?host=" + ownerID, "?scope=all&host=" + ownerID} {
		got := listIDs(listBookings(t, h, memberKey, q))
		if len(got) != 0 {
			t.Errorf("%s leaked %v to a non-admin member", q, got)
		}
	}
}

// TestListBookings_paginates checks the page is a window on the match set and that
// the counts describe the whole of it. Deriving counts from len(items) is the obvious
// mistake once pagination exists, and it silently mislabels the UI's tabs.
func TestListBookings_paginates(t *testing.T) {
	h, database, key, ownerID := setupWorkspaceWithDB(t)
	_, etID := seedEventTypeHTTP(t, h, key)
	for i := 0; i < 5; i++ {
		seedBooking(t, database,
			fmt.Sprintf("b%d", i), etID, ownerID,
			fmt.Sprintf("2027-07-0%dT10:00:00Z", i+1),
			fmt.Sprintf("2027-07-0%dT10:30:00Z", i+1), "confirmed")
	}

	first := listBookings(t, h, key, "?limit=2")
	if len(first.Items) != 2 {
		t.Fatalf("page size = %d, want 2", len(first.Items))
	}
	if first.Total != 5 {
		t.Errorf("total = %d, want 5 - the count describes the match set, not the page", first.Total)
	}
	if first.Counts.Upcoming != 5 {
		t.Errorf("counts.upcoming = %d, want 5", first.Counts.Upcoming)
	}
	if got := listIDs(first); got[0] != "b0" || got[1] != "b1" {
		t.Errorf("first page = %v, want the two soonest", got)
	}

	second := listBookings(t, h, key, "?limit=2&offset=2")
	if got := listIDs(second); len(got) != 2 || got[0] != "b2" || got[1] != "b3" {
		t.Errorf("second page = %v, want [b2 b3]", got)
	}

	// The page cap must hold even when a caller asks for more.
	big := listBookings(t, h, key, "?limit=99999")
	if big.Limit > 200 {
		t.Errorf("limit = %d, want it capped at 200", big.Limit)
	}
}

// Upcoming/past is keyed on end_at, so a meeting that has started but not finished
// still counts as upcoming. This is the rule the old client-side filter used and the
// server has to preserve it exactly, or rows jump tabs after the change.
func TestListBookings_whenSplitsOnEndTime(t *testing.T) {
	h, database, key, ownerID := setupWorkspaceWithDB(t)
	_, etID := seedEventTypeHTTP(t, h, key)
	seedBooking(t, database, "b-past", etID, ownerID, "2020-01-01T10:00:00Z", "2020-01-01T10:30:00Z", "confirmed")
	seedBooking(t, database, "b-future", etID, ownerID, "2027-07-01T10:00:00Z", "2027-07-01T10:30:00Z", "confirmed")

	if got := listIDs(listBookings(t, h, key, "?when=upcoming")); len(got) != 1 || got[0] != "b-future" {
		t.Errorf("when=upcoming gave %v, want [b-future]", got)
	}
	if got := listIDs(listBookings(t, h, key, "?when=past")); len(got) != 1 || got[0] != "b-past" {
		t.Errorf("when=past gave %v, want [b-past]", got)
	}
	// Counts ignore `when`, so both tabs can be labelled from one response.
	r := listBookings(t, h, key, "?when=upcoming")
	if r.Counts.Upcoming != 1 || r.Counts.Past != 1 {
		t.Errorf("counts = %+v, want 1 upcoming and 1 past regardless of the active tab", r.Counts)
	}
}

// Ordering has to move server-side with pagination: sorting a page in the browser
// only sorts that page.
func TestListBookings_orderDesc(t *testing.T) {
	h, database, key, ownerID := setupWorkspaceWithDB(t)
	_, etID := seedEventTypeHTTP(t, h, key)
	seedBooking(t, database, "b1", etID, ownerID, "2027-07-01T10:00:00Z", "2027-07-01T10:30:00Z", "confirmed")
	seedBooking(t, database, "b2", etID, ownerID, "2027-07-02T10:00:00Z", "2027-07-02T10:30:00Z", "confirmed")

	if got := listIDs(listBookings(t, h, key, "?order=desc")); got[0] != "b2" {
		t.Errorf("order=desc gave %v, want most recent first", got)
	}
}

func TestListBookings_filtersByEventTypeAndDate(t *testing.T) {
	h, database, key, ownerID := setupWorkspaceWithDB(t)
	slugA, etA := seedEventTypeHTTP(t, h, key)
	_, etB := seedEventTypeHTTP(t, h, key)
	seedBooking(t, database, "b-a", etA, ownerID, "2027-07-01T10:00:00Z", "2027-07-01T10:30:00Z", "confirmed")
	seedBooking(t, database, "b-b", etB, ownerID, "2027-07-05T10:00:00Z", "2027-07-05T10:30:00Z", "confirmed")

	if got := listIDs(listBookings(t, h, key, "?event_type="+slugA)); len(got) != 1 || got[0] != "b-a" {
		t.Errorf("event_type filter gave %v, want [b-a]", got)
	}
	// An unknown slug matches nothing rather than erroring - a stale bookmark should
	// show an empty list, not a failure.
	if got := listIDs(listBookings(t, h, key, "?event_type=no-such-thing")); len(got) != 0 {
		t.Errorf("unknown event type gave %v, want nothing", got)
	}
	// `to` names a day and includes the whole of it.
	if got := listIDs(listBookings(t, h, key, "?from=2027-07-05&to=2027-07-05")); len(got) != 1 || got[0] != "b-b" {
		t.Errorf("date range gave %v, want [b-b] - `to` must include its whole day", got)
	}
}

// Teams associate with bookings through their members, because event_types.team_id
// is never written - a team is a shortcut for populating event_type_hosts.
func TestListBookings_filtersByTeamMembership(t *testing.T) {
	h, database, key, ownerID := setupWorkspaceWithDB(t)
	if _, err := database.Exec(
		`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u2','u2@example.com','Two','UTC',0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO teams (id,name,slug) VALUES ('t1','Sales','sales')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(
		`INSERT INTO team_members (id,team_id,user_id,role) VALUES ('tm1','t1','u2','member')`); err != nil {
		t.Fatal(err)
	}
	_, etID := seedEventTypeHTTP(t, h, key)
	seedBooking(t, database, "b-team", etID, "u2", "2027-07-01T10:00:00Z", "2027-07-01T10:30:00Z", "confirmed")
	seedBooking(t, database, "b-outside", etID, ownerID, "2027-07-02T10:00:00Z", "2027-07-02T10:30:00Z", "confirmed")

	got := listIDs(listBookings(t, h, key, "?scope=all&team=t1"))
	if len(got) != 1 || got[0] != "b-team" {
		t.Errorf("team filter gave %v, want [b-team]", got)
	}
}

// Bad input is rejected rather than silently returning an odd result set.
func TestListBookings_rejectsBadParameters(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	for _, q := range []string{
		"?status=banana", "?when=someday", "?from=07-2027", "?to=nonsense",
		"?limit=0", "?limit=-1", "?limit=abc", "?offset=-1",
	} {
		req := authReq(http.MethodGet, "/v1/bookings"+q, "", key)
		rec := httptest.NewRecorder()
		h.RequireAuth(h.ListBookings)(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", q, rec.Code)
		}
	}
}
