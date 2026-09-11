package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calnode/calnode/internal/handler"
)

// bookWithEmail posts a booking with the given raw email value and returns the response.
func bookWithEmail(t *testing.T, h *handler.Handler, slug, hhmm, email string) *httptest.ResponseRecorder {
	t.Helper()
	body := fmt.Sprintf(`{"event_type_slug":%q,"start_at":"2026-06-15T%s:00Z","name":"Bob","email":%q}`,
		slug, hhmm, email)
	req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateBooking(rec, req)
	return rec
}

// organizerEmail reads back the stored organizer address for the workspace's single
// booking. The public create response carries no attendees, so this goes through the
// admin list, which does.
func organizerEmail(t *testing.T, h *handler.Handler, apiKey string) string {
	t.Helper()
	req := authReq(http.MethodGet, "/v1/bookings", "", apiKey)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ListBookings)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/bookings: %d - %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Items []struct {
			Attendees []struct {
				Email string `json:"email"`
			} `json:"attendees"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode bookings: %v", err)
	}
	if len(out.Items) != 1 || len(out.Items[0].Attendees) != 1 {
		t.Fatalf("want exactly one booking with one organizer; got %+v", out.Items)
	}
	return out.Items[0].Attendees[0].Email
}

// TestCreateBooking_rejectsMalformedEmail: the booker's address ends up in an outbound
// message's To: header, so it is checked at intake rather than at the mailer alone. The
// emptiness check above it only proves the field was filled in.
func TestCreateBooking_rejectsMalformedEmail(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	for _, tc := range []struct {
		name  string
		email string
	}{
		{"not an address", "not-an-address"},
		{"header injection", "a@b.example\r\nBcc: x@y.example"},
		{"two addresses", "a@b.example, c@d.example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := bookWithEmail(t, h, slug, "09:00", tc.email)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("got %d - %s; want 400", rec.Code, rec.Body.String())
			}
			var resp struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if resp.Error != "email must be a valid email address" {
				t.Errorf("error = %q; want the plain intake sentence", resp.Error)
			}
		})
	}

	// Nothing was stored on any of those attempts.
	req := authReq(http.MethodGet, "/v1/bookings", "", key)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ListBookings)(rec, req)
	if strings.Contains(rec.Body.String(), "b.example") {
		t.Errorf("a rejected booking was persisted: %s", rec.Body.String())
	}
}

// TestCreateBooking_normalisesDisplayNameEmail: a pasted `Bob <bob@example.com>` is
// stored as the bare address. Deliberate — every later reader (the hourly throttle, the
// per-invitee cap, the To: header) treats the stored value as a plain address.
func TestCreateBooking_normalisesDisplayNameEmail(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	rec := bookWithEmail(t, h, slug, "09:00", " Bob <bob@example.com> ")
	if rec.Code != http.StatusCreated {
		t.Fatalf("got %d - %s; want 201", rec.Code, rec.Body.String())
	}
	if got := organizerEmail(t, h, key); got != "bob@example.com" {
		t.Errorf("stored organizer email = %q; want %q", got, "bob@example.com")
	}
}

// TestCreateBooking_ordinaryEmailUnchanged: normalisation must not rewrite the common case.
func TestCreateBooking_ordinaryEmailUnchanged(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	rec := bookWithEmail(t, h, slug, "09:00", "Bob.Booker@example.com")
	if rec.Code != http.StatusCreated {
		t.Fatalf("got %d - %s; want 201", rec.Code, rec.Body.String())
	}
	if got := organizerEmail(t, h, key); got != "Bob.Booker@example.com" {
		t.Errorf("stored organizer email = %q; want it unchanged", got)
	}
}
