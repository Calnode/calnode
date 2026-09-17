package caldav

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/calendar"
)

// multiCalServer is one CalDAV account whose calendar home holds every kind of collection the
// calendar listing has to tell apart:
//
//	/calendars/user/elsewhere/  an href on ANOTHER origin (a second server), listed first
//	/calendars/user/personal/   "Personal", VEVENT, write privilege: the bound calendar
//	/calendars/user/team/       no display name, VEVENT+VTODO, privileges not reported
//	/calendars/user/tasks/      "Tasks", VTODO only
//	/calendars/user/holidays/   "Holidays", VEVENT, read + write-properties only
//
// Every request is recorded, and any request reaching the other origin is counted.
type multiCalServer struct {
	srv   *httptest.Server
	other *httptest.Server

	// noPrincipalOnCollections makes a PROPFIND for the principal on anything but "/" fail,
	// as on a server that does not implement RFC 5397 on calendar collections.
	noPrincipalOnCollections bool

	mu        sync.Mutex
	reqs      []string          // "METHOD /path"
	events    map[string]string // path -> stored iCalendar body
	otherHits int
}

func newMultiCalServer(t *testing.T) *multiCalServer {
	t.Helper()
	fs := &multiCalServer{events: map[string]string{}}
	fs.other = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fs.mu.Lock()
		fs.otherHits++
		fs.mu.Unlock()
		w.WriteHeader(http.StatusMultiStatus)
		io.WriteString(w, `<d:multistatus xmlns:d="DAV:"/>`)
	}))
	t.Cleanup(fs.other.Close)
	fs.srv = httptest.NewServer(http.HandlerFunc(fs.serve))
	t.Cleanup(fs.srv.Close)
	return fs
}

func (fs *multiCalServer) url(p string) string { return fs.srv.URL + p }

func (fs *multiCalServer) reset() {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.reqs = nil
}

// paths returns the sorted paths of recorded requests with the given method.
func (fs *multiCalServer) paths(method string) []string {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	var out []string
	for _, r := range fs.reqs {
		if m, p, _ := strings.Cut(r, " "); m == method {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func (fs *multiCalServer) hitsElsewhere() int {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.otherHits
}

const msOpen = `<d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">`

func okResponse(href, props string) string {
	return `<d:response><d:href>` + href + `</d:href><d:propstat><d:status>HTTP/1.1 200 OK</d:status><d:prop>` +
		props + `</d:prop></d:propstat></d:response>`
}

func calendarProps(name, comps, privileges string) string {
	p := `<d:resourcetype><d:collection/><c:calendar/></d:resourcetype>`
	if name != "" {
		p += `<d:displayname>` + name + `</d:displayname>`
	}
	p += `<c:supported-calendar-component-set>` + comps + `</c:supported-calendar-component-set>`
	if privileges != "" {
		p += `<d:current-user-privilege-set>` + privileges + `</d:current-user-privilege-set>`
	}
	return p
}

const (
	vevent = `<c:comp name="VEVENT"/>`
	vtodo  = `<c:comp name="VTODO"/>`
	priv   = `<d:privilege><d:%s/></d:privilege>`
)

// busyHour is the hour (UTC, 2026-06-25) of the one event each calendar returns, so a FreeBusy
// result says which calendars were read.
var busyHour = map[string]int{
	"/calendars/user/personal/": 9,
	"/calendars/user/team/":     10,
	"/calendars/user/holidays/": 11,
}

func (fs *multiCalServer) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	fs.mu.Lock()
	fs.reqs = append(fs.reqs, r.Method+" "+r.URL.Path)
	fs.mu.Unlock()

	multistatus := func(inner string) {
		w.WriteHeader(http.StatusMultiStatus)
		io.WriteString(w, msOpen+inner+`</d:multistatus>`)
	}
	switch {
	case r.Method == "PROPFIND" && strings.Contains(string(body), "current-user-principal"):
		if fs.noPrincipalOnCollections && r.URL.Path != "/" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		multistatus(okResponse(r.URL.Path, `<d:current-user-principal><d:href>/principals/user/</d:href></d:current-user-principal>`))
	case r.Method == "PROPFIND" && strings.Contains(string(body), "calendar-home-set") && r.URL.Path == "/principals/user/":
		multistatus(okResponse(r.URL.Path, `<c:calendar-home-set><d:href>/calendars/user/</d:href></c:calendar-home-set>`))
	case r.Method == "PROPFIND" && r.URL.Path == "/calendars/user/" && r.Header.Get("Depth") == "1":
		multistatus(
			okResponse("/calendars/user/", `<d:resourcetype><d:collection/></d:resourcetype>`) +
				okResponse(fs.other.URL+"/calendars/user/elsewhere/",
					calendarProps("Elsewhere", vevent, fmt.Sprintf(priv, "read")+fmt.Sprintf(priv, "write"))) +
				okResponse("/calendars/user/personal/",
					calendarProps("Personal", vevent, fmt.Sprintf(priv, "read")+fmt.Sprintf(priv, "write"))) +
				// Privileges reported under a 404 propstat, listed before the 200 one: not reported.
				`<d:response><d:href>/calendars/user/team/</d:href>` +
				`<d:propstat><d:status>HTTP/1.1 404 Not Found</d:status><d:prop><d:current-user-privilege-set/></d:prop></d:propstat>` +
				`<d:propstat><d:status>HTTP/1.1 200 OK</d:status><d:prop>` + calendarProps("", vevent+vtodo, "") + `</d:prop></d:propstat>` +
				`</d:response>` +
				okResponse("/calendars/user/tasks/",
					calendarProps("Tasks", vtodo, fmt.Sprintf(priv, "all"))) +
				okResponse("/calendars/user/holidays/",
					calendarProps("Holidays", vevent, fmt.Sprintf(priv, "read")+fmt.Sprintf(priv, "write-properties"))),
		)
	case r.Method == "REPORT":
		hour, ok := busyHour[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		multistatus(okResponse(r.URL.Path+"ev.ics", fmt.Sprintf(`<c:calendar-data>BEGIN:VCALENDAR
BEGIN:VEVENT
UID:%[1]d@x
DTSTART:20260625T%02[1]d0000Z
DTEND:20260625T%02[2]d0000Z
END:VEVENT
END:VCALENDAR</c:calendar-data>`, hour, hour+1)))
	case r.Method == http.MethodPut:
		fs.mu.Lock()
		fs.events[r.URL.Path] = string(body)
		fs.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	case r.Method == http.MethodGet:
		fs.mu.Lock()
		ev, ok := fs.events[r.URL.Path]
		fs.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("ETag", `"1"`)
		io.WriteString(w, ev)
	case r.Method == http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

const multiCalAccount = "user@example.com"

// connectMultiCal connects user u1 to a fresh multiCalServer and clears the request log.
func connectMultiCal(t *testing.T) (*Client, *multiCalServer) {
	t.Helper()
	c := newTestClient(t)
	seedUser(t, c.db, "u1")
	fs := newMultiCalServer(t)
	_, bound, err := c.Connect(context.Background(), "u1", fs.srv.URL, multiCalAccount, "app-pw")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if want := fs.url("/calendars/user/personal/"); bound != want {
		t.Fatalf("connect bound %q, want %q (the first same-origin VEVENT calendar)", bound, want)
	}
	fs.reset()
	return c, fs
}

func selectCalendars(t *testing.T, c *Client, sels ...calendar.CalendarSelection) {
	t.Helper()
	svc := calendar.NewService(c.db)
	svc.Register(c)
	if err := svc.SetAccountCalendars(context.Background(), "u1", "caldav", multiCalAccount, sels); err != nil {
		t.Fatalf("SetAccountCalendars: %v", err)
	}
}

func sel(id string, check, dest bool) calendar.CalendarSelection {
	return calendar.CalendarSelection{CalendarInfo: calendar.CalendarInfo{ID: id}, CheckConflicts: check, IsDestination: dest}
}

func busyHours(t *testing.T, c *Client) []int {
	t.Helper()
	day := time.Date(2026, 6, 25, 0, 0, 0, 0, time.UTC)
	busy, err := c.FreeBusy(context.Background(), "u1", day, day.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("FreeBusy: %v", err)
	}
	var hours []int
	for _, iv := range busy {
		hours = append(hours, iv.Start.Hour())
	}
	sort.Ints(hours)
	return hours
}

// Issue #42: a CalDAV account with several calendars only ever showed the one it was bound to.
func TestListCalendars_listsEveryEventCalendarOnTheAccount(t *testing.T) {
	c, fs := connectMultiCal(t)

	got, err := c.ListCalendars(context.Background(), "u1", multiCalAccount)
	if err != nil {
		t.Fatalf("ListCalendars: %v", err)
	}
	want := []calendar.CalendarInfo{
		{ID: fs.url("/calendars/user/personal/"), Name: "Personal", Primary: true, Writable: true},
		// No display name: named by its path. Privileges not reported: writable.
		{ID: fs.url("/calendars/user/team/"), Name: "team", Primary: false, Writable: true},
		// read + write-properties is a read-only share.
		{ID: fs.url("/calendars/user/holidays/"), Name: "Holidays", Primary: false, Writable: false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListCalendars =\n  %+v\nwant (no VTODO-only list, nothing from another origin)\n  %+v", got, want)
	}
	// Discovery restarted from the stored collection, not a server URL that is never stored.
	if p := fs.paths("PROPFIND"); !slices.Contains(p, "/calendars/user/personal/") {
		t.Errorf("PROPFINDs = %v, want discovery to start at the bound collection", p)
	}
	if n := fs.hitsElsewhere(); n != 0 {
		t.Errorf("%d request(s) reached the other origin; its collection must be skipped, not contacted", n)
	}
}

// A server that does not answer current-user-principal on a calendar collection still gets its
// calendars listed, from the bound collection's parent.
func TestListCalendars_fallsBackToTheBoundCalendarsParent(t *testing.T) {
	c, fs := connectMultiCal(t)
	fs.noPrincipalOnCollections = true

	got, err := c.ListCalendars(context.Background(), "u1", multiCalAccount)
	if err != nil {
		t.Fatalf("ListCalendars: %v", err)
	}
	if len(got) != 3 || !got[0].Primary {
		t.Errorf("ListCalendars = %+v, want the same three calendars with the bound one primary", got)
	}
	if slices.Contains(fs.paths("PROPFIND"), "/principals/user/") {
		t.Error("expected no principal walk when the collection reports no principal")
	}
}

// A stored app password the server now rejects is a reconnect, not an unreachable server.
func TestListCalendars_rejectedPasswordNeedsAReconnect(t *testing.T) {
	c := newTestClient(t)
	seedUser(t, c.db, "u1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	if err := c.saveConnection(context.Background(), "u1", multiCalAccount, "revoked", srv.URL+"/calendars/user/personal/"); err != nil {
		t.Fatalf("saveConnection: %v", err)
	}
	_, err := c.ListCalendars(context.Background(), "u1", multiCalAccount)
	if !calendar.IsReauthErr(err) {
		t.Errorf("err = %v, want one IsReauthErr recognises", err)
	}
	if err == nil || !strings.Contains(err.Error(), "authentication failed") {
		t.Errorf("err = %v, want the connect form's authentication message kept", err)
	}
}

func TestFreeBusy_readsExactlyTheCalendarsSelectedForConflicts(t *testing.T) {
	c, fs := connectMultiCal(t)
	selectCalendars(t, c,
		sel(fs.url("/calendars/user/personal/"), false, true), // unticked: never read
		sel(fs.url("/calendars/user/team/"), true, false),
		sel(fs.url("/calendars/user/holidays/"), true, false), // read-only is fine for conflicts
	)
	fs.reset()

	if got, want := busyHours(t, c), []int{10, 11}; !reflect.DeepEqual(got, want) {
		t.Errorf("busy hours = %v, want %v (team + holidays)", got, want)
	}
	if got, want := fs.paths("REPORT"), []string{"/calendars/user/holidays/", "/calendars/user/team/"}; !reflect.DeepEqual(got, want) {
		t.Errorf("REPORTs = %v, want exactly %v", got, want)
	}
	if p := fs.paths("PROPFIND"); len(p) != 0 {
		t.Errorf("free/busy made discovery requests %v; it must read stored selections only", p)
	}
}

// Connections that never saved a selection keep reading only the calendar they were bound to,
// whether they have no selection rows at all or the one row migration 00049 seeded.
func TestFreeBusy_withoutASavedSelectionReadsOnlyTheBoundCalendar(t *testing.T) {
	for _, tc := range []struct {
		name string
		seed bool
	}{{"no selection rows", false}, {"row seeded by migration 00049", true}} {
		t.Run(tc.name, func(t *testing.T) {
			c, fs := connectMultiCal(t)
			if tc.seed {
				if _, err := c.db.Exec(`
					INSERT INTO connection_calendars (id, user_id, provider, account_email, calendar_id, name, check_conflicts, is_destination)
					SELECT 'seeded', user_id, provider, account_email, calendar_id, '', check_conflicts, is_destination
					FROM calendar_connections WHERE user_id = 'u1'`); err != nil {
					t.Fatalf("seed: %v", err)
				}
			}

			if got, want := busyHours(t, c), []int{9}; !reflect.DeepEqual(got, want) {
				t.Errorf("busy hours = %v, want %v (the bound calendar only)", got, want)
			}
			if got, want := fs.paths("REPORT"), []string{"/calendars/user/personal/"}; !reflect.DeepEqual(got, want) {
				t.Errorf("REPORTs = %v, want exactly %v", got, want)
			}
		})
	}
}

func TestCreateEvent_writesIntoTheChosenCalendar(t *testing.T) {
	c, fs := connectMultiCal(t)
	team := fs.url("/calendars/user/team/")
	selectCalendars(t, c,
		sel(fs.url("/calendars/user/personal/"), true, false),
		sel(team, true, true),
	)
	fs.reset()

	start := time.Date(2026, 6, 26, 15, 0, 0, 0, time.UTC)
	eventID, _, calID, err := c.CreateEvent(context.Background(), "u1", calendar.CreateEventParams{
		Summary: "Intro call", Start: start, End: start.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	puts := fs.paths(http.MethodPut)
	if len(puts) != 1 || !strings.HasPrefix(puts[0], "/calendars/user/team/") {
		t.Fatalf("PUTs = %v, want one into /calendars/user/team/", puts)
	}
	if eventID != fs.url(puts[0]) {
		t.Errorf("eventID = %q, want the resource URL written %q", eventID, fs.url(puts[0]))
	}
	if calID != team {
		t.Errorf("calendarID = %q, want %q recorded against the booking", calID, team)
	}

	// The destination moves back to Personal. The booking's event still lives in Team, and
	// moving or cancelling it must act on the stored resource URL.
	selectCalendars(t, c,
		sel(fs.url("/calendars/user/personal/"), true, true),
		sel(team, true, false),
	)
	fs.reset()
	if err := c.UpdateEvent(context.Background(), "u1", calID, eventID, start.Add(time.Hour), start.Add(2*time.Hour)); err != nil {
		t.Fatalf("UpdateEvent: %v", err)
	}
	if err := c.CancelEvent(context.Background(), "u1", calID, eventID); err != nil {
		t.Fatalf("CancelEvent: %v", err)
	}
	for _, m := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		if got := fs.paths(m); !reflect.DeepEqual(got, puts) {
			t.Errorf("%s paths = %v, want %v (the stored event URL)", m, got, puts)
		}
	}
}

// A calendar id is a URL that later receives the account's credentials, so a selection may only
// name calendars the server listed. Nothing is saved when any id is refused, and validating never
// contacts the refused URL.
func TestSetAccountCalendars_refusesACalendarTheServerDidNotList(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   func(fs *multiCalServer) string
	}{
		{"VTODO-only list", func(fs *multiCalServer) string { return fs.url("/calendars/user/tasks/") }},
		{"collection on another origin", func(fs *multiCalServer) string { return fs.other.URL + "/calendars/user/elsewhere/" }},
		{"URL the server never listed", func(fs *multiCalServer) string { return fs.url("/calendars/someone-else/private/") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, fs := connectMultiCal(t)
			svc := calendar.NewService(c.db)
			svc.Register(c)
			id := tc.id(fs)

			err := svc.SetAccountCalendars(context.Background(), "u1", "caldav", multiCalAccount,
				[]calendar.CalendarSelection{sel(fs.url("/calendars/user/personal/"), true, false), sel(id, true, true)})
			var unknown *calendar.UnknownCalendarError
			if !errors.As(err, &unknown) || unknown.CalendarID != id {
				t.Fatalf("err = %v, want *UnknownCalendarError for %q", err, id)
			}
			var n int
			if err := c.db.QueryRow(`SELECT COUNT(*) FROM connection_calendars WHERE user_id = 'u1'`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Errorf("%d selection row(s) saved despite the refusal", n)
			}
			if n := fs.hitsElsewhere(); n != 0 {
				t.Errorf("%d request(s) reached the other origin while validating", n)
			}
		})
	}
}

// A self-hosted server behind a TLS-terminating reverse proxy can report its internal scheme or
// host in every href. Those calendars are skipped for origin, and connect must say that is why it
// found nothing, rather than "no calendar found", which points the operator at the wrong thing.
// A server that genuinely has no event calendar keeps the old message.
func TestConnect_explainsCalendarsListedOnlyAtAnotherAddress(t *testing.T) {
	for _, tc := range []struct {
		name     string
		hrefBase func(r *http.Request) string // prefix for each calendar href
		comps    string
		want     func(host string) string
	}{
		{
			name:     "every event calendar on another origin",
			hrefBase: func(r *http.Request) string { return "https://" + r.Host },
			comps:    vevent,
			want: func(host string) string {
				return "the server lists its calendars at a different address (https://" + host +
					") than the one Calnode reached it at (http://" + host + "); check the server's reverse proxy or base URL settings"
			},
		},
		{
			name:     "no event calendar at all",
			hrefBase: func(*http.Request) string { return "" },
			comps:    vtodo,
			want:     func(string) string { return "no writable calendar found on the server" },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t)
			seedUser(t, c.db, "u1")
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				w.WriteHeader(http.StatusMultiStatus)
				switch {
				case strings.Contains(string(body), "current-user-principal"):
					io.WriteString(w, msOpen+okResponse(r.URL.Path, `<d:current-user-principal><d:href>/principals/user/</d:href></d:current-user-principal>`)+`</d:multistatus>`)
				case strings.Contains(string(body), "calendar-home-set"):
					io.WriteString(w, msOpen+okResponse(r.URL.Path, `<c:calendar-home-set><d:href>/calendars/user/</d:href></c:calendar-home-set>`)+`</d:multistatus>`)
				default:
					base := tc.hrefBase(r)
					io.WriteString(w, msOpen+
						okResponse("/calendars/user/", `<d:resourcetype><d:collection/></d:resourcetype>`)+
						okResponse(base+"/calendars/user/personal/", calendarProps("Personal", tc.comps, ""))+
						okResponse(base+"/calendars/user/work/", calendarProps("Work", tc.comps, ""))+
						// A same-origin tasks list must not stop the origin explanation.
						okResponse("/calendars/user/tasks/", calendarProps("Tasks", vtodo, ""))+
						`</d:multistatus>`)
				}
			}))
			defer srv.Close()

			_, _, err := c.Connect(context.Background(), "u1", srv.URL, multiCalAccount, "app-pw")
			if err == nil {
				t.Fatal("Connect succeeded, want an error")
			}
			if want := tc.want(strings.TrimPrefix(srv.URL, "http://")); !strings.Contains(err.Error(), want) {
				t.Errorf("err = %q\nwant it to contain %q", err, want)
			}
			if n := countConns(t, c.db, "u1"); n != 0 {
				t.Errorf("%d connection(s) saved by a failed connect", n)
			}
		})
	}
}

func TestSameOrigin(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"https://dav.example.com/cal/", "https://dav.example.com:443/other/", true},
		{"http://dav.example.com/cal/", "http://DAV.example.com:80/", true},
		{"https://dav.example.com/cal/", "http://dav.example.com/cal/", false},
		{"https://dav.example.com/cal/", "https://dav.example.com:8443/cal/", false},
		{"https://dav.example.com/cal/", "https://evil.example.com/cal/", false},
		{"/relative/", "/relative/", false},
	} {
		if got := sameOrigin(tc.a, tc.b); got != tc.want {
			t.Errorf("sameOrigin(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
