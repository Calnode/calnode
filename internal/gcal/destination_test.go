package gcal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/uid"
)

// recordPath spins up a server that records the request path it was called with, so a test
// can assert WHICH calendar an operation targeted rather than merely that it succeeded.
//
// r.URL.Path is already percent-decoded, so the calendar id appears literally here even
// though url.PathEscape encodes the @ on the wire.
func recordPath(t *testing.T, status int, body string) (*httptest.Server, *string) {
	t.Helper()
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Path
		w.WriteHeader(status)
		if body != "" {
			_, _ = w.Write([]byte(body))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

// pickSubCalendar marks one calendar inside the account as the write target, the way the
// settings picker does.
func pickSubCalendar(t *testing.T, c *Client, userID, accountEmail, calID string) {
	t.Helper()
	if _, err := c.db.Exec(`
		INSERT INTO connection_calendars (id, user_id, provider, account_email, calendar_id, check_conflicts, is_destination)
		VALUES (?, ?, 'google', ?, ?, 1, 1)`,
		uid.New(), userID, accountEmail, calID); err != nil {
		t.Fatalf("pick sub-calendar: %v", err)
	}
}

// TestCreateEvent_writesToThePickedCalendar is the fix for discussion #10: bookings must
// land in the calendar the host chose, not the account's primary.
func TestCreateEvent_writesToThePickedCalendar(t *testing.T) {
	srv, path := recordPath(t, http.StatusOK, `{"id":"evt-1"}`)
	c := newTestClient(t)
	c.apiBase = srv.URL
	saveDestinationConnection(t, c, "user-1", "primary")
	pickSubCalendar(t, c, "user-1", "", "work@company.com")

	_, _, calID, err := c.CreateEvent(context.Background(), "user-1", calendar.CreateEventParams{
		Summary: "Booking", Start: time.Now(), End: time.Now().Add(30 * time.Minute),
	})
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	if !strings.Contains(*path, "work@company.com") {
		t.Errorf("wrote to %q, want the picked work calendar; this is the reported bug", *path)
	}
	if calID != "work@company.com" {
		t.Errorf("reported calendar = %q, want the one actually written to - the caller stores this", calID)
	}
}

// Without a pick, nothing changes. Every existing install is in this state after the 00049
// seed, so a regression here would silently move everyone's bookings.
func TestCreateEvent_noPickKeepsTheAccountDefault(t *testing.T) {
	srv, path := recordPath(t, http.StatusOK, `{"id":"evt-1"}`)
	c := newTestClient(t)
	c.apiBase = srv.URL
	saveDestinationConnection(t, c, "user-1", "primary")

	_, _, calID, err := c.CreateEvent(context.Background(), "user-1", calendar.CreateEventParams{
		Summary: "Booking", Start: time.Now(), End: time.Now().Add(30 * time.Minute),
	})
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	if !strings.Contains(*path, "/calendars/primary/") {
		t.Errorf("wrote to %q, want the account default", *path)
	}
	if calID != "primary" {
		t.Errorf("reported calendar = %q, want \"primary\"", calID)
	}
}

// TestCancelEvent_usesTheStoredCalendarNotTheCurrentDestination is the orphaning guard.
// A host books, then changes their destination. Cancelling that booking must still delete
// the event from the calendar it actually lives in - otherwise the booking cancels in
// Calnode while the meeting stays on their calendar forever, with nothing surfaced.
func TestCancelEvent_usesTheStoredCalendarNotTheCurrentDestination(t *testing.T) {
	srv, path := recordPath(t, http.StatusNoContent, "")
	c := newTestClient(t)
	c.apiBase = srv.URL
	saveDestinationConnection(t, c, "user-1", "primary")
	// The host has since moved their destination to a different calendar.
	pickSubCalendar(t, c, "user-1", "", "newly-chosen@company.com")

	// The booking was created back when the destination was the work calendar.
	if err := c.CancelEvent(context.Background(), "user-1", "work@company.com", "evt-1"); err != nil {
		t.Fatalf("CancelEvent: %v", err)
	}
	if !strings.Contains(*path, "work@company.com") {
		t.Errorf("deleted from %q, want the calendar the event was created in; "+
			"targeting the current destination orphans the event", *path)
	}
}

// The fallback path: bookings made before the calendar was recorded pass "" and must
// resolve exactly as they did before, or upgrading would break every existing booking.
func TestCancelEvent_emptyStoredCalendarFallsBackToTheDestination(t *testing.T) {
	srv, path := recordPath(t, http.StatusNoContent, "")
	c := newTestClient(t)
	c.apiBase = srv.URL
	saveDestinationConnection(t, c, "user-1", "primary")
	pickSubCalendar(t, c, "user-1", "", "work@company.com")

	if err := c.CancelEvent(context.Background(), "user-1", "", "evt-1"); err != nil {
		t.Fatalf("CancelEvent: %v", err)
	}
	if !strings.Contains(*path, "work@company.com") {
		t.Errorf("deleted from %q, want the resolved current destination", *path)
	}
}

func TestUpdateEvent_usesTheStoredCalendar(t *testing.T) {
	srv, path := recordPath(t, http.StatusOK, `{"id":"evt-1"}`)
	c := newTestClient(t)
	c.apiBase = srv.URL
	saveDestinationConnection(t, c, "user-1", "primary")
	pickSubCalendar(t, c, "user-1", "", "newly-chosen@company.com")

	now := time.Now()
	if err := c.UpdateEvent(context.Background(), "user-1", "work@company.com", "evt-1", now, now.Add(time.Hour)); err != nil {
		t.Fatalf("UpdateEvent: %v", err)
	}
	if !strings.Contains(*path, "work@company.com") {
		t.Errorf("patched %q, want the calendar the event lives in", *path)
	}
}
