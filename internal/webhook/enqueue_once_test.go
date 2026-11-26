package webhook_test

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"

	"github.com/calnode/calnode/internal/webhook"
)

func reminderPayload() webhook.BookingPayload {
	return webhook.BookingPayload{
		EventTypeSlug: "30-min",
		HostID:        testUserID,
		StartAt:       "2026-06-15T09:00:00Z",
		EndAt:         "2026-06-15T09:30:00Z",
		Status:        "confirmed",
		CreatedAt:     "2026-06-14T15:00:00Z",
		HoursBefore:   2,
	}
}

func countRows(t *testing.T, e *env, query string, args ...any) int {
	t.Helper()
	var n int
	if err := e.db.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// A retried reminder job calls EnqueueOnce again with the same key: it must not
// produce a second delivery or a second webhook.deliver job.
func TestEnqueueOnce_sameKeyCollapses(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	wh, _, _ := e.svc.Create(ctx, testUserID, "https://example.com/hook", []string{"booking.reminder"})

	for i := 0; i < 3; i++ {
		if err := e.svc.EnqueueOnce(ctx, "booking.reminder", reminderPayload(), "k1"); err != nil {
			t.Fatalf("EnqueueOnce #%d: %v", i+1, err)
		}
	}
	if n := countRows(t, e, `SELECT COUNT(*) FROM webhook_deliveries WHERE webhook_id = ?`, wh.ID); n != 1 {
		t.Errorf("deliveries = %d; want 1", n)
	}
	if n := countRows(t, e, `SELECT COUNT(*) FROM jobs WHERE type = 'webhook.deliver'`); n != 1 {
		t.Errorf("webhook.deliver jobs = %d; want 1", n)
	}

	// Still collapsed after the delivery finished and its job is no longer live,
	// which is where the jobs table's own (type, payload) guard stops helping.
	e.db.ExecContext(ctx, `UPDATE jobs SET status = 'done' WHERE type = 'webhook.deliver'`)
	e.db.ExecContext(ctx, `UPDATE webhook_deliveries SET status = 'success'`)
	if err := e.svc.EnqueueOnce(ctx, "booking.reminder", reminderPayload(), "k1"); err != nil {
		t.Fatalf("EnqueueOnce after success: %v", err)
	}
	if n := countRows(t, e, `SELECT COUNT(*) FROM webhook_deliveries`); n != 1 {
		t.Errorf("deliveries after success = %d; want 1", n)
	}
	if n := countRows(t, e, `SELECT COUNT(*) FROM jobs WHERE type = 'webhook.deliver'`); n != 1 {
		t.Errorf("jobs after success = %d; want 1", n)
	}
}

func TestEnqueueOnce_differentKeyOrWebhookGetsOwnDelivery(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.svc.Create(ctx, testUserID, "https://example.com/a", []string{"booking.reminder"})
	e.svc.Create(ctx, testUserID, "https://example.com/b", []string{"booking.reminder"})

	if err := e.svc.EnqueueOnce(ctx, "booking.reminder", reminderPayload(), "k1"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.EnqueueOnce(ctx, "booking.reminder", reminderPayload(), "k2"); err != nil {
		t.Fatal(err)
	}
	// Two webhooks × two keys.
	if n := countRows(t, e, `SELECT COUNT(*) FROM webhook_deliveries`); n != 4 {
		t.Errorf("deliveries = %d; want 4", n)
	}

	uuid := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-5[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	rows, _ := e.db.QueryContext(ctx, `SELECT id FROM webhook_deliveries`)
	defer rows.Close()
	for rows.Next() {
		var id string
		rows.Scan(&id)
		if !uuid.MatchString(id) {
			t.Errorf("delivery id %q is not UUID-shaped", id)
		}
	}
}

func TestEnqueueOnce_requiresKey(t *testing.T) {
	e := newEnv(t)
	if err := e.svc.EnqueueOnce(context.Background(), "booking.reminder", reminderPayload(), ""); err == nil {
		t.Fatal("EnqueueOnce with empty key: want error")
	}
}

// hours_before is part of the event, not a selectable booking field: it is sent even
// when the webhook narrowed its fields, and never on other events.
func TestEnqueue_hoursBeforeAlwaysSentWithReminder(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	wh, _, _ := e.svc.Create(ctx, testUserID, "https://example.com/hook",
		[]string{"booking.reminder", "booking.created"})
	e.db.ExecContext(ctx, `UPDATE webhooks SET fields = '["id"]' WHERE id = ?`, wh.ID)

	if err := e.svc.EnqueueOnce(ctx, "booking.reminder", reminderPayload(), "k1"); err != nil {
		t.Fatal(err)
	}
	created := reminderPayload()
	created.HoursBefore = 0
	if err := e.svc.Enqueue(ctx, "booking.created", created); err != nil {
		t.Fatal(err)
	}

	data := map[string]map[string]any{}
	rows, _ := e.db.QueryContext(ctx, `SELECT event, payload FROM webhook_deliveries`)
	defer rows.Close()
	for rows.Next() {
		var event, payload string
		rows.Scan(&event, &payload)
		var env struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal([]byte(payload), &env); err != nil {
			t.Fatalf("payload: %v", err)
		}
		data[event] = env.Data
	}
	if got := data["booking.reminder"]["hours_before"]; got != float64(2) {
		t.Errorf("booking.reminder hours_before = %v; want 2", got)
	}
	if _, ok := data["booking.reminder"]["start_at"]; ok {
		t.Error("booking.reminder carries start_at although the webhook selected only id")
	}
	if _, ok := data["booking.created"]["hours_before"]; ok {
		t.Error("booking.created carries hours_before")
	}
}
