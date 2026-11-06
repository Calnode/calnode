package db_test

import (
	"os"
	"testing"

	"github.com/calnode/calnode/internal/db"
	"github.com/pressly/goose/v3"
)

func TestMigrate_eventTypeVisibility(t *testing.T) {
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	goose.SetBaseFS(os.DirFS("."))
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(database, "migrations", 71); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`INSERT INTO users (id,email,name) VALUES ('owner','owner@example.com','Owner')`,
		`INSERT INTO event_types (id,user_id,slug,name,duration_minutes,is_public,is_active) VALUES ('pub','owner','pub','Public',30,1,1)`,
		`INSERT INTO event_types (id,user_id,slug,name,duration_minutes,is_public,is_active) VALUES ('priv','owner','priv','Private',30,0,1)`,
		`INSERT INTO event_types (id,user_id,slug,name,duration_minutes,is_public,is_active) VALUES ('inactive','owner','inactive','Inactive',30,1,0)`,
	} {
		if _, err := database.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id               string
		isPublic, active int
	}{{"pub", 1, 1}, {"priv", 0, 1}, {"inactive", 1, 0}} {
		var isPublic, isUnlisted, active int
		if err := database.QueryRow(`SELECT is_public,is_unlisted,is_active FROM event_types WHERE id = ?`, tc.id).Scan(&isPublic, &isUnlisted, &active); err != nil {
			t.Fatal(err)
		}
		if isPublic != tc.isPublic || active != tc.active || isUnlisted != 0 {
			t.Errorf("%s: public=%d unlisted=%d active=%d; want %d/0/%d", tc.id, isPublic, isUnlisted, active, tc.isPublic, tc.active)
		}
	}
	if _, err := database.Exec(`INSERT INTO event_types (id,user_id,slug,name,duration_minutes) VALUES ('new','owner','new','New',30)`); err != nil {
		t.Fatal(err)
	}
	var isPublic, isUnlisted int
	if err := database.QueryRow(`SELECT is_public,is_unlisted FROM event_types WHERE id = 'new'`).Scan(&isPublic, &isUnlisted); err != nil {
		t.Fatal(err)
	}
	if isPublic != 1 || isUnlisted != 0 {
		t.Errorf("new row: public=%d unlisted=%d; want 1/0", isPublic, isUnlisted)
	}
	if _, err := database.Exec(`UPDATE event_types SET is_unlisted = 2 WHERE id = 'new'`); err == nil {
		t.Fatal("invalid is_unlisted accepted")
	}
	if _, err := database.Exec(`UPDATE event_types SET is_unlisted = 1 WHERE id = 'new'`); err != nil {
		t.Fatal(err)
	}

	// Rollback must not publish an event type its owner explicitly hid.
	if err := goose.DownTo(database, "migrations", 71); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]int{"pub": 1, "priv": 0, "inactive": 1, "new": 0} {
		if err := database.QueryRow(`SELECT is_public FROM event_types WHERE id = ?`, id).Scan(&isPublic); err != nil {
			t.Fatal(err)
		}
		if isPublic != want {
			t.Errorf("%s after rollback: is_public=%d; want %d", id, isPublic, want)
		}
	}
}
