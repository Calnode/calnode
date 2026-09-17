package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/db"
	"github.com/calnode/calnode/internal/mailer"
)

// Booking emails must name and notify the host a booking was assigned to
// (bookings.host_id), not the owner of its event type (event_types.user_id). On a
// round-robin or hosts-tab event type those are different people, and the owner
// may not be attending at all. Issue #48 reported the reminder; the same owner
// join fed the cancellation, reschedule and reassign emails through
// loadCancellationData.
//
// Every test here uses one shape: the event type is owned by the admin who ran
// setup (Test Host), and the booking is assigned to a second member (Hana Host).

const (
	etOwnerEmail     = "host@example.com" // the setup admin, who owns the event type
	etOwnerName      = "Test Host"
	bookingHostEmail = "hana@example.com"
	bookingHostName  = "Hana Host"
	bookerEmail      = "dana@example.com"
)

type hostMailFixture struct {
	h      *Handler
	cap    *captureMailer
	apiKey string
	b      *booking.Booking
}

func newHostMailFixture(t *testing.T) hostMailFixture {
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
	cap := &captureMailer{}
	h.SetMailer(cap, "https://calnode.example.com")

	rec := httptest.NewRecorder()
	h.Setup(rec, httptest.NewRequest(http.MethodPost, "/v1/setup",
		strings.NewReader(`{"name":"`+etOwnerName+`","email":"`+etOwnerEmail+`","timezone":"UTC"}`)))
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

	start := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Hour)
	end := start.Add(30 * time.Minute)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO users (id, email, name, iana_timezone, is_admin) VALUES ('u-hana', ?, ?, 'UTC', 0)`,
			[]any{bookingHostEmail, bookingHostName}},
		{`INSERT INTO users (id, email, name, iana_timezone, is_admin) VALUES ('u-nico', 'nico@example.com', 'Nico New', 'UTC', 0)`, nil},
		{`INSERT INTO event_types (id, user_id, slug, name, duration_minutes) VALUES ('et-team', ?, 'team-intro', 'Team Intro', 30)`,
			[]any{setup.UserID}},
		{`INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at, status) VALUES ('b-1', 'et-team', 'u-hana', ?, ?, 'confirmed')`,
			[]any{start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano)}},
		{`INSERT INTO booking_hosts (id, booking_id, user_id, is_primary) VALUES ('bh-1', 'b-1', 'u-hana', 1)`, nil},
		{`INSERT INTO booking_attendees (id, booking_id, name, email, iana_timezone, is_organizer) VALUES ('a-1', 'b-1', 'Dana', ?, 'UTC', 1)`,
			[]any{bookerEmail}},
	} {
		if _, err := database.Exec(q.sql, q.args...); err != nil {
			t.Fatalf("seed: %v\n%s", err, q.sql)
		}
	}

	b, err := h.bookingSvc.Get(context.Background(), "b-1")
	if err != nil {
		t.Fatalf("load booking: %v", err)
	}
	return hostMailFixture{h: h, cap: cap, apiKey: setup.APIKey, b: b}
}

// assertNamesOnly fails unless every body of msg names want and none names the
// event type's owner.
func assertNamesOnly(t *testing.T, label string, msg *mailer.Message, want string) {
	t.Helper()
	for part, body := range map[string]string{"text": msg.Text, "html": msg.HTML} {
		if !strings.Contains(body, want) {
			t.Errorf("%s (%s) does not name %q:\n%s", label, part, want, body)
		}
		if strings.Contains(body, etOwnerName) {
			t.Errorf("%s (%s) names the event type's owner %q, who is not this booking's host:\n%s",
				label, part, etOwnerName, body)
		}
	}
}

func TestLoadCancellationData_namesTheBookingsHostNotTheEventTypeOwner(t *testing.T) {
	f := newHostMailFixture(t)

	d, err := f.h.loadCancellationData(context.Background(), f.b)
	if err != nil {
		t.Fatalf("loadCancellationData: %v", err)
	}
	if d.HostName != bookingHostName || d.HostEmail != bookingHostEmail {
		t.Errorf("host = %q <%s>; want the booking's host %q <%s>, not the event type's owner",
			d.HostName, d.HostEmail, bookingHostName, bookingHostEmail)
	}
	if d.EventTypeName != "Team Intro" || d.EventTypeSlug != "team-intro" {
		t.Errorf("event type = %q/%q; want Team Intro/team-intro", d.EventTypeName, d.EventTypeSlug)
	}
}

// The reschedule notice went to the event type's owner, and the host the booking
// was assigned to heard nothing, while the preference consulted was the assigned
// host's. This is the path where the wrong join changed who received mail.
func TestRescheduleSideEffects_notifiesTheBookingsHostNotTheEventTypeOwner(t *testing.T) {
	f := newHostMailFixture(t)

	f.h.rescheduleSideEffects(*f.b, f.b.EventTypeID, f.b.StartAt.Add(-24*time.Hour), f.b.EndAt.Add(-24*time.Hour))

	if msg := f.cap.find(etOwnerEmail); msg != nil {
		t.Errorf("the event type's owner was sent %q; they are not this booking's host", msg.Subject)
	}
	host := f.cap.find(bookingHostEmail)
	if host == nil {
		t.Fatalf("the booking's host was not notified of the reschedule; sent to: %v", f.cap.recipients())
	}
	assertNamesOnly(t, "host reschedule notice", host, bookingHostName)

	attendee := f.cap.find(bookerEmail)
	if attendee == nil {
		t.Fatalf("no reschedule email to the attendee; sent to: %v", f.cap.recipients())
	}
	assertNamesOnly(t, "attendee reschedule email", attendee, bookingHostName)
	// The invite's ORGANIZER is the host too, or the attendee's calendar shows the
	// owner as the person running the meeting.
	if len(attendee.Attachments) == 0 {
		t.Fatal("attendee reschedule email carries no .ics (no calendar is connected in this test, so it should)")
	}
	if ics := string(attendee.Attachments[0].Content); !strings.Contains(ics, "mailto:"+bookingHostEmail) ||
		strings.Contains(ics, etOwnerEmail) {
		t.Errorf("attendee .ics ORGANIZER is not the booking's host:\n%s", ics)
	}
}

// Cancellation already fanned out over booking_hosts and overwrote the owner's
// details per host, so this held before the fix. It is pinned here so that the
// two paths sharing loadCancellationData stay in step.
func TestCancelSideEffects_notifiesTheBookingsHostNotTheEventTypeOwner(t *testing.T) {
	f := newHostMailFixture(t)
	ctx := context.Background()
	if err := f.h.bookingSvc.CancelByID(ctx, f.b.ID, "conflict"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	b, err := f.h.bookingSvc.Get(ctx, f.b.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	f.h.cancelSideEffects(*b)

	if msg := f.cap.find(etOwnerEmail); msg != nil {
		t.Errorf("the event type's owner was sent %q; they are not this booking's host", msg.Subject)
	}
	host := f.cap.find(bookingHostEmail)
	if host == nil {
		t.Fatalf("the booking's host was not notified of the cancellation; sent to: %v", f.cap.recipients())
	}
	assertNamesOnly(t, "host cancellation notice", host, bookingHostName)
	attendee := f.cap.find(bookerEmail)
	if attendee == nil {
		t.Fatalf("no cancellation email to the attendee; sent to: %v", f.cap.recipients())
	}
	assertNamesOnly(t, "attendee cancellation email", attendee, bookingHostName)
}

// Reassign emails the attendee and the NEW host. They must name the new host: not
// the old one, and not the event type's owner. ReassignBooking sends them from its
// own goroutine, so this polls by recipient.
func TestReassignBooking_emailsNameTheNewHost(t *testing.T) {
	f := newHostMailFixture(t)

	req := httptest.NewRequest(http.MethodPost, "/v1/bookings/b-1/reassign", strings.NewReader(`{"host_id":"u-nico"}`))
	req.Header.Set("X-API-Key", f.apiKey)
	req.SetPathValue("id", "b-1")
	rec := httptest.NewRecorder()
	f.h.RequireAuth(f.h.ReassignBooking)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("reassign: %d — %s", rec.Code, rec.Body.String())
	}

	// The attendee is emailed before the new host, so once the host's email has
	// arrived both sends have happened.
	var newHost *mailer.Message
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if newHost = f.cap.find("nico@example.com"); newHost != nil {
			break
		}
	}
	if newHost == nil {
		t.Fatalf("the new host was not notified; sent to: %v", f.cap.recipients())
	}
	assertNamesOnly(t, "new host notice", newHost, "Nico New")

	attendee := f.cap.find(bookerEmail)
	if attendee == nil {
		t.Fatalf("no email to the attendee; sent to: %v", f.cap.recipients())
	}
	assertNamesOnly(t, "attendee reassign email", attendee, "Nico New")
	if strings.Contains(attendee.Text, bookingHostName) {
		t.Errorf("attendee reassign email still names the previous host %q:\n%s", bookingHostName, attendee.Text)
	}
	for _, to := range []string{etOwnerEmail, bookingHostEmail} {
		if msg := f.cap.find(to); msg != nil {
			t.Errorf("%s was sent %q; only the attendee and the new host should be emailed", to, msg.Subject)
		}
	}
}
