package demo_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/calnode/calnode/internal/db"
	"github.com/calnode/calnode/internal/demo"
)

func newMigratedDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	return database
}

func assertCount(t *testing.T, ctx context.Context, database *sql.DB, table string, want int) {
	t.Helper()
	var got int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Errorf("count(%s) = %d; want %d", table, got, want)
	}
}

func TestSeed_populatesExpectedData(t *testing.T) {
	database := newMigratedDB(t)
	ctx := context.Background()

	if err := demo.Seed(ctx, database); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	assertCount(t, ctx, database, "users", 2)
	assertCount(t, ctx, database, "event_types", 3)
	assertCount(t, ctx, database, "bookings", 3)
	assertCount(t, ctx, database, "booking_attendees", 3)
	assertCount(t, ctx, database, "booking_hosts", 3)
	assertCount(t, ctx, database, "teams", 1)
	assertCount(t, ctx, database, "team_members", 2)
	assertCount(t, ctx, database, "availability_rules", 10) // 2 users * 5 weekdays

	var isAdmin, isOwner int
	if err := database.QueryRowContext(ctx,
		`SELECT is_admin, is_owner FROM users WHERE id = ?`, demo.OwnerUserID).
		Scan(&isAdmin, &isOwner); err != nil {
		t.Fatalf("query owner: %v", err)
	}
	if isAdmin != 1 || isOwner != 1 {
		t.Errorf("owner user is_admin=%d is_owner=%d; want both 1", isAdmin, isOwner)
	}
}

func TestReset_wipesVisitorDataAndReseeds(t *testing.T) {
	database := newMigratedDB(t)
	ctx := context.Background()

	if err := demo.Seed(ctx, database); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	// Simulate a visitor booking made during the demo's life.
	if _, err := database.ExecContext(ctx, `
		INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at, status)
		VALUES ('visitor-booking', 'demo-et-intro', ?, '2099-01-01T10:00:00Z', '2099-01-01T10:15:00Z', 'confirmed')`,
		demo.OwnerUserID); err != nil {
		t.Fatalf("insert visitor booking: %v", err)
	}
	assertCount(t, ctx, database, "bookings", 4)

	if err := demo.Reset(ctx, database); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	// Back to exactly the seeded 3 — the visitor row is gone, not just added to.
	assertCount(t, ctx, database, "bookings", 3)
	assertCount(t, ctx, database, "users", 2)

	var count int
	if err := database.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM bookings WHERE id = 'visitor-booking'`).Scan(&count); err != nil {
		t.Fatalf("query visitor booking: %v", err)
	}
	if count != 0 {
		t.Error("visitor-booking survived Reset")
	}

	// Foreign keys must be back on after Reset — an orphaned insert should fail.
	if _, err := database.ExecContext(ctx, `
		INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at, status)
		VALUES ('orphan', 'does-not-exist', ?, '2099-01-01T10:00:00Z', '2099-01-01T10:15:00Z', 'confirmed')`,
		demo.OwnerUserID); err == nil {
		t.Error("insert with dangling event_type_id succeeded; want foreign key violation")
	}
}

// TestSeed_eventTypesCarryAJoinableLocation guards the demo's first impression.
//
// The seeder inserts straight into event_types, so validateLocation never sees the row.
// It used to write location_type 'link' with a NULL location_value, which that validator
// rejects - and because the admin editor submits the whole form on every save, the first
// thing a demo visitor did after changing a duration was get "enter a valid meeting URL"
// about a field they had never touched.
//
// Any location that needs no external account is fine here. What is not fine is one that
// needs a value and does not have one.
func TestSeed_eventTypesCarryAJoinableLocation(t *testing.T) {
	database := newMigratedDB(t)
	ctx := context.Background()
	if err := demo.Seed(ctx, database); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	rows, err := database.QueryContext(ctx,
		`SELECT slug, location_type, COALESCE(location_value, '') FROM event_types`)
	if err != nil {
		t.Fatalf("query event types: %v", err)
	}
	defer rows.Close()

	// Materialised before asserting: the pool is MaxOpenConns(1), so anything that
	// queried inside this cursor would deadlock.
	type et struct{ slug, locType, locVal string }
	var seeded []et
	for rows.Next() {
		var e et
		if err := rows.Scan(&e.slug, &e.locType, &e.locVal); err != nil {
			t.Fatalf("scan: %v", err)
		}
		seeded = append(seeded, e)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(seeded) == 0 {
		t.Fatal("no event types seeded, so this proves nothing")
	}

	// The types that carry their join info in location_value. 'zoom', 'google_meet' and
	// 'teams' are absent on purpose: those may legitimately be empty when the owner's
	// account auto-generates a link, which the demo owner's does not, so they should not
	// appear here either.
	needsValue := map[string]bool{"link": true, "custom_video": true, "phone": true}
	for _, e := range seeded {
		if needsValue[e.locType] && e.locVal == "" {
			t.Errorf("demo event type %q is location_type %q with no location_value: "+
				"validateLocation rejects that, so the editor refuses to save it and a "+
				"visitor's first edit fails on a field they never touched",
				e.slug, e.locType)
		}
	}
}
