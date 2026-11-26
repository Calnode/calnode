package worker_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/mailer"
	"github.com/calnode/calnode/internal/webhook"
	"github.com/calnode/calnode/internal/worker"
)

// flakyMailer fails the first `fail` sends, then records like captureMailer.
type flakyMailer struct {
	fail int
	sent []mailer.Message
}

func (m *flakyMailer) Send(_ context.Context, msg mailer.Message) error {
	if m.fail > 0 {
		m.fail--
		return errors.New("smtp: temporary failure")
	}
	m.sent = append(m.sent, msg)
	return nil
}

// seedReminderBooking creates a confirmed booking for host-01, a webhook subscribed to
// booking.reminder pointing at a local receiver, and one due reminder.send job.
func seedReminderBooking(t *testing.T, database *sql.DB, svc *webhook.Service, hoursBefore int) (start string) {
	t.Helper()
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	start = time.Now().UTC().Add(time.Duration(hoursBefore)*time.Hour + 30*time.Minute).Truncate(time.Second).Format(time.RFC3339)
	end := time.Now().UTC().Add(time.Duration(hoursBefore)*time.Hour + time.Hour).Truncate(time.Second).Format(time.RFC3339)
	database.ExecContext(ctx,
		`INSERT INTO event_types (id, user_id, slug, name, duration_minutes)
		 VALUES ('et-rw','host-01','rem-wh','Reminder Webhook',30)`)
	database.ExecContext(ctx,
		`INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at, status, location_value)
		 VALUES ('bk-rw','et-rw','host-01',?,?,'confirmed','+48 500 000 000')`, start, end)
	database.ExecContext(ctx,
		`INSERT INTO booking_attendees (id, booking_id, name, email, iana_timezone, is_organizer)
		 VALUES ('att-rw','bk-rw','Alice','alice@example.com','Europe/Warsaw',1)`)
	if _, _, err := svc.Create(ctx, "host-01", srv.URL, []string{"booking.reminder"}); err != nil {
		t.Fatalf("create webhook: %v", err)
	}
	queueReminderJob(t, database, "job-rw-1", hoursBefore)
	return start
}

func queueReminderJob(t *testing.T, database *sql.DB, id string, hoursBefore int) {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{"booking_id": "bk-rw", "hours_before": hoursBefore})
	if _, err := database.ExecContext(context.Background(), `
		INSERT INTO jobs (id, type, payload, run_at, status, attempts, max_attempts)
		VALUES (?, 'reminder.send', ?, ?, 'pending', 0, 3)`,
		id, string(payload), time.Now().UTC().Add(-time.Second).Format(time.RFC3339)); err != nil {
		t.Fatalf("queue reminder job: %v", err)
	}
}

type reminderDelivery struct {
	id   string
	data map[string]any
}

func reminderDeliveries(t *testing.T, database *sql.DB) []reminderDelivery {
	t.Helper()
	rows, err := database.QueryContext(context.Background(),
		`SELECT id, payload FROM webhook_deliveries WHERE event = 'booking.reminder' ORDER BY rowid`)
	if err != nil {
		t.Fatalf("query deliveries: %v", err)
	}
	defer rows.Close()
	var out []reminderDelivery
	for rows.Next() {
		var id, payload string
		rows.Scan(&id, &payload)
		var env struct {
			Event string         `json:"event"`
			Data  map[string]any `json:"data"`
		}
		if err := json.Unmarshal([]byte(payload), &env); err != nil {
			t.Fatalf("payload: %v", err)
		}
		if env.Event != "booking.reminder" {
			t.Errorf("envelope event = %q; want booking.reminder", env.Event)
		}
		out = append(out, reminderDelivery{id: id, data: env.Data})
	}
	return out
}

func reminderWorker(database *sql.DB, svc *webhook.Service, m mailer.Mailer) *worker.Worker {
	return worker.New(database, svc, slog.Default(),
		worker.WithMailer(m),
		worker.WithHTTPClient(&http.Client{Timeout: 10 * time.Second}))
}

func TestWorker_reminderFiresBookingReminderWebhook(t *testing.T) {
	database, svc := setup(t)
	start := seedReminderBooking(t, database, svc, 2)
	m := &captureMailer{}
	reminderWorker(database, svc, m).Poll(context.Background())

	if len(m.sent) != 1 {
		t.Fatalf("emails = %d; want 1 (the webhook is in addition to the email)", len(m.sent))
	}
	ds := reminderDeliveries(t, database)
	if len(ds) != 1 {
		t.Fatalf("booking.reminder deliveries = %d; want 1", len(ds))
	}
	d := ds[0].data
	for k, want := range map[string]any{
		"id":              "bk-rw",
		"host_id":         "host-01",
		"event_type_slug": "rem-wh",
		"status":          "confirmed",
		"start_at":        start,
		"location_value":  "+48 500 000 000",
		"hours_before":    float64(2),
	} {
		if d[k] != want {
			t.Errorf("data[%q] = %v; want %v", k, d[k], want)
		}
	}
}

// The webhook is gated by the same preference as the reminder email.
func TestWorker_reminderWebhookRespectsNotifyReminder(t *testing.T) {
	database, svc := setup(t)
	seedReminderBooking(t, database, svc, 2)
	database.Exec(`UPDATE users SET notify_reminder = 0 WHERE id = 'host-01'`)
	m := &captureMailer{}
	reminderWorker(database, svc, m).Poll(context.Background())

	if len(m.sent) != 0 {
		t.Errorf("emails = %d; want 0", len(m.sent))
	}
	if ds := reminderDeliveries(t, database); len(ds) != 0 {
		t.Errorf("booking.reminder deliveries = %d; want 0 with reminders turned off", len(ds))
	}
}

func TestWorker_reminderWebhookSkipsCancelledBooking(t *testing.T) {
	database, svc := setup(t)
	seedReminderBooking(t, database, svc, 2)
	database.Exec(`UPDATE bookings SET status = 'cancelled' WHERE id = 'bk-rw'`)
	reminderWorker(database, svc, &captureMailer{}).Poll(context.Background())

	if ds := reminderDeliveries(t, database); len(ds) != 0 {
		t.Errorf("booking.reminder deliveries = %d; want 0 for a cancelled booking", len(ds))
	}
}

// The email failing after the webhook was enqueued makes the job retry; the retry must
// not enqueue the same reminder again (the "08:00 and 08:01" case).
func TestWorker_reminderRetryDoesNotDuplicateWebhook(t *testing.T) {
	database, svc := setup(t)
	seedReminderBooking(t, database, svc, 2)
	m := &flakyMailer{fail: 1}
	w := reminderWorker(database, svc, m)
	ctx := context.Background()

	w.Poll(ctx)
	var status string
	var attempts int
	database.QueryRow(`SELECT status, attempts FROM jobs WHERE id = 'job-rw-1'`).Scan(&status, &attempts)
	if status != "pending" || attempts != 1 {
		t.Fatalf("after failed email: job status=%q attempts=%d; want pending/1", status, attempts)
	}

	// Skip the backoff and retry.
	database.Exec(`UPDATE jobs SET run_at = ? WHERE id = 'job-rw-1'`,
		time.Now().UTC().Add(-time.Second).Format(time.RFC3339))
	w.Poll(ctx)

	if len(m.sent) != 1 {
		t.Fatalf("emails = %d; want 1 after the retry", len(m.sent))
	}
	if ds := reminderDeliveries(t, database); len(ds) != 1 {
		t.Errorf("booking.reminder deliveries = %d; want 1 across the retry", len(ds))
	}
	var jobs int
	database.QueryRow(`SELECT COUNT(*) FROM jobs WHERE type = 'webhook.deliver'`).Scan(&jobs)
	if jobs != 1 {
		t.Errorf("webhook.deliver jobs = %d; want 1", jobs)
	}
}

// Each configured reminder, and each new time after a reschedule, gets its own event;
// running the same reminder for the same time again does not.
func TestWorker_reminderWebhookKeyedByHoursAndStart(t *testing.T) {
	database, svc := setup(t)
	start := seedReminderBooking(t, database, svc, 2)
	w := reminderWorker(database, svc, &captureMailer{})
	ctx := context.Background()
	w.Poll(ctx)

	queueReminderJob(t, database, "job-rw-24", 24) // a second configured reminder
	w.Poll(ctx)
	queueReminderJob(t, database, "job-rw-dup", 2) // same reminder, same time again
	w.Poll(ctx)

	newStart := time.Now().UTC().Add(5 * time.Hour).Truncate(time.Second).Format(time.RFC3339)
	database.Exec(`UPDATE bookings SET start_at = ? WHERE id = 'bk-rw'`, newStart)
	queueReminderJob(t, database, "job-rw-moved", 2) // rescheduled
	w.Poll(ctx)

	ds := reminderDeliveries(t, database)
	if len(ds) != 3 {
		t.Fatalf("booking.reminder deliveries = %d; want 3 (2h, 24h, 2h after reschedule)", len(ds))
	}
	got := [][2]any{}
	for _, d := range ds {
		got = append(got, [2]any{d.data["hours_before"], d.data["start_at"]})
	}
	want := [][2]any{{float64(2), start}, {float64(24), start}, {float64(2), newStart}}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("delivery %d = %v; want %v", i, got[i], want[i])
		}
	}
}
