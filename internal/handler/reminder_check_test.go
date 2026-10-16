package handler_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/mailer"
	"github.com/calnode/calnode/internal/webhook"
	"github.com/calnode/calnode/internal/worker"
)

type reminderCalendar struct {
	calendar.Provider
	cancelled bool
	err       error
	calls     int
}

func (p *reminderCalendar) Name() string { return "google" }
func (p *reminderCalendar) EventCancelled(_ context.Context, userID, calendarID, eventID string) (bool, error) {
	p.calls++
	if userID == "" || calendarID != "original-calendar" || eventID != "original-event" {
		return false, errors.New("wrong stored event identity")
	}
	return p.cancelled, p.err
}

type reminderMailer struct{ sent int }

func (m *reminderMailer) Send(context.Context, mailer.Message) error { m.sent++; return nil }

// Run the actual worker and handler guard against a booking that is still locally
// confirmed, exactly the state left by a Google-origin cancellation.
func TestReminderChecksExternalCancellation(t *testing.T) {
	for _, tc := range []struct {
		name                                                        string
		cancelled, lookupErr, noEvent, noService, cancelDuringCheck bool
		wantSent                                                    int
		wantJob                                                     string
	}{
		{name: "external_cancel", cancelled: true, wantJob: "done"},
		{name: "active", wantSent: 1, wantJob: "done"},
		{name: "lookup_error", lookupErr: true, wantJob: "pending"},
		{name: "no_calendar_event", noEvent: true, wantSent: 1, wantJob: "done"},
		{name: "unconfigured_service", noService: true, wantJob: "pending"},
		{name: "cancel_during_lookup", cancelDuringCheck: true, wantJob: "done"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, database, key, hostID := setupWorkspaceWithDB(t)
			_, etID := seedEventTypeHTTP(t, h, key)
			start := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
			calendarCheckExec(t, database, `INSERT INTO bookings (id,event_type_id,host_id,start_at,end_at,status) VALUES ('rem-booking',?,?,?,?,'confirmed')`, etID, hostID, start, start)
			calendarCheckExec(t, database, `INSERT INTO booking_attendees (id,booking_id,name,email,iana_timezone,is_organizer) VALUES ('rem-att','rem-booking','Test','test@example.com','UTC',1)`)
			if !tc.noEvent {
				calendarCheckExec(t, database, `INSERT INTO booking_hosts (booking_id,user_id,external_event_id,external_calendar_id,external_provider) VALUES ('rem-booking',?,'original-event','original-calendar','google')`, hostID)
			}
			calendarCheckExec(t, database, `INSERT INTO jobs (id,type,payload,run_at,status,attempts,max_attempts) VALUES ('rem-job','reminder.send','{"booking_id":"rem-booking"}',?,'pending',0,3)`, time.Now().UTC().Add(-time.Second).Format(time.RFC3339))
			p := &reminderCalendar{cancelled: tc.cancelled}
			if tc.lookupErr {
				p.err = errors.New("Google unavailable")
			}
			if !tc.noService {
				svc := calendar.NewService(database)
				svc.Register(p)
				h.SetCalendar(svc)
			}
			check := h.ReminderAllowed
			if tc.cancelDuringCheck {
				check = func(ctx context.Context, id string) (bool, error) {
					allowed, err := h.ReminderAllowed(ctx, id)
					_, updateErr := database.ExecContext(ctx, `UPDATE bookings SET status='cancelled' WHERE id=?`, id)
					if updateErr != nil {
						return false, updateErr
					}
					return allowed, err
				}
			}
			m := &reminderMailer{}
			whs, err := webhook.New(database, strings.Repeat("01", 32))
			if err != nil {
				t.Fatal(err)
			}
			w := worker.New(database, whs, slog.Default(), worker.WithMailer(m), worker.WithReminderCheck(check))
			w.Poll(context.Background())
			if m.sent != tc.wantSent {
				t.Fatalf("sent %d emails, want %d", m.sent, tc.wantSent)
			}
			var status string
			if err := database.QueryRow(`SELECT status FROM jobs WHERE id='rem-job'`).Scan(&status); err != nil {
				t.Fatal(err)
			}
			if status != tc.wantJob {
				t.Fatalf("job status %q, want %q", status, tc.wantJob)
			}
			if tc.lookupErr {
				p.err = nil
				calendarCheckExec(t, database, `UPDATE jobs SET run_at=? WHERE id='rem-job'`, time.Now().UTC().Add(-time.Second).Format(time.RFC3339))
				w.Poll(context.Background())
				if m.sent != 1 {
					t.Fatalf("recovered lookup sent %d reminders, want 1", m.sent)
				}
				if err := database.QueryRow(`SELECT status FROM jobs WHERE id='rem-job'`).Scan(&status); err != nil {
					t.Fatal(err)
				}
				if status != "done" {
					t.Fatalf("recovered job = %s", status)
				}
			}
			if tc.cancelled {
				if err := database.QueryRow(`SELECT status FROM bookings WHERE id='rem-booking'`).Scan(&status); err != nil {
					t.Fatal(err)
				}
				if status != "confirmed" {
					t.Fatal("reminder check changed booking state")
				}
			}
		})
	}
}
