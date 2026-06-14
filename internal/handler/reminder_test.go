package handler_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Verify reminder job is enqueued at booking creation
// ---------------------------------------------------------------------------

func TestCreateBooking_enqueuedReminderJob(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	ctx := context.Background()

	// Create event type + availability rule.
	slug, etID := seedEventTypeHTTP(t, h, key)
	ruleBody := fmt.Sprintf(`{"event_type_id":%q,"day_of_week":1,"start_time":"09:00","end_time":"17:00"}`, etID)
	ruleReq := authReq(http.MethodPost, "/v1/availability-rules", ruleBody, key)
	ruleRec := httptest.NewRecorder()
	h.RequireAuth(h.CreateAvailabilityRule)(ruleRec, ruleReq)
	if ruleRec.Code != http.StatusCreated {
		t.Fatalf("create rule: %d", ruleRec.Code)
	}

	// Create booking for a Monday (2026-06-15) at 09:00.
	bookBody := fmt.Sprintf(
		`{"event_type_slug":%q,"start_at":"2026-06-15T09:00:00Z","name":"Alice","email":"alice@example.com"}`,
		slug)
	req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(bookBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateBooking(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create booking: %d — %s", rec.Code, rec.Body.String())
	}

	// The goroutine that enqueues the reminder runs asynchronously.
	deadline := time.Now().Add(2 * time.Second)
	var jobCount int
	for time.Now().Before(deadline) {
		database.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM jobs WHERE type = 'reminder.send'`).Scan(&jobCount)
		if jobCount > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if jobCount == 0 {
		t.Fatal("no reminder.send job found after booking creation")
	}

	// Verify job fields.
	var jobStatus, payload, runAt string
	database.QueryRowContext(ctx,
		`SELECT status, payload, run_at FROM jobs WHERE type = 'reminder.send' LIMIT 1`).
		Scan(&jobStatus, &payload, &runAt)

	if jobStatus != "pending" {
		t.Errorf("job status = %q; want pending", jobStatus)
	}
	if !strings.Contains(payload, "booking_id") {
		t.Errorf("payload = %q; want booking_id field", payload)
	}

	// run_at should be 24h before booking start (2026-06-15T09:00:00Z → 2026-06-14T09:00:00Z).
	wantRunAt, _ := time.Parse(time.RFC3339, "2026-06-14T09:00:00Z")
	gotRunAt, err := time.Parse(time.RFC3339, runAt)
	if err != nil {
		t.Fatalf("parse run_at %q: %v", runAt, err)
	}
	diff := gotRunAt.Sub(wantRunAt)
	if diff < -time.Minute || diff > time.Minute {
		t.Errorf("run_at = %s; want ~2026-06-14T09:00:00Z (±1m)", runAt)
	}
}
