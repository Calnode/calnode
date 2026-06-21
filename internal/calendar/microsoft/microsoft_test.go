package microsoft

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/db"
)

const testKeyHex = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func newTestClient(t *testing.T) *Client {
	t.Helper()
	c, err := New(newTestDB(t), "client-id", "client-secret", "common", "http://localhost/cb", testKeyHex)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func seedUser(t *testing.T, database *sql.DB, userID string) {
	t.Helper()
	_, err := database.ExecContext(context.Background(), `
		INSERT INTO users (id, email, name, iana_timezone, is_admin, created_at)
		VALUES (?, ?, 'Test User', 'UTC', 0, '2026-01-01T00:00:00Z')`,
		userID, userID+"@test.example")
	if err != nil {
		t.Fatalf("seedUser: %v", err)
	}
}

// connect seeds a non-expiring token so the oauth2 client serves it without
// hitting the network — the mock server then only sees the Graph API call.
func connect(t *testing.T, c *Client, userID string) {
	t.Helper()
	seedUser(t, c.db, userID)
	tok := &oauth2.Token{AccessToken: "access", RefreshToken: "refresh", Expiry: time.Now().Add(time.Hour)}
	if err := c.saveToken(context.Background(), userID, "primary", tok); err != nil {
		t.Fatalf("saveToken: %v", err)
	}
}

func TestProvider_identity(t *testing.T) {
	c := newTestClient(t)
	if c.Name() != "microsoft" {
		t.Errorf("Name()=%q; want microsoft", c.Name())
	}
	if !c.InvitesGuests() {
		t.Error("InvitesGuests()=false; want true (Graph invites guests itself)")
	}
}

func TestFreeBusy_parsesCalendarView(t *testing.T) {
	c := newTestClient(t)
	connect(t, c, "u1")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/me/calendarView") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"value":[
			{"start":{"dateTime":"2026-06-22T21:00:00.0000000","timeZone":"UTC"},
			 "end":{"dateTime":"2026-06-22T21:30:00.0000000","timeZone":"UTC"}}
		]}`))
	}))
	defer srv.Close()
	c.apiBase = srv.URL

	iv, err := c.FreeBusy(context.Background(), "u1", time.Now(), time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatalf("FreeBusy: %v", err)
	}
	if len(iv) != 1 {
		t.Fatalf("got %d intervals; want 1", len(iv))
	}
	want := time.Date(2026, 6, 22, 21, 0, 0, 0, time.UTC)
	if !iv[0].Start.Equal(want) {
		t.Errorf("start=%v; want %v", iv[0].Start, want)
	}
}

func TestCreateEvent_returnsIDAndTeamsLink(t *testing.T) {
	c := newTestClient(t)
	connect(t, c, "u1")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.Contains(r.URL.Path, "/me/events") {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"evt-1","onlineMeeting":{"joinUrl":"https://teams.microsoft.com/l/meetup-join/x"}}`))
	}))
	defer srv.Close()
	c.apiBase = srv.URL

	start := time.Date(2026, 6, 22, 21, 0, 0, 0, time.UTC)
	id, join, err := c.CreateEvent(context.Background(), "u1", calendar.CreateEventParams{
		Summary:        "Intro call",
		Start:          start,
		End:            start.Add(30 * time.Minute),
		OrganizerName:  "Alex",
		OrganizerEmail: "alex@example.com",
		AddMeet:        true,
	})
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	if id != "evt-1" {
		t.Errorf("id=%q; want evt-1", id)
	}
	if !strings.Contains(join, "teams.microsoft.com") {
		t.Errorf("joinURL=%q; want a Teams link", join)
	}
}

func TestCreateEvent_noConnection(t *testing.T) {
	c := newTestClient(t)
	seedUser(t, c.db, "u2") // no token saved
	id, join, err := c.CreateEvent(context.Background(), "u2", calendar.CreateEventParams{Summary: "x"})
	if err != nil || id != "" || join != "" {
		t.Errorf("want no-op (\"\",\"\",nil); got %q,%q,%v", id, join, err)
	}
}

func TestUpdateEvent_patchesNewTime(t *testing.T) {
	c := newTestClient(t)
	connect(t, c, "u1")

	var gotMethod, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"evt-1"}`))
	}))
	defer srv.Close()
	c.apiBase = srv.URL

	start := time.Date(2026, 6, 22, 21, 0, 0, 0, time.UTC)
	if err := c.UpdateEvent(context.Background(), "u1", "evt-1", start, start.Add(30*time.Minute)); err != nil {
		t.Fatalf("UpdateEvent: %v", err)
	}
	if gotMethod != http.MethodPatch {
		t.Errorf("method=%s; want PATCH", gotMethod)
	}
	if !strings.HasSuffix(gotPath, "/me/events/evt-1") {
		t.Errorf("path=%s; want .../me/events/evt-1", gotPath)
	}
	if !strings.Contains(gotBody, "2026-06-22T21:00:00") || !strings.Contains(gotBody, "2026-06-22T21:30:00") {
		t.Errorf("body missing new start/end: %s", gotBody)
	}
}

func TestUpdateEvent_emptyIDNoOp(t *testing.T) {
	c := newTestClient(t)
	connect(t, c, "u1")
	// No server set; an empty eventID must short-circuit before any HTTP call.
	if err := c.UpdateEvent(context.Background(), "u1", "", time.Now(), time.Now()); err != nil {
		t.Errorf("UpdateEvent(emptyID): %v; want nil no-op", err)
	}
}

func TestCancelEvent_deletes(t *testing.T) {
	c := newTestClient(t)
	connect(t, c, "u1")

	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c.apiBase = srv.URL

	if err := c.CancelEvent(context.Background(), "u1", "evt-1"); err != nil {
		t.Fatalf("CancelEvent: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("method=%s; want DELETE", gotMethod)
	}
	if !strings.HasSuffix(gotPath, "/me/events/evt-1") {
		t.Errorf("path=%s; want .../me/events/evt-1", gotPath)
	}
}

func TestCancelEvent_alreadyGoneIsOK(t *testing.T) {
	c := newTestClient(t)
	connect(t, c, "u1")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound) // event already deleted on Graph's side
	}))
	defer srv.Close()
	c.apiBase = srv.URL

	if err := c.CancelEvent(context.Background(), "u1", "evt-1"); err != nil {
		t.Errorf("CancelEvent(404): %v; want nil (already gone is fine)", err)
	}
}
