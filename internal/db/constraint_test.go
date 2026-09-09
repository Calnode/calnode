package db_test

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/calnode/calnode/internal/db"
)

// openMigrated returns an in-memory database with the schema applied, so the
// constraints these tests provoke are the real ones.
func openMigrated(t *testing.T) *sql.DB {
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

// TestConstraintPredicates provokes a real violation of each class against the real
// schema and asserts the predicate recognises it.
//
// Provoked rather than constructed: a hand-built error would only prove the
// predicate agrees with whatever the test author believed the driver returns, and
// the belief this replaces (that the message distinguishes the cases) was wrong.
func TestConstraintPredicates(t *testing.T) {
	database := openMigrated(t)

	// A user and an event type to hang the booking constraints off.
	const userID = "u-constraint"
	if _, err := database.Exec(
		`INSERT INTO users (id, email, name, iana_timezone) VALUES (?, ?, 'Host', 'UTC')`,
		userID, userID+"@example.com"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	const etID = "et-constraint"
	if _, err := database.Exec(
		`INSERT INTO event_types (id, user_id, slug, name, duration_minutes)
		 VALUES (?, ?, ?, 'Call', 30)`, etID, userID, etID); err != nil {
		t.Fatalf("seed event type: %v", err)
	}

	t.Run("unique", func(t *testing.T) {
		// users.email is UNIQUE.
		_, err := database.Exec(
			`INSERT INTO users (id, email, name, iana_timezone) VALUES (?, ?, 'Clash', 'UTC')`,
			"u-clash", userID+"@example.com")
		assertOnly(t, err, "unique", db.IsUniqueViolation)
	})

	// ⛔ The case that distinguishes a correct implementation from a plausible one,
	// and the reason the text match could not be kept.
	//
	// SQLite reports a PRIMARY KEY collision as SQLITE_CONSTRAINT_PRIMARYKEY (1555),
	// NOT as SQLITE_CONSTRAINT_UNIQUE (2067) — while still saying "UNIQUE constraint
	// failed" in the message. So a predicate matching only 2067 passes the subtest
	// above and fails here, and the message cannot tell the two apart at all.
	//
	// This is not a theoretical shape: idempotency_keys.idempotency_key is a bare
	// PRIMARY KEY, so claimIdempotencyKey's entire replay path depends on 1555 being
	// classified as a unique violation.
	t.Run("unique via primary key", func(t *testing.T) {
		const key = "idem-key-constraint"
		if _, err := database.Exec(
			`INSERT INTO idempotency_keys (idempotency_key, request_hash, created_at) VALUES (?, 'h', ?)`,
			key, "2026-06-01T00:00:00Z"); err != nil {
			t.Fatalf("seed idempotency key: %v", err)
		}
		_, err := database.Exec(
			`INSERT INTO idempotency_keys (idempotency_key, request_hash, created_at) VALUES (?, 'h', ?)`,
			key, "2026-06-01T00:00:00Z")
		assertOnly(t, err, "primary key", db.IsUniqueViolation)
	})

	t.Run("check", func(t *testing.T) {
		// bookings.status has CHECK (status IN ('confirmed','cancelled')).
		_, err := database.Exec(`
			INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at, status, created_at, updated_at)
			VALUES (?, ?, ?, '2026-06-15T09:00:00Z', '2026-06-15T09:30:00Z', 'not-a-status', ?, ?)`,
			"b-check", etID, userID, "2026-06-01T00:00:00Z", "2026-06-01T00:00:00Z")
		assertOnly(t, err, "check", db.IsCheckViolation)
	})

	t.Run("foreign key", func(t *testing.T) {
		// event_type_id references event_types(id). This needs foreign_keys=ON,
		// which db.Open sets.
		_, err := database.Exec(`
			INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at, status, created_at, updated_at)
			VALUES (?, 'no-such-event-type', ?, '2026-06-15T10:00:00Z', '2026-06-15T10:30:00Z', 'confirmed', ?, ?)`,
			"b-fk", userID, "2026-06-01T00:00:00Z", "2026-06-01T00:00:00Z")
		assertOnly(t, err, "foreign key", db.IsForeignKeyViolation)
	})

	t.Run("unrelated error", func(t *testing.T) {
		// A predicate that answered true for everything would satisfy every caller
		// above and be badly wrong, so the negative cases carry as much weight.
		_, err := database.Exec(`SELECT * FROM a_table_that_does_not_exist`)
		if err == nil {
			t.Fatal("expected an error from a missing table")
		}
		assertNone(t, err, "missing table")
		assertNone(t, errors.New("some unrelated failure"), "plain error")
		assertNone(t, nil, "nil")
	})

	t.Run("unhandled constraint class", func(t *testing.T) {
		// A NOT NULL violation is a constraint violation of a class nothing here
		// classifies (SQLITE_CONSTRAINT_NOTNULL, 1299). It must match none of the
		// three, so a caller cannot turn it into a 409 by accident.
		_, err := database.Exec(
			`INSERT INTO users (id, email, name, iana_timezone) VALUES (?, ?, NULL, 'UTC')`,
			"u-null", "u-null@example.com")
		if err == nil {
			t.Fatal("expected a NOT NULL violation")
		}
		assertNone(t, err, "not-null violation")
	})
}

// TestConstraintTextFallback covers the branch the live driver never reaches: an
// error that arrives without its driver type still attached.
//
// The branch exists for a driver release that changes its error type, or a layer
// that reformats an error into a plain one rather than wrapping it — in which case
// the message is the only signal left, and answering from it beats returning a 500.
// Without this test the fallback would be unexecuted code that reads like an
// accident.
func TestConstraintTextFallback(t *testing.T) {
	cases := []struct {
		text string
		want func(error) bool
		name string
	}{
		{"boom: UNIQUE constraint failed: t.a", db.IsUniqueViolation, "unique"},
		{"boom: CHECK constraint failed: n > 0", db.IsCheckViolation, "check"},
		{"boom: FOREIGN KEY constraint failed", db.IsForeignKeyViolation, "foreign key"},
	}
	for _, c := range cases {
		err := errors.New(c.text)
		if !c.want(err) {
			t.Errorf("%s: fallback did not recognise %q", c.name, c.text)
		}
	}
	// And the fallback is still discriminating, not a catch-all.
	assertNone(t, errors.New("NOT NULL constraint failed: t.a"), "not-null text")
}

// assertOnly checks that want recognises err and the other two predicates do not:
// the classes have to be distinguishable, or a CHECK violation becomes a 409.
func assertOnly(t *testing.T, err error, class string, want func(error) bool) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected a constraint violation, got nil", class)
	}
	if !want(err) {
		t.Errorf("%s: predicate did not recognise %v", class, err)
	}
	matches := 0
	for _, p := range []func(error) bool{db.IsUniqueViolation, db.IsCheckViolation, db.IsForeignKeyViolation} {
		if p(err) {
			matches++
		}
	}
	if matches != 1 {
		t.Errorf("%s: %d of 3 predicates matched %v; want exactly 1", class, matches, err)
	}
}

func assertNone(t *testing.T, err error, what string) {
	t.Helper()
	for name, p := range map[string]func(error) bool{
		"IsUniqueViolation":     db.IsUniqueViolation,
		"IsCheckViolation":      db.IsCheckViolation,
		"IsForeignKeyViolation": db.IsForeignKeyViolation,
	} {
		if p(err) {
			t.Errorf("%s returned true for %s (%v)", name, what, err)
		}
	}
}
