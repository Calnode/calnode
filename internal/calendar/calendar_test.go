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
