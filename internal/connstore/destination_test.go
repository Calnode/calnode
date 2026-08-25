package connstore

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// newDestDB builds just enough schema to exercise the destination lookup.
func newDestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE connection_calendars (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			provider TEXT NOT NULL,
			account_email TEXT NOT NULL DEFAULT '',
			calendar_id TEXT NOT NULL,
			name TEXT NOT NULL DEFAULT '',
			check_conflicts INTEGER NOT NULL DEFAULT 1,
			is_destination INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

func insertCal(t *testing.T, db *sql.DB, id, user, provider, email, calID string, conflicts, dest int) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO connection_calendars (id, user_id, provider, account_email, calendar_id, check_conflicts, is_destination)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, user, provider, email, calID, conflicts, dest); err != nil {
		t.Fatalf("insert: %v", err)
	}
}

// TestDestinationCalendarID covers the behaviour discussion #10 reported: a user whose work
// calendar is conflict-checked but who cannot make it the calendar bookings land in.
func TestDestinationCalendarID(t *testing.T) {
	ctx := context.Background()

	t.Run("returns the picked calendar, not the primary", func(t *testing.T) {
		db := newDestDB(t)
		insertCal(t, db, "1", "u1", "google", "me@gmail.com", "primary", 1, 0)
		insertCal(t, db, "2", "u1", "google", "me@gmail.com", "work@company.com", 1, 1)

		got, ok, err := DestinationCalendarID(ctx, db, "u1", "google", "me@gmail.com")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if !ok || got != "work@company.com" {
			t.Errorf("got (%q, %v), want the work calendar", got, ok)
		}
	})

	t.Run("no explicit pick falls back to the account default", func(t *testing.T) {
		db := newDestDB(t)
		// Conflict-checked but nothing marked as the write target: this is what every
		// install looks like after the 00049 seed, so behaviour must be unchanged.
		insertCal(t, db, "1", "u1", "google", "me@gmail.com", "primary", 1, 0)
		insertCal(t, db, "2", "u1", "google", "me@gmail.com", "work@company.com", 1, 0)

		got, ok, err := DestinationCalendarID(ctx, db, "u1", "google", "me@gmail.com")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if ok || got != "" {
			t.Errorf("got (%q, %v), want no override so the caller keeps the account calendar_id", got, ok)
		}
	})

	t.Run("scoped to the account, not just the user", func(t *testing.T) {
		db := newDestDB(t)
		// A destination picked in a DIFFERENT account must not leak into this one - the
		// caller has already chosen which account to write to.
		insertCal(t, db, "1", "u1", "google", "other@gmail.com", "work@company.com", 1, 1)

		got, ok, err := DestinationCalendarID(ctx, db, "u1", "google", "me@gmail.com")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if ok {
			t.Errorf("got %q for the wrong account; selections must not cross accounts", got)
		}
	})

	t.Run("scoped to the provider", func(t *testing.T) {
		db := newDestDB(t)
		insertCal(t, db, "1", "u1", "caldav", "me@gmail.com", "https://dav/work/", 1, 1)

		if _, ok, err := DestinationCalendarID(ctx, db, "u1", "google", "me@gmail.com"); err != nil || ok {
			t.Errorf("a CalDAV selection must not be returned for Google (ok=%v, err=%v)", ok, err)
		}
	})

	t.Run("an empty calendar_id is treated as no selection", func(t *testing.T) {
		db := newDestDB(t)
		insertCal(t, db, "1", "u1", "google", "me@gmail.com", "", 1, 1)

		if got, ok, err := DestinationCalendarID(ctx, db, "u1", "google", "me@gmail.com"); err != nil || ok {
			t.Errorf("got (%q, %v); an empty id must not override the account default", got, ok)
		}
	})

	t.Run("no rows at all is not an error", func(t *testing.T) {
		db := newDestDB(t)
		if _, ok, err := DestinationCalendarID(ctx, db, "nobody", "google", "x@y.z"); err != nil || ok {
			t.Errorf("ok=%v err=%v; an unconfigured account should return cleanly", ok, err)
		}
	})
}
