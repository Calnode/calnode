package worker_test

import (
	"context"
	"log/slog"
	"net/http"
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
