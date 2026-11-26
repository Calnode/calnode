package handler_test

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/handler"
)

// createBookingForDelete books the seeded slot through the public endpoint, then moves the
// booking to now+offset, which frees the slot for the next one and keeps the tests
// independent of today's date.
func createBookingForDelete(t *testing.T, h *handler.Handler, database *sql.DB, slug, email string, offset time.Duration) string {
	t.Helper()
	body := fmt.Sprintf(`{"event_type_slug":%q,"start_at":"2026-06-15T11:00:00Z","name":"Carol","email":%q}`, slug, email)
	req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateBooking(rec, req)
	id := mustString(t, mustCreated(t, rec, "create booking"), "id", "create booking")
	setBookingWindow(t, database, id, offset)
	return id
}

func deleteBooking(h *handler.Handler, id, key string) *httptest.ResponseRecorder {
	req := authReq(http.MethodDelete, "/v1/bookings/"+id, "", key)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.DeleteBooking)(rec, req)
	return rec
}

func countRows(t *testing.T, database *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := database.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

// setBookingWindow moves a booking to the given time (relative to now), so the tests do
// not depend on today's date.
func setBookingWindow(t *testing.T, database *sql.DB, id string, start time.Duration) {
	t.Helper()
	s := time.Now().UTC().Add(start)
	if _, err := database.Exec(`UPDATE bookings SET start_at = ?, end_at = ? WHERE id = ?`,
		s.Format(time.RFC3339Nano), s.Add(30*time.Minute).Format(time.RFC3339Nano), id); err != nil {
		t.Fatal(err)
	}
}

// TestDeleteBooking_erasesBookingAndEverythingThatNamesThePerson: a past booking is
// removed together with every row that holds the attendee's data, and only those;
// a second booking stays untouched. Nothing new is queued (no webhook, no email job).
func TestDeleteBooking_erasesBookingAndEverythingThatNamesThePerson(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	id := createBookingForDelete(t, h, database, slug, "carol@example.com", -48*time.Hour)
	other := createBookingForDelete(t, h, database, slug, "dana@example.com", -24*time.Hour)

	var ownerID string
	if err := database.QueryRow(`SELECT host_id FROM bookings WHERE id = ?`, id).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	// Data that references the booking without a cascade, as the real paths write it.
	for _, q := range []string{
		`INSERT INTO webhooks (id, user_id, url, events, secret_enc) VALUES ('wh1', '` + ownerID + `', 'https://example.test/hook', '["booking.created"]', 'x')`,
		`INSERT INTO webhook_deliveries (id, webhook_id, booking_id, event, payload, status) VALUES ('d1', 'wh1', '` + id + `', 'booking.created', '{"attendee_email":"carol@example.com"}', 'success')`,
		`INSERT INTO webhook_deliveries (id, webhook_id, booking_id, event, payload, status) VALUES ('d2', 'wh1', '` + other + `', 'booking.created', '{"attendee_email":"dana@example.com"}', 'success')`,
		`INSERT INTO jobs (id, type, payload, run_at) VALUES ('j1', 'webhook.deliver', '{"webhook_delivery_id":"d1"}', '2030-01-01T00:00:00Z')`,
		`INSERT INTO jobs (id, type, payload, run_at) VALUES ('j2', 'webhook.deliver', '{"webhook_delivery_id":"d2"}', '2030-01-01T00:00:00Z')`,
		`INSERT INTO jobs (id, type, payload, run_at) VALUES ('j3', 'reminder.send', '{"booking_id":"` + id + `"}', '2030-01-01T00:00:00Z')`,
		`INSERT INTO idempotency_keys (idempotency_key, request_hash, status_code, response_body, booking_id, created_at) VALUES ('k1', 'h', 201, '{"email":"carol@example.com"}', '` + id + `', '2026-01-01')`,
		`INSERT INTO notes (id, booking_id, content) VALUES ('n1', '` + id + `', 'Carol wants a call back')`,
	} {
		if _, err := database.Exec(q); err != nil {
			t.Fatalf("seed: %v\n%s", err, q)
		}
	}
	if countRows(t, database, `SELECT COUNT(*) FROM booking_attendees WHERE booking_id = ?`, id) == 0 {
		t.Fatal("fixture: booking has no attendee row")
	}

	rec := deleteBooking(h, id, key)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d — %s", rec.Code, rec.Body.String())
	}

	for _, q := range []string{
		`SELECT COUNT(*) FROM bookings WHERE id = ?`,
		`SELECT COUNT(*) FROM booking_attendees WHERE booking_id = ?`,
		`SELECT COUNT(*) FROM booking_answers WHERE booking_id = ?`,
		`SELECT COUNT(*) FROM booking_manage_tokens WHERE booking_id = ?`,
		`SELECT COUNT(*) FROM booking_hosts WHERE booking_id = ?`,
		`SELECT COUNT(*) FROM webhook_deliveries WHERE booking_id = ?`,
		`SELECT COUNT(*) FROM idempotency_keys WHERE booking_id = ?`,
		`SELECT COUNT(*) FROM notes WHERE booking_id = ?`,
		`SELECT COUNT(*) FROM jobs WHERE json_extract(payload, '$.booking_id') = ?`,
	} {
		if n := countRows(t, database, q, id); n != 0 {
			t.Errorf("%s: %d rows left", q, n)
		}
	}
	if n := countRows(t, database, `SELECT COUNT(*) FROM jobs WHERE id = 'j1'`); n != 0 {
		t.Errorf("webhook job for the deleted booking's delivery is still queued")
	}
	if n := countRows(t, database, `SELECT COUNT(*) FROM bookings WHERE id = ?`, other); n != 1 {
		t.Errorf("the other booking was touched")
	}
	if n := countRows(t, database, `SELECT COUNT(*) FROM jobs WHERE id = 'j2'`); n != 1 {
		t.Errorf("the other booking's webhook job was removed")
	}
	if n := countRows(t, database, `SELECT COUNT(*) FROM webhook_deliveries WHERE id = 'd2'`); n != 1 {
		t.Errorf("the other booking's webhook delivery was removed")
	}
	// Deleting queues nothing: no webhook delivery and no job for the deleted booking.
	// (Counting all jobs would race with the asynchronous side effects of the bookings
	// created above, which may still be enqueuing their own webhooks.)
	if n := countRows(t, database, `SELECT COUNT(*) FROM webhook_deliveries WHERE event = 'booking.cancelled'`); n != 0 {
		t.Errorf("delete fired a cancellation webhook")
	}

	// Gone means gone: a second delete is a 404, not a silent success.
	if rec := deleteBooking(h, id, key); rec.Code != http.StatusNotFound {
		t.Errorf("second delete: %d; want 404", rec.Code)
	}
}

// TestDeleteBooking_refusesWhatMustComeFirst: each refusal names the step to take first.
func TestDeleteBooking_refusesWhatMustComeFirst(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	t.Run("upcoming confirmed booking: cancel first", func(t *testing.T) {
		id := createBookingForDelete(t, h, database, slug, "up@example.com", 72*time.Hour)
		rec := deleteBooking(h, id, key)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "cancel_first") {
			t.Fatalf("delete upcoming: %d — %s", rec.Code, rec.Body.String())
		}
		// Once cancelled, it can be deleted (no calendar connected, so nothing pending).
		if _, err := database.Exec(`UPDATE bookings SET status = 'cancelled' WHERE id = ?`, id); err != nil {
			t.Fatal(err)
		}
		if rec := deleteBooking(h, id, key); rec.Code != http.StatusNoContent {
			t.Fatalf("delete after cancel: %d — %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("cancelled booking whose calendar event is still being removed", func(t *testing.T) {
		id := createBookingForDelete(t, h, database, slug, "cal@example.com", 72*time.Hour)
		if _, err := database.Exec(`UPDATE bookings SET status = 'cancelled' WHERE id = ?`, id); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`UPDATE booking_hosts SET external_event_id = 'evt_1' WHERE booking_id = ?`, id); err != nil {
			t.Fatal(err)
		}
		rec := deleteBooking(h, id, key)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "calendar_pending") {
			t.Fatalf("delete with pending calendar event: %d — %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("paid booking keeps its record", func(t *testing.T) {
		id := createBookingForDelete(t, h, database, slug, "paid@example.com", -72*time.Hour)
		if _, err := database.Exec(`UPDATE bookings SET payment_status = 'paid' WHERE id = ?`, id); err != nil {
			t.Fatal(err)
		}
		rec := deleteBooking(h, id, key)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "paid_booking") {
			t.Fatalf("delete paid: %d — %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("recordings first", func(t *testing.T) {
		id := createBookingForDelete(t, h, database, slug, "rec@example.com", -96*time.Hour)
		if _, err := database.Exec(`INSERT INTO recordings (id, booking_id, room, egress_id, status, object_key) VALUES ('r1', ?, 'booking-x', 'eg1', 'complete', 'recordings/x.mp4')`, id); err != nil {
			t.Fatal(err)
		}
		rec := deleteBooking(h, id, key)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "recordings_first") {
			t.Fatalf("delete with recording: %d — %s", rec.Code, rec.Body.String())
		}
		if n := countRows(t, database, `SELECT COUNT(*) FROM bookings WHERE id = ?`, id); n != 1 {
			t.Errorf("refused delete still removed the booking")
		}
	})

	t.Run("unknown booking", func(t *testing.T) {
		if rec := deleteBooking(h, "does-not-exist", key); rec.Code != http.StatusNotFound {
			t.Fatalf("delete unknown: %d", rec.Code)
		}
	})
}

// TestDeleteBooking_adminOnly: a member cannot erase bookings, not even their own.
func TestDeleteBooking_adminOnly(t *testing.T) {
	h, database, _, _ := setupWorkspaceWithDB(t)
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u2','miri@example.com','Miri','UTC',0)`) //nolint:errcheck
	const memberKey = "cno_memberkey"
	database.Exec(`INSERT INTO api_keys (id,user_id,name,key_hash,created_at) VALUES ('k2','u2','test',?,'2024-01-01')`, sha256HexForTest(memberKey)) //nolint:errcheck
	slug, _ := seedEventTypeHTTP(t, h, memberKey)
	id := createBookingForDelete(t, h, database, slug, "carol@example.com", -48*time.Hour)

	if rec := deleteBooking(h, id, memberKey); rec.Code != http.StatusForbidden {
		t.Fatalf("member delete: %d; want 403", rec.Code)
	}
	if n := countRows(t, database, `SELECT COUNT(*) FROM bookings WHERE id = ?`, id); n != 1 {
		t.Errorf("forbidden delete removed the booking")
	}
}
