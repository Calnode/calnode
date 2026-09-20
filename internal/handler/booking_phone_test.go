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

	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/slots"
)

type telephoneCalendar struct {
	calendar.Provider
	events chan calendar.CreateEventParams
}

func (p telephoneCalendar) Name() string                                         { return "google" }
func (p telephoneCalendar) InvitesGuests() bool                                  { return true }
func (p telephoneCalendar) HasDestination(context.Context, string) (bool, error) { return true, nil }
func (p telephoneCalendar) FreeBusy(context.Context, string, time.Time, time.Time) ([]slots.Interval, error) {
	return nil, nil
}
func (p telephoneCalendar) CreateEvent(_ context.Context, _ string, in calendar.CreateEventParams) (string, string, string, error) {
	p.events <- in
	return "event-id", "", "primary", nil
}

func TestBookingTelephoneChoice(t *testing.T) {
	for _, tc := range []struct {
		name, phone, locationType, location string
		status                              int
		meet                                bool
	}{
		{"telephone", "+31 6 12345678", "phone", "tel:+31 6 12345678", 201, false},
		{"meet", "", "google_meet", "", 201, true},
		{"invalid", "call me", "", "", 400, false},
		{"disabled", "+31 6 12345678", "", "", 400, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, db, key, userID := setupWorkspaceWithDB(t)
			_, eventID := seedEventTypeHTTP(t, h, key)
			if _, err := db.Exec(`UPDATE event_types SET slug = 'studiozoek-kennismaking', location_type = 'google_meet', allow_phone_call = 1 WHERE id = ?`, eventID); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO calendar_connections (id,user_id,provider,access_token_enc,calendar_id,is_destination) VALUES ('conn',?,'google','test','primary',1)`, userID); err != nil {
				t.Fatal(err)
			}
			if tc.name == "disabled" {
				if _, err := db.Exec(`UPDATE event_types SET allow_phone_call = 0 WHERE id = ?`, eventID); err != nil {
					t.Fatal(err)
				}
			}
			p := telephoneCalendar{events: make(chan calendar.CreateEventParams, 1)}
			svc := calendar.NewService(db)
			svc.Register(p)
			h.SetCalendar(svc)
			body := fmt.Sprintf(`{"event_type_slug":"studiozoek-kennismaking","start_at":"2026-06-15T09:00:00Z","name":"Test","email":"test@example.com","phone":%q}`, tc.phone)
			req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(body))
			rec := httptest.NewRecorder()
			h.CreateBooking(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}
			if tc.status != 201 {
				var count int
				db.QueryRow(`SELECT COUNT(*) FROM bookings`).Scan(&count)
				if count != 0 {
					t.Fatal("invalid number created a booking")
				}
				return
			}
			var response struct {
				ID           string `json:"id"`
				LocationType string `json:"location_type"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.LocationType != tc.locationType {
				t.Fatalf("response location type %q", response.LocationType)
			}
			var locType, loc string
			if err := db.QueryRow(`SELECT location_type, location_value FROM bookings WHERE id = ?`, response.ID).Scan(&locType, &loc); err != nil {
				t.Fatal(err)
			}
			if locType != tc.locationType || loc != tc.location {
				t.Fatalf("stored location %q %q", locType, loc)
			}
			select {
			case event := <-p.events:
				if event.AddMeet != tc.meet || event.Location != tc.location {
					t.Fatalf("calendar AddMeet=%v location=%q", event.AddMeet, event.Location)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("calendar event was not created")
			}
		})
	}
}
