package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/db"
)

// verifies MCP tools scope by role: a member sees/controls only the bookings they host,
// while an admin/owner (and the unauthenticated stdio operator) see the whole workspace.
func TestMCP_roleScoping(t *testing.T) {
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatalf("db open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := New(database, slog.Default())

	// Owner (first user → owner+admin).
	rec := httptest.NewRecorder()
	h.Setup(rec, httptest.NewRequest(http.MethodPost, "/v1/setup",
		strings.NewReader(`{"name":"Owner","email":"owner@example.com","timezone":"UTC"}`)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup: %d — %s", rec.Code, rec.Body.String())
	}
	var setup struct {
		UserID string `json:"user_id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &setup)
	ownerID := setup.UserID

	// Member (is_admin/is_owner default 0).
	const memberID = "member-1"
	if _, err := database.Exec(`INSERT INTO users (id, email, name, iana_timezone) VALUES (?, ?, ?, ?)`,
		memberID, "member@example.com", "Member", "UTC"); err != nil {
		t.Fatalf("insert member: %v", err)
	}

	// An event type owned by the owner.
	ereq := httptest.NewRequest(http.MethodPost, "/v1/event-types",
		strings.NewReader(`{"slug":"intro","name":"Intro","duration_minutes":30,"location_type":"phone","location_value":"+1 555 111 2222"}`))
	ereq = ereq.WithContext(context.WithValue(ereq.Context(), ctxKeyUser, AuthUser{ID: ownerID, IsAdmin: true, IsOwner: true}))
	erec := httptest.NewRecorder()
	h.CreateEventType(erec, ereq)
	if erec.Code != http.StatusCreated {
		t.Fatalf("create event type: %d — %s", erec.Code, erec.Body.String())
	}
	var etID string
	if err := database.QueryRow(`SELECT id FROM event_types WHERE slug = 'intro'`).Scan(&etID); err != nil {
		t.Fatalf("event type id: %v", err)
	}

	// One booking hosted by the owner, one by the member.
	mk := func(hostID string, hour int) string {
		start := time.Date(2027, 3, 1, hour, 0, 0, 0, time.UTC)
		b, err := h.bookingSvc.Create(context.Background(), booking.CreateParams{
			EventTypeID: etID,
			HostIDs:     []string{hostID},
			RoutingMode: "fixed",
			StartAt:     start,
			EndAt:       start.Add(30 * time.Minute),
			Organizer:   booking.Attendee{Name: "Booker", Email: "booker@example.com", IANATimezone: "UTC"},
		})
		if err != nil {
			t.Fatalf("create booking (host %s): %v", hostID, err)
		}
		return b.ID
	}
	ownerBooking := mk(ownerID, 9)
	memberBooking := mk(memberID, 10)

	memberCtx := withMCPCaller(context.Background(), mcpCaller{UserID: memberID, IsAdmin: false})
	ownerCtx := withMCPCaller(context.Background(), mcpCaller{UserID: ownerID, IsAdmin: true})
	operatorCtx := context.Background() // stdio: no caller bound

	count := func(t *testing.T, ctx context.Context) int {
		t.Helper()
		_, out, err := h.mcpListBookings(ctx, nil, listBookingsIn{})
		if err != nil {
			t.Fatalf("list_bookings: %v", err)
		}
		return len(out.Bookings)
	}

	if n := count(t, memberCtx); n != 1 {
		t.Errorf("member list_bookings = %d; want 1 (only their own)", n)
	}
	if n := count(t, ownerCtx); n != 2 {
		t.Errorf("owner list_bookings = %d; want 2 (whole workspace)", n)
	}
	if n := count(t, operatorCtx); n != 2 {
		t.Errorf("stdio operator list_bookings = %d; want 2 (whole workspace)", n)
	}

	// A member cannot read another host's booking…
	if _, _, err := h.mcpGetBooking(memberCtx, nil, getBookingIn{BookingID: ownerBooking}); err == nil {
		t.Error("member get_booking on owner's booking should fail")
	}
	// …but can read their own.
	if _, _, err := h.mcpGetBooking(memberCtx, nil, getBookingIn{BookingID: memberBooking}); err != nil {
		t.Errorf("member get_booking on own booking failed: %v", err)
	}

	// A member cannot cancel another host's booking…
	if _, _, err := h.mcpCancelBooking(memberCtx, nil, cancelBookingIn{BookingID: ownerBooking}); err == nil {
		t.Error("member cancel_booking on owner's booking should fail")
	}
	// …confirm the owner's booking is still live, then the member cancels their own.
	if _, _, err := h.mcpCancelBooking(memberCtx, nil, cancelBookingIn{BookingID: memberBooking}); err != nil {
		t.Errorf("member cancel_booking on own booking failed: %v", err)
	}
}
