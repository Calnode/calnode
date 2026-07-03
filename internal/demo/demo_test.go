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
