package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/db"
	"github.com/calnode/calnode/internal/mailer"
	"github.com/calnode/calnode/internal/slots"
)

// inviteProvider is a Google-like destination (it emails guests itself) that records the
// events Calnode asks it to create, so a test can see who would have been invited.
type inviteProvider struct {
	mu      sync.Mutex
	created []calendar.CreateEventParams
}

func (p *inviteProvider) Name() string                                           { return "google" }
func (p *inviteProvider) InvitesGuests() bool                                    { return true }
func (p *inviteProvider) AuthURL(string) string                                  { return "" }
func (p *inviteProvider) EncryptState(string) (string, error)                    { return "", nil }
func (p *inviteProvider) DecryptState(string) (string, error)                    { return "", nil }
func (p *inviteProvider) Exchange(context.Context, string, string, string) error { return nil }
func (p *inviteProvider) Connected(context.Context, string) (bool, error)        { return true, nil }
func (p *inviteProvider) Disconnect(context.Context, string) error               { return nil }
func (p *inviteProvider) HasDestination(context.Context, string) (bool, error)   { return true, nil }
func (p *inviteProvider) ListCalendars(context.Context, string, string) ([]calendar.CalendarInfo, error) {
	return nil, nil
}
func (p *inviteProvider) FreeBusy(context.Context, string, time.Time, time.Time) ([]slots.Interval, error) {
	return nil, nil
}
func (p *inviteProvider) CreateEvent(_ context.Context, _ string, params calendar.CreateEventParams) (string, string, string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.created = append(p.created, params)
	return fmt.Sprintf("evt-%d", len(p.created)), "", "primary", nil
}
func (p *inviteProvider) UpdateEvent(context.Context, string, string, string, time.Time, time.Time) error {
	return nil
}
func (p *inviteProvider) CancelEvent(context.Context, string, string, string) error { return nil }

func (p *inviteProvider) events() []calendar.CreateEventParams {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]calendar.CreateEventParams(nil), p.created...)
}

type inviteFixture struct {
	h      *Handler
	mail   *captureMailer
	cal    *inviteProvider
	apiKey string
}

const (
	inviteHostEmail   = "jeroen@host.example"
	inviteBookerEmail = "guest@booker.example"
	inviteSenderEmail = "bookings@team.example"
)

// newInviteFixture boots a workspace whose host has a Google-like destination calendar.
// withSender configures Settings → Email, which Calnode-sent invites require.
func newInviteFixture(t *testing.T, withSender bool) *inviteFixture {
	t.Helper()
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}

	h := New(database, slog.Default())
	f := &inviteFixture{h: h, mail: &captureMailer{}, cal: &inviteProvider{}}
	h.SetMailer(f.mail, "https://book.team.example")
	svc := calendar.NewService(database)
	svc.Register(f.cal)
	h.SetCalendar(svc)

	rec := httptest.NewRecorder()
	h.Setup(rec, httptest.NewRequest(http.MethodPost, "/v1/setup",
		strings.NewReader(`{"name":"Jeroen Host","email":"`+inviteHostEmail+`","timezone":"UTC"}`)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup: %d — %s", rec.Code, rec.Body.String())
	}
	var setup struct {
		APIKey string `json:"api_key"`
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &setup); err != nil {
		t.Fatalf("decode setup: %v", err)
	}
	f.apiKey = setup.APIKey

	if _, err := database.Exec(`
		INSERT INTO calendar_connections
		  (id, user_id, provider, access_token_enc, calendar_id, check_conflicts, is_destination, created_at)
		VALUES ('conn-1', ?, 'google', 'e', 'primary', 1, 1, '2026-01-01T00:00:00Z')`, setup.UserID); err != nil {
		t.Fatalf("seed calendar connection: %v", err)
	}
	if withSender {
		if _, err := database.Exec(`UPDATE server_settings
			SET smtp_host = 'smtp.team.example', email_from = ?, email_from_name = 'Palamond Team'
			WHERE id = 1`, inviteSenderEmail); err != nil {
			t.Fatalf("seed email settings: %v", err)
		}
	}
	for day := 0; day < 7; day++ {
		rec := f.do(t, h.CreateAvailabilityRule, http.MethodPost, "/v1/availability-rules",
			fmt.Sprintf(`{"day_of_week":%d,"start_time":"00:00","end_time":"23:59"}`, day))
		if rec.Code != http.StatusCreated {
			t.Fatalf("seed availability: %d — %s", rec.Code, rec.Body.String())
		}
	}
	return f
}

func (f *inviteFixture) do(t *testing.T, fn http.HandlerFunc, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", f.apiKey)
	if rest, ok := strings.CutPrefix(path, "/v1/event-types/"); ok {
		req.SetPathValue("slug", rest)
	}
	rec := httptest.NewRecorder()
	f.h.RequireAuth(fn)(rec, req)
	return rec
}

func (f *inviteFixture) createEventType(t *testing.T, slug, delivery string) *httptest.ResponseRecorder {
	t.Helper()
	return f.do(t, f.h.CreateEventType, http.MethodPost, "/v1/event-types", fmt.Sprintf(
		`{"slug":%q,"name":"Intro","duration_minutes":30,"location_type":"phone","location_value":"+31 20 000 0000",`+
			`"max_active_bookings":0,"max_future_days":0,"invite_delivery":%q}`, slug, delivery))
}

// book creates a booking through the public endpoint and waits for the booker's
// confirmation, which the handler sends from a background goroutine.
func (f *inviteFixture) book(t *testing.T, slug string) (string, *mailer.Message) {
	t.Helper()
	start := time.Now().UTC().Add(72 * time.Hour).Truncate(24 * time.Hour).Add(10 * time.Hour)
	req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(fmt.Sprintf(
		`{"event_type_slug":%q,"start_at":%q,"name":"Guest","email":%q,"timezone":"UTC"}`,
		slug, start.Format(time.RFC3339), inviteBookerEmail)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.h.CreateBooking(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create booking: %d — %s", rec.Code, rec.Body.String())
	}
	var b struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("decode booking: %v", err)
	}
	return b.ID, f.waitFor(t, inviteBookerEmail, 1)
}

// waitFor returns the n-th message sent to addr, polling while background sends land.
func (f *inviteFixture) waitFor(t *testing.T, addr string, n int) *mailer.Message {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		f.mail.mu.Lock()
		var seen []mailer.Message
		for _, m := range f.mail.msgs {
			if len(m.To) > 0 && m.To[0] == addr {
				seen = append(seen, m)
			}
		}
		f.mail.mu.Unlock()
		if len(seen) >= n {
			return &seen[n-1]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("message %d to %s never arrived; sent to: %v", n, addr, f.mail.recipients())
	return nil
}

func icsOf(m *mailer.Message) string {
	for _, a := range m.Attachments {
		if strings.HasPrefix(a.ContentType, "text/calendar") {
			// Unfold RFC 5545 continuation lines so assertions see whole properties.
			return strings.ReplaceAll(string(a.Content), "\r\n ", "")
		}
	}
	return ""
}

// The point of Calnode-sent invites: nothing the booker receives, and nothing the host's
// calendar provider is asked to send, carries the host's own address.
func TestInviteDelivery_calnodeKeepsHostAddressFromBooker(t *testing.T) {
	f := newInviteFixture(t, true)
	if rec := f.createEventType(t, "intro", booking.InviteByCalnode); rec.Code != http.StatusCreated {
		t.Fatalf("create event type: %d — %s", rec.Code, rec.Body.String())
	}
	_, msg := f.book(t, "intro")

	events := f.cal.events()
	if len(events) != 1 {
		t.Fatalf("host calendar events: got %d; want 1 (the host still needs the meeting on their calendar)", len(events))
	}
	if events[0].OrganizerEmail != "" {
		t.Errorf("host event invites %q; want no guest, so the provider emails nobody from the host's account",
			events[0].OrganizerEmail)
	}

	ics := icsOf(msg)
	if ics == "" {
		t.Fatal("booker confirmation has no .ics: with the provider silenced, it is the only invite")
	}
	if !strings.Contains(ics, `ORGANIZER;CN="Palamond Team":mailto:`+inviteSenderEmail) {
		t.Errorf("invite ORGANIZER is not the workspace sender:\n%s", ics)
	}
	if !strings.Contains(ics, "ATTENDEE") || !strings.Contains(ics, inviteBookerEmail) {
		t.Errorf("invite does not list the booker as attendee:\n%s", ics)
	}
	for part, body := range map[string]string{"ics": ics, "text": msg.Text, "html": msg.HTML, "subject": msg.Subject} {
		if strings.Contains(body, inviteHostEmail) {
			t.Errorf("booker's %s contains the host's address %s", part, inviteHostEmail)
		}
	}

	// The host's own notification still names the host as organizer: it is their meeting.
	hostMsg := f.waitFor(t, inviteHostEmail, 1)
	if hostICS := icsOf(hostMsg); strings.Contains(hostICS, inviteSenderEmail) {
		t.Errorf("host notification's invite is organized by the workspace sender:\n%s", hostICS)
	}
}

// The default stays exactly as before: the host's calendar invites the booker, and
// Calnode adds no second invite on top.
func TestInviteDelivery_calendarInvitesThroughHostCalendar(t *testing.T) {
	f := newInviteFixture(t, true)
	if rec := f.createEventType(t, "intro", booking.InviteByCalendar); rec.Code != http.StatusCreated {
		t.Fatalf("create event type: %d — %s", rec.Code, rec.Body.String())
	}
	_, msg := f.book(t, "intro")

	events := f.cal.events()
	if len(events) != 1 || events[0].OrganizerEmail != inviteBookerEmail {
		t.Fatalf("host calendar event guests = %+v; want the booker invited by the host's calendar", events)
	}
	if ics := icsOf(msg); ics != "" {
		t.Errorf("booker got Calnode's .ics as well as the calendar's own invite (a duplicate):\n%s", ics)
	}
}

// A booking keeps the channel its invite went out on. Switching the event type back to
// calendar-sent must still cancel a Calnode-invited booking with Calnode's .ics, since the
// host's calendar never invited that booker and has no cancellation to send.
func TestInviteDelivery_cancelFollowsTheBookingNotTheEventType(t *testing.T) {
	f := newInviteFixture(t, true)
	if rec := f.createEventType(t, "intro", booking.InviteByCalnode); rec.Code != http.StatusCreated {
		t.Fatalf("create event type: %d — %s", rec.Code, rec.Body.String())
	}
	id, _ := f.book(t, "intro")

	if rec := f.do(t, f.h.PatchEventType, http.MethodPatch, "/v1/event-types/intro",
		`{"invite_delivery":"calendar"}`); rec.Code != http.StatusOK {
		t.Fatalf("switch back to calendar: %d — %s", rec.Code, rec.Body.String())
	}

	ctx := context.Background()
	if err := f.h.bookingSvc.CancelByID(ctx, id, "changed plans"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	b, err := f.h.bookingSvc.Get(ctx, id)
	if err != nil {
		t.Fatalf("get booking: %v", err)
	}
	f.h.cancelSideEffects(*b)

	ics := icsOf(f.waitFor(t, inviteBookerEmail, 2))
	if !strings.Contains(ics, "METHOD:CANCEL") {
		t.Fatalf("cancellation carries no .ics CANCEL; the booker's calendar keeps a dead meeting:\n%s", ics)
	}
	if strings.Contains(ics, inviteHostEmail) || !strings.Contains(ics, inviteSenderEmail) {
		t.Errorf("cancellation is not organized by the workspace sender:\n%s", ics)
	}
}

// Calnode-sent invites ARE email, so turning them on without a sender would silently
// leave bookers with no invite at all.
func TestInviteDelivery_requiresEmailSender(t *testing.T) {
	f := newInviteFixture(t, false)
	rec := f.createEventType(t, "intro", booking.InviteByCalnode)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Settings → Email") {
		t.Errorf("create with calnode invites and no email: %d — %s; want 400 pointing at Settings → Email",
			rec.Code, rec.Body.String())
	}

	if rec := f.createEventType(t, "intro", booking.InviteByCalendar); rec.Code != http.StatusCreated {
		t.Fatalf("create event type: %d — %s", rec.Code, rec.Body.String())
	}
	rec = f.do(t, f.h.PatchEventType, http.MethodPatch, "/v1/event-types/intro", `{"invite_delivery":"calnode"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("switch to calnode invites with no email: %d — %s; want 400", rec.Code, rec.Body.String())
	}
	rec = f.do(t, f.h.PatchEventType, http.MethodPatch, "/v1/event-types/intro", `{"invite_delivery":"carrier-pigeon"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown invite_delivery: %d; want 400", rec.Code)
	}
}

// assertWorkspaceInvite fails unless ics is a Calnode-organized invite of the given method
// that carries none of the listed host addresses.
func assertWorkspaceInvite(t *testing.T, what, ics, method string, hostAddrs ...string) {
	t.Helper()
	if !strings.Contains(ics, "METHOD:"+method) {
		t.Fatalf("%s: no %s invite; with the host's calendar silenced it is the booker's only update:\n%s", what, method, ics)
	}
	if !strings.Contains(ics, "mailto:"+inviteSenderEmail) {
		t.Errorf("%s: invite not organized by the workspace sender:\n%s", what, ics)
	}
	for _, a := range hostAddrs {
		if strings.Contains(ics, a) {
			t.Errorf("%s: invite carries host address %s:\n%s", what, a, ics)
		}
	}
}

func (f *inviteFixture) calnodeBooking(t *testing.T) (*booking.Booking, string) {
	t.Helper()
	if rec := f.createEventType(t, "intro", booking.InviteByCalnode); rec.Code != http.StatusCreated {
		t.Fatalf("create event type: %d — %s", rec.Code, rec.Body.String())
	}
	id, _ := f.book(t, "intro")
	b, err := f.h.bookingSvc.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("get booking: %v", err)
	}
	if b.InviteDelivery != booking.InviteByCalnode {
		t.Fatalf("booking invite_delivery = %q; want calnode copied from the event type", b.InviteDelivery)
	}
	return b, b.EventTypeID
}

// A reschedule must reach the booker as Calnode's updated invite, since the host's
// calendar event (moved silently) never listed them.
func TestInviteDelivery_rescheduleSendsWorkspaceInvite(t *testing.T) {
	f := newInviteFixture(t, true)
	b, etID := f.calnodeBooking(t)

	newStart := b.StartAt.Add(24 * time.Hour)
	updated, err := f.h.bookingSvc.Reschedule(context.Background(), b.ID, newStart, newStart.Add(30*time.Minute))
	if err != nil {
		t.Fatalf("reschedule: %v", err)
	}
	f.h.rescheduleSideEffects(*updated, etID, b.StartAt, b.EndAt)

	ics := icsOf(f.waitFor(t, inviteBookerEmail, 2))
	assertWorkspaceInvite(t, "reschedule", ics, "REQUEST", inviteHostEmail)
	if !strings.Contains(ics, "DTSTART:"+newStart.UTC().Format("20060102T150405Z")) {
		t.Errorf("reschedule invite does not carry the new time:\n%s", ics)
	}
}

// Reassigning moves the event to the new host's calendar without inviting the booker
// there, and re-issues Calnode's invite: neither host's address reaches the booker.
func TestInviteDelivery_reassignKeepsBothHostsPrivate(t *testing.T) {
	f := newInviteFixture(t, true)
	b, _ := f.calnodeBooking(t)

	const newHostEmail = "colleague@host.example"
	if _, err := f.h.db.Exec(`INSERT INTO users (id, email, name, iana_timezone, is_admin, created_at)
		VALUES ('host-2', ?, 'Colleague', 'UTC', 0, '2026-01-01T00:00:00Z')`, newHostEmail); err != nil {
		t.Fatalf("seed second host: %v", err)
	}
	if _, err := f.h.db.Exec(`INSERT INTO calendar_connections
		  (id, user_id, provider, access_token_enc, calendar_id, check_conflicts, is_destination, created_at)
		VALUES ('conn-2', 'host-2', 'google', 'e', 'primary', 1, 1, '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed second host calendar: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/bookings/"+b.ID+"/reassign", strings.NewReader(`{"host_id":"host-2"}`))
	req.SetPathValue("id", b.ID)
	req.Header.Set("X-API-Key", f.apiKey)
	rec := httptest.NewRecorder()
	f.h.RequireAuth(f.h.ReassignBooking)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("reassign: %d — %s", rec.Code, rec.Body.String())
	}

	assertWorkspaceInvite(t, "reassign", icsOf(f.waitFor(t, inviteBookerEmail, 2)), "REQUEST",
		inviteHostEmail, newHostEmail)
	events := f.cal.events()
	if len(events) != 2 || events[1].OrganizerEmail != "" {
		t.Errorf("new host's calendar events = %+v; want the moved event created without the booker", events)
	}
}

// The reconciler re-creates a host event whose inline create failed; for a Calnode
// booking it must not add the booker, or the provider would invite them after all.
func TestInviteDelivery_reconcilerHealsWithoutInvitingBooker(t *testing.T) {
	f := newInviteFixture(t, true)
	b, _ := f.calnodeBooking(t)

	if _, err := f.h.db.Exec(`UPDATE booking_hosts SET external_event_id = NULL WHERE booking_id = ?`, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.db.Exec(`UPDATE bookings SET created_at = ? WHERE id = ?`,
		time.Now().UTC().Add(-time.Hour).Format(time.RFC3339), b.ID); err != nil {
		t.Fatal(err)
	}
	f.h.reconcileCreations(context.Background(), f.h.getCal())

	events := f.cal.events()
	if len(events) != 2 {
		t.Fatalf("host calendar events = %d; want the missing one re-created", len(events))
	}
	if events[1].OrganizerEmail != "" {
		t.Errorf("healed event invites %q; want no guest", events[1].OrganizerEmail)
	}
}
