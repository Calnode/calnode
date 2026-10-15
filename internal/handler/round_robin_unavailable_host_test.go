package handler_test

import (
	"net/http"
	"testing"

	"github.com/calnode/calnode/internal/uid"
)

// An unavailability override must remove a rotation host from the pool for that
// day, not merely make the day look unavailable when every host is out.
//
// The bug (#146): validateBookingTime's round-robin branch only requires that AT
// LEAST ONE candidate be inside their own hours — correct for deciding whether
// the slot exists at all (mirrors slots.pickHosts), but wrong for deciding WHO
// is assigned. The full candidate list was then handed to booking.Service.Create,
// which picks by rotation strategy, so it could assign a host who had declared
// themselves unavailable. calendarFreeHosts does not catch this: it filters on
// calendar busy, and returns the pool untouched when no calendar provider is
// connected (the common self-hosted case).
func TestRoundRobin_neverAssignsHostWithUnavailableOverride(t *testing.T) {
	h, database, key, ownerID := setupWorkspaceWithDB(t)
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u2','u2@example.com','Two','UTC',0)`)
	seedFullAvailabilityDB(t, database, ownerID)
	seedFullAvailabilityDB(t, database, "u2")
	slug, etID := seedEventTypeHTTP(t, h, key)
	makeRoundRobin(t, h, slug, key, ownerID, "u2")

	const day = "2027-05-03"

	// The FIRST rotation host (priority 0) declares the whole day unavailable.
	// Round-robin's default least-loaded strategy would otherwise pick it first.
	if _, err := database.Exec(
		`INSERT INTO availability_overrides (id,user_id,date,is_available,reason) VALUES (?,?,?,0,'day off')`,
		uid.New(), ownerID, day); err != nil {
		t.Fatalf("seed unavailability override: %v", err)
	}

	rec := postBooking(t, h, slug, day+"T10:00:00Z", "Alice", "alice@example.com")
	if rec.Code != http.StatusCreated {
		t.Fatalf("booking: %d — %s", rec.Code, rec.Body.String())
	}

	var hostID string
	if err := database.QueryRow(`SELECT host_id FROM bookings WHERE event_type_id=?`, etID).Scan(&hostID); err != nil {
		t.Fatalf("read assigned host: %v", err)
	}
	if hostID == ownerID {
		t.Errorf("booking assigned to %s, who declared %s unavailable", hostID, day)
	}
}

// Same rule via the slug path that MCP and the booking assistant share. Without
// this, a fix applied only to the REST handler would leave those two entry points
// assigning unavailable hosts.
func TestRoundRobin_slugPathSkipsUnavailableHost(t *testing.T) {
	h, database, key, ownerID := setupWorkspaceWithDB(t)
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u2','u2@example.com','Two','UTC',0)`)
	seedFullAvailabilityDB(t, database, ownerID)
	seedFullAvailabilityDB(t, database, "u2")
	slug, etID := seedEventTypeHTTP(t, h, key)
	makeRoundRobin(t, h, slug, key, ownerID, "u2")

	const day = "2027-05-03"
	if _, err := database.Exec(
		`INSERT INTO availability_overrides (id,user_id,date,is_available,reason) VALUES (?,?,?,0,'day off')`,
		uid.New(), ownerID, day); err != nil {
		t.Fatalf("seed unavailability override: %v", err)
	}

	if rec := postBooking(t, h, slug, day+"T14:00:00Z", "Alice", "alice@example.com"); rec.Code != http.StatusCreated {
		t.Fatalf("booking: %d — %s", rec.Code, rec.Body.String())
	}
	var hostID string
	if err := database.QueryRow(`SELECT host_id FROM bookings WHERE event_type_id=?`, etID).Scan(&hostID); err != nil {
		t.Fatalf("read assigned host: %v", err)
	}
	if hostID == ownerID {
		t.Errorf("slug path assigned %s, who declared %s unavailable", hostID, day)
	}
}

// The all-hosts-unavailable case must still be refused, not silently assigned.
func TestRoundRobin_allHostsUnavailableStillRejected(t *testing.T) {
	h, database, key, ownerID := setupWorkspaceWithDB(t)
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u2','u2@example.com','Two','UTC',0)`)
	seedFullAvailabilityDB(t, database, ownerID)
	seedFullAvailabilityDB(t, database, "u2")
	slug, _ := seedEventTypeHTTP(t, h, key)
	makeRoundRobin(t, h, slug, key, ownerID, "u2")

	const day = "2027-05-03"
	for _, u := range []string{ownerID, "u2"} {
		if _, err := database.Exec(
			`INSERT INTO availability_overrides (id,user_id,date,is_available,reason) VALUES (?,?,?,0,'day off')`,
			uid.New(), u, day); err != nil {
			t.Fatalf("seed unavailability override for %s: %v", u, err)
		}
	}

	if rec := postBooking(t, h, slug, day+"T10:00:00Z", "Alice", "alice@example.com"); rec.Code == http.StatusCreated {
		t.Errorf("booking created with every host unavailable")
	}
}
