package handler_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCreateBooking_honeypotRejected: a filled honeypot ("company") field marks an
// automated submission and is rejected without creating a booking.
func TestCreateBooking_honeypotRejected(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	body := fmt.Sprintf(`{"event_type_slug":%q,"start_at":"2026-06-15T09:00:00Z","name":"Bot","email":"bot@example.com","company":"Acme Spam Co"}`, slug)
	req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateBooking(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("honeypot-filled booking: %d; want 400", rec.Code)
	}
}

// TestCreateBooking_perEmailThrottle: one email can create up to the hourly cap
// across distinct slots, then is throttled with 429.
func TestCreateBooking_perEmailThrottle(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	book := func(hhmm string) int {
		body := fmt.Sprintf(`{"event_type_slug":%q,"start_at":"2026-06-15T%s:00Z","name":"Sam","email":"sam@example.com"}`, slug, hhmm)
		req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.CreateBooking(rec, req)
		return rec.Code
	}

	// 10 distinct slots (the cap), all same email — all succeed.
	times := []string{"09:00", "09:30", "10:00", "10:30", "11:00", "11:30", "12:00", "12:30", "13:00", "13:30"}
	for i, hhmm := range times {
		if code := book(hhmm); code != http.StatusCreated {
			t.Fatalf("booking %d (%s): %d; want 201", i+1, hhmm, code)
		}
	}
	// 11th distinct slot, same email → over the hourly cap.
	if code := book("14:00"); code != http.StatusTooManyRequests {
		t.Errorf("11th booking from same email: %d; want 429", code)
	}
}
