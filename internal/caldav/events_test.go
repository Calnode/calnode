package caldav

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/slots"
)

// davReq is one request a davServer received, with the credentials it carried.
type davReq struct {
	Method, Path, User, Pass, IfMatch string
}

// davServer is a CalDAV server that holds the events of exactly one account. It records every
// request together with the Basic credentials it carried, so a test can assert which account's
// password reached which server, and it refuses any other credentials the way a real server does.
type davServer struct {
	*httptest.Server
	user, pass string

	mu  sync.Mutex
	got []davReq
}

func newDAVServer(t *testing.T, user, pass string) *davServer {
	t.Helper()
	s := &davServer{user: user, pass: pass}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, _ := r.BasicAuth()
		s.mu.Lock()
		s.got = append(s.got, davReq{Method: r.Method, Path: r.URL.Path, User: u, Pass: p, IfMatch: r.Header.Get("If-Match")})
		s.mu.Unlock()
		if u != s.user || p != s.pass {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("ETag", `"v1"`)
			io.WriteString(w, "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:x@calnode\r\n"+
				"DTSTART:20260701T090000Z\r\nDTEND:20260701T100000Z\r\nSEQUENCE:0\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")
		case http.MethodPut:
			w.WriteHeader(http.StatusCreated)
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *davServer) requests() []davReq {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]davReq(nil), s.got...)
}

func (s *davServer) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got = nil
}

// onlyOwnCredentials fails the test if any request to s carried credentials other than the
// account s belongs to.
func (s *davServer) onlyOwnCredentials(t *testing.T, name string) {
	t.Helper()
	for _, r := range s.requests() {
		if r.User != s.user || r.Pass != s.pass {
			t.Errorf("server %s received %s %s as %q with password %q; only %q's credentials belong there",
				name, r.Method, r.Path, r.User, r.Pass, s.user)
		}
	}
}

func (s *davServer) noRequests(t *testing.T, name string) {
	t.Helper()
	if got := s.requests(); len(got) != 0 {
		t.Errorf("server %s received %d request(s), want none: %+v", name, len(got), got)
	}
}

var (
	moveStart = time.Date(2026, 7, 2, 14, 0, 0, 0, time.UTC)
	moveEnd   = moveStart.Add(time.Hour)
)

func bookingParams() calendar.CreateEventParams {
	return calendar.CreateEventParams{
		Summary: "Intro call", Start: time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC),
		End: time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC), OrganizerEmail: "booker@x.test",
	}
}

// createOn writes a booking's event while accountEmail is the destination, exactly as the
// booking flow does, and returns the ids the flow stores on booking_hosts.
func createOn(t *testing.T, svc *calendar.Service, accountEmail string) (eventID, calendarID string) {
	t.Helper()
	ctx := context.Background()
	if err := svc.SetDestination(ctx, "u1", "caldav", accountEmail); err != nil {
		t.Fatalf("set destination %s: %v", accountEmail, err)
	}
	eventID, _, calendarID, _, err := svc.CreateEvent(ctx, "u1", bookingParams())
	if err != nil || eventID == "" {
		t.Fatalf("CreateEvent on %s: id=%q err=%v", accountEmail, eventID, err)
	}
	return eventID, calendarID
}

func newSvc(c *Client) *calendar.Service {
	svc := calendar.NewService(c.db)
	svc.Register(c)
	return svc
}

// A host books on CalDAV account A, then makes account B (another server) the destination.
// Moving or cancelling the old booking must authenticate to A's server as A. Before the fix
// both operations sent B's password to A's server, which refused it.
func TestUpdateCancel_eventOnPreviousAccountUsesThatAccount(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	seedUser(t, c.db, "u1")
	srvA := newDAVServer(t, "a@a.test", "pw-a")
	srvB := newDAVServer(t, "b@b.test", "pw-b")
	if err := c.saveConnection(ctx, "u1", "a@a.test", "pw-a", srvA.URL+"/calendars/a/home/", ""); err != nil {
		t.Fatal(err)
	}
	if err := c.saveConnection(ctx, "u1", "b@b.test", "pw-b", srvB.URL+"/calendars/b/home/", ""); err != nil {
		t.Fatal(err)
	}
	svc := newSvc(c)
	eventID, calendarID := createOn(t, svc, "a@a.test")
	if err := svc.SetDestination(ctx, "u1", "caldav", "b@b.test"); err != nil {
		t.Fatal(err)
	}
	srvA.reset()

	if err := svc.UpdateEvent(ctx, "u1", calendarID, eventID, "", moveStart, moveEnd); err != nil {
		t.Errorf("UpdateEvent: %v", err)
	}
	if err := svc.CancelEvent(ctx, "u1", calendarID, eventID, ""); err != nil {
		t.Errorf("CancelEvent: %v", err)
	}

	srvA.onlyOwnCredentials(t, "A")
	srvB.noRequests(t, "B")
	var methods []string
	for _, r := range srvA.requests() {
		methods = append(methods, r.Method)
	}
	if want := []string{"GET", "PUT", "DELETE"}; !reflect.DeepEqual(methods, want) {
		t.Errorf("server A saw %v, want %v", methods, want)
	}
}

// Reassign cancels with an empty calendar id (it does not load the stored one), and so do
// bookings made before external_calendar_id existed. The owner is then found from the event
// URL alone.
func TestCancel_emptyCalendarIDResolvesOwnerByURL(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	seedUser(t, c.db, "u1")
	srvA := newDAVServer(t, "a@a.test", "pw-a")
	srvB := newDAVServer(t, "b@b.test", "pw-b")
	if err := c.saveConnection(ctx, "u1", "a@a.test", "pw-a", srvA.URL+"/calendars/a/home/", ""); err != nil {
		t.Fatal(err)
	}
	if err := c.saveConnection(ctx, "u1", "b@b.test", "pw-b", srvB.URL+"/calendars/b/home/", ""); err != nil {
		t.Fatal(err)
	}
	svc := newSvc(c)
	eventID, _ := createOn(t, svc, "a@a.test")
	if err := svc.SetDestination(ctx, "u1", "caldav", "b@b.test"); err != nil {
		t.Fatal(err)
	}
	srvA.reset()

	if err := svc.CancelEvent(ctx, "u1", "", eventID, ""); err != nil {
		t.Errorf("CancelEvent with no calendar id: %v", err)
	}
	if err := svc.UpdateEvent(ctx, "u1", "", eventID, "", moveStart, moveEnd); err != nil {
		t.Errorf("UpdateEvent with no calendar id: %v", err)
	}
	srvA.onlyOwnCredentials(t, "A")
	srvB.noRequests(t, "B")
	if n := len(srvA.requests()); n != 3 {
		t.Errorf("server A saw %d requests, want 3 (DELETE, GET, PUT): %+v", n, srvA.requests())
	}
}

// An event written into a calendar picked inside the account (connection_calendars), not the
// account's bound collection, is still found: by its recorded calendar id, and by URL when no
// calendar id was recorded.
func TestUpdate_eventInPickedCalendarResolvesToItsAccount(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	seedUser(t, c.db, "u1")
	srvA := newDAVServer(t, "a@a.test", "pw-a")
	srvB := newDAVServer(t, "b@b.test", "pw-b")
	if err := c.saveConnection(ctx, "u1", "a@a.test", "pw-a", srvA.URL+"/calendars/a/home/", ""); err != nil {
		t.Fatal(err)
	}
	if err := c.saveConnection(ctx, "u1", "b@b.test", "pw-b", srvB.URL+"/calendars/b/home/", ""); err != nil {
		t.Fatal(err)
	}
	svc := newSvc(c)
	work := srvA.URL + "/calendars/a/work/"
	if err := svc.SetAccountCalendars(ctx, "u1", "caldav", "a@a.test", []calendar.CalendarSelection{
		{CalendarInfo: calendar.CalendarInfo{ID: srvA.URL + "/calendars/a/home/"}, CheckConflicts: true},
		{CalendarInfo: calendar.CalendarInfo{ID: work}, CheckConflicts: true, IsDestination: true},
	}); err != nil {
		t.Fatal(err)
	}
	eventID, _, calendarID, _, err := svc.CreateEvent(ctx, "u1", bookingParams())
	if err != nil || calendarID != work || !strings.HasPrefix(eventID, work) {
		t.Fatalf("CreateEvent: id=%q cal=%q err=%v, want an event in %s", eventID, calendarID, err, work)
	}
	if err := svc.SetDestination(ctx, "u1", "caldav", "b@b.test"); err != nil {
		t.Fatal(err)
	}
	srvA.reset()

	for _, calID := range []string{calendarID, ""} {
		if err := svc.UpdateEvent(ctx, "u1", calID, eventID, "", moveStart, moveEnd); err != nil {
			t.Errorf("UpdateEvent(calendarID=%q): %v", calID, err)
		}
	}
	srvA.onlyOwnCredentials(t, "A")
	srvB.noRequests(t, "B")
	if n := len(srvA.requests()); n != 4 {
		t.Errorf("server A saw %d requests, want 4 (two GET+PUT pairs): %+v", n, srvA.requests())
	}
}

// otherProvider stands in for Google (the provider column only admits real provider names) as
// the user's destination. Like the real one, it cannot find an event id it never issued; it
// records every id it is asked about.
type otherProvider struct {
	mu      sync.Mutex
	touched []string
}

func (p *otherProvider) record(eventID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.touched = append(p.touched, eventID)
	return errors.New("other: event not found")
}

func (p *otherProvider) Name() string                                           { return "google" }
func (p *otherProvider) InvitesGuests() bool                                    { return true }
func (p *otherProvider) AuthURL(string) string                                  { return "" }
func (p *otherProvider) EncryptState(string) (string, error)                    { return "", nil }
func (p *otherProvider) DecryptState(string) (string, error)                    { return "", nil }
func (p *otherProvider) Exchange(context.Context, string, string, string) error { return nil }
func (p *otherProvider) Connected(context.Context, string) (bool, error)        { return true, nil }
func (p *otherProvider) Disconnect(context.Context, string) error               { return nil }
func (p *otherProvider) HasDestination(context.Context, string) (bool, error)   { return true, nil }
func (p *otherProvider) ListCalendars(context.Context, string, string) ([]calendar.CalendarInfo, error) {
	return nil, nil
}
func (p *otherProvider) FreeBusy(context.Context, string, time.Time, time.Time) ([]slots.Interval, error) {
	return nil, nil
}
func (p *otherProvider) CreateEvent(context.Context, string, calendar.CreateEventParams) (string, string, string, error) {
	return "other-event-1", "", "primary", nil
}
func (p *otherProvider) UpdateEvent(_ context.Context, _, _, eventID string, _, _ time.Time) error {
	return p.record(eventID)
}
func (p *otherProvider) CancelEvent(_ context.Context, _, _, eventID string) error {
	return p.record(eventID)
}

func seedOtherDestination(t *testing.T, c *Client, svc *calendar.Service) {
	t.Helper()
	ctx := context.Background()
	if _, err := c.db.ExecContext(ctx, `
		INSERT INTO calendar_connections
		    (id, user_id, provider, account_email, access_token_enc, calendar_id,
		     check_conflicts, is_destination, created_at)
		VALUES ('google-1', 'u1', 'google', 'o@o.test', 'enc', 'primary', 1, 0, '2026-01-03T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetDestination(ctx, "u1", "google", "o@o.test"); err != nil {
		t.Fatal(err)
	}
}

// The destination moves from a CalDAV account to another provider. The CalDAV event must
// still be moved and deleted on its own server, as its own account, and the other provider
// must never be handed a CalDAV event URL. Before the fix the Service routed both calls to
// the new destination's provider, so the event on the CalDAV server was never touched.
func TestUpdateCancel_destinationMovedToAnotherProvider(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	seedUser(t, c.db, "u1")
	srvA := newDAVServer(t, "a@a.test", "pw-a")
	if err := c.saveConnection(ctx, "u1", "a@a.test", "pw-a", srvA.URL+"/calendars/a/home/", ""); err != nil {
		t.Fatal(err)
	}
	other := &otherProvider{}
	svc := newSvc(c)
	svc.Register(other)
	eventID, calendarID := createOn(t, svc, "a@a.test")
	seedOtherDestination(t, c, svc)
	srvA.reset()

	if err := svc.UpdateEvent(ctx, "u1", calendarID, eventID, "", moveStart, moveEnd); err != nil {
		t.Errorf("UpdateEvent: %v", err)
	}
	if err := svc.CancelEvent(ctx, "u1", calendarID, eventID, ""); err != nil {
		t.Errorf("CancelEvent: %v", err)
	}
	if len(other.touched) != 0 {
		t.Errorf("the other provider was asked about CalDAV event(s) %v", other.touched)
	}
	srvA.onlyOwnCredentials(t, "A")
	if n := len(srvA.requests()); n != 3 {
		t.Errorf("server A saw %d requests, want 3 (GET, PUT, DELETE): %+v", n, srvA.requests())
	}

	// The other provider's own events still route to it, by destination, as before.
	if err := svc.CancelEvent(ctx, "u1", "primary", "other-event-1", ""); err == nil {
		t.Error("CancelEvent of the other provider's event: want its error, got nil")
	}
	if !reflect.DeepEqual(other.touched, []string{"other-event-1"}) {
		t.Errorf("other provider touched %v, want [other-event-1]", other.touched)
	}
}

// The CalDAV account that held the event is gone. There are no credentials left that belong
// to its server, so nothing is sent anywhere, and nothing is retried against a provider that
// never issued the id.
func TestUpdateCancel_owningAccountDisconnectedSendsNothing(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	seedUser(t, c.db, "u1")
	srvA := newDAVServer(t, "a@a.test", "pw-a")
	if err := c.saveConnection(ctx, "u1", "a@a.test", "pw-a", srvA.URL+"/calendars/a/home/", ""); err != nil {
		t.Fatal(err)
	}
	other := &otherProvider{}
	svc := newSvc(c)
	svc.Register(other)
	eventID, calendarID := createOn(t, svc, "a@a.test")
	seedOtherDestination(t, c, svc)
	if err := svc.DisconnectOne(ctx, "u1", "caldav", "a@a.test"); err != nil {
		t.Fatal(err)
	}
	srvA.reset()

	if err := svc.UpdateEvent(ctx, "u1", calendarID, eventID, "", moveStart, moveEnd); err != nil {
		t.Errorf("UpdateEvent with no CalDAV account left: %v, want nil (no connection to act as)", err)
	}
	if err := svc.CancelEvent(ctx, "u1", calendarID, eventID, ""); err != nil {
		t.Errorf("CancelEvent with no CalDAV account left: %v, want nil (no connection to act as)", err)
	}
	srvA.noRequests(t, "A")
	if len(other.touched) != 0 {
		t.Errorf("the other provider was asked about CalDAV event(s) %v", other.touched)
	}
}

// When the stored ids do not identify exactly one of the user's CalDAV accounts, nothing is
// sent: not to the event's server, and not with the destination's credentials.
func TestUpdateCancel_ambiguousOrUnknownOwnerSendsNothing(t *testing.T) {
	type setup struct {
		c          *Client
		srvA, srvB *davServer
		stranger   *davServer // a server none of the user's connections uses
	}
	newSetup := func(t *testing.T) setup {
		c := newTestClient(t)
		seedUser(t, c.db, "u1")
		s := setup{
			c:        c,
			srvA:     newDAVServer(t, "a@a.test", "pw-a"),
			srvB:     newDAVServer(t, "b@b.test", "pw-b"),
			stranger: newDAVServer(t, "a@a.test", "pw-a"),
		}
		ctx := context.Background()
		if err := c.saveConnection(ctx, "u1", "a@a.test", "pw-a", s.srvA.URL+"/calendars/a/home/", ""); err != nil {
			t.Fatal(err)
		}
		if err := c.saveConnection(ctx, "u1", "b@b.test", "pw-b", s.srvB.URL+"/calendars/b/home", ""); err != nil {
			t.Fatal(err)
		}
		return s
	}

	cases := []struct {
		name  string
		extra func(t *testing.T, s setup) // optional additional state
		ids   func(s setup) (calendarID, eventID string)
	}{
		{
			name: "event on a server no connection uses",
			ids: func(s setup) (string, string) {
				return "", s.stranger.URL + "/calendars/a/home/ev1.ics"
			},
		},
		{
			name: "calendar id names an account but the event is on another server",
			ids: func(s setup) (string, string) {
				return s.srvB.URL + "/calendars/b/home", s.stranger.URL + "/calendars/b/home/ev1.ics"
			},
		},
		{
			name: "event on a connection's server but outside its collection",
			ids: func(s setup) (string, string) {
				return "", s.srvA.URL + "/calendars/someone-else/home/ev1.ics"
			},
		},
		{
			name: "collection path is a prefix only up to a segment boundary",
			ids: func(s setup) (string, string) {
				return "", s.srvB.URL + "/calendars/b/homework/ev1.ics"
			},
		},
		{
			name: "event id is not a URL at all (another provider's id)",
			ids: func(s setup) (string, string) {
				return "primary", "abc123def456"
			},
		},
		{
			name: "two accounts bound to the same collection",
			extra: func(t *testing.T, s setup) {
				if err := s.c.saveConnection(context.Background(), "u1", "delegate@a.test", "pw-d", s.srvA.URL+"/calendars/a/home/", ""); err != nil {
					t.Fatal(err)
				}
			},
			ids: func(s setup) (string, string) {
				return s.srvA.URL + "/calendars/a/home/", s.srvA.URL + "/calendars/a/home/ev1.ics"
			},
		},
		{
			name: "two accounts bound to the same collection, no calendar id",
			extra: func(t *testing.T, s setup) {
				if err := s.c.saveConnection(context.Background(), "u1", "delegate@a.test", "pw-d", s.srvA.URL+"/calendars/a/home/", ""); err != nil {
					t.Fatal(err)
				}
			},
			ids: func(s setup) (string, string) {
				return "", s.srvA.URL + "/calendars/a/home/ev1.ics"
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSetup(t)
			if tc.extra != nil {
				tc.extra(t, s)
			}
			ctx := context.Background()
			svc := newSvc(s.c)
			calendarID, eventID := tc.ids(s)
			// ErrEventUnreachable specifically, not just an error: it is what stops the
			// reconciler retrying a refusal that no later sweep can change.
			if err := svc.UpdateEvent(ctx, "u1", calendarID, eventID, "", moveStart, moveEnd); !errors.Is(err, calendar.ErrEventUnreachable) {
				t.Errorf("UpdateEvent = %v, want calendar.ErrEventUnreachable when the owning account cannot be established", err)
			}
			if err := svc.CancelEvent(ctx, "u1", calendarID, eventID, ""); !errors.Is(err, calendar.ErrEventUnreachable) {
				t.Errorf("CancelEvent = %v, want calendar.ErrEventUnreachable when the owning account cannot be established", err)
			}
			s.srvA.noRequests(t, "A")
			s.srvB.noRequests(t, "B")
			s.stranger.noRequests(t, "stranger")
		})
	}
}

// Once the owning account is established, a failure talking to its server is an ordinary error
// and must not read as ErrEventUnreachable: the server can come back, and the app password can
// be fixed by reconnecting, so the reconciler has to keep retrying these.
func TestUpdateCancel_serverFailureStaysRetryable(t *testing.T) {
	cases := []struct {
		name string
		fail func(t *testing.T, c *Client, s *davServer)
	}{
		{"server refuses the stored password", func(t *testing.T, c *Client, s *davServer) {
			if err := c.saveConnection(context.Background(), "u1", "a@a.test", "revoked", s.URL+"/calendars/a/home/", ""); err != nil {
				t.Fatal(err)
			}
		}},
		{"server is unreachable", func(_ *testing.T, _ *Client, s *davServer) { s.Close() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t)
			ctx := context.Background()
			seedUser(t, c.db, "u1")
			srvA := newDAVServer(t, "a@a.test", "pw-a")
			if err := c.saveConnection(ctx, "u1", "a@a.test", "pw-a", srvA.URL+"/calendars/a/home/", ""); err != nil {
				t.Fatal(err)
			}
			svc := newSvc(c)
			eventID, calendarID := createOn(t, svc, "a@a.test")
			tc.fail(t, c, srvA)

			err := svc.UpdateEvent(ctx, "u1", calendarID, eventID, "", moveStart, moveEnd)
			if err == nil || errors.Is(err, calendar.ErrEventUnreachable) {
				t.Errorf("UpdateEvent = %v, want a retryable error (not ErrEventUnreachable)", err)
			}
			err = svc.CancelEvent(ctx, "u1", calendarID, eventID, "")
			if err == nil || errors.Is(err, calendar.ErrEventUnreachable) {
				t.Errorf("CancelEvent = %v, want a retryable error (not ErrEventUnreachable)", err)
			}
		})
	}
}

// The common case, one CalDAV account that is still the destination, sends exactly the
// requests it always did, whether or not a calendar id was recorded.
func TestUpdateCancel_singleAccountUnchanged(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	seedUser(t, c.db, "u1")
	srvA := newDAVServer(t, "a@a.test", "pw-a")
	if err := c.saveConnection(ctx, "u1", "a@a.test", "pw-a", srvA.URL+"/calendars/a/home/", ""); err != nil {
		t.Fatal(err)
	}
	svc := newSvc(c)
	eventID, calendarID := createOn(t, svc, "a@a.test")
	path := strings.TrimPrefix(eventID, srvA.URL)
	srvA.reset()

	if err := svc.UpdateEvent(ctx, "u1", calendarID, eventID, "", moveStart, moveEnd); err != nil {
		t.Errorf("UpdateEvent: %v", err)
	}
	if err := svc.UpdateEvent(ctx, "u1", "", eventID, "", moveStart, moveEnd); err != nil {
		t.Errorf("UpdateEvent (no calendar id): %v", err)
	}
	if err := svc.CancelEvent(ctx, "u1", calendarID, eventID, ""); err != nil {
		t.Errorf("CancelEvent: %v", err)
	}
	if err := svc.CancelEvent(ctx, "u1", "", eventID, ""); err != nil {
		t.Errorf("CancelEvent (no calendar id): %v", err)
	}
	a := func(method, ifMatch string) davReq {
		return davReq{Method: method, Path: path, User: "a@a.test", Pass: "pw-a", IfMatch: ifMatch}
	}
	want := []davReq{
		a("GET", ""), a("PUT", `"v1"`),
		a("GET", ""), a("PUT", `"v1"`),
		a("DELETE", ""),
		a("DELETE", ""),
	}
	if got := srvA.requests(); !reflect.DeepEqual(got, want) {
		t.Errorf("requests:\n got %+v\nwant %+v", got, want)
	}
}

// pickEventOwner decides which account's credentials an event URL may receive, so the origin and
// path rules are pinned directly, including the ones an httptest server cannot exercise (a scheme
// or default-port change needs a second listener on the same address).
func TestPickEventOwner(t *testing.T) {
	acct := func(collections ...string) eventAccount { return eventAccount{collections: collections} }
	cases := []struct {
		name       string
		accounts   []eventAccount
		calendarID string
		eventID    string
		want       int
		wantErr    error
	}{
		{"inside the bound collection", []eventAccount{acct("https://a.test/cal/a/"), acct("https://b.test/cal/b/")},
			"", "https://a.test/cal/a/e.ics", 0, nil},
		{"inside a saved calendar", []eventAccount{acct("https://a.test/cal/a/"), acct("https://b.test/cal/b/", "https://b.test/cal/b-work/")},
			"https://b.test/cal/b-work/", "https://b.test/cal/b-work/e.ics", 1, nil},
		{"collection stored without a trailing slash", []eventAccount{acct("https://a.test/cal/a")},
			"https://a.test/cal/a/", "https://a.test/cal/a/e.ics", 0, nil},
		{"host case and an explicit default port are the same origin", []eventAccount{acct("https://A.test:443/cal/a/")},
			"", "https://a.TEST/cal/a/e.ics", 0, nil},
		{"http and https are different origins", []eventAccount{acct("https://a.test/cal/a/")},
			"", "http://a.test/cal/a/e.ics", -1, errNoEventOwner},
		{"a non-default port is a different origin", []eventAccount{acct("https://a.test/cal/a/")},
			"", "https://a.test:8443/cal/a/e.ics", -1, errNoEventOwner},
		{"a sibling path is not inside", []eventAccount{acct("https://a.test/cal/a/")},
			"", "https://a.test/cal/ab/e.ics", -1, errNoEventOwner},
		{"the collection itself is not an event inside it", []eventAccount{acct("https://a.test/cal/a/")},
			"", "https://a.test/cal/a/", -1, errNoEventOwner},
		{"the more specific collection wins", []eventAccount{acct("https://a.test/dav/"), acct("https://a.test/dav/b/home/")},
			"", "https://a.test/dav/b/home/e.ics", 1, nil},
		{"one calendar recorded in two accounts is a tie", []eventAccount{acct("https://a.test/dav/b/home/"), acct("https://a.test/dav/b/home/")},
			"https://a.test/dav/b/home/", "https://a.test/dav/b/home/e.ics", -1, errManyEventOwner},
		{"a recorded calendar id outranks a more specific collection", []eventAccount{acct("https://a.test/dav/"), acct("https://a.test/dav/x/")},
			"https://a.test/dav/", "https://a.test/dav/x/e.ics", 0, nil},
		{"equally specific collections on one server tie", []eventAccount{acct("https://a.test/cal/shared/"), acct("https://a.test/cal/shared")},
			"", "https://a.test/cal/shared/e.ics", -1, errManyEventOwner},
		{"a calendar id outside the event's origin does not count", []eventAccount{acct("https://a.test/cal/a/"), acct("https://b.test/cal/a/")},
			"https://b.test/cal/a/", "https://a.test/cal/a/e.ics", 0, nil},
		{"a Google event id", []eventAccount{acct("https://a.test/cal/a/")}, "primary", "7cbh8rpc10lrc0ckih9tafss99", -1, errEventNotURL},
		{"a Microsoft event id", []eventAccount{acct("https://a.test/cal/a/")}, "", "AAMkAGI2TG93AAA=", -1, errEventNotURL},
		{"no accounts", nil, "", "https://a.test/cal/a/e.ics", -1, errNoEventOwner},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := pickEventOwner(tc.accounts, tc.calendarID, tc.eventID)
			if got != tc.want || !errors.Is(err, tc.wantErr) {
				t.Errorf("pickEventOwner = (%d, %v), want (%d, %v)", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestRecognizesEvent(t *testing.T) {
	c := newTestClient(t)
	for id, want := range map[string]bool{
		"https://caldav.icloud.com/123/calendars/home/e.ics":             true,
		"http://nextcloud.lan/remote.php/dav/calendars/u/personal/e.ics": true,
		"7cbh8rpc10lrc0ckih9tafss99":                                     false, // Google
		"AAMkAGI2TG93AAA=":                                               false, // Microsoft
		"":                                                               false,
		"/calendars/home/e.ics":                                          false,
		"mailto:someone@x.test":                                          false,
	} {
		if got := c.RecognizesEvent(id); got != want {
			t.Errorf("RecognizesEvent(%q) = %v, want %v", id, got, want)
		}
	}
}
