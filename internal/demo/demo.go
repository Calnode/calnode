// Package demo seeds and resets the public demo instance's database. The
// demo runs with no persistent volume — every container boot starts from an
// empty, freshly-migrated DB — so Seed doubles as "first boot" and "after a
// restart", and Reset (wipe + re-seed) is how the demo clears visitor data
// on a schedule without waiting for the container to restart on its own.
package demo

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/calnode/calnode/internal/uid"
)

// OwnerUserID is the fixed ID of the seeded demo owner account. Fixed (not
// randomly generated) so the one-click "enter demo" endpoint can mint a
// session for it without a password or a DB lookup.
const OwnerUserID = "demo-owner-user"

const (
	memberUserID = "demo-member-user"
	teamID       = "demo-team"
)

// Seed populates a freshly-migrated, empty database with sample data: an
// owner + one team member, three event types (two fixed, one round-robin),
// Monday-Friday availability, and a few upcoming sample bookings. Rows are
// inserted directly via SQL, bypassing the HTTP layer — the same pattern
// already used by this package's handler test fixtures.
func Seed(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("demo seed: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO server_settings (id) VALUES (1)`); err != nil {
		return fmt.Errorf("demo seed: server_settings: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO users (id, email, name, iana_timezone, is_admin, is_owner, email_login)
		VALUES (?, 'demo@calnode.com', 'Demo Owner', 'UTC', 1, 1, 0)`, OwnerUserID); err != nil {
		return fmt.Errorf("demo seed: owner user: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO users (id, email, name, iana_timezone, is_admin, is_owner, email_login)
		VALUES (?, 'alex@calnode.com', 'Alex Rivera', 'UTC', 0, 0, 0)`, memberUserID); err != nil {
		return fmt.Errorf("demo seed: member user: %w", err)
	}

	for _, userID := range []string{OwnerUserID, memberUserID} {
		for day := 1; day <= 5; day++ { // Monday(1)-Friday(5)
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO availability_rules (id, user_id, day_of_week, start_time, end_time)
				VALUES (?, ?, ?, '09:00', '17:00')`, uid.New(), userID, day); err != nil {
				return fmt.Errorf("demo seed: availability for %s: %w", userID, err)
			}
		}
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO teams (id, name, slug) VALUES (?, 'Demo Team', 'demo-team')`, teamID); err != nil {
		return fmt.Errorf("demo seed: team: %w", err)
	}
	teamMembers := []struct {
		userID   string
		role     string
		priority int
	}{
		{OwnerUserID, "owner", 0},
		{memberUserID, "member", 1},
	}
	for _, m := range teamMembers {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO team_members (id, team_id, user_id, role, routing_priority)
			VALUES (?, ?, ?, ?, ?)`, uid.New(), teamID, m.userID, m.role, m.priority); err != nil {
			return fmt.Errorf("demo seed: team member %s: %w", m.userID, err)
		}
	}

	eventTypes := []struct {
		id, slug, name, description string
		durationMinutes             int
		minNoticeMinutes            int
		maxFutureDays               int
		roundRobin                  bool
	}{
		{"demo-et-intro", "intro-call", "15-Minute Intro Call",
			"A quick chat to see if we're a good fit.", 15, 60, 30, false},
		{"demo-et-meeting", "30-min-meeting", "30-Minute Meeting",
			"A standard half-hour meeting slot.", 30, 60, 30, false},
		{"demo-et-teamsync", "team-sync", "Team Sync",
			"Round-robin sync across the team.", 45, 60, 30, true},
	}
	for _, et := range eventTypes {
		routingMode := "fixed"
		var teamVal any // NULL for fixed event types
		if et.roundRobin {
			routingMode = "round_robin"
			teamVal = teamID
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO event_types
			  (id, user_id, team_id, slug, name, description, duration_minutes,
			   location_type, routing_mode, min_notice_minutes, max_future_days,
			   is_active, is_public)
			VALUES (?, ?, ?, ?, ?, ?, ?, 'link', ?, ?, ?, 1, 1)`,
			et.id, OwnerUserID, teamVal, et.slug, et.name, et.description, et.durationMinutes,
			routingMode, et.minNoticeMinutes, et.maxFutureDays); err != nil {
			return fmt.Errorf("demo seed: event type %s: %w", et.slug, err)
		}

		hosts := []string{OwnerUserID}
		role := "required"
		if et.roundRobin {
			hosts = []string{OwnerUserID, memberUserID}
			role = "rotation"
		}
		for _, hostID := range hosts {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO event_type_hosts (id, event_type_id, user_id, role, priority)
				VALUES (?, ?, ?, ?, 0)`, uid.New(), et.id, hostID, role); err != nil {
				return fmt.Errorf("demo seed: event type host %s/%s: %w", et.slug, hostID, err)
			}
		}
	}

	now := time.Now().UTC()
	bookings := []struct {
		id, eventTypeID, hostID           string
		attendeeName, attendeeEmail       string
		minDaysOut, hour, durationMinutes int
	}{
		{"demo-booking-1", "demo-et-intro", OwnerUserID,
			"Jordan Lee", "jordan@example.com", 1, 10, 15},
		{"demo-booking-2", "demo-et-meeting", OwnerUserID,
			"Sam Patel", "sam@example.com", 2, 14, 30},
		{"demo-booking-3", "demo-et-teamsync", memberUserID,
			"Taylor Kim", "taylor@example.com", 3, 11, 45},
	}
	for _, b := range bookings {
		startAt := nextWeekdayAt(now, b.minDaysOut, b.hour)
		endAt := startAt.Add(time.Duration(b.durationMinutes) * time.Minute)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO bookings (id, event_type_id, host_id, start_at, end_at, status)
			VALUES (?, ?, ?, ?, ?, 'confirmed')`,
			b.id, b.eventTypeID, b.hostID, startAt.Format(time.RFC3339), endAt.Format(time.RFC3339)); err != nil {
			return fmt.Errorf("demo seed: booking %s: %w", b.id, err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO booking_hosts (id, booking_id, user_id, is_primary)
			VALUES (?, ?, ?, 1)`, uid.New(), b.id, b.hostID); err != nil {
			return fmt.Errorf("demo seed: booking host %s: %w", b.id, err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO booking_attendees (id, booking_id, name, email, iana_timezone, is_organizer)
			VALUES (?, ?, ?, ?, 'UTC', 1)`, uid.New(), b.id, b.attendeeName, b.attendeeEmail); err != nil {
			return fmt.Errorf("demo seed: booking attendee %s: %w", b.id, err)
		}
	}

	return tx.Commit()
}

// nextWeekdayAt returns the next Monday-Friday date at least minDaysOut days
// after now, at the given UTC hour — so seeded sample bookings always land
// inside the seeded Mon-Fri 09:00-17:00 availability window.
func nextWeekdayAt(now time.Time, minDaysOut, hour int) time.Time {
	d := now.AddDate(0, 0, minDaysOut)
	for d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
		d = d.AddDate(0, 0, 1)
	}
	return time.Date(d.Year(), d.Month(), d.Day(), hour, 0, 0, 0, time.UTC)
}

// Reset wipes every table in db and re-seeds it. Tables are enumerated
// dynamically rather than hardcoded, since this actively deletes data —
// see docs/ARCHITECTURE.md's hardcoded-column-count incident for why a
// stale hardcoded list is worth avoiding here specifically.
func Reset(ctx context.Context, db *sql.DB) (err error) {
	tables, err := listTables(ctx, db)
	if err != nil {
		return err
	}

	// foreign_keys is connection-scoped and can't be toggled mid-transaction,
	// so it's set here, before BeginTx — safe only because the pool is a
	// single persistent connection (db.SetMaxOpenConns(1), internal/db/db.go).
	// Needed because the delete order below is arbitrary relative to the
	// schema's 46 migrations' worth of foreign-key relationships.
	if _, ferr := db.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); ferr != nil {
		return fmt.Errorf("demo reset: disable foreign keys: %w", ferr)
	}
	defer func() {
		if _, ferr := db.ExecContext(ctx, `PRAGMA foreign_keys = ON`); ferr != nil && err == nil {
			err = fmt.Errorf("demo reset: re-enable foreign keys: %w", ferr)
		}
	}()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("demo reset: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	for _, t := range tables {
		if _, err = tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %q`, t)); err != nil {
			return fmt.Errorf("demo reset: delete from %s: %w", t, err)
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("demo reset: commit: %w", err)
	}

	return Seed(ctx, db)
}

func listTables(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT name FROM sqlite_master
		WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name != 'goose_db_version'`)
	if err != nil {
		return nil, fmt.Errorf("demo reset: list tables: %w", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("demo reset: scan table name: %w", err)
		}
		tables = append(tables, name)
	}
	return tables, rows.Err()
}
