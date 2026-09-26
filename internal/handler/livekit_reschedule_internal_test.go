package handler

import (
	"context"
	"log/slog"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/db"
	"github.com/calnode/calnode/internal/livekit"
)

func newLiveKitTestHandler(t *testing.T, withLiveKit bool) *Handler {
	t.Helper()
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := db.Migrate(database); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	h := New(database, slog.Default())
	h.baseURL = "https://calnode.example.com"
	if withLiveKit {
		h.SetLiveKit(livekit.New("wss://lk.example.com", "key", "secret", [32]byte{1, 2, 3}))
	}
	ctx := context.Background()
	database.ExecContext(ctx,
		`INSERT INTO users (id, email, name, iana_timezone) VALUES ('u1','h@example.com','Host','UTC')`) //nolint:errcheck
	database.ExecContext(ctx,
		`INSERT INTO event_types (id, user_id, slug, name, duration_minutes) VALUES ('et1','u1','meet','Meet',30)`) //nolint:errcheck
	return h
}

func seedLiveKitBooking(t *testing.T, h *Handler, end time.Time, room, location string) booking.Booking {
	t.Helper()
	ctx := context.Background()
	start := end.Add(-30 * time.Minute)
	_, err := h.db.ExecContext(ctx,
		`INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at, status, location_value, livekit_room)
		 VALUES ('b1','et1','u1',?,?, 'confirmed',?,?)`,
		start.Format(time.RFC3339), end.Format(time.RFC3339), location, room)
	if err != nil {
		t.Fatalf("seed booking: %v", err)
	}
	return booking.Booking{ID: "b1", EventTypeID: "et1", HostID: "u1",
		StartAt: start, EndAt: end, Status: "confirmed", LocationValue: location}
}

// TestRemintLiveKitLinks_refreshesExpiredURLs is the #98 regression: after a reschedule
// past the original end, the stored join URL must carry the new expiry.
func TestRemintLiveKitLinks_refreshesExpiredURLs(t *testing.T) {
	h := newLiveKitTestHandler(t, true)
	ctx := context.Background()
	lk := h.getLiveKit()

	oldEnd := time.Now().UTC().Add(-time.Hour) // original meeting already over
	oldURL := lk.BookingJoinURL(h.baseURL, "booking-b1", "", oldEnd.Add(2*time.Hour))
	b := seedLiveKitBooking(t, h, oldEnd, "booking-b1", oldURL)

	// Reschedule to tomorrow: same room, new end.
	b.EndAt = time.Now().UTC().Add(25 * time.Hour).Truncate(time.Second)
	b.StartAt = b.EndAt.Add(-30 * time.Minute)
	h.remintLiveKitLinks(ctx, &b)

	var stored string
	if err := h.db.QueryRowContext(ctx, `SELECT location_value FROM bookings WHERE id = 'b1'`).Scan(&stored); err != nil {
		t.Fatalf("read location: %v", err)
	}
	if stored == oldURL {
		t.Fatal("location_value unchanged after reschedule; want a re-minted URL")
	}
	if b.LocationValue != stored {
		t.Error("in-memory booking not updated alongside the stored row")
	}
	u, err := url.Parse(stored)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	if !strings.HasPrefix(u.Path, "/room/booking-b1") {
		t.Errorf("URL path = %q; want the same room", u.Path)
	}
	lc := livekit.New("wss://lk.example.com", "key", "secret", [32]byte{1, 2, 3})
	_, _, exp, err := lc.VerifyRoomToken(u.Query().Get("t"))
	if err != nil {
		t.Fatalf("fresh token does not verify: %v", err)
	}
	if !exp.Equal(b.EndAt.Add(2 * time.Hour)) {
		t.Errorf("token expiry = %v; want new end + 2h", exp)
	}
}

func TestRemintLiveKitLinks_skipsWhenNothingToDo(t *testing.T) {
	ctx := context.Background()
	end := time.Now().UTC().Add(time.Hour)

	// LiveKit disabled: untouched.
	h := newLiveKitTestHandler(t, false)
	b := seedLiveKitBooking(t, h, end, "booking-b1", "https://calnode.example.com/room/booking-b1?t=old")
	h.remintLiveKitLinks(ctx, &b)
	var stored string
	h.db.QueryRowContext(ctx, `SELECT location_value FROM bookings WHERE id = 'b1'`).Scan(&stored) //nolint:errcheck
	if stored != "https://calnode.example.com/room/booking-b1?t=old" {
		t.Errorf("location changed with LiveKit disabled: %q", stored)
	}

	// LiveKit on but no room minted (non-LiveKit booking): untouched.
	h2 := newLiveKitTestHandler(t, true)
	b2 := seedLiveKitBooking(t, h2, end, "", "https://meet.example.com/manual")
	h2.remintLiveKitLinks(ctx, &b2)
	h2.db.QueryRowContext(ctx, `SELECT location_value FROM bookings WHERE id = 'b1'`).Scan(&stored) //nolint:errcheck
	if stored != "https://meet.example.com/manual" {
		t.Errorf("non-LiveKit location changed: %q", stored)
	}
}
