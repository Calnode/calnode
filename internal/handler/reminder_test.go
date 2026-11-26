package handler_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// nextWeekday returns the first occurrence of wd on or after t.
func nextWeekday(t time.Time, wd time.Weekday) time.Time {
	days := (int(wd) - int(t.Weekday()) + 7) % 7
	return t.AddDate(0, 0, days)
}

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

	// Find the next Monday that's at least 48h away so the 24h reminder window is
	// comfortably in the future when the test runs.
	bookingStart := nextWeekday(time.Now().UTC().Add(48*time.Hour), time.Monday).
		Truncate(24 * time.Hour).Add(9 * time.Hour) // 09:00 UTC on that Monday
	wantRunAt := bookingStart.Add(-24 * time.Hour)

	bookBody := fmt.Sprintf(
		`{"event_type_slug":%q,"start_at":%q,"name":"Alice","email":"alice@example.com"}`,
		slug, bookingStart.Format(time.RFC3339))
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

	// run_at should be 24h before the booking start.
	gotRunAt, err := time.Parse(time.RFC3339, runAt)
	if err != nil {
		t.Fatalf("parse run_at %q: %v", runAt, err)
	}
	diff := gotRunAt.Sub(wantRunAt)
	if diff < -time.Minute || diff > time.Minute {
		t.Errorf("run_at = %s; want ~%s (±1m)", runAt, wantRunAt.Format(time.RFC3339))
	}
}

// ---------------------------------------------------------------------------
// A reminder whose moment has already passed is skipped, not sent at once
// ---------------------------------------------------------------------------

// reminderHours returns the hours_before of the pending reminder jobs for a booking.
func reminderHours(t *testing.T, database *sql.DB, bookingID string) map[int]bool {
	t.Helper()
	rows, err := database.Query(`
		SELECT json_extract(payload, '$.hours_before') FROM jobs
		WHERE type = 'reminder.send'
		  AND json_extract(payload, '$.booking_id') = ?
		  AND status = 'pending'`, bookingID)
	if err != nil {
		t.Fatalf("query reminder jobs: %v", err)
	}
	defer rows.Close()
	got := map[int]bool{}
	for rows.Next() {
		var hb int
		if err := rows.Scan(&hb); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[hb] = true
	}
	return got
}

// waitReminderHours polls until the booking's pending reminders equal want (the jobs
// are enqueued by a goroutine) and fails with what it last saw otherwise.
func waitReminderHours(t *testing.T, database *sql.DB, bookingID string, want ...int) {
	t.Helper()
	var got map[int]bool
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got = reminderHours(t, database, bookingID)
		if len(got) == len(want) {
			match := true
			for _, hb := range want {
				match = match && got[hb]
			}
			if match {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("pending reminders (hours_before) = %v; want %v", got, want)
}

func TestCreateBooking_skipsReminderAlreadyPast(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	slug, etID := seedEventTypeHTTP(t, h, key)
	if _, err := database.Exec(`
		INSERT INTO event_type_reminders (id, event_type_id, hours_before)
		VALUES ('rem-24', ?, 24), ('rem-2', ?, 2)`, etID, etID); err != nil {
		t.Fatalf("seed reminders: %v", err)
	}

	// Booked 5-6 hours ahead: the 24-hour mark is long gone, the 2-hour one is not.
	start := time.Now().UTC().Truncate(time.Hour).Add(6 * time.Hour)
	bookingID := createBookingViaHTTP(t, h, slug, start.Format(time.RFC3339))

	waitReminderHours(t, database, bookingID, 2)
}

func TestRescheduleBooking_skipsReminderAlreadyPast(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	slug, etID := seedEventTypeHTTP(t, h, key)
	if _, err := database.Exec(`
		INSERT INTO event_type_reminders (id, event_type_id, hours_before)
		VALUES ('rem-24', ?, 24), ('rem-2', ?, 2)`, etID, etID); err != nil {
		t.Fatalf("seed reminders: %v", err)
	}

	bookingID := createBookingViaHTTP(t, h, slug, futureAt(10, 10, 0).Format(time.RFC3339))
	waitReminderHours(t, database, bookingID, 24, 2)

	// Moving the booking onto a slot 5-6 hours away must drop the 24-hour reminder
	// instead of firing it straight away.
	start := time.Now().UTC().Truncate(time.Hour).Add(6 * time.Hour)
	rec := patchReschedule(t, h, bookingID, start.Format(time.RFC3339), key)
	if rec.Code != http.StatusOK {
		t.Fatalf("reschedule: got %d — %s", rec.Code, rec.Body.String())
	}
	waitReminderHours(t, database, bookingID, 2)
}
