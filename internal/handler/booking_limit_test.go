package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calnode/calnode/internal/handler"
	"github.com/calnode/calnode/internal/uid"
)

// seedEventTypeWithCap creates an event type with an explicit max_active_bookings.
func seedEventTypeWithCap(t *testing.T, h *handler.Handler, apiKey string, cap int) string {
	t.Helper()
	slug := "cap-meeting-" + uid.New()[:8]
	body := fmt.Sprintf(`{"slug":%q,"name":"Cap Meeting","duration_minutes":30,"max_active_bookings":%d,"max_future_days":0}`, slug, cap)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateEventType)(rec, authReq(http.MethodPost, "/v1/event-types", body, apiKey))
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed event type w/ cap %d: got %d — %s", cap, rec.Code, rec.Body.String())
	}
	seedFullAvailability(t, h, apiKey)
	return slug
}

// postBooking submits a public booking and returns the recorder for status assertions.
func postBooking(t *testing.T, h *handler.Handler, slug, startAt, name, email string) *httptest.ResponseRecorder {
	t.Helper()
	body := fmt.Sprintf(`{"event_type_slug":%q,"start_at":%q,"name":%q,"email":%q,"timezone":"UTC"}`,
		slug, startAt, name, email)
	req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateBooking(rec, req)
	return rec
}

// TestCreateBooking_maxActiveBookingsLimit verifies the per-invitee active-booking
// cap: with max_active_bookings=1 a single email may hold only one upcoming booking,
// the limit is keyed by email (case-insensitively) and scoped per invitee.
func TestCreateBooking_maxActiveBookingsLimit(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)
	slug := seedEventTypeWithCap(t, h, apiKey, 1)

	// Distinct, non-overlapping future slots so the double-booking guard (409)
	// never fires — any rejection here must come from the active-booking cap (422).
	const (
		slot1 = "2027-03-01T10:00:00Z"
		slot2 = "2027-03-01T12:00:00Z"
		slot3 = "2027-03-01T14:00:00Z"
	)

	// First booking for Alice succeeds.
	if rec := postBooking(t, h, slug, slot1, "Alice", "alice@example.com"); rec.Code != http.StatusCreated {
		t.Fatalf("first booking: got %d — %s", rec.Code, rec.Body.String())
	}

	// Second booking for Alice (different slot) is rejected by the cap with 422.
	rec := postBooking(t, h, slug, slot2, "Alice", "alice@example.com")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("second booking (same invitee): got %d; want 422 — %s", rec.Code, rec.Body.String())
	}
	var errResp struct {
		Error string `json:"error"`
	}
	json.Unmarshal(rec.Body.Bytes(), &errResp)
	if !strings.Contains(errResp.Error, "maximum number of upcoming bookings") {
		t.Errorf("error message = %q; want it to mention the booking limit", errResp.Error)
	}

	// A different invitee is unaffected — the cap is per email.
	if rec := postBooking(t, h, slug, slot2, "Bob", "bob@example.com"); rec.Code != http.StatusCreated {
		t.Fatalf("different invitee booking: got %d; want 201 — %s", rec.Code, rec.Body.String())
	}

	// Email match is case-insensitive: ALICE is the same invitee as alice.
	if rec := postBooking(t, h, slug, slot3, "Alice", "ALICE@example.com"); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("case-insensitive invitee: got %d; want 422 — %s", rec.Code, rec.Body.String())
	}
}

// TestCreateBooking_unlimitedActiveBookings verifies max_active_bookings=0 disables
// the cap so one invitee may hold several upcoming bookings.
func TestCreateBooking_unlimitedActiveBookings(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)
	slug := seedEventTypeWithCap(t, h, apiKey, 0)

	for i, slot := range []string{"2027-04-01T09:00:00Z", "2027-04-01T11:00:00Z", "2027-04-01T13:00:00Z"} {
		if rec := postBooking(t, h, slug, slot, "Alice", "alice@example.com"); rec.Code != http.StatusCreated {
			t.Fatalf("booking %d with unlimited cap: got %d — %s", i+1, rec.Code, rec.Body.String())
		}
	}
}
