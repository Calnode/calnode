package calendar

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/calnode/calnode/internal/db"
)

// newTestDB opens a migrated in-memory DB with one user.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := database.Exec(
		`INSERT INTO users (id, email, name, iana_timezone, is_admin, created_at)
		 VALUES ('u1','u1@x.test','U','UTC',0,'2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return database
}

func seedConn(t *testing.T, database *sql.DB, id, userID, provider, email string, check, dest int) {
	t.Helper()
	if _, err := database.Exec(
		`INSERT INTO calendar_connections
		   (id, user_id, provider, account_email, access_token_enc, refresh_token_enc, calendar_id,
		    check_conflicts, is_destination, expiry_at, created_at)
		 VALUES (?, ?, ?, ?, 'e', 'e', 'primary', ?, ?, '', '2026-01-01T00:00:00Z')`,
		id, userID, provider, email, check, dest); err != nil {
		t.Fatalf("seed conn %s: %v", id, err)
	}
}

// These cover a whole class of bug rather than one report: calendar_connections rows are
// DELETEd and re-INSERTed under a new id on every OAuth token refresh (see migration
// 00049), and listing an account's calendars can itself trigger a refresh. Any operation
// addressed by row id therefore breaks for a page that loaded even moments earlier.
//
// The symptom reported was "calendar connection not found" when choosing where bookings
// go. Auditing for the same shape found Disconnect had it too, and worse.

// refreshToken reproduces what a provider does on token refresh: same account, brand new
// row id.
func refreshToken(t *testing.T, db *sql.DB, userID, provider, email, newID string) {
	t.Helper()
	var dest, check int
	if err := db.QueryRow(
		`SELECT is_destination, check_conflicts FROM calendar_connections
		 WHERE user_id = ? AND provider = ? AND account_email = ?`,
		userID, provider, email).Scan(&dest, &check); err != nil {
		t.Fatalf("read before refresh: %v", err)
	}
	if _, err := db.Exec(
		`DELETE FROM calendar_connections WHERE user_id = ? AND provider = ? AND account_email = ?`,
		userID, provider, email); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO calendar_connections (id, user_id, provider, account_email, access_token_enc,
		    calendar_id, check_conflicts, is_destination, created_at)
		 VALUES (?, ?, ?, ?, 'tok', 'primary', ?, ?, '2026-01-01T00:00:00Z')`,
		newID, userID, provider, email, check, dest); err != nil {
		t.Fatalf("reinsert: %v", err)
	}
}

func TestSetDestination_survivesATokenRefresh(t *testing.T) {
	db := newTestDB(t)
	seedConn(t, db, "old-id", "u1", "google", "work@x.test", 1, 0)
	seedConn(t, db, "other", "u1", "google", "personal@x.test", 1, 1)

	svc := NewService(db)
	// The user opens the calendar picker, which refreshes the token and rebuilds the row.
	refreshToken(t, db, "u1", "google", "work@x.test", "brand-new-id")

	// The page still holds "old-id", but the request is addressed by account, so it works.
	if err := svc.SetDestination(context.Background(), "u1", "google", "work@x.test"); err != nil {
		t.Fatalf("SetDestination after a token refresh: %v", err)
	}
	var email string
	if err := db.QueryRow(
		`SELECT account_email FROM calendar_connections WHERE user_id='u1' AND is_destination=1`).Scan(&email); err != nil {
		t.Fatalf("read destination: %v", err)
	}
	if email != "work@x.test" {
		t.Errorf("destination = %q, want work@x.test", email)
	}
}

// The one that was silently wrong: a stale id made DisconnectOne return nil, so the API
// answered 204 Success having deleted nothing and the account stayed on the page.
func TestDisconnectOne_survivesATokenRefresh(t *testing.T) {
	db := newTestDB(t)
	seedConn(t, db, "old-id", "u1", "google", "work@x.test", 1, 1)
	seedConn(t, db, "keep", "u1", "google", "personal@x.test", 1, 0)

	svc := NewService(db)
	refreshToken(t, db, "u1", "google", "work@x.test", "brand-new-id")

	if err := svc.DisconnectOne(context.Background(), "u1", "google", "work@x.test"); err != nil {
		t.Fatalf("DisconnectOne after a token refresh: %v", err)
	}
	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM calendar_connections WHERE user_id='u1' AND account_email='work@x.test'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("account still connected after disconnect (%d rows) - the delete silently did nothing", n)
	}
}

// A genuinely absent account must be reported, not swallowed. Returning nil here is what
// let a failed disconnect look like a successful one.
func TestDisconnectOne_unknownAccountIsAnError(t *testing.T) {
	db := newTestDB(t)
	seedConn(t, db, "c1", "u1", "google", "work@x.test", 1, 1)

	err := NewService(db).DisconnectOne(context.Background(), "u1", "google", "never-connected@x.test")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("err = %v, want sql.ErrNoRows so the API can report it rather than answering 204", err)
	}
}

// Disconnecting must take the account's calendar selections with it. connection_calendars
// has no FK (an FK would cascade-delete selections on every token refresh), so the cleanup
// has to be explicit - and it was missing, despite migration 00049 claiming otherwise.
// Left behind, reconnecting the same address silently inherits stale picks, including a
// destination pointing at a calendar the user may no longer have.
func TestDisconnectOne_removesTheAccountsCalendarSelections(t *testing.T) {
	db := newTestDB(t)
	seedConn(t, db, "c1", "u1", "google", "work@x.test", 1, 1)
	seedConn(t, db, "c2", "u1", "google", "personal@x.test", 1, 0)
	if _, err := db.Exec(`
		INSERT INTO connection_calendars (id, user_id, provider, account_email, calendar_id, check_conflicts, is_destination)
		VALUES ('s1','u1','google','work@x.test','team@x.test',1,1),
		       ('s2','u1','google','personal@x.test','primary',1,0)`); err != nil {
		t.Fatalf("seed selections: %v", err)
	}

	if err := NewService(db).DisconnectOne(context.Background(), "u1", "google", "work@x.test"); err != nil {
		t.Fatalf("DisconnectOne: %v", err)
	}

	var gone int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM connection_calendars WHERE user_id='u1' AND account_email='work@x.test'`).Scan(&gone); err != nil {
		t.Fatal(err)
	}
	if gone != 0 {
		t.Errorf("%d selection rows survived the disconnect; reconnecting would inherit them", gone)
	}
	// The other account's picks must be untouched.
	var kept int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM connection_calendars WHERE user_id='u1' AND account_email='personal@x.test'`).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if kept != 1 {
		t.Errorf("kept = %d, want the other account's selection left alone", kept)
	}
}

func TestDisconnectAll_removesCalendarSelectionsToo(t *testing.T) {
	db := newTestDB(t)
	seedConn(t, db, "c1", "u1", "google", "work@x.test", 1, 1)
	if _, err := db.Exec(`
		INSERT INTO connection_calendars (id, user_id, provider, account_email, calendar_id, check_conflicts, is_destination)
		VALUES ('s1','u1','google','work@x.test','team@x.test',1,1)`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := NewService(db).Disconnect(context.Background(), "u1"); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM connection_calendars WHERE user_id='u1'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d selection rows survived a full disconnect", n)
	}
}
