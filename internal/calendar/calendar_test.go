package calendar

import (
	"context"
	"testing"

	"github.com/calnode/calnode/internal/db"
)

func TestCanAutoGenerate(t *testing.T) {
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx := context.Background()

	seed := func(userID, provider, kind string) {
		if _, err := database.ExecContext(ctx,
			`INSERT INTO users (id, email, name, iana_timezone, is_admin, created_at)
			 VALUES (?, ?, 'U', 'UTC', 0, '2026-01-01T00:00:00Z')`, userID, userID+"@x.test"); err != nil {
			t.Fatalf("seed user: %v", err)
		}
		if _, err := database.ExecContext(ctx,
			`INSERT INTO calendar_connections
			   (id, user_id, provider, access_token_enc, refresh_token_enc, calendar_id,
			    check_conflicts, is_destination, expiry_at, created_at, account_kind)
			 VALUES (?, ?, ?, 'e', 'e', 'primary', 1, 1, '', '2026-01-01T00:00:00Z', ?)`,
			userID+"-c", userID, provider, kind); err != nil {
			t.Fatalf("seed connection: %v", err)
		}
	}
	seed("g", "google", "")           // Google → Meet capable
	seed("mw", "microsoft", "work")   // MS work → Teams capable
	seed("mp", "microsoft", "personal") // MS personal → not Teams capable
	seed("mu", "microsoft", "")       // unknown kind → treated as capable

	cases := []struct {
		user, locType string
		want          bool
	}{
		{"g", "google_meet", true},
		{"g", "teams", false},
		{"mw", "teams", true},
		{"mw", "google_meet", false},
		{"mp", "teams", false},
		{"mu", "teams", true},
		{"none", "teams", false}, // no connection
		{"g", "phone", false},    // non-online type
	}
	svc := NewService(database)
	for _, tc := range cases {
		got, err := svc.CanAutoGenerate(ctx, tc.user, tc.locType)
		if err != nil {
			t.Fatalf("CanAutoGenerate(%s,%s): %v", tc.user, tc.locType, err)
		}
		if got != tc.want {
			t.Errorf("CanAutoGenerate(%s,%s)=%v; want %v", tc.user, tc.locType, got, tc.want)
		}
	}
}

// TestConnectionManagement covers the multi-calendar Service helpers: listing connections,
// switching the single destination, and promoting a survivor when the destination is removed.
func TestConnectionManagement(t *testing.T) {
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx := context.Background()
	if _, err := database.ExecContext(ctx,
		`INSERT INTO users (id, email, name, iana_timezone, is_admin, created_at)
		 VALUES ('u1', 'u1@x.test', 'U', 'UTC', 0, '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	// Two connections: c1 (destination, oldest), c2 (check-only).
	seed := func(id, provider, email string, dest int, created string) {
		if _, err := database.ExecContext(ctx,
			`INSERT INTO calendar_connections
			   (id, user_id, provider, account_email, access_token_enc, refresh_token_enc, calendar_id,
			    check_conflicts, is_destination, expiry_at, created_at)
			 VALUES (?, 'u1', ?, ?, 'e', 'e', 'primary', 1, ?, '', ?)`,
			id, provider, email, dest, created); err != nil {
			t.Fatalf("seed conn %s: %v", id, err)
		}
	}
	seed("c1", "google", "work@x.test", 1, "2026-01-01T00:00:00Z")
	seed("c2", "google", "personal@x.test", 0, "2026-01-02T00:00:00Z")

	svc := NewService(database)

	conns, err := svc.Connections(ctx, "u1")
	if err != nil || len(conns) != 2 {
		t.Fatalf("Connections: %v, n=%d", err, len(conns))
	}
	if !conns[0].IsDestination || conns[0].ID != "c1" {
		t.Errorf("destination-first ordering wrong: %+v", conns)
	}

	// Switch destination to c2.
	if err := svc.SetDestination(ctx, "u1", "c2"); err != nil {
		t.Fatalf("SetDestination: %v", err)
	}
	var destID string
	database.QueryRowContext(ctx, `SELECT id FROM calendar_connections WHERE user_id='u1' AND is_destination=1`).Scan(&destID) //nolint:errcheck
	if destID != "c2" {
		t.Errorf("destination = %q after SetDestination; want c2", destID)
	}

	// Setting a connection that isn't theirs fails.
	if err := svc.SetDestination(ctx, "u1", "nope"); err == nil {
		t.Error("SetDestination on unknown id should error")
	}

	// Disconnect the destination (c2) → the survivor (c1) is promoted.
	if err := svc.DisconnectOne(ctx, "u1", "c2"); err != nil {
		t.Fatalf("DisconnectOne: %v", err)
	}
	conns, _ = svc.Connections(ctx, "u1")
	if len(conns) != 1 || conns[0].ID != "c1" || !conns[0].IsDestination {
		t.Errorf("after disconnecting destination, expected c1 promoted; got %+v", conns)
	}
}
