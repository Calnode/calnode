package db_test

import (
	"context"
	"testing"

	"github.com/calnode/calnode/internal/db"
)

func TestOpen_inMemory(t *testing.T) {
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer database.Close()

	if err := database.Ping(); err != nil {
		t.Fatalf("db.Ping: %v", err)
	}
}

func TestMigrate_runsClean(t *testing.T) {
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer database.Close()

	if err := db.Migrate(database); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
}

func TestMigrate_idempotent(t *testing.T) {
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer database.Close()

	// Running twice should not error (goose is idempotent).
	for range 2 {
		if err := db.Migrate(database); err != nil {
			t.Fatalf("db.Migrate (run 2): %v", err)
		}
	}
}

func TestMigrate_tablesExist(t *testing.T) {
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer database.Close()

	if err := db.Migrate(database); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}

	tables := []string{
		"users", "api_keys", "teams", "team_members",
		"event_types", "event_type_questions",
		"availability_rules", "availability_overrides",
		"calendar_connections",
		"bookings", "booking_attendees", "booking_answers",
		"webhooks", "webhook_deliveries",
		"jobs",
	}

	for _, table := range tables {
		var name string
		err := database.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q not found after migration: %v", table, err)
		}
	}
}

func TestSchemaReady_falseBeforeMigrate_trueAfter(t *testing.T) {
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer database.Close()

	ctx := context.Background()

	// Before migrating, the goose bookkeeping table is absent → not ready.
	if ready, _ := db.SchemaReady(ctx, database); ready {
		t.Error("SchemaReady = true before migrations ran; want false")
	}

	if err := db.Migrate(database); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}

	ready, err := db.SchemaReady(ctx, database)
	if err != nil {
		t.Fatalf("SchemaReady after migrate: %v", err)
	}
	if !ready {
		t.Error("SchemaReady = false after migrations ran; want true")
	}

	// Applied version must equal the embedded target version.
	target, err := db.TargetVersion()
	if err != nil {
		t.Fatalf("TargetVersion: %v", err)
	}
	applied, err := db.AppliedVersion(ctx, database)
	if err != nil {
		t.Fatalf("AppliedVersion: %v", err)
	}
	if applied != target {
		t.Errorf("applied version = %d; want target %d", applied, target)
	}
	if target < 17 {
		t.Errorf("target version = %d; want >= 17 (sanity check against known migrations)", target)
	}
}

func TestDoubleBookingIndex_exists(t *testing.T) {
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer database.Close()

	if err := db.Migrate(database); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}

	var name string
	err = database.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='index' AND name='idx_bookings_no_double'`,
	).Scan(&name)
	if err != nil {
		t.Errorf("double-booking guard index not found: %v", err)
	}
}
