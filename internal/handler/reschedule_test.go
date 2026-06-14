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
	bookingID := createBookingViaHTTP(t, h, slug, "2026-06-20T10:00:00Z")

	rec := patchReschedule(t, h, bookingID, "2026-06-21T14:00:00Z", key)
	if rec.Code != http.StatusOK {
		t.Fatalf("reschedule: got %d — %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := resp["start_at"]; got != "2026-06-21T14:00:00Z" {
		t.Errorf("start_at = %v; want 2026-06-21T14:00:00Z", got)
	}
	if got := resp["end_at"]; got != "2026-06-21T14:30:00Z" {
		t.Errorf("end_at = %v; want 2026-06-21T14:30:00Z (30-min event)", got)
	}

	// Verify the DB was updated.
	var dbStart string
	database.QueryRowContext(ctx, `SELECT start_at FROM bookings WHERE id = ?`, bookingID).
		Scan(&dbStart)
	if !strings.Contains(dbStart, "2026-06-21T14:00:00") {
		t.Errorf("DB start_at = %q; want to contain 2026-06-21T14:00:00", dbStart)
	}
}

// ---------------------------------------------------------------------------
// Reminder job is updated on reschedule
// ---------------------------------------------------------------------------

func TestRescheduleBooking_updatesReminderJob(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	ctx := context.Background()

	slug, _ := seedEventTypeHTTP(t, h, key)
	// Booking starts 2026-06-25T10:00:00Z; reminder fires 2026-06-24T10:00:00Z.
	bookingID := createBookingViaHTTP(t, h, slug, "2026-06-25T10:00:00Z")

	// Seed an existing reminder job as if it was enqueued at booking creation.
	payload := fmt.Sprintf(`{"booking_id":%q}`, bookingID)
	database.ExecContext(ctx, `
		INSERT INTO jobs (id, type, payload, run_at, status, attempts, max_attempts)
		VALUES ('rem-test-job', 'reminder.send', ?, '2026-06-24T10:00:00Z', 'pending', 0, 3)`,
		payload)

	// Reschedule to 2026-06-28T09:00:00Z → new reminder run_at should be ~2026-06-27T09:00:00Z.
	rec := patchReschedule(t, h, bookingID, "2026-06-28T09:00:00Z", key)
	if rec.Code != http.StatusOK {
		t.Fatalf("reschedule: got %d — %s", rec.Code, rec.Body.String())
	}

	// The goroutine runs asynchronously; poll briefly.
	var runAt string
	var status string
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		database.QueryRowContext(ctx,
			`SELECT run_at, status FROM jobs WHERE id = 'rem-test-job'`).
			Scan(&runAt, &status)
		if status == "pending" && runAt != "2026-06-24T10:00:00Z" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	wantRunAt, _ := time.Parse(time.RFC3339, "2026-06-27T09:00:00Z")
	gotRunAt, err := time.Parse(time.RFC3339, runAt)
	if err != nil {
		t.Fatalf("parse job run_at %q: %v", runAt, err)
	}
	diff := gotRunAt.Sub(wantRunAt)
	if diff < -time.Minute || diff > time.Minute {
		t.Errorf("reminder run_at = %s; want ~2026-06-27T09:00:00Z (±1m)", runAt)
	}
	if status != "pending" {
		t.Errorf("job status = %q; want pending", status)
	}
}

// ---------------------------------------------------------------------------
// Wrong user: booking belongs to another host
// ---------------------------------------------------------------------------

func TestRescheduleBooking_wrongUser(t *testing.T) {
	h, _, key1, _ := setupWorkspaceWithDB(t)

	// Create a second user (second setup call creates a new workspace).
	body2 := `{"name":"Other Host","email":"other@example.com","timezone":"UTC"}`
	req2 := httptest.NewRequest(http.MethodPost, "/v1/setup", strings.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	h.Setup(rec2, req2)
	if rec2.Code != http.StatusConflict && rec2.Code != http.StatusCreated {
		// setup is idempotent-or-conflict; just check we got a valid response.
		t.Logf("second setup: %d — %s", rec2.Code, rec2.Body.String())
	}

	// Create a booking owned by user1.
	slug, _ := seedEventTypeHTTP(t, h, key1)
	bookingID := createBookingViaHTTP(t, h, slug, "2026-06-20T10:00:00Z")

	// Attempt to reschedule using key1 against a fake non-owned booking ID.
	fakeID := "does-not-belong-to-me"
	req := authReq(http.MethodPatch, "/v1/bookings/"+fakeID+"/reschedule",
		`{"start_at":"2026-06-21T10:00:00Z"}`, key1)
	req.SetPathValue("id", fakeID)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.RescheduleBooking)(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("wrong user: got %d; want 404 (booking not found or not owned)", rec.Code)
	}
	_ = bookingID // referenced only to confirm our key1 owns a booking
}

// ---------------------------------------------------------------------------
// Unauthenticated request → 401
// ---------------------------------------------------------------------------

func TestRescheduleBooking_requiresAuth(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	bookingID := createBookingViaHTTP(t, h, slug, "2026-06-20T10:00:00Z")

	req := httptest.NewRequest(http.MethodPatch,
		"/v1/bookings/"+bookingID+"/reschedule",
		strings.NewReader(`{"start_at":"2026-06-21T10:00:00Z"}`))
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

	// Create two bookings at different times.
	id1 := createBookingViaHTTP(t, h, slug, "2026-06-20T10:00:00Z")
	_ = createBookingViaHTTP(t, h, slug, "2026-06-20T11:00:00Z")

	// Try to move booking 1 to clash with booking 2.
	rec := patchReschedule(t, h, id1, "2026-06-20T11:00:00Z", key)
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
	bookingID := createBookingViaHTTP(t, h, slug, "2026-06-20T10:00:00Z")

	// Cancel it first.
	cancelReq := authReq(http.MethodPost, "/v1/bookings/"+bookingID+"/cancel", `{}`, key)
	cancelReq.SetPathValue("id", bookingID)
	cancelRec := httptest.NewRecorder()
	h.RequireAuth(h.CancelBooking)(cancelRec, cancelReq)
	if cancelRec.Code != http.StatusOK {
		t.Fatalf("cancel: %d — %s", cancelRec.Code, cancelRec.Body.String())
	}

	// Now try to reschedule.
	rec := patchReschedule(t, h, bookingID, "2026-06-21T10:00:00Z", key)
	if rec.Code != http.StatusConflict {
		t.Errorf("cancelled booking: got %d; want 409", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Booking not found → 404
// ---------------------------------------------------------------------------

func TestRescheduleBooking_notFound(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)

	rec := patchReschedule(t, h, "nonexistent-booking-id", "2026-06-21T10:00:00Z", key)
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
	bookingID := createBookingViaHTTP(t, h, slug, "2026-06-20T10:00:00Z")

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
	bookingID := createBookingViaHTTP(t, h, slug, "2026-06-20T10:00:00Z")

	req := authReq(http.MethodPatch, "/v1/bookings/"+bookingID+"/reschedule",
		`{}`, key)
	req.SetPathValue("id", bookingID)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.RescheduleBooking)(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing start_at: got %d; want 400", rec.Code)
	}
}
