package worker_test

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/mailer"
	"github.com/calnode/calnode/internal/worker"
)

// captureMailer records every Send call so tests can assert on sent emails.
type captureMailer struct {
	sent []mailer.Message
}

func (m *captureMailer) Send(_ context.Context, msg mailer.Message) error {
	m.sent = append(m.sent, msg)
	return nil
}

// ---------------------------------------------------------------------------
// reminder.send: confirmed booking → email sent
// ---------------------------------------------------------------------------

func TestWorker_sendsReminderForConfirmedBooking(t *testing.T) {
	database, svc := setup(t)
	ctx := context.Background()

	pastRunAt := time.Now().UTC().Add(-time.Second).Format(time.RFC3339)
	bookingStart := time.Now().UTC().Add(25 * time.Hour).Format(time.RFC3339)

	database.ExecContext(ctx,
		`INSERT INTO event_types (id, user_id, slug, name, duration_minutes)
		 VALUES ('et-r1','host-01','rem-test-1','Reminder Meeting',30)`)
	database.ExecContext(ctx,
		`INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at, status)
		 VALUES ('bk-r1','et-r1','host-01',?,?,'confirmed')`, bookingStart, bookingStart)
	database.ExecContext(ctx,
		`INSERT INTO booking_attendees (id, booking_id, name, email, iana_timezone, is_organizer)
		 VALUES ('att-r1','bk-r1','Alice','alice@example.com','UTC',1)`)
	database.ExecContext(ctx, `
		INSERT INTO jobs (id, type, payload, run_at, status, attempts, max_attempts)
		VALUES ('job-r1','reminder.send','{"booking_id":"bk-r1"}',?,'pending',0,3)`,
		pastRunAt)

	m := &captureMailer{}
	w := worker.New(database, svc, slog.Default(),
		worker.WithMailer(m),
		worker.WithHTTPClient(&http.Client{}))
	w.Poll(ctx)

	if len(m.sent) != 1 {
		t.Fatalf("sent %d emails; want 1", len(m.sent))
	}
	msg := m.sent[0]
	if len(msg.To) == 0 || msg.To[0] != "alice@example.com" {
		t.Errorf("To = %v; want [alice@example.com]", msg.To)
	}
	if msg.Subject == "" {
		t.Error("Subject is empty")
	}
	if msg.Text == "" {
		t.Error("Text body is empty")
	}

	var jobStatus string
	database.QueryRowContext(ctx, `SELECT status FROM jobs WHERE id = 'job-r1'`).Scan(&jobStatus)
	if jobStatus != "done" {
		t.Errorf("job status = %q; want done", jobStatus)
	}
}

// ---------------------------------------------------------------------------
// reminder.send: attendee locale drives the email language
// ---------------------------------------------------------------------------

func TestWorker_reminderRespectsAttendeeLocale(t *testing.T) {
	database, svc := setup(t)
	ctx := context.Background()

	pastRunAt := time.Now().UTC().Add(-time.Second).Format(time.RFC3339)
	bookingStart := time.Now().UTC().Add(25 * time.Hour).Format(time.RFC3339)

	database.ExecContext(ctx,
		`INSERT INTO event_types (id, user_id, slug, name, duration_minutes)
		 VALUES ('et-r2','host-01','rem-test-2','Reminder Meeting',30)`)
	database.ExecContext(ctx,
		`INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at, status)
		 VALUES ('bk-r2','et-r2','host-01',?,?,'confirmed')`, bookingStart, bookingStart)
	database.ExecContext(ctx,
		`INSERT INTO booking_attendees (id, booking_id, name, email, iana_timezone, is_organizer, locale)
		 VALUES ('att-r2','bk-r2','Ana','ana@example.com','UTC',1,'es')`)
	database.ExecContext(ctx, `
		INSERT INTO jobs (id, type, payload, run_at, status, attempts, max_attempts)
		VALUES ('job-r2','reminder.send','{"booking_id":"bk-r2"}',?,'pending',0,3)`,
		pastRunAt)

	m := &captureMailer{}
	w := worker.New(database, svc, slog.Default(),
		worker.WithMailer(m),
		worker.WithHTTPClient(&http.Client{}))
	w.Poll(ctx)

	if len(m.sent) != 1 {
		t.Fatalf("sent %d emails; want 1", len(m.sent))
	}
	msg := m.sent[0]
	if !strings.Contains(msg.Subject, "Recordatorio") {
		t.Errorf("Subject = %q; want the Spanish reminder subject", msg.Subject)
	}
	if !strings.Contains(msg.Text, "Hola Ana,") {
		t.Errorf("Text missing Spanish greeting: %q", msg.Text)
	}
}

// ---------------------------------------------------------------------------
// reminder.send: names the booking's host, not the event type's owner (#48)
// ---------------------------------------------------------------------------

// seedAssignedReminder creates an event type owned by host-01 whose booking was
// assigned to host-02, the shape a round-robin or hosts-tab event type produces,
// plus a due reminder job for it.
func seedAssignedReminder(t *testing.T, database *sql.DB, suffix string) {
	t.Helper()
	ctx := context.Background()
	pastRunAt := time.Now().UTC().Add(-time.Second).Format(time.RFC3339)
	bookingStart := time.Now().UTC().Add(25 * time.Hour).Format(time.RFC3339)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO event_types (id, user_id, slug, name, duration_minutes)
		  VALUES (?, 'host-01', ?, 'Team Intro', 30)`, []any{"et-" + suffix, "team-intro-" + suffix}},
		{`INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at, status)
		  VALUES (?, ?, 'host-02', ?, ?, 'confirmed')`, []any{"bk-" + suffix, "et-" + suffix, bookingStart, bookingStart}},
		{`INSERT INTO booking_attendees (id, booking_id, name, email, iana_timezone, is_organizer)
		  VALUES (?, ?, 'Dana', 'dana@example.com', 'UTC', 1)`, []any{"att-" + suffix, "bk-" + suffix}},
		{`INSERT INTO jobs (id, type, payload, run_at, status, attempts, max_attempts)
		  VALUES (?, 'reminder.send', ?, ?, 'pending', 0, 3)`, []any{"job-" + suffix, `{"booking_id":"bk-` + suffix + `"}`, pastRunAt}},
	} {
		if _, err := database.ExecContext(ctx, q.sql, q.args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

func TestWorker_reminderNamesTheBookingHostNotTheEventTypeOwner(t *testing.T) {
	database, svc := setup(t)
	ctx := context.Background()
	seedAssignedReminder(t, database, "h1")

	m := &captureMailer{}
	w := worker.New(database, svc, slog.Default(),
		worker.WithMailer(m),
		worker.WithHTTPClient(&http.Client{}))
	w.Poll(ctx)

	if len(m.sent) != 1 {
		t.Fatalf("sent %d emails; want 1", len(m.sent))
	}
	msg := m.sent[0]
	for name, body := range map[string]string{"Text": msg.Text, "HTML": msg.HTML} {
		if !strings.Contains(body, "Host Two") {
			t.Errorf("%s does not name the assigned host (Host Two):\n%s", name, body)
		}
		if strings.Contains(body, "Host One") {
			t.Errorf("%s names the event type's owner (Host One), who is not the host of this booking:\n%s", name, body)
		}
	}
}

// The notify_reminder preference that decides whether the reminder goes out is
// the assigned host's, since it is their meeting the email is about.
func TestWorker_reminderFollowsTheBookingHostsPreference(t *testing.T) {
	cases := []struct {
		name                string
		ownerPref, hostPref int
		wantSent            int
	}{
		{"owner opted out, host did not", 0, 1, 1},
		{"host opted out, owner did not", 1, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			database, svc := setup(t)
			ctx := context.Background()
			seedAssignedReminder(t, database, "p1")
			if _, err := database.ExecContext(ctx,
				`UPDATE users SET notify_reminder = ? WHERE id = 'host-01'`, tc.ownerPref); err != nil {
				t.Fatal(err)
			}
			if _, err := database.ExecContext(ctx,
				`UPDATE users SET notify_reminder = ? WHERE id = 'host-02'`, tc.hostPref); err != nil {
				t.Fatal(err)
			}

			m := &captureMailer{}
			w := worker.New(database, svc, slog.Default(),
				worker.WithMailer(m),
				worker.WithHTTPClient(&http.Client{}))
			w.Poll(ctx)

			if len(m.sent) != tc.wantSent {
				t.Errorf("sent %d emails; want %d", len(m.sent), tc.wantSent)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// reminder.send: cancelled booking → silent skip, job done
// ---------------------------------------------------------------------------

func TestWorker_skipsReminderForCancelledBooking(t *testing.T) {
	database, svc := setup(t)
	ctx := context.Background()

	pastRunAt := time.Now().UTC().Add(-time.Second).Format(time.RFC3339)
	bookingStart := time.Now().UTC().Add(25 * time.Hour).Format(time.RFC3339)

	database.ExecContext(ctx,
		`INSERT INTO event_types (id, user_id, slug, name, duration_minutes)
		 VALUES ('et-c1','host-01','cancel-test','Cancelled Meeting',30)`)
	database.ExecContext(ctx,
		`INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at, status)
		 VALUES ('bk-c1','et-c1','host-01',?,?,'cancelled')`, bookingStart, bookingStart)
	database.ExecContext(ctx, `
		INSERT INTO jobs (id, type, payload, run_at, status, attempts, max_attempts)
		VALUES ('job-c1','reminder.send','{"booking_id":"bk-c1"}',?,'pending',0,3)`,
		pastRunAt)

	m := &captureMailer{}
	w := worker.New(database, svc, slog.Default(),
		worker.WithMailer(m),
		worker.WithHTTPClient(&http.Client{}))
	w.Poll(ctx)

	if len(m.sent) != 0 {
		t.Errorf("sent %d emails; want 0 (booking cancelled)", len(m.sent))
	}
	var jobStatus string
	database.QueryRowContext(ctx, `SELECT status FROM jobs WHERE id = 'job-c1'`).Scan(&jobStatus)
	if jobStatus != "done" {
		t.Errorf("job status = %q; want done (skip is not a failure)", jobStatus)
	}
}

// ---------------------------------------------------------------------------
// reminder.send: deleted booking → silent skip, job done
// ---------------------------------------------------------------------------

func TestWorker_skipsReminderForDeletedBooking(t *testing.T) {
	database, svc := setup(t)
	ctx := context.Background()

	pastRunAt := time.Now().UTC().Add(-time.Second).Format(time.RFC3339)

	database.ExecContext(ctx, `
		INSERT INTO jobs (id, type, payload, run_at, status, attempts, max_attempts)
		VALUES ('job-d1','reminder.send','{"booking_id":"nonexistent"}',?,'pending',0,3)`,
		pastRunAt)

	m := &captureMailer{}
	w := worker.New(database, svc, slog.Default(),
		worker.WithMailer(m),
		worker.WithHTTPClient(&http.Client{}))
	w.Poll(ctx)

	if len(m.sent) != 0 {
		t.Errorf("sent %d emails; want 0 (booking not found)", len(m.sent))
	}
	var jobStatus string
	database.QueryRowContext(ctx, `SELECT status FROM jobs WHERE id = 'job-d1'`).Scan(&jobStatus)
	if jobStatus != "done" {
		t.Errorf("job status = %q; want done", jobStatus)
	}
}

// ---------------------------------------------------------------------------
// reminder.send: not fired before run_at
// ---------------------------------------------------------------------------

func TestWorker_reminderNotFiredBeforeRunAt(t *testing.T) {
	database, svc := setup(t)
	ctx := context.Background()

	futureRunAt := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	bookingStart := time.Now().UTC().Add(25 * time.Hour).Format(time.RFC3339)

	database.ExecContext(ctx,
		`INSERT INTO event_types (id, user_id, slug, name, duration_minutes)
		 VALUES ('et-f1','host-01','future-test','Future Meeting',30)`)
	database.ExecContext(ctx,
		`INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at, status)
		 VALUES ('bk-f1','et-f1','host-01',?,?,'confirmed')`, bookingStart, bookingStart)
	database.ExecContext(ctx,
		`INSERT INTO booking_attendees (id, booking_id, name, email, iana_timezone, is_organizer)
		 VALUES ('att-f1','bk-f1','Carol','carol@example.com','UTC',1)`)
	database.ExecContext(ctx, `
		INSERT INTO jobs (id, type, payload, run_at, status, attempts, max_attempts)
		VALUES ('job-f1','reminder.send','{"booking_id":"bk-f1"}',?,'pending',0,3)`,
		futureRunAt)

	m := &captureMailer{}
	w := worker.New(database, svc, slog.Default(),
		worker.WithMailer(m),
		worker.WithHTTPClient(&http.Client{}))
	w.Poll(ctx)

	if len(m.sent) != 0 {
		t.Errorf("sent %d emails; want 0 (not yet due)", len(m.sent))
	}
	var jobStatus string
	database.QueryRowContext(ctx, `SELECT status FROM jobs WHERE id = 'job-f1'`).Scan(&jobStatus)
	if jobStatus != "pending" {
		t.Errorf("job status = %q; want pending", jobStatus)
	}
}
