package microsoft

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
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
	if err := c.saveToken(context.Background(), userID, "primary", "", "work", tok); err != nil {
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
	id, join, _, err := c.CreateEvent(context.Background(), "u1", calendar.CreateEventParams{
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
	id, join, _, err := c.CreateEvent(context.Background(), "u2", calendar.CreateEventParams{Summary: "x"})
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
	if err := c.UpdateEvent(context.Background(), "u1", "", "evt-1", start, start.Add(30*time.Minute)); err != nil {
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
	if err := c.UpdateEvent(context.Background(), "u1", "", "", time.Now(), time.Now()); err != nil {
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

	if err := c.CancelEvent(context.Background(), "u1", "", "evt-1"); err != nil {
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

	if err := c.CancelEvent(context.Background(), "u1", "", "evt-1"); err != nil {
		t.Errorf("CancelEvent(404): %v; want nil (already gone is fine)", err)
	}
}

// fakeGraphCalendars serves GET /me/calendars the way Graph does: with $select, each
// calendar carries only the selected properties. A fake that returned every property
// regardless could never notice a decoded property missing from the request's $select.
// onSelect, when non-nil, receives the raw $select value.
func fakeGraphCalendars(t *testing.T, cals []map[string]any, onSelect func(string)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/me/calendars") {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		sel := r.URL.Query().Get("$select")
		if onSelect != nil {
			onSelect(sel)
		}
		value := cals
		if sel != "" {
			value = make([]map[string]any, 0, len(cals))
			for _, cal := range cals {
				picked := map[string]any{}
				for _, prop := range strings.Split(sel, ",") {
					if v, ok := cal[prop]; ok {
						picked[prop] = v
					}
				}
				value = append(value, picked)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"value": value})
	}))
}

// graphCalendars are two calendars as Graph describes them: the user's own default, and
// one shared with them read-only (canEdit false).
var graphCalendars = []map[string]any{
	{
		"id": "cal-own", "name": "Calendar", "isDefaultCalendar": true, "canEdit": true,
		"canShare": true, "color": "auto",
		"owner": map[string]any{"name": "Alex", "address": "alex@example.com"},
	},
	{
		"id": "cal-shared", "name": "Team rota", "isDefaultCalendar": false, "canEdit": false,
		"canShare": false, "color": "lightBlue",
		"owner": map[string]any{"name": "Sam", "address": "sam@example.com"},
	},
}

func TestListCalendars_writableFollowsCanEdit(t *testing.T) {
	c := newTestClient(t)
	connect(t, c, "u1")

	srv := fakeGraphCalendars(t, graphCalendars, nil)
	defer srv.Close()
	c.apiBase = srv.URL

	got, err := c.ListCalendars(context.Background(), "u1", "")
	if err != nil {
		t.Fatalf("ListCalendars: %v", err)
	}
	want := []calendar.CalendarInfo{
		{ID: "cal-own", Name: "Calendar", Primary: true, Writable: true},
		{ID: "cal-shared", Name: "Team rota", Primary: false, Writable: false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListCalendars =\n  %+v\nwant\n  %+v", got, want)
	}
}

// decodedJSONNames lists the JSON property names a struct decodes. For a Graph response
// item that is the set of properties its request has to $select.
func decodedJSONNames(t *testing.T, typ reflect.Type) []string {
	t.Helper()
	if typ.Kind() != reflect.Struct {
		t.Fatalf("decodedJSONNames(%s): want a struct", typ)
	}
	var names []string
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Anonymous {
			// encoding/json promotes an embedded struct's fields; not needed yet, so refuse
			// rather than under-report.
			t.Fatalf("decodedJSONNames(%s): embedded field %s is not handled", typ, f.Name)
		}
		if !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		names = append(names, name)
	}
	return names
}

// Graph omits any property the request does not $select, and an absent bool decodes as
// false without error. So every property the response item decodes must be selected, or
// it silently reads as its zero value in production while a lenient fake hides it.
func TestListCalendars_selectNamesEveryDecodedProperty(t *testing.T) {
	c := newTestClient(t)
	connect(t, c, "u1")

	var gotSelect string
	srv := fakeGraphCalendars(t, graphCalendars, func(s string) { gotSelect = s })
	defer srv.Close()
	c.apiBase = srv.URL

	if _, err := c.ListCalendars(context.Background(), "u1", ""); err != nil {
		t.Fatalf("ListCalendars: %v", err)
	}
	if gotSelect == "" {
		return // no $select: Graph returns the default properties, which is not this bug
	}
	selected := map[string]bool{}
	for _, prop := range strings.Split(gotSelect, ",") {
		selected[prop] = true
	}
	value, ok := reflect.TypeOf(msCalListResp{}).FieldByName("Value")
	if !ok {
		t.Fatal("msCalListResp has no Value field")
	}
	for _, name := range decodedJSONNames(t, value.Type.Elem()) {
		if !selected[name] {
			t.Errorf("$select=%q omits %q, which msCalListResp decodes; Graph will not return it", gotSelect, name)
		}
	}
}

func TestListCalendars_followsNextLink(t *testing.T) {
	c := newTestClient(t)
	connect(t, c, "u1")

	var calls []string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.String())
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("$skiptoken") == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"value":          []map[string]any{graphCalendars[0]},
				"@odata.nextLink": srv.URL + "/me/calendars?$skiptoken=page2",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"value": []map[string]any{graphCalendars[1]},
		})
	}))
	defer srv.Close()
	c.apiBase = srv.URL

	got, err := c.ListCalendars(context.Background(), "u1", "")
	if err != nil {
		t.Fatalf("ListCalendars: %v", err)
	}
	want := []calendar.CalendarInfo{
		{ID: "cal-own", Name: "Calendar", Primary: true, Writable: true},
		{ID: "cal-shared", Name: "Team rota", Primary: false, Writable: false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListCalendars =\n  %+v\nwant\n  %+v", got, want)
	}
	if len(calls) != 2 {
		t.Errorf("expected 2 page fetches, got %d: %v", len(calls), calls)
	}
}

func mkIDToken(tid string) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"tid":"` + tid + `"}`))
	return "header." + payload + ".sig"
}

func TestAccountKindFromIDToken(t *testing.T) {
	cases := []struct {
		name, idToken, want string
	}{
		{"personal", mkIDToken(consumersTenantID), "personal"},
		{"work", mkIDToken("c4bf7066-8da6-4242-8a58-cc60c1cbded5"), "work"},
		{"empty tid", mkIDToken(""), ""},
		{"no id_token", "", ""},
		{"malformed", "not-a-jwt", ""},
	}
	for _, tc := range cases {
		tok := &oauth2.Token{}
		if tc.idToken != "" {
			tok = tok.WithExtra(map[string]any{"id_token": tc.idToken})
		}
		if got := accountKindFromIDToken(tok); got != tc.want {
			t.Errorf("%s: accountKindFromIDToken=%q; want %q", tc.name, got, tc.want)
		}
	}
}

func TestSaveToken_refreshPreservesKind(t *testing.T) {
	c := newTestClient(t)
	seedUser(t, c.db, "u1")
	tok := &oauth2.Token{AccessToken: "a", RefreshToken: "r", Expiry: time.Now().Add(time.Hour)}
	if err := c.saveToken(context.Background(), "u1", "primary", "", "personal", tok); err != nil {
		t.Fatalf("saveToken: %v", err)
	}
	// Refresh path passes kind="" — must not wipe the stored "personal".
	if err := c.saveToken(context.Background(), "u1", "primary", "", "", tok); err != nil {
		t.Fatalf("saveToken refresh: %v", err)
	}
	var kind string
	if err := c.db.QueryRow(`SELECT account_kind FROM calendar_connections WHERE user_id = ?`, "u1").Scan(&kind); err != nil {
		t.Fatalf("query: %v", err)
	}
	if kind != "personal" {
		t.Errorf("account_kind=%q after refresh; want personal (preserved)", kind)
	}
}
