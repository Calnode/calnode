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

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/calnode/calnode/internal/calendar"
)

// TestBookingPhoneOutbound covers the "we'll call you" location: the attendee's number is
// required, a valid one is stored exactly like the allow_phone_call path (location_type
// "phone", "tel:<number>"), and a missing or invalid one is refused rather than downgraded
// to a Meet. A Google calendar that could mint a Meet link is connected on purpose, so a
// silent fallback to video would show up here.
func TestBookingPhoneOutbound(t *testing.T) {
	for _, tc := range []struct {
		name, phone string
		status      int
	}{
		{"valid", "+48 600 100 200", http.StatusCreated},
		{"missing", "", http.StatusBadRequest},
		{"blank", "   ", http.StatusBadRequest},
		{"invalid", "call me", http.StatusBadRequest},
		{"too long", "+48 " + strings.Repeat("1", 40), http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, db, key, userID := setupWorkspaceWithDB(t)
			slug, _ := seedEventTypeHTTP(t, h, key)
			// Through the API, so validateLocation and the widened CHECK are both exercised:
			// the type needs no host number.
			if code, out := patchET(t, h, key, slug, `{"location_type":"phone_outbound"}`); code != http.StatusOK {
				t.Fatalf("set phone_outbound: %d %v", code, out)
			}
			if _, err := db.Exec(`INSERT INTO calendar_connections (id,user_id,provider,access_token_enc,calendar_id,is_destination) VALUES ('conn',?,'google','test','primary',1)`, userID); err != nil {
				t.Fatal(err)
			}
			p := telephoneCalendar{events: make(chan calendar.CreateEventParams, 1)}
			svc := calendar.NewService(db)
			svc.Register(p)
			h.SetCalendar(svc)

			body := fmt.Sprintf(`{"event_type_slug":%q,"start_at":"2026-06-15T09:00:00Z","name":"Test","email":"test@example.com","phone":%q}`, slug, tc.phone)
			rec := httptest.NewRecorder()
			h.CreateBooking(rec, httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(body)))
			if rec.Code != tc.status {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}
			if tc.status != http.StatusCreated {
				var count int
				if err := db.QueryRow(`SELECT COUNT(*) FROM bookings`).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count != 0 {
					t.Fatalf("a %s number created a booking instead of being refused", tc.name)
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
			want := "tel:" + tc.phone
			var locType, locValue string
			if err := db.QueryRow(`SELECT location_type, location_value FROM bookings WHERE id = ?`, response.ID).Scan(&locType, &locValue); err != nil {
				t.Fatal(err)
			}
			if response.LocationType != "phone" || locType != "phone" || locValue != want {
				t.Fatalf("booking location: response %q, stored %q %q; want phone %q", response.LocationType, locType, locValue, want)
			}
			select {
			case event := <-p.events:
				if event.AddMeet || event.Location != want {
					t.Fatalf("calendar AddMeet=%v location=%q; want no Meet and %q", event.AddMeet, event.Location, want)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("calendar event was not created")
			}
		})
	}
}

// TestBookPage_phoneOutboundRequiresNumber checks that the booking page and the widget's
// event-type info both mark the number as required, and that the assistant (which cannot
// collect it) is off.
func TestBookPage_phoneOutboundRequiresNumber(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	if code, out := patchET(t, h, key, slug, `{"location_type":"phone_outbound"}`); code != http.StatusOK {
		t.Fatalf("set phone_outbound: %d %v", code, out)
	}

	req := httptest.NewRequest(http.MethodGet, "/book/"+slug, nil)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.BookPage(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("BookPage: %d", rec.Code)
	}
	page := rec.Body.String()
	if !strings.Contains(page, `data-phone-required="true"`) || !strings.Contains(page, `id="f-phone"`) {
		t.Error("booking page has no required phone field")
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/event-types/"+slug+"/public", nil)
	req.SetPathValue("slug", slug)
	rec = httptest.NewRecorder()
	h.PublicEventType(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PublicEventType: %d %s", rec.Code, rec.Body)
	}
	var info struct {
		PhoneRequired    bool   `json:"phone_required"`
		AllowPhoneCall   bool   `json:"allow_phone_call"`
		LocationType     string `json:"location_type"`
		AssistantEnabled bool   `json:"assistant_enabled"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if !info.PhoneRequired || info.AllowPhoneCall || info.LocationType != "phone_outbound" || info.AssistantEnabled {
		t.Errorf("public info = %+v; want phone_required, not allow_phone_call, assistant off", info)
	}
}

// TestMCP_createBooking_phoneOutboundRefused: MCP has no way to pass the attendee's number,
// so the shared booking core refuses instead of creating a call nobody can make.
func TestMCP_createBooking_phoneOutboundRefused(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)
	body := `{"slug":"mcp-we-call","name":"We call you","duration_minutes":30,"location_type":"phone_outbound","max_future_days":0}`
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateEventType)(rec, authReq(http.MethodPost, "/v1/event-types", body, apiKey))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create event type: %d — %s", rec.Code, rec.Body.String())
	}
	seedFullAvailability(t, h, apiKey)

	cs := connectMCP(t, h)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "create_booking", Arguments: map[string]any{
		"event_type_id":  "mcp-we-call",
		"slot_start":     "2027-01-15T10:00:00Z",
		"attendee_name":  "Pat Booker",
		"attendee_email": "pat@example.com",
	}})
	if err != nil {
		t.Fatalf("create_booking: %v", err)
	}
	if !res.IsError {
		t.Fatalf("create_booking on a phone_outbound event type succeeded: %+v", res.Content)
	}
}
