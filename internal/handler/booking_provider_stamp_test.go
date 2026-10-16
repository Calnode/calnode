package handler_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/slots"
)

type stampCalendar struct {
	calendar.Provider
	created chan struct{}
}

func (p stampCalendar) Name() string                                         { return "google" }
func (p stampCalendar) InvitesGuests() bool                                  { return true }
func (p stampCalendar) HasDestination(context.Context, string) (bool, error) { return true, nil }
func (p stampCalendar) FreeBusy(context.Context, string, time.Time, time.Time) ([]slots.Interval, error) {
	return nil, nil
}
func (p stampCalendar) CreateEvent(context.Context, string, calendar.CreateEventParams) (string, string, string, error) {
	p.created <- struct{}{}
	return "event-id", "", "primary", nil
}

// TestBookingStampsEventProvider is the write half of the issue #58 fix: the
// provider that writes the event is recorded on booking_hosts so later updates
// and cancels route back to it after a destination move. Calendar creation runs
// in a background goroutine, so the test waits for the create before asserting.
func TestBookingStampsEventProvider(t *testing.T) {
	h, db, key, userID := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	if _, err := db.Exec(`INSERT INTO calendar_connections (id,user_id,provider,access_token_enc,calendar_id,is_destination) VALUES ('conn',?,'google','test','primary',1)`, userID); err != nil {
		t.Fatal(err)
	}
	p := stampCalendar{created: make(chan struct{}, 1)}
	svc := calendar.NewService(db)
	svc.Register(p)
	h.SetCalendar(svc)

	body := fmt.Sprintf(`{"event_type_slug":%q,"start_at":"2026-06-15T09:00:00Z","name":"Test","email":"test@example.com"}`, slug)
	req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateBooking(rec, req)
	if rec.Code != 201 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	select {
	case <-p.created:
	case <-time.After(5 * time.Second):
		t.Fatal("calendar event was not created")
	}
	var provider string
	if err := db.QueryRow(`SELECT COALESCE(external_provider,'') FROM booking_hosts WHERE external_event_id = 'event-id'`).Scan(&provider); err != nil {
		t.Fatal(err)
	}
	if provider != "google" {
		t.Fatalf("external_provider = %q, want google", provider)
	}
}
