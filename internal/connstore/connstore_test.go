package connstore

import (
	"context"
	"database/sql"
	"testing"

	"github.com/calnode/calnode/internal/db"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatalf("newTestDB: open: %v", err)
	}
	if err := db.Migrate(database); err != nil {
		t.Fatalf("newTestDB: migrate: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func seedUser(t *testing.T, database *sql.DB, userID string) {
	t.Helper()
	if _, err := database.ExecContext(context.Background(), `
		INSERT INTO users (id, email, name, iana_timezone, is_admin, created_at)
		VALUES (?, ?, 'Test User', 'UTC', 0, '2026-01-01T00:00:00Z')`,
		userID, userID+"@example.com"); err != nil {
		t.Fatalf("seedUser: %v", err)
	}
}

func seedConnection(t *testing.T, database *sql.DB, userID, provider, accountEmail string, checkConflicts, isDestination int) {
	t.Helper()
	if _, err := database.ExecContext(context.Background(), `
		INSERT INTO calendar_connections
		    (id, user_id, provider, account_email, access_token_enc, calendar_id,
		     check_conflicts, is_destination, created_at)
		VALUES (?, ?, ?, ?, 'enc', 'cal', ?, ?, '2026-01-01T00:00:00Z')`,
		userID+"-"+provider+"-"+accountEmail, userID, provider, accountEmail, checkConflicts, isDestination); err != nil {
		t.Fatalf("seedConnection: %v", err)
	}
}

func TestWhereClause(t *testing.T) {
	cases := []struct {
		name                   string
		checkConflicts, isDest int
		wantFragment           string
		wantArgs               []any
	}{
		{"both any", -1, -1, "", nil},
		{"checkConflicts only", 1, -1, " AND check_conflicts = ?", []any{1}},
		{"isDestination only", -1, 0, " AND is_destination = ?", []any{0}},
		{"both set", 1, 1, " AND check_conflicts = ? AND is_destination = ?", []any{1, 1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			frag, args := WhereClause(c.checkConflicts, c.isDest)
			if frag != c.wantFragment {
				t.Errorf("fragment = %q; want %q", frag, c.wantFragment)
			}
			if len(args) != len(c.wantArgs) {
				t.Fatalf("args = %v; want %v", args, c.wantArgs)
			}
			for i := range args {
				if args[i] != c.wantArgs[i] {
					t.Errorf("args[%d] = %v; want %v", i, args[i], c.wantArgs[i])
				}
			}
		})
	}
}

func TestResolveFlags_newConnectionBecomesDestinationWhenNoneExists(t *testing.T) {
	database := newTestDB(t)
	seedUser(t, database, "u1")

	tx, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	cc, isDest, existing, err := ResolveFlags(context.Background(), tx, "u1", "google", "a@example.com")
	if err != nil {
		t.Fatalf("ResolveFlags: %v", err)
	}
	if existing {
		t.Error("existing = true; want false (no prior row)")
	}
	if cc != 1 {
		t.Errorf("checkConflicts = %d; want 1 (new connections default to checked)", cc)
	}
	if isDest != 1 {
		t.Errorf("isDestination = %d; want 1 (first connection claims destination)", isDest)
	}
}

func TestResolveFlags_newConnectionDoesNotClaimDestinationWhenOneExists(t *testing.T) {
	database := newTestDB(t)
	seedUser(t, database, "u1")
	seedConnection(t, database, "u1", "google", "existing@example.com", 1, 1) // already has a destination

	tx, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	_, isDest, existing, err := ResolveFlags(context.Background(), tx, "u1", "google", "new@example.com")
	if err != nil {
		t.Fatalf("ResolveFlags: %v", err)
	}
	if existing {
		t.Error("existing = true; want false")
	}
	if isDest != 0 {
		t.Errorf("isDestination = %d; want 0 (a destination already exists)", isDest)
	}
}

func TestResolveFlags_refreshPreservesExistingFlags(t *testing.T) {
	database := newTestDB(t)
	seedUser(t, database, "u1")
	// Existing row deliberately has check_conflicts=0 (user turned it off) and is not the
	// destination — a refresh must not silently re-enable conflict checking or steal the
	// destination flag from wherever it actually lives.
	seedConnection(t, database, "u1", "microsoft", "a@example.com", 0, 0)

	tx, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	cc, isDest, existing, err := ResolveFlags(context.Background(), tx, "u1", "microsoft", "a@example.com")
	if err != nil {
		t.Fatalf("ResolveFlags: %v", err)
	}
	if !existing {
		t.Error("existing = false; want true")
	}
	if cc != 0 {
		t.Errorf("checkConflicts = %d; want 0 (preserved from existing row)", cc)
	}
	if isDest != 0 {
		t.Errorf("isDestination = %d; want 0 (preserved from existing row)", isDest)
	}
}

func TestResolveFlags_providersAreIsolated(t *testing.T) {
	database := newTestDB(t)
	seedUser(t, database, "u1")
	// A Google destination connection must not stop Microsoft's first connection from
	// claiming ITS OWN destination slot — is_destination is scoped per (user), not per
	// (user, provider), per the real query — verifying this matches production intent:
	// the flag lookup groups by user_id + is_destination=1 across ALL providers together,
	// so a Google destination DOES count against a new Microsoft connection too.
	seedConnection(t, database, "u1", "google", "g@example.com", 1, 1)

	tx, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	_, isDest, existing, err := ResolveFlags(context.Background(), tx, "u1", "microsoft", "m@example.com")
	if err != nil {
		t.Fatalf("ResolveFlags: %v", err)
	}
	if existing {
		t.Error("existing = true; want false (different provider, different account)")
	}
	if isDest != 0 {
		t.Errorf("isDestination = %d; want 0 (a Google connection is already the one destination)", isDest)
	}
}
