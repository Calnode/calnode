package handler

import (
	"context"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/mailer"
)

// stallMailer models an SMTP server that drops packets: every send hangs until its
// context gives up.
type stallMailer struct{}

func (stallMailer) Send(ctx context.Context, _ mailer.Message) error {
	<-ctx.Done()
	return ctx.Err()
}

// ctxProvider is inviteProvider honouring its context, as the real HTTP providers do.
type ctxProvider struct{ *inviteProvider }

func (p ctxProvider) CreateEvent(ctx context.Context, userID string, params calendar.CreateEventParams) (string, string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", "", err
	}
	return p.inviteProvider.CreateEvent(ctx, userID, params)
}

// A stalled mail server used to spend the single dispatch deadline on the primary host's
// email, so the next host's calendar event, the reminders and the webhook - all queued
// after it - were silently dropped. Every host's event and the reminder must now land
// while mail is still hanging, and the lost confirmation is flagged on the booking.
func TestDispatch_stalledMailDoesNotStarveCalendarOrReminders(t *testing.T) {
	origMail := dispatchMailBudget
	dispatchMailBudget = 300 * time.Millisecond
	t.Cleanup(func() { dispatchMailBudget = origMail })

	f := newInviteFixture(t, false)
	f.h.SetMailer(stallMailer{}, "https://book.team.example")
	svc := calendar.NewService(f.h.db)
	svc.Register(ctxProvider{f.cal})
	f.h.SetCalendar(svc)

	if rec := f.createEventType(t, "intro", "calendar"); rec.Code != 201 {
		t.Fatalf("create event type: %d — %s", rec.Code, rec.Body.String())
	}
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := f.h.db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	mustExec(`UPDATE event_types SET routing_mode = 'collective' WHERE slug = 'intro'`)
	mustExec(`INSERT INTO users (id, email, name, iana_timezone) VALUES ('host-2', 'second@host.example', 'Second Host', 'UTC')`)
	for day := 0; day < 7; day++ {
		mustExec(`INSERT INTO availability_rules (id, user_id, day_of_week, start_time, end_time) VALUES (?, 'host-2', ?, '00:00', '23:59')`,
			"rule-h2-"+string(rune('0'+day)), day)
	}
	mustExec(`INSERT INTO calendar_connections
		  (id, user_id, provider, access_token_enc, calendar_id, check_conflicts, is_destination, created_at)
		VALUES ('conn-2', 'host-2', 'google', 'e', 'primary', 1, 1, '2026-01-01T00:00:00Z')`)
	mustExec(`INSERT INTO event_type_hosts (id, event_type_id, user_id, role, priority)
		SELECT 'seat-2', id, 'host-2', 'required', 1 FROM event_types WHERE slug = 'intro'`)

	start := time.Now().UTC().Add(72 * time.Hour).Truncate(24 * time.Hour).Add(10 * time.Hour)
	req := `{"event_type_slug":"intro","start_at":"` + start.Format(time.RFC3339) + `","name":"Guest","email":"` + inviteBookerEmail + `","timezone":"UTC"}`
	rec := f.do(t, f.h.CreateBooking, "POST", "/v1/bookings", req)
	if rec.Code != 201 {
		t.Fatalf("create booking: %d — %s", rec.Code, rec.Body.String())
	}

	var events, reminders, flagged int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_ = f.h.db.QueryRow(`SELECT COUNT(*) FROM booking_hosts WHERE COALESCE(external_event_id, '') != ''`).Scan(&events)
		_ = f.h.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE type = 'reminder.send'`).Scan(&reminders)
		_ = f.h.db.QueryRow(`SELECT COUNT(*) FROM bookings WHERE confirm_failed = 1`).Scan(&flagged)
		if events == 2 && reminders > 0 && flagged == 1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("host events = %d (want 2), reminder jobs = %d (want > 0), confirm_failed bookings = %d (want 1)",
		events, reminders, flagged)
}
