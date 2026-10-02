package db_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/calnode/calnode/internal/db"
	"github.com/pressly/goose/v3"
)

func TestInvitationLifecycleUpgradeFromPopulatedFoundation(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "foundation.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	provider, err := goose.NewProvider(goose.DialectSQLite3, database, os.DirFS("migrations"), goose.WithDisableGlobalRegistry(true))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := provider.UpTo(ctx, 71); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO users (id,email,name) VALUES ('owner','owner@example.com','Owner')`,
		`INSERT INTO event_types (id,user_id,slug,name,duration_minutes,location_type,location_value) VALUES ('event','owner','support','Support',30,'in_person','Office')`,
		`INSERT INTO bookings (id,event_type_id,host_id,start_at,end_at,status) VALUES ('booking','event','owner','2027-01-01T10:00:00Z','2027-01-01T11:00:00Z','confirmed')`,
		`INSERT INTO booking_hosts (booking_id,user_id,is_primary) VALUES ('booking','owner',1)`,
		`INSERT INTO scheduling_invitations (id,event_type_id,created_by,recipient_email,duration_minutes,slot_interval_minutes,buffer_before_minutes,buffer_after_minutes,min_notice_minutes,max_future_days,routing_mode,rr_strategy,expires_at,status,booking_id,external_system,external_reference) VALUES ('booked','event','owner','recipient@example.com',60,15,0,0,0,30,'fixed','even','2099-01-01T00:00:00Z','booked','booking','tickets','42'),('active','event','owner','recipient@example.com',45,15,0,0,0,30,'fixed','even','2099-01-01T00:00:00Z','active',NULL,'tickets','43')`,
		`INSERT INTO scheduling_invitation_hosts (invitation_id,user_id,role) VALUES ('booked','owner','required'),('active','owner','required')`,
		`INSERT INTO scheduling_invitation_tokens (token_hash,invitation_id,expires_at) VALUES ('0123456789012345678901234567890123456789012345678901234567890123','active','2099-01-01T00:00:00Z')`,
	} {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	for pass := 0; pass < 2; pass++ {
		if err := db.Migrate(database); err != nil {
			t.Fatal(err)
		}
	}
	var inv, location, updated, created string
	var duration int
	if err := database.QueryRow(`SELECT scheduling_invitation_id FROM bookings WHERE id='booking'`).Scan(&inv); err != nil || inv != "booked" {
		t.Fatalf("lost booking relationship %q %v", inv, err)
	}
	if err := database.QueryRow(`SELECT duration_minutes,location_value,updated_at,created_at FROM scheduling_invitations WHERE id='active'`).Scan(&duration, &location, &updated, &created); err != nil || duration != 45 || location != "Office" || updated != created {
		t.Fatalf("snapshot/backfill failed: %d %q %q %q %v", duration, location, updated, created, err)
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM scheduling_invitation_events`).Scan(&count); err != nil || count != 0 {
		t.Fatal("upgrade emitted historical events", count, err)
	}
	if _, err := database.Exec(`INSERT INTO bookings (id,event_type_id,host_id,start_at,end_at,status,scheduling_invitation_id) VALUES ('duplicate','event','owner','2027-01-02T10:00:00Z','2027-01-02T11:00:00Z','confirmed','booked')`); err == nil {
		t.Fatal("invitation uniqueness invariant missing")
	}
	if _, err := database.Exec(`UPDATE scheduling_invitations SET status='cancelled',updated_at='2026-10-02T00:00:00Z' WHERE id='active'`); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM scheduling_invitation_events WHERE event='scheduling_invitation.cancelled'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("transition outbox failed", count, err)
	}
	if _, err := provider.DownTo(ctx, 71); err != nil {
		t.Fatal("down", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal("re-upgrade", err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&count); err != nil || count != 0 {
		t.Fatal("foreign key violations", count, err)
	}
}

func TestInvitationUpgradeAfterDirectoryOrderMigration(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "directory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	// Model the upstream dependency without adding its unrelated implementation
	// to this branch. The database really has its version 70 migration applied.
	source := fstest.MapFS{}
	entries, err := os.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		content, err := os.ReadFile(filepath.Join("migrations", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		source[entry.Name()] = &fstest.MapFile{Data: content}
	}
	source["00070_event_type_display_order.sql"] = &fstest.MapFile{Data: []byte("-- +goose Up\nALTER TABLE event_types ADD COLUMN display_order INTEGER NOT NULL DEFAULT 0;\n-- +goose Down\n")}
	provider, err := goose.NewProvider(goose.DialectSQLite3, database, source, goose.WithDisableGlobalRegistry(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(t.Context(), 70); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO users (id,email,name) VALUES ('owner','owner@example.com','Owner'); INSERT INTO event_types (id,user_id,slug,name,duration_minutes,display_order) VALUES ('event','owner','support','Support',30,7)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	var order, version int
	if err := database.QueryRow(`SELECT display_order FROM event_types WHERE id='event'`).Scan(&order); err != nil || order != 7 {
		t.Fatalf("directory order changed: %d %v", order, err)
	}
	if err := database.QueryRow(`SELECT MAX(version_id) FROM goose_db_version WHERE is_applied=1`).Scan(&version); err != nil || version != 72 {
		t.Fatalf("upgrade version: %d %v", version, err)
	}
	// Invitation rollback must leave the additive upstream migration applied,
	// otherwise re-upgrading would try to add display_order a second time.
	if _, err := provider.DownTo(t.Context(), 70); err != nil {
		t.Fatal("invitation rollback:", err)
	}
	if err := database.QueryRow(`SELECT MAX(version_id) FROM goose_db_version WHERE is_applied=1`).Scan(&version); err != nil || version != 70 {
		t.Fatalf("rollback version: %d %v", version, err)
	}
	if _, err := provider.Up(t.Context()); err != nil {
		t.Fatal("re-upgrade after directory migration:", err)
	}
	if err := database.QueryRow(`SELECT display_order FROM event_types WHERE id='event'`).Scan(&order); err != nil || order != 7 {
		t.Fatalf("directory order changed after rollback: %d %v", order, err)
	}
}
