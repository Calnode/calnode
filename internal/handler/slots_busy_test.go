package handler

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/db"
)

// TestHostAvailability_includesAdjacentUTCDayBooking is the regression guard for
// the timezone-boundary slot bug. Slots are generated per host-local day, but
// bookings are stored in UTC: a 10:00 booking for a UTC+12 host (e.g. NZ) lands
// on the PREVIOUS UTC day. The busy-interval fetch must still include it, or slot
// generation would offer a slot the host is already booked for (then 409 at
// booking time). Everything is stored UTC — this guards the fetch *window*.
func TestHostAvailability_includesAdjacentUTCDayBooking(t *testing.T) {
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := New(database, slog.New(slog.DiscardHandler))

	// Host in NZ; a booking at 18 Jun 10:00 NZST = 17 Jun 22:00Z (previous UTC day).
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin,is_owner) VALUES ('u1','a@example.com','A','Pacific/Auckland',1,1)`)
	database.Exec(`INSERT INTO event_types (id,user_id,slug,name,duration_minutes) VALUES ('et1','u1','s','S',30)`)
	database.Exec(`INSERT INTO bookings (id,event_type_id,host_id,start_at,end_at,status)
		VALUES ('b1','et1','u1','2027-06-17T22:00:00Z','2027-06-17T22:30:00Z','confirmed')`)
	// Busy is computed from booking_hosts (every attended seat, not just primary),
	// matching how real bookings are recorded.
	database.Exec(`INSERT INTO booking_hosts (id,booking_id,user_id,is_primary) VALUES ('bh1','b1','u1',1)`)

	// The booker requests slots for local day 18 Jun → UTC-midnight window.
	dateFrom := time.Date(2027, 6, 18, 0, 0, 0, 0, time.UTC)
	ha, err := h.hostAvailability(context.Background(), "u1", "et1", dateFrom, dateFrom)
	if err != nil {
		t.Fatalf("hostAvailability: %v", err)
	}

	want := time.Date(2027, 6, 17, 22, 0, 0, 0, time.UTC)
	found := false
	for _, iv := range ha.Busy {
		if iv.Start.UTC().Equal(want) {
			found = true
		}
	}
	if !found {
		t.Errorf("busy intervals %v are missing the adjacent-UTC-day booking at %s — slot generation would wrongly offer that time", ha.Busy, want)
	}
}

// TestHostAvailability_includesNonPrimaryGroupSeat guards the multi-host bug: a
// host who attends a Group / fixed-host booking as a NON-primary must still be
// counted busy for that time (busy is computed from booking_hosts, not just
// bookings.host_id) — otherwise their slots on other events stay open and they
// get double-booked.
func TestHostAvailability_includesNonPrimaryGroupSeat(t *testing.T) {
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := New(database, slog.New(slog.DiscardHandler))

	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin,is_owner) VALUES ('u1','a@example.com','A','UTC',1,1)`)
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone) VALUES ('u2','b@example.com','B','UTC')`)
	database.Exec(`INSERT INTO event_types (id,user_id,slug,name,duration_minutes) VALUES ('et1','u1','s','S',30)`)
	// A booking primarily hosted by u2, with u1 attending as a non-primary host.
	database.Exec(`INSERT INTO bookings (id,event_type_id,host_id,start_at,end_at,status)
		VALUES ('b2','et1','u2','2027-06-18T03:00:00Z','2027-06-18T03:30:00Z','confirmed')`)
	database.Exec(`INSERT INTO booking_hosts (id,booking_id,user_id,is_primary) VALUES ('bh-p','b2','u2',1)`)
	database.Exec(`INSERT INTO booking_hosts (id,booking_id,user_id,is_primary) VALUES ('bh-s','b2','u1',0)`)

	dateFrom := time.Date(2027, 6, 18, 0, 0, 0, 0, time.UTC)
	ha, err := h.hostAvailability(context.Background(), "u1", "et1", dateFrom, dateFrom)
	if err != nil {
		t.Fatalf("hostAvailability: %v", err)
	}
	want := time.Date(2027, 6, 18, 3, 0, 0, 0, time.UTC)
	found := false
	for _, iv := range ha.Busy {
		if iv.Start.UTC().Equal(want) {
			found = true
		}
	}
	if !found {
		t.Errorf("busy %v missing u1's non-primary group seat at %s — u1 would be offered an overlapping slot and double-booked", ha.Busy, want)
	}
}
