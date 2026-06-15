package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// futureAt returns a UTC time that is daysFromNow days ahead at hour:min.
// Using a date well in the future ensures tests don't break as time passes.
func futureAt(daysFromNow, hour, min int) time.Time {
	base := time.Now().UTC().Truncate(24 * time.Hour).AddDate(0, 0, daysFromNow)
	return base.Add(time.Duration(hour)*time.Hour + time.Duration(min)*time.Minute)
}

// patchReschedule sends PATCH /v1/bookings/{id}/reschedule with the given start_at.
func patchReschedule(t *testing.T, h interface {
	RequireAuth(http.HandlerFunc) http.HandlerFunc
	RescheduleBooking(http.ResponseWriter, *http.Request)
}, bookingID, startAt, apiKey string) *httptest.ResponseRecorder {
	t.Helper()
	body := fmt.Sprintf(`{"start_at":%q}`, startAt)
	req := authReq(http.MethodPatch, "/v1/bookings/"+bookingID+"/reschedule", body, apiKey)
	req.SetPathValue("id", bookingID)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.RescheduleBooking)(rec, req)
	return rec
}

// ---------------------------------------------------------------------------
// Happy path: host reschedules own booking
// ---------------------------------------------------------------------------

func TestRescheduleBooking_success(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	ctx := context.Background()

	slug, _ := seedEventTypeHTTP(t, h, key)
	bookStart := futureAt(10, 10, 0)
	newStart := futureAt(11, 14, 0)
	newEnd := newStart.Add(30 * time.Minute)

	bookingID := createBookingViaHTTP(t, h, slug, bookStart.Format(time.RFC3339))

	rec := patchReschedule(t, h, bookingID, newStart.Format(time.RFC3339), key)
	if rec.Code != http.StatusOK {
		t.Fatalf("reschedule: got %d — %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	wantStart := newStart.Format(time.RFC3339)
	wantEnd := newEnd.Format(time.RFC3339)
	if got := resp["start_at"]; got != wantStart {
		t.Errorf("start_at = %v; want %s", got, wantStart)
	}
	if got := resp["end_at"]; got != wantEnd {
		t.Errorf("end_at = %v; want %s (30-min event)", got, wantEnd)
	}

	// Verify the DB was updated.
	var dbStart string
	database.QueryRowContext(ctx, `SELECT start_at FROM bookings WHERE id = ?`, bookingID).
		Scan(&dbStart)
	if !strings.Contains(dbStart, newStart.Format("2006-01-02T15:04:05")) {
		t.Errorf("DB start_at = %q; want to contain %s", dbStart, newStart.Format("2006-01-02T15:04:05"))
	}
}

// ---------------------------------------------------------------------------
// Reminder job is updated on reschedule
// ---------------------------------------------------------------------------

func TestRescheduleBooking_updatesReminderJob(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	ctx := context.Background()

	slug, _ := seedEventTypeHTTP(t, h, key)

	bookStart := futureAt(10, 10, 0)
	oldReminderAt := bookStart.Add(-24 * time.Hour)
	newStart := futureAt(13, 9, 0)
	wantNewReminder := newStart.Add(-24 * time.Hour)

	bookingID := createBookingViaHTTP(t, h, slug, bookStart.Format(time.RFC3339))

	// Seed an old-format reminder job (no hours_before field) to verify that
	// replaceReminderJobs correctly removes stale jobs of any payload format.
	oldPayload := fmt.Sprintf(`{"booking_id":%q}`, bookingID)
	database.ExecContext(ctx, `
		INSERT INTO jobs (id, type, payload, run_at, status, attempts, max_attempts)
		VALUES ('rem-test-job', 'reminder.send', ?, ?, 'pending', 0, 3)`,
		oldPayload, oldReminderAt.Format(time.RFC3339))

	rec := patchReschedule(t, h, bookingID, newStart.Format(time.RFC3339), key)
	if rec.Code != http.StatusOK {
		t.Fatalf("reschedule: got %d — %s", rec.Code, rec.Body.String())
	}

	// replaceReminderJobs deletes old jobs and inserts fresh ones keyed by booking_id.
	// Poll until a pending reminder job with the correct run_at appears.
	var runAt string
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		database.QueryRowContext(ctx, `
			SELECT run_at FROM jobs
			WHERE type = 'reminder.send'
			  AND json_extract(payload, '$.booking_id') = ?
			  AND status = 'pending'`, bookingID).
			Scan(&runAt)
		if runAt != "" && runAt != oldReminderAt.Format(time.RFC3339) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	gotRunAt, err := time.Parse(time.RFC3339, runAt)
	if err != nil {
		t.Fatalf("parse job run_at %q: %v", runAt, err)
	}
	diff := gotRunAt.Sub(wantNewReminder)
	if diff < -time.Minute || diff > time.Minute {
		t.Errorf("reminder run_at = %s; want ~%s (±1m)", runAt, wantNewReminder.Format(time.RFC3339))
	}

	// Verify the old-format job was deleted.
	var oldJobExists int
	database.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE id = 'rem-test-job'`).Scan(&oldJobExists)
	if oldJobExists != 0 {
		t.Errorf("old reminder job 'rem-test-job' was not deleted by replaceReminderJobs")
	}
}

// ---------------------------------------------------------------------------
// Wrong user: booking belongs to another host
// ---------------------------------------------------------------------------

func TestRescheduleBooking_wrongUser(t *testing.T) {
	h, _, key1, _ := setupWorkspaceWithDB(t)

	slug, _ := seedEventTypeHTTP(t, h, key1)
	_ = createBookingViaHTTP(t, h, slug, futureAt(10, 10, 0).Format(time.RFC3339))

	fakeID := "does-not-belong-to-me"
	req := authReq(http.MethodPatch, "/v1/bookings/"+fakeID+"/reschedule",
		fmt.Sprintf(`{"start_at":%q}`, futureAt(11, 10, 0).Format(time.RFC3339)), key1)
	req.SetPathValue("id", fakeID)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.RescheduleBooking)(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("wrong user: got %d; want 404", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Unauthenticated request → 401
// ---------------------------------------------------------------------------

func TestRescheduleBooking_requiresAuth(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	bookingID := createBookingViaHTTP(t, h, slug, futureAt(10, 10, 0).Format(time.RFC3339))

	body := fmt.Sprintf(`{"start_at":%q}`, futureAt(11, 10, 0).Format(time.RFC3339))
	req := httptest.NewRequest(http.MethodPatch,
		"/v1/bookings/"+bookingID+"/reschedule",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", bookingID)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.RescheduleBooking)(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no auth: got %d; want 401", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Double-booked conflict → 409
// ---------------------------------------------------------------------------

func TestRescheduleBooking_doubleBooked(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	id1 := createBookingViaHTTP(t, h, slug, futureAt(10, 10, 0).Format(time.RFC3339))
	_ = createBookingViaHTTP(t, h, slug, futureAt(10, 11, 0).Format(time.RFC3339))

	rec := patchReschedule(t, h, id1, futureAt(10, 11, 0).Format(time.RFC3339), key)
	if rec.Code != http.StatusConflict {
		t.Errorf("double-booked: got %d; want 409", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Already cancelled → 409
// ---------------------------------------------------------------------------

func TestRescheduleBooking_alreadyCancelled(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	bookingID := createBookingViaHTTP(t, h, slug, futureAt(10, 10, 0).Format(time.RFC3339))

	cancelReq := authReq(http.MethodPost, "/v1/bookings/"+bookingID+"/cancel", `{}`, key)
	cancelReq.SetPathValue("id", bookingID)
	cancelRec := httptest.NewRecorder()
	h.RequireAuth(h.CancelBooking)(cancelRec, cancelReq)
	if cancelRec.Code != http.StatusOK {
		t.Fatalf("cancel: %d — %s", cancelRec.Code, cancelRec.Body.String())
	}

	rec := patchReschedule(t, h, bookingID, futureAt(11, 10, 0).Format(time.RFC3339), key)
	if rec.Code != http.StatusConflict {
		t.Errorf("cancelled booking: got %d; want 409", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Booking not found → 404
// ---------------------------------------------------------------------------

func TestRescheduleBooking_notFound(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)

	rec := patchReschedule(t, h, "nonexistent-booking-id", futureAt(11, 10, 0).Format(time.RFC3339), key)
	if rec.Code != http.StatusNotFound {
		t.Errorf("not found: got %d; want 404", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Invalid start_at format → 400
// ---------------------------------------------------------------------------

func TestRescheduleBooking_badStartAt(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	bookingID := createBookingViaHTTP(t, h, slug, futureAt(10, 10, 0).Format(time.RFC3339))

	req := authReq(http.MethodPatch, "/v1/bookings/"+bookingID+"/reschedule",
		`{"start_at":"not-a-date"}`, key)
	req.SetPathValue("id", bookingID)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.RescheduleBooking)(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad date: got %d; want 400", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Missing start_at → 400
// ---------------------------------------------------------------------------

func TestRescheduleBooking_missingStartAt(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	bookingID := createBookingViaHTTP(t, h, slug, futureAt(10, 10, 0).Format(time.RFC3339))

	req := authReq(http.MethodPatch, "/v1/bookings/"+bookingID+"/reschedule", `{}`, key)
	req.SetPathValue("id", bookingID)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.RescheduleBooking)(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing start_at: got %d; want 400", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Past date → 400
// ---------------------------------------------------------------------------

func TestRescheduleBooking_pastDate(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	bookingID := createBookingViaHTTP(t, h, slug, futureAt(10, 10, 0).Format(time.RFC3339))

	yesterday := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	rec := patchReschedule(t, h, bookingID, yesterday, key)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("past date: got %d; want 400", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Reschedule to same time → 200 (self-overlap is allowed)
// ---------------------------------------------------------------------------

func TestRescheduleBooking_sameTime(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	start := futureAt(10, 10, 0).Format(time.RFC3339)
	bookingID := createBookingViaHTTP(t, h, slug, start)

	rec := patchReschedule(t, h, bookingID, start, key)
	if rec.Code != http.StatusOK {
		t.Errorf("same time reschedule: got %d; want 200", rec.Code)
	}
}
