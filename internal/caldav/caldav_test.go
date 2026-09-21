package caldav

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	c, err := New(newTestDB(t), testKeyHex)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Tests talk to a local httptest.Server, so bypass the production SSRF guard
	// (same reasoning as internal/worker/worker_test.go's WithHTTPClient).
	c.hc.Transport = nil
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

func TestNew_badKey(t *testing.T) {
	if _, err := New(newTestDB(t), "nothex"); err == nil {
		t.Error("expected error for invalid key")
	}
}

func TestEncryptRoundTrip(t *testing.T) {
	c := newTestClient(t)
	enc, err := c.encrypt([]byte("app-specific-pw"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	got, err := c.decrypt(enc)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(got) != "app-specific-pw" {
		t.Errorf("round-trip = %q, want app-specific-pw", got)
	}
}

// saveConnection must mirror the multi-account invariants of the OAuth providers: the first
// account becomes the destination; a second is conflict-check only; re-saving an account
// preserves its flags and refreshes the password.
func TestSaveConnection_multiAccountDestinationInvariants(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	seedUser(t, c.db, "u1")

	if err := c.saveConnection(ctx, "u1", "a@icloud.com", "pw1", "https://x/cal/a/", "https://x/home/"); err != nil {
		t.Fatalf("save a: %v", err)
	}
	if err := c.saveConnection(ctx, "u1", "b@icloud.com", "pw2", "https://x/cal/b/", "https://x/home/"); err != nil {
		t.Fatalf("save b: %v", err)
	}

	dest := destEmail(t, c.db, "u1")
	if dest != "a@icloud.com" {
		t.Fatalf("destination = %q, want first account a@icloud.com", dest)
	}
	if n := countConns(t, c.db, "u1"); n != 2 {
		t.Fatalf("connections = %d, want 2", n)
	}

	// Promote b, then re-save b (a "refresh") — destination must stay b, password updated.
	// Addressed by account, not row id: the id changes on exactly the refresh below.
	svc := calendar.NewService(c.db)
	svc.Register(c)
	if err := svc.SetDestination(ctx, "u1", "caldav", "b@icloud.com"); err != nil {
		t.Fatalf("set dest b: %v", err)
	}
	if err := c.saveConnection(ctx, "u1", "b@icloud.com", "pw2-rotated", "https://x/cal/b/", "https://x/home/"); err != nil {
		t.Fatalf("re-save b: %v", err)
	}
	if dest := destEmail(t, c.db, "u1"); dest != "b@icloud.com" {
		t.Errorf("after refresh destination = %q, want b@icloud.com (preserved)", dest)
	}
	if n := countConns(t, c.db, "u1"); n != 2 {
		t.Errorf("after refresh connections = %d, want 2 (no duplicate)", n)
	}
}

// A fake CalDAV server: answers the three discovery PROPFINDs, a calendar-query REPORT,
// and records PUT/DELETE so write-back can be asserted. The home exposes two VEVENT
// calendars (work, personal) plus a tasks-only collection that must be filtered out.
// REPORT targets are appended to reportPaths so multi-calendar conflict checks can be
// asserted.
func fakeServer(t *testing.T, putBody, deletePath *string, reportPaths *[]string) *httptest.Server {
	t.Helper()
	const principal = `<d:multistatus xmlns:d="DAV:"><d:response><d:href>/</d:href><d:propstat><d:status>HTTP/1.1 200 OK</d:status><d:prop><d:current-user-principal><d:href>/principals/user/</d:href></d:current-user-principal></d:prop></d:propstat></d:response></d:multistatus>`
	const home = `<d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:response><d:href>/principals/user/</d:href><d:propstat><d:status>HTTP/1.1 200 OK</d:status><d:prop><c:calendar-home-set><d:href>/calendars/user/</d:href></c:calendar-home-set></d:prop></d:propstat></d:response></d:multistatus>`
	const collections = `<d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
	  <d:response><d:href>/calendars/user/</d:href><d:propstat><d:status>HTTP/1.1 200 OK</d:status><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop></d:propstat></d:response>
	  <d:response><d:href>/calendars/user/work/</d:href><d:propstat><d:status>HTTP/1.1 200 OK</d:status><d:prop><d:resourcetype><d:collection/><c:calendar/></d:resourcetype><d:displayname>Calendar</d:displayname><c:supported-calendar-component-set><c:comp name="VEVENT"/></c:supported-calendar-component-set></d:prop></d:propstat></d:response>
	  <d:response><d:href>/calendars/user/personal/</d:href><d:propstat><d:status>HTTP/1.1 200 OK</d:status><d:prop><d:resourcetype><d:collection/><c:calendar/></d:resourcetype><d:displayname>Personal</d:displayname><c:supported-calendar-component-set><c:comp name="VEVENT"/></c:supported-calendar-component-set></d:prop></d:propstat></d:response>
	  <d:response><d:href>/calendars/user/tasks/</d:href><d:propstat><d:status>HTTP/1.1 200 OK</d:status><d:prop><d:resourcetype><d:collection/><c:calendar/></d:resourcetype><d:displayname>Reminders</d:displayname><c:supported-calendar-component-set><c:comp name="VTODO"/></c:supported-calendar-component-set></d:prop></d:propstat></d:response>
	</d:multistatus>`
	// One busy event, one TRANSPARENT (ignored), one CANCELLED (ignored).
	const report = `<d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:response><d:href>/calendars/user/work/ev1.ics</d:href><d:propstat><d:status>HTTP/1.1 200 OK</d:status><d:prop><c:calendar-data>BEGIN:VCALENDAR
BEGIN:VEVENT
UID:busy@x
DTSTART:20260625T090000Z
DTEND:20260625T100000Z
SUMMARY:Busy
END:VEVENT
BEGIN:VEVENT
UID:free@x
DTSTART:20260625T110000Z
DTEND:20260625T120000Z
TRANSP:TRANSPARENT
END:VEVENT
BEGIN:VEVENT
UID:gone@x
DTSTART:20260625T130000Z
DTEND:20260625T140000Z
STATUS:CANCELLED
END:VEVENT
END:VCALENDAR</c:calendar-data></d:prop></d:propstat></d:response></d:multistatus>`
	const personalReport = `<d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:response><d:href>/calendars/user/personal/ev9.ics</d:href><d:propstat><d:status>HTTP/1.1 200 OK</d:status><d:prop><c:calendar-data>BEGIN:VCALENDAR
BEGIN:VEVENT
UID:personal-busy@x
DTSTART:20260625T150000Z
DTEND:20260625T160000Z
SUMMARY:Personal busy
END:VEVENT
END:VCALENDAR</c:calendar-data></d:prop></d:propstat></d:response></d:multistatus>`

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "PROPFIND" && r.URL.Path == "/":
			w.WriteHeader(http.StatusMultiStatus)
			io.WriteString(w, principal)
		case r.Method == "PROPFIND" && r.URL.Path == "/principals/user/":
			w.WriteHeader(http.StatusMultiStatus)
			io.WriteString(w, home)
		case r.Method == "PROPFIND" && r.URL.Path == "/calendars/user/":
			w.WriteHeader(http.StatusMultiStatus)
			io.WriteString(w, collections)
		case r.Method == "REPORT" && r.URL.Path == "/calendars/user/work/":
			if reportPaths != nil {
				*reportPaths = append(*reportPaths, r.URL.Path)
			}
			w.WriteHeader(http.StatusMultiStatus)
			io.WriteString(w, report)
		case r.Method == "REPORT" && r.URL.Path == "/calendars/user/personal/":
			if reportPaths != nil {
				*reportPaths = append(*reportPaths, r.URL.Path)
			}
			w.WriteHeader(http.StatusMultiStatus)
			io.WriteString(w, personalReport)
		case r.Method == "PUT":
			if putBody != nil {
				b, _ := io.ReadAll(r.Body)
				*putBody = string(b)
			}
			w.WriteHeader(http.StatusCreated)
		case r.Method == "DELETE":
			if deletePath != nil {
				*deletePath = r.URL.Path
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Logf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestConnect_discoverFreeBusyWriteback(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	seedUser(t, c.db, "u1")

	var putBody, delPath string
	srv := fakeServer(t, &putBody, &delPath, nil)
	defer srv.Close()

	// Connect → discovery walks principal/home/collections and stores the connection.
	email, calURL, err := c.Connect(ctx, "u1", srv.URL, "User@iCloud.com", "app-pw")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if email != "user@icloud.com" {
		t.Errorf("account email = %q, want lowercased user@icloud.com", email)
	}
	if !strings.HasSuffix(calURL, "/calendars/user/work/") {
		t.Errorf("calendar URL = %q, want .../calendars/user/work/", calURL)
	}

	// FreeBusy parses the REPORT: only the busy (opaque, non-cancelled) event counts.
	from := time.Date(2026, 6, 25, 0, 0, 0, 0, time.UTC)
	busy, err := c.FreeBusy(ctx, "u1", from, from.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("FreeBusy: %v", err)
	}
	if len(busy) != 1 {
		t.Fatalf("busy intervals = %d, want 1 (transparent + cancelled skipped): %+v", len(busy), busy)
	}
	if !busy[0].Start.Equal(time.Date(2026, 6, 25, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("busy start = %v, want 09:00Z", busy[0].Start)
	}

	// CreateEvent PUTs an .ics to the destination; CancelEvent DELETEs it.
	eventID, joinURL, _, err := c.CreateEvent(ctx, "u1", calendar.CreateEventParams{
		Summary: "Intro call", Start: from.Add(15 * time.Hour), End: from.Add(16 * time.Hour),
		OrganizerName: "Wynne", OrganizerEmail: "w@x.com",
	})
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	if joinURL != "" {
		t.Errorf("CalDAV has no join URL, got %q", joinURL)
	}
	if !strings.Contains(putBody, "SUMMARY:Intro call") || !strings.Contains(putBody, "BEGIN:VEVENT") {
		t.Errorf("PUT body missing event content:\n%s", putBody)
	}
	if !strings.HasPrefix(eventID, srv.URL) || !strings.HasSuffix(eventID, ".ics") {
		t.Errorf("eventID = %q, want absolute .ics URL", eventID)
	}
	if err := c.CancelEvent(ctx, "u1", "", eventID); err != nil {
		t.Fatalf("CancelEvent: %v", err)
	}
	if !strings.HasSuffix(delPath, ".ics") {
		t.Errorf("DELETE path = %q, want the .ics resource", delPath)
	}
}

func TestConnect_discoversAllCalendars(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	seedUser(t, c.db, "u1")

	srv := fakeServer(t, nil, nil, nil)
	defer srv.Close()

	_, calURL, err := c.Connect(ctx, "u1", srv.URL, "user@icloud.com", "app-pw")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	// The "Calendar"-named collection stays the bound default.
	if !strings.HasSuffix(calURL, "/calendars/user/work/") {
		t.Errorf("bound calendar = %q, want .../calendars/user/work/", calURL)
	}

	// Both VEVENT collections seeded; the tasks-only one filtered out.
	type row struct{ id, name string }
	var check int
	rows, err := c.db.QueryContext(ctx,
		`SELECT calendar_id, name, check_conflicts FROM connection_calendars
		 WHERE user_id = 'u1' AND provider = 'caldav' ORDER BY calendar_id`)
	if err != nil {
		t.Fatalf("seeded rows: %v", err)
	}
	var got []row
	var checks []int
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.name, &check); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, r)
		checks = append(checks, check)
	}
	rows.Close()
	if len(got) != 2 {
		t.Fatalf("seeded calendars = %+v, want work + personal (no tasks)", got)
	}
	if got[0].name != "Personal" || got[1].name != "Calendar" {
		t.Errorf("seeded names = %+v, want Personal + Calendar", got)
	}
	// Fresh connect preserves today's behaviour: the bound calendar is checked.
	if checks[0] != 0 || checks[1] != 1 {
		t.Errorf("seeded check flags = %v, want [0 1] (bound work checked)", checks)
	}

	var home string
	if err := c.db.QueryRowContext(ctx,
		`SELECT COALESCE(caldav_home_url,'') FROM calendar_connections WHERE user_id = 'u1'`).Scan(&home); err != nil {
		t.Fatalf("home url: %v", err)
	}
	if home != srv.URL+"/calendars/user/" {
		t.Errorf("stored home = %q, want %s/calendars/user/", home, srv.URL)
	}
}

func TestListCalendars_liveThenFallback(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	seedUser(t, c.db, "u1")

	srv := fakeServer(t, nil, nil, nil)

	if _, _, err := c.Connect(ctx, "u1", srv.URL, "user@icloud.com", "app-pw"); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	cals, err := c.ListCalendars(ctx, "u1", "user@icloud.com")
	if err != nil {
		t.Fatalf("ListCalendars: %v", err)
	}
	if len(cals) != 2 {
		t.Fatalf("live calendars = %d, want 2 (tasks filtered)", len(cals))
	}
	byID := map[string]calendar.CalendarInfo{}
	for _, cal := range cals {
		byID[cal.ID] = cal
	}
	work, ok := byID[srv.URL+"/calendars/user/work/"]
	if !ok || work.Name != "Calendar" || !work.Primary || !work.Writable {
		t.Errorf("work entry = %+v, want named Calendar + primary + writable", work)
	}
	personal, ok := byID[srv.URL+"/calendars/user/personal/"]
	if !ok || personal.Name != "Personal" || personal.Primary {
		t.Errorf("personal entry = %+v, want named Personal, not primary", personal)
	}

	// Server gone: the stored selection keeps the picker working.
	srv.Close()
	cals, err = c.ListCalendars(ctx, "u1", "user@icloud.com")
	if err != nil {
		t.Fatalf("fallback ListCalendars: %v", err)
	}
	if len(cals) != 2 {
		t.Fatalf("fallback calendars = %d, want the 2 seeded rows", len(cals))
	}
}

func TestFreeBusy_checksEverySelectedCalendar(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	seedUser(t, c.db, "u1")

	var reports []string
	srv := fakeServer(t, nil, nil, &reports)
	defer srv.Close()

	if _, _, err := c.Connect(ctx, "u1", srv.URL, "user@icloud.com", "app-pw"); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	// Fresh connect checks only the bound calendar; tick the second one like the picker would.
	if _, err := c.db.ExecContext(ctx,
		`UPDATE connection_calendars SET check_conflicts = 1 WHERE user_id = 'u1' AND provider = 'caldav'`); err != nil {
		t.Fatalf("enable both: %v", err)
	}

	from := time.Date(2026, 6, 25, 0, 0, 0, 0, time.UTC)
	busy, err := c.FreeBusy(ctx, "u1", from, from.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("FreeBusy: %v", err)
	}
	if len(busy) != 2 {
		t.Fatalf("busy intervals = %+v, want work 09:00 + personal 15:00", busy)
	}
	for _, want := range []string{"/calendars/user/work/", "/calendars/user/personal/"} {
		found := false
		for _, p := range reports {
			if p == want {
				found = true
			}
		}
		if !found {
			t.Errorf("no REPORT to %s (targets hit: %v)", want, reports)
		}
	}
}

func TestRewriteEventTimes(t *testing.T) {
	in := "BEGIN:VEVENT\r\nUID:x@y\r\nDTSTART;TZID=Pacific/Auckland:20260625T090000\r\nDTEND;TZID=Pacific/Auckland:20260625T093000\r\nSUMMARY:Keep me\r\nSEQUENCE:2\r\nEND:VEVENT\r\n"
	start := time.Date(2026, 6, 26, 1, 0, 0, 0, time.UTC)
	end := time.Date(2026, 6, 26, 1, 30, 0, 0, time.UTC)
	out := rewriteEventTimes(in, start, end)
	if !strings.Contains(out, "DTSTART:20260626T010000Z") {
		t.Errorf("missing rewritten DTSTART:\n%s", out)
	}
	if !strings.Contains(out, "DTEND:20260626T013000Z") {
		t.Errorf("missing rewritten DTEND:\n%s", out)
	}
	if !strings.Contains(out, "SEQUENCE:3") {
		t.Errorf("SEQUENCE not bumped:\n%s", out)
	}
	if !strings.Contains(out, "SUMMARY:Keep me") {
		t.Errorf("dropped preserved content:\n%s", out)
	}
	if strings.Contains(out, "TZID=") {
		t.Errorf("TZID start/end should be normalized to UTC Z:\n%s", out)
	}
}

func TestInvitesGuests_false(t *testing.T) {
	if newTestClient(t).InvitesGuests() {
		t.Error("CalDAV must report InvitesGuests()=false so the .ics invite is attached")
	}
}

// ----- helpers -----

func destEmail(t *testing.T, database *sql.DB, userID string) string {
	t.Helper()
	var e string
	err := database.QueryRowContext(context.Background(),
		`SELECT account_email FROM calendar_connections WHERE user_id=? AND is_destination=1`, userID).Scan(&e)
	if err != nil {
		t.Fatalf("destEmail: %v", err)
	}
	return e
}

func countConns(t *testing.T, database *sql.DB, userID string) int {
	t.Helper()
	var n int
	if err := database.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM calendar_connections WHERE user_id=?`, userID).Scan(&n); err != nil {
		t.Fatalf("countConns: %v", err)
	}
	return n
}
