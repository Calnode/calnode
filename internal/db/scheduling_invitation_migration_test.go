package db_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/calnode/calnode/internal/db"
	"github.com/pressly/goose/v3"
)

func TestSchedulingInvitationMigrationExistingDatabase(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "existing.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	provider, err := goose.NewProvider(goose.DialectSQLite3, database, os.DirFS("migrations"), goose.WithDisableGlobalRegistry(true))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := provider.UpTo(ctx, 69); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO users (id,email,name,iana_timezone) VALUES ('owner','owner@example.com','Owner','UTC')`,
		`INSERT INTO event_types (id,user_id,slug,name,duration_minutes,slot_interval_minutes) VALUES ('event','owner','meeting','Meeting',45,15)`,
		`INSERT INTO event_type_hosts (id,event_type_id,user_id,role) VALUES ('host','event','owner','required')`,
		`INSERT INTO availability_rules (id,user_id,day_of_week,start_time,end_time) VALUES ('rule','owner',1,'09:00','17:00')`,
		`INSERT INTO bookings (id,event_type_id,host_id,start_at,end_at,status) VALUES ('booking','event','owner','2026-11-01T09:00:00Z','2026-11-01T09:45:00Z','confirmed')`,
		`INSERT INTO booking_hosts (booking_id,user_id,is_primary) VALUES ('booking','owner',1)`,
	} {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(database); err != nil {
		t.Fatal("migration not idempotent:", err)
	}
	var duration, interval int
	var min, max, increment sql.NullInt64
	if err := database.QueryRow(`SELECT duration_minutes, slot_interval_minutes, min_duration_minutes, max_duration_minutes, duration_increment_minutes FROM event_types WHERE id = 'event'`).Scan(&duration, &interval, &min, &max, &increment); err != nil {
		t.Fatal(err)
	}
	if duration != 45 || interval != 15 || min.Valid || max.Valid || increment.Valid {
		t.Fatal("existing event-type policy changed")
	}
	// A legacy default can still change without becoming a duration-range event.
	if _, err := database.Exec(`UPDATE event_types SET duration_minutes = 60 WHERE id = 'event'`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"bookings", "booking_hosts", "event_type_hosts", "availability_rules"} {
		var count int
		if err := database.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s was not preserved: count=%d err=%v", table, count, err)
		}
	}
	for _, statement := range []string{
		`INSERT INTO scheduling_invitations (id,event_type_id,created_by,recipient_email,duration_minutes,slot_interval_minutes,buffer_before_minutes,buffer_after_minutes,min_notice_minutes,max_future_days,routing_mode,rr_strategy,expires_at) VALUES ('invitation','event','owner','customer@example.com',90,15,0,0,0,60,'fixed','even','2027-01-01T00:00:00Z')`,
		`INSERT INTO scheduling_invitation_hosts (invitation_id,user_id,role) VALUES ('invitation','owner','required')`,
		`INSERT INTO scheduling_invitation_tokens (token_hash,invitation_id,expires_at) VALUES ('0123456789012345678901234567890123456789012345678901234567890123','invitation','2027-01-01T00:00:00Z')`,
	} {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`UPDATE event_types SET min_duration_minutes = 15 WHERE id = 'event'`,
		`UPDATE scheduling_invitations SET duration_minutes = 0 WHERE id = 'invitation'`,
		`UPDATE scheduling_invitations SET status = 'booked' WHERE id = 'invitation'`,
		`UPDATE scheduling_invitation_hosts SET role = 'bogus' WHERE invitation_id = 'invitation'`,
		`DELETE FROM users WHERE id = 'owner'`,
		`DELETE FROM event_types WHERE id = 'event'`,
	} {
		if _, err := database.Exec(statement); err == nil {
			t.Fatalf("invalid state accepted: %s", statement)
		}
	}
	// Down works on a populated database, and does not disturb legacy data.
	if _, err := provider.DownTo(ctx, 69); err != nil {
		t.Fatal("down:", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal("re-upgrade:", err)
	}
	var violations int
	if err := database.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&violations); err != nil || violations != 0 {
		t.Fatalf("foreign key check: %d, %v", violations, err)
	}
}
