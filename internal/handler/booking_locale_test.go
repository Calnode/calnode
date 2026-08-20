package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCreateBooking_storesAttendeeLocale verifies the "language" field on POST /v1/bookings
// is captured onto booking_attendees.locale, and that an unsupported/missing value falls
// back to English rather than being stored as-is or left empty.
func TestCreateBooking_storesAttendeeLocale(t *testing.T) {
	requireUnsupported(t)
	h, database, apiKey, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, apiKey)

	create := func(startAt, language string) string {
		t.Helper()
		body := fmt.Sprintf(`{
			"event_type_slug": %q,
			"start_at": %q,
			"name": "Test Attendee",
			"email": "attendee@example.com",
			"timezone": "UTC",
			"language": %q
		}`, slug, startAt, language)
		req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.CreateBooking(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("createBooking: got %d — %s", rec.Code, rec.Body.String())
		}
		var resp struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return resp.ID
	}

	localeOf := func(bookingID string) string {
		t.Helper()
		var locale string
		if err := database.QueryRow(
			`SELECT locale FROM booking_attendees WHERE booking_id = ? AND is_organizer = 1`, bookingID,
		).Scan(&locale); err != nil {
			t.Fatalf("query stored locale: %v", err)
		}
		return locale
	}

	esID := create("2026-06-20T09:00:00Z", "es")
	if got := localeOf(esID); got != "es" {
		t.Errorf("language:\"es\" should store locale \"es\", got %q", got)
	}

	unsupportedID := create("2026-06-20T10:00:00Z", unsupportedLocaleCodes[0])
	if got := localeOf(unsupportedID); got != "en" {
		t.Errorf("unsupported language should fall back to English, got %q", got)
	}

	missingID := create("2026-06-20T11:00:00Z", "")
	if got := localeOf(missingID); got != "en" {
		t.Errorf("missing language should default to English, got %q", got)
	}
}
