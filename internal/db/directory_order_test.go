package db_test

import (
	"os"
	"testing"

	"github.com/calnode/calnode/internal/db"
	"github.com/pressly/goose/v3"
)

func TestMigrate_displayOrderPreservesExistingEvents(t *testing.T) {
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	// Start from the schema immediately before display_order was introduced.
	goose.SetBaseFS(os.DirFS("."))
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(database, "migrations", 69); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO users (id,email,name) VALUES ('owner','owner@example.com','Owner')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO event_types (id,user_id,slug,name,duration_minutes) VALUES ('event','owner','existing','Existing event',30)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	var name, slug string
	var order int
	if err := database.QueryRow(`SELECT name,slug,display_order FROM event_types WHERE id = 'event'`).Scan(&name, &slug, &order); err != nil {
		t.Fatal(err)
	}
	if name != "Existing event" || slug != "existing" || order != 0 {
		t.Fatalf("existing event changed: name=%q slug=%q order=%d", name, slug, order)
	}
}
