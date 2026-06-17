package gcal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// mockCreateEventServer returns a server that records event requests and returns
// a synthetic event ID.
func mockCreateEventServer(t *testing.T, returnEventID string, returnStatus int) (*httptest.Server, *calEventReq) {
	t.Helper()
	gotReq := new(calEventReq)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			json.NewDecoder(r.Body).Decode(gotReq) //nolint:errcheck
			w.WriteHeader(returnStatus)
			if returnStatus == http.StatusOK {
				json.NewEncoder(w).Encode(calEventResp{ID: returnEventID}) //nolint:errcheck
			}
		} else if r.Method == http.MethodDelete {
			w.WriteHeader(returnStatus)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, gotReq
}

func saveDestinationConnection(t *testing.T, c *Client, userID, calID string) {
	t.Helper()
	seedUser(t, c.db, userID)
	tok := &oauth2.Token{AccessToken: "dst-token", Expiry: time.Now().Add(time.Hour)}
	if err := c.saveToken(context.Background(), userID, calID, tok); err != nil {
		t.Fatalf("saveToken: %v", err)
	}
}

// ---------------------------------------------------------------------------
// CreateEvent
// ---------------------------------------------------------------------------

func TestCreateEvent_returnsEventID(t *testing.T) {
	srv, _ := mockCreateEventServer(t, "gcal-event-id-123", http.StatusOK)

	c := newTestClient(t)
	c.apiBase = srv.URL
	saveDestinationConnection(t, c, "user-1", "primary")

	eventID, err := c.CreateEvent(context.Background(), "user-1", CreateEventParams{
		Summary:        "30-Minute Call with Bob",
		Description:    "Booking ID: abc123",
		Start:          time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC),
		End:            time.Date(2026, 6, 15, 9, 30, 0, 0, time.UTC),
		OrganizerName:  "Bob Booker",
		OrganizerEmail: "bob@example.com",
	})
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	if eventID != "gcal-event-id-123" {
		t.Errorf("eventID = %q; want gcal-event-id-123", eventID)
	}
}

func TestCreateEvent_sendsCorrectFields(t *testing.T) {
	srv, gotReq := mockCreateEventServer(t, "ev1", http.StatusOK)

	c := newTestClient(t)
	c.apiBase = srv.URL
	saveDestinationConnection(t, c, "user-1", "primary")

	p := CreateEventParams{
		Summary:        "Team Sync with Alice",
		Description:    "Booking ID: xyz",
		Start:          time.Date(2026, 6, 20, 14, 0, 0, 0, time.UTC),
		End:            time.Date(2026, 6, 20, 15, 0, 0, 0, time.UTC),
		OrganizerName:  "Alice",
		OrganizerEmail: "alice@example.com",
	}
	c.CreateEvent(context.Background(), "user-1", p) //nolint:errcheck

	if gotReq.Summary != p.Summary {
		t.Errorf("Summary = %q; want %q", gotReq.Summary, p.Summary)
	}
	if gotReq.Description != p.Description {
		t.Errorf("Description = %q; want %q", gotReq.Description, p.Description)
	}
	if !strings.Contains(gotReq.Start.DateTime, "2026-06-20") {
		t.Errorf("Start.DateTime = %q; want date 2026-06-20", gotReq.Start.DateTime)
	}
	if len(gotReq.Attendees) != 1 || gotReq.Attendees[0].Email != "alice@example.com" {
		t.Errorf("Attendees = %v; want [{alice@example.com}]", gotReq.Attendees)
	}
}

func TestCreateEvent_notConnected_returnsEmpty(t *testing.T) {
	c := newTestClient(t)
	eventID, err := c.CreateEvent(context.Background(), "user-no-connection", CreateEventParams{
		Summary: "Test",
		Start:   time.Now(),
		End:     time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if eventID != "" {
		t.Errorf("eventID = %q; want empty for unconnected user", eventID)
	}
}

func TestCreateEvent_nonOK_returnsError(t *testing.T) {
	srv, _ := mockCreateEventServer(t, "", http.StatusForbidden)

	c := newTestClient(t)
	c.apiBase = srv.URL
	saveDestinationConnection(t, c, "user-1", "primary")

	_, err := c.CreateEvent(context.Background(), "user-1", CreateEventParams{
		Start: time.Now(),
		End:   time.Now().Add(time.Hour),
	})
	if err == nil {
		t.Error("expected error for non-200 response")
	}
}

func TestCreateEvent_onlyDestinationConnections(t *testing.T) {
	// Connect with is_destination = 0 — CreateEvent should return "".
	c := newTestClient(t)
	seedUser(t, c.db, "user-1")
	tok := &oauth2.Token{AccessToken: "tok", Expiry: time.Now().Add(time.Hour)}
	c.saveToken(context.Background(), "user-1", "primary", tok) //nolint:errcheck
	c.db.ExecContext(context.Background(),                       //nolint:errcheck
		`UPDATE calendar_connections SET is_destination = 0 WHERE user_id = ?`, "user-1")

	eventID, err := c.CreateEvent(context.Background(), "user-1", CreateEventParams{
		Start: time.Now(), End: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if eventID != "" {
		t.Errorf("got eventID %q from is_destination=0 connection; want empty", eventID)
	}
}

// ---------------------------------------------------------------------------
// CancelEvent
// ---------------------------------------------------------------------------

func TestCancelEvent_success(t *testing.T) {
	srv, _ := mockCreateEventServer(t, "", http.StatusNoContent)

	c := newTestClient(t)
	c.apiBase = srv.URL
	saveDestinationConnection(t, c, "user-1", "primary")

	if err := c.CancelEvent(context.Background(), "user-1", "event-to-delete"); err != nil {
		t.Errorf("CancelEvent: %v", err)
	}
}

func TestCancelEvent_goneIsNotAnError(t *testing.T) {
	// 410 Gone means the event was already deleted — treat as success.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	t.Cleanup(srv.Close)

	c := newTestClient(t)
	c.apiBase = srv.URL
	saveDestinationConnection(t, c, "user-1", "primary")

	if err := c.CancelEvent(context.Background(), "user-1", "already-gone"); err != nil {
		t.Errorf("CancelEvent on 410: %v (want nil)", err)
	}
}

func TestCancelEvent_emptyEventID_noOp(t *testing.T) {
	// Empty event ID must return nil without making any HTTP call.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("unexpected HTTP call for empty event ID")
	}))
	t.Cleanup(srv.Close)

	c := newTestClient(t)
	c.apiBase = srv.URL
	saveDestinationConnection(t, c, "user-1", "primary")

	if err := c.CancelEvent(context.Background(), "user-1", ""); err != nil {
		t.Errorf("CancelEvent(\"\") = %v; want nil", err)
	}
}

func TestCancelEvent_notConnected_returnsNil(t *testing.T) {
	c := newTestClient(t)
	if err := c.CancelEvent(context.Background(), "user-no-connection", "some-event"); err != nil {
		t.Errorf("CancelEvent for unconnected user: %v", err)
	}
}

func TestCancelEvent_serverError_returnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	c := newTestClient(t)
	c.apiBase = srv.URL
	saveDestinationConnection(t, c, "user-1", "primary")

	if err := c.CancelEvent(context.Background(), "user-1", "event-id"); err == nil {
		t.Error("expected error for 500 response")
	}
}

func TestUpdateEvent_sendsPatchWithNewTimes(t *testing.T) {
	var gotMethod string
	var gotBody calEventReq
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		json.NewDecoder(r.Body).Decode(&gotBody) //nolint:errcheck
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"evt-1"}`)) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)

	c := newTestClient(t)
	c.apiBase = srv.URL
	saveDestinationConnection(t, c, "user-1", "primary")

	start := time.Date(2027, 6, 18, 10, 0, 0, 0, time.UTC)
	if err := c.UpdateEvent(context.Background(), "user-1", "evt-1", start, start.Add(30*time.Minute)); err != nil {
		t.Fatalf("UpdateEvent: %v", err)
	}
	if gotMethod != http.MethodPatch {
		t.Errorf("method = %s; want PATCH", gotMethod)
	}
	if gotBody.Start.DateTime != "2027-06-18T10:00:00Z" {
		t.Errorf("start = %q; want 2027-06-18T10:00:00Z", gotBody.Start.DateTime)
	}
	if gotBody.End.DateTime != "2027-06-18T10:30:00Z" {
		t.Errorf("end = %q; want 2027-06-18T10:30:00Z", gotBody.End.DateTime)
	}
}

func TestUpdateEvent_emptyEventID_noOp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("unexpected HTTP call for empty event ID")
	}))
	t.Cleanup(srv.Close)

	c := newTestClient(t)
	c.apiBase = srv.URL
	saveDestinationConnection(t, c, "user-1", "primary")

	if err := c.UpdateEvent(context.Background(), "user-1", "", time.Now(), time.Now()); err != nil {
		t.Errorf("UpdateEvent(\"\") = %v; want nil", err)
	}
}
