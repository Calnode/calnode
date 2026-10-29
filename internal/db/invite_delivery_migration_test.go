package db_test

import (
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/calnode/calnode/internal/db"
	"github.com/pressly/goose/v3"
)

// migrateTo70WithInviteModes starts from the schema before invite_delivery gained 'none'
// and seeds one event type and one booking per mode that existed then.
func migrateTo70WithInviteModes(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	goose.SetBaseFS(os.DirFS("."))
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(database, "migrations", 70); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO users (id,email,name) VALUES ('owner','owner@example.com','Owner')`,
		`INSERT INTO event_types (id,user_id,slug,name,duration_minutes,invite_delivery) VALUES ('et-cal','owner','cal','Cal',30,'calendar')`,
		`INSERT INTO event_types (id,user_id,slug,name,duration_minutes,invite_delivery) VALUES ('et-cn','owner','cn','Cn',30,'calnode')`,
		`INSERT INTO bookings (id,event_type_id,host_id,start_at,end_at,invite_delivery) VALUES ('b-cal','et-cal','owner','2026-11-02T10:00:00Z','2026-11-02T10:30:00Z','calendar')`,
		`INSERT INTO bookings (id,event_type_id,host_id,start_at,end_at,invite_delivery) VALUES ('b-cn','et-cn','owner','2026-11-02T11:00:00Z','2026-11-02T11:30:00Z','calnode')`,
		`INSERT INTO booking_attendees (id,booking_id,name,email,is_organizer) VALUES ('a-1','b-cn','Guest','guest@example.com',1)`,
	} {
		if _, err := database.Exec(q); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	return database
}

// The column swap in 00071 must keep every stored mode, leave child rows and the
// double-booking guard alone, and accept 'none' while still rejecting unknown values.
func TestMigrate_inviteDeliveryNoneKeepsExistingModes(t *testing.T) {
	database := migrateTo70WithInviteModes(t)
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}

	for table, want := range map[string]map[string]string{
		"event_types": {"et-cal": "calendar", "et-cn": "calnode"},
		"bookings":    {"b-cal": "calendar", "b-cn": "calnode"},
	} {
		for id, mode := range want {
			var got string
			if err := database.QueryRow(`SELECT invite_delivery FROM `+table+` WHERE id = ?`, id).Scan(&got); err != nil {
				t.Fatalf("%s %s: %v", table, id, err)
			}
			if got != mode {
				t.Errorf("%s %s invite_delivery = %q after migrating; want %q", table, id, got, mode)
			}
		}
	}
	var attendees int
	if err := database.QueryRow(`SELECT COUNT(*) FROM booking_attendees WHERE booking_id = 'b-cn'`).Scan(&attendees); err != nil || attendees != 1 {
		t.Errorf("booking attendees after migrating = %d (%v); want the seeded one kept", attendees, err)
	}

	if _, err := database.Exec(`INSERT INTO event_types (id,user_id,slug,name,duration_minutes,invite_delivery) VALUES ('et-none','owner','none','None',30,'none')`); err != nil {
		t.Fatalf("event type with invite_delivery 'none': %v", err)
	}
	if _, err := database.Exec(`INSERT INTO bookings (id,event_type_id,host_id,start_at,end_at,invite_delivery) VALUES ('b-none','et-none','owner','2026-11-02T12:00:00Z','2026-11-02T12:30:00Z','none')`); err != nil {
		t.Fatalf("booking with invite_delivery 'none': %v", err)
	}
	for _, q := range []string{
		`INSERT INTO event_types (id,user_id,slug,name,duration_minutes,invite_delivery) VALUES ('et-bad','owner','bad','Bad',30,'carrier-pigeon')`,
		`INSERT INTO bookings (id,event_type_id,host_id,start_at,end_at,invite_delivery) VALUES ('b-bad','et-cal','owner','2026-11-02T13:00:00Z','2026-11-02T13:30:00Z','carrier-pigeon')`,
	} {
		if _, err := database.Exec(q); err == nil || !strings.Contains(err.Error(), "CHECK") {
			t.Errorf("unknown invite_delivery accepted (err=%v); want the CHECK to reject it: %s", err, q)
		}
	}
	var defaulted string
	if _, err := database.Exec(`INSERT INTO bookings (id,event_type_id,host_id,start_at,end_at) VALUES ('b-def','et-cal','owner','2026-11-02T14:00:00Z','2026-11-02T14:30:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT invite_delivery FROM bookings WHERE id = 'b-def'`).Scan(&defaulted); err != nil || defaulted != "calendar" {
		t.Errorf("default invite_delivery = %q (%v); want 'calendar'", defaulted, err)
	}

	// The double-booking guard is an index on bookings; a table rebuild could have lost it.
	if _, err := database.Exec(`INSERT INTO bookings (id,event_type_id,host_id,start_at,end_at) VALUES ('b-dup','et-cal','owner','2026-11-02T10:00:00Z','2026-11-02T10:30:00Z')`); err == nil {
		t.Error("double booking accepted after migrating; idx_bookings_no_double is gone")
	}
}

// Rolling back maps 'none' to 'calnode', which an older binary reads as guestless too.
func TestMigrate_inviteDeliveryNoneDownKeepsEventsGuestless(t *testing.T) {
	database := migrateTo70WithInviteModes(t)
	if err := goose.UpTo(database, "migrations", 71); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE bookings SET invite_delivery = 'none' WHERE id = 'b-cn'`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE event_types SET invite_delivery = 'none' WHERE id = 'et-cn'`); err != nil {
		t.Fatal(err)
	}
	if err := goose.DownTo(database, "migrations", 70); err != nil {
		t.Fatal(err)
	}
	var b, et string
	if err := database.QueryRow(`SELECT invite_delivery FROM bookings WHERE id = 'b-cn'`).Scan(&b); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT invite_delivery FROM event_types WHERE id = 'et-cn'`).Scan(&et); err != nil {
		t.Fatal(err)
	}
	if b != "calnode" || et != "calnode" {
		t.Errorf("after down: booking %q, event type %q; want both 'calnode'", b, et)
	}
}
