package handler

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/db"
	"github.com/calnode/calnode/internal/slots"
)

// stuckProvider claims every event id (as CalDAV does for its URLs) and fails every update and
// cancel with err, counting the calls so a test can tell a retry from a sweep that let go.
type stuckProvider struct {
	err error

	mu      sync.Mutex
	updates int
	cancels int
}

func (p *stuckProvider) calls() (updates, cancels int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.updates, p.cancels
}

func (p *stuckProvider) Name() string                                           { return "caldav" }
func (p *stuckProvider) InvitesGuests() bool                                    { return false }
func (p *stuckProvider) RecognizesEvent(string) bool                            { return true }
func (p *stuckProvider) AuthURL(string) string                                  { return "" }
func (p *stuckProvider) EncryptState(string) (string, error)                    { return "", nil }
func (p *stuckProvider) DecryptState(string) (string, error)                    { return "", nil }
func (p *stuckProvider) Exchange(context.Context, string, string, string) error { return nil }
func (p *stuckProvider) Connected(context.Context, string) (bool, error)        { return true, nil }
func (p *stuckProvider) Disconnect(context.Context, string) error               { return nil }
func (p *stuckProvider) HasDestination(context.Context, string) (bool, error)   { return false, nil }
func (p *stuckProvider) ListCalendars(context.Context, string, string) ([]calendar.CalendarInfo, error) {
	return nil, nil
}
func (p *stuckProvider) FreeBusy(context.Context, string, time.Time, time.Time) ([]slots.Interval, error) {
	return nil, nil
}
func (p *stuckProvider) CreateEvent(context.Context, string, calendar.CreateEventParams) (string, string, string, error) {
	return "", "", "", nil
}
func (p *stuckProvider) UpdateEvent(context.Context, string, string, string, time.Time, time.Time) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.updates++
	return p.err
}
func (p *stuckProvider) CancelEvent(context.Context, string, string, string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cancels++
	return p.err
}

// unreachable stands in for the CalDAV provider's refusal: its own message, matching the
// sentinel the way caldav's errors do, rather than the bare sentinel.
type unreachable struct{}

func (unreachable) Error() string        { return "caldav: more than one account could hold this event" }
func (unreachable) Is(target error) bool { return target == calendar.ErrEventUnreachable }

// newReconcileFixture seeds one host, an event type and a booking in the given status whose
// host row carries an event id, with needs_sync set. It returns the handler (logging into logs),
// the database and the booking id.
func newReconcileFixture(t *testing.T, status string, logs *bytes.Buffer) (*Handler, *sql.DB, string) {
	t.Helper()
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := db.Migrate(database); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	start := time.Now().UTC().Add(24 * time.Hour)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO users (id, email, name, iana_timezone) VALUES ('host-1', 'host@example.com', 'Host', 'UTC')`, nil},
		{`INSERT INTO event_types (id, user_id, slug, name, duration_minutes) VALUES ('et-1', 'host-1', 'intro', 'Intro', 30)`, nil},
		{`INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at, status) VALUES ('bk-1', 'et-1', 'host-1', ?, ?, ?)`,
			[]any{start.Format(time.RFC3339), start.Add(30 * time.Minute).Format(time.RFC3339), status}},
		{`INSERT INTO booking_hosts (id, booking_id, user_id, is_primary, external_event_id, external_calendar_id, needs_sync)
		  VALUES ('bh-1', 'bk-1', 'host-1', 1, 'https://dav.example.com/calendars/host/home/ev1.ics', 'https://dav.example.com/calendars/host/home/', 1)`, nil},
	} {
		if _, err := database.Exec(q.sql, q.args...); err != nil {
			t.Fatalf("seed: %v\n%s", err, q.sql)
		}
	}
	return New(database, slog.New(slog.NewTextHandler(logs, nil))), database, "bk-1"
}

func hostRow(t *testing.T, database *sql.DB) (eventID sql.NullString, needsSync int) {
	t.Helper()
	if err := database.QueryRow(
		`SELECT external_event_id, needs_sync FROM booking_hosts WHERE booking_id = 'bk-1' AND user_id = 'host-1'`,
	).Scan(&eventID, &needsSync); err != nil {
		t.Fatalf("read booking_hosts: %v", err)
	}
	return eventID, needsSync
}

// A reschedule the provider refuses as unreachable is not retried: the first sweep logs one
// warning and clears needs_sync, and the second sweep does not call the provider at all. Any
// other error leaves the flag set, so the next sweep tries again.
func TestReconcileReschedules_unreachableEventIsNotRetried(t *testing.T) {
	cases := []struct {
		name        string
		err         error
		wantUpdates int
		wantSync    int
	}{
		{"unreachable: flag cleared, second sweep sends nothing", fmt.Errorf("update: %w", unreachable{}), 1, 0},
		{"transient: flag kept, retried every sweep", errors.New("caldav: fetch event for update returned status 503"), 2, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			h, database, _ := newReconcileFixture(t, "confirmed", &logs)
			p := &stuckProvider{err: tc.err}
			svc := calendar.NewService(database)
			svc.Register(p)

			h.reconcileReschedules(context.Background(), svc)
			h.reconcileReschedules(context.Background(), svc)

			if updates, _ := p.calls(); updates != tc.wantUpdates {
				t.Errorf("UpdateEvent called %d times over two sweeps, want %d", updates, tc.wantUpdates)
			}
			eventID, needsSync := hostRow(t, database)
			if needsSync != tc.wantSync {
				t.Errorf("needs_sync = %d, want %d", needsSync, tc.wantSync)
			}
			if !eventID.Valid {
				t.Error("external_event_id was cleared; a reschedule must keep the event id")
			}
			if tc.wantSync == 0 {
				if n := strings.Count(logs.String(), "not retrying an event no single account holds"); n != 1 {
					t.Errorf("logged the give-up warning %d times, want exactly once:\n%s", n, logs.String())
				}
			}
		})
	}
}

// A cancellation the provider refuses as unreachable is not retried either. This sweep has no
// date bound, so before this change the refusal repeated every two minutes forever. The event id
// is logged (it is the only record of which event was left behind), then cleared.
func TestReconcileCancellations_unreachableEventIsNotRetried(t *testing.T) {
	cases := []struct {
		name        string
		err         error
		wantCancels int
		wantID      bool
	}{
		{"unreachable: id cleared, second sweep sends nothing", fmt.Errorf("cancel: %w", unreachable{}), 1, false},
		{"transient: id kept, retried every sweep", errors.New("caldav: delete event returned status 503"), 2, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			h, database, _ := newReconcileFixture(t, "cancelled", &logs)
			p := &stuckProvider{err: tc.err}
			svc := calendar.NewService(database)
			svc.Register(p)

			h.reconcileCancellations(context.Background(), svc)
			h.reconcileCancellations(context.Background(), svc)

			if _, cancels := p.calls(); cancels != tc.wantCancels {
				t.Errorf("CancelEvent called %d times over two sweeps, want %d", cancels, tc.wantCancels)
			}
			eventID, _ := hostRow(t, database)
			if eventID.Valid != tc.wantID {
				t.Errorf("external_event_id present = %v, want %v", eventID.Valid, tc.wantID)
			}
			if !tc.wantID {
				out := logs.String()
				if n := strings.Count(out, "not retrying the delete of an event no single account holds"); n != 1 {
					t.Errorf("logged the give-up warning %d times, want exactly once:\n%s", n, out)
				}
				if !strings.Contains(out, "calendars/host/home/ev1.ics") {
					t.Errorf("the warning does not name the event it leaves behind:\n%s", out)
				}
			}
		})
	}
}

// The event id is a URL a host typed the server part of, so it can carry userinfo. The give-up
// warning must not write a password into the log.
func TestReconcileCancellations_warningRedactsEventURL(t *testing.T) {
	var logs bytes.Buffer
	h, database, _ := newReconcileFixture(t, "cancelled", &logs)
	if _, err := database.Exec(`UPDATE booking_hosts SET external_event_id = 'https://host:s3cret@dav.example.com/calendars/host/home/ev1.ics'`); err != nil {
		t.Fatal(err)
	}
	svc := calendar.NewService(database)
	svc.Register(&stuckProvider{err: unreachable{}})

	h.reconcileCancellations(context.Background(), svc)

	if strings.Contains(logs.String(), "s3cret") {
		t.Errorf("the warning logged the password embedded in the event URL:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), "calendars/host/home/ev1.ics") {
		t.Errorf("the warning does not name the event:\n%s", logs.String())
	}
}
