package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCreateBooking_idempotentReplay verifies that retrying a booking POST with
// the same Idempotency-Key replays the original response (same booking id, 201)
// rather than creating a second booking.
func TestCreateBooking_idempotentReplay(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	body := fmt.Sprintf(`{"event_type_slug":%q,"start_at":"2026-06-15T09:00:00Z","name":"Alice","email":"alice@example.com"}`, slug)
	do := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "key-abc-123")
		rec := httptest.NewRecorder()
		h.CreateBooking(rec, req)
		return rec
	}

	rec1 := do()
	if rec1.Code != http.StatusCreated {
		t.Fatalf("first: %d — %s", rec1.Code, rec1.Body.String())
	}
	rec2 := do()
	if rec2.Code != http.StatusCreated {
		t.Fatalf("replay: %d — %s", rec2.Code, rec2.Body.String())
	}
	if rec2.Header().Get("Idempotency-Replayed") != "true" {
		t.Error("replay response should carry Idempotency-Replayed: true")
	}

	var b1, b2 map[string]any
	json.Unmarshal(rec1.Body.Bytes(), &b1)
	json.Unmarshal(rec2.Body.Bytes(), &b2)
	if b1["id"] == nil || b1["id"] == "" {
		t.Fatalf("first response has no booking id")
	}
	if b1["id"] != b2["id"] {
		t.Errorf("replay must return the same booking id: %v vs %v", b1["id"], b2["id"])
	}
}

// TestCreateBooking_idempotencyKeyReusedDifferentBody verifies that a key reused
// with a different payload is rejected (422) rather than silently mismatching.
func TestCreateBooking_idempotencyKeyReusedDifferentBody(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	post := func(start string) *httptest.ResponseRecorder {
		body := fmt.Sprintf(`{"event_type_slug":%q,"start_at":%q,"name":"Alice","email":"alice@example.com"}`, slug, start)
		req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "reuse-key")
		rec := httptest.NewRecorder()
		h.CreateBooking(rec, req)
		return rec
	}

	if rec := post("2026-06-15T09:00:00Z"); rec.Code != http.StatusCreated {
		t.Fatalf("first: %d — %s", rec.Code, rec.Body.String())
	}
	// Same key, different start time → the request body no longer matches.
	if rec := post("2026-06-15T10:00:00Z"); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("reused key with different body: %d; want 422", rec.Code)
	}
}

// TestCreateBooking_idempotentRetryAfterConflict verifies that a *different* key
// for the same slot still hits the double-booking guard (the key changes the
// retry identity, not the slot's availability).
func TestCreateBooking_differentKeysSameSlot(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	post := func(idemKey string) *httptest.ResponseRecorder {
		body := fmt.Sprintf(`{"event_type_slug":%q,"start_at":"2026-06-15T09:00:00Z","name":"Alice","email":"alice@example.com"}`, slug)
		req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", idemKey)
		rec := httptest.NewRecorder()
		h.CreateBooking(rec, req)
		return rec
	}

	if rec := post("key-one"); rec.Code != http.StatusCreated {
		t.Fatalf("first: %d — %s", rec.Code, rec.Body.String())
	}
	if rec := post("key-two"); rec.Code != http.StatusConflict {
		t.Errorf("second key, same slot: %d; want 409", rec.Code)
	}
}
