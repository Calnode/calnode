package handler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/calnode/calnode/internal/uid"
)

// copySlugSuffix is appended to the source slug for the first copy; subsequent copies
// get "-copy-2", "-copy-3", … The slug is the public booking URL, so it stays readable
// instead of being randomised.
const copySlugSuffix = "-copy"

// maxCopySlugAttempts bounds the "-copy-N" search. It is far past anything useful — it
// exists so a pathological slug space fails with a clear 409 rather than looping.
const maxCopySlugAttempts = 50

// errNoFreeCopySlug means every candidate slug up to maxCopySlugAttempts was taken.
var errNoFreeCopySlug = errors.New("no free copy slug")

// questionCopy is one intake question, without its id (the copy gets a fresh one).
type questionCopy struct {
	label    string
	qType    string
	options  sql.NullString // JSON array for type='select'; NULL otherwise
	required int
	position int
}

// hostCopy is one host assignment, without its id.
type hostCopy struct {
	userID   string
	role     string
	priority int
}

// availabilityRuleCopy is one event-type-specific weekly availability rule, without
// its id.
type availabilityRuleCopy struct {
	userID    string
	dayOfWeek int
	startTime string
	endTime   string
}

// eventTypeChildren is every row that hangs off an event type and belongs in a copy of
// it: the intake form, the host list, the event-specific availability rules, and the
// reminder schedule.
//
// Bookings are deliberately absent. A duplicate is a fresh, unpublished template; a
// copied booking would be a meeting nobody agreed to, with a live manage link. The
// per-email booking cap and the double-booking guard also key on real bookings, so
// copies would distort both.
//
// The per-event-type email copy (msg_* / subj_*) needs no entry here: those are columns
// on event_types, so the row copy carries them.
type eventTypeChildren struct {
	questions []questionCopy
	hosts     []hostCopy
	rules     []availabilityRuleCopy
	reminders []int // hours_before
}

// DuplicateEventType handles POST /v1/event-types/{slug}/duplicate.
//
// It copies the event type and everything that hangs off it — intake questions, host
// assignments, event-specific availability rules, reminder schedule, and the custom
// email subjects/notes — as a single transaction, so a partial copy can never be left
// behind for someone to find and puzzle over.
//
// Three fields are deliberately not inherited:
//
//   - is_active is forced to 0. A copy exists to be edited; publishing one the moment
//     it is created is the accident this endpoint has to avoid.
//   - archived_at is cleared. Archiving is a property of the original's lifecycle, not
//     of a new draft, and an archived copy would be invisible in the default list.
//   - slug is regenerated, because it is UNIQUE.
//
// Everything else is copied verbatim, price_cents and currency included. Zeroing a
// price would be the more dangerous default: the operator would see a familiar event
// type, publish it, and start taking free bookings for a paid meeting.
func (h *Handler) DuplicateEventType(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	srcSlug := r.PathValue("slug")

	// Owner-scoped, like PATCH and DELETE. An assigned host sees an event type
	// read-only (only the owner can edit), so handing them a copy they could not
	// then change would be worse than refusing.
	srcID := h.eventTypeIDForOwner(w, r, srcSlug, user.ID)
	if srcID == "" {
		return // eventTypeIDForOwner already wrote 404/500
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "duplicate event type: begin tx", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback() //nolint:errcheck

	// Read every child row up front, before anything is written: see
	// loadEventTypeChildren on why the reads and the writes cannot interleave.
	children, err := loadEventTypeChildren(r.Context(), tx, srcID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "duplicate event type: load children", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	newSlug, err := uniqueCopySlug(r.Context(), tx, srcSlug)
	if errors.Is(err, errNoFreeCopySlug) {
		h.writeError(w, http.StatusConflict,
			"could not generate a free slug for the copy — rename or delete some existing copies first")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "duplicate event type: pick slug", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	newID := uid.New()
	// INSERT … SELECT rather than reading the row into Go and writing it back: every
	// column the copy inherits is named once, and its value never passes through this
	// process, so a column cannot be quietly zeroed or truncated in transit. The
	// overridden fields are the only literals here. created_at is omitted so the copy
	// gets its own creation timestamp from the column default.
	if _, err := tx.ExecContext(r.Context(), `
		INSERT INTO event_types (
		  id, user_id, team_id, slug, name, description,
		  duration_minutes, slot_interval_minutes,
		  location_type, location_value,
		  routing_mode, rr_strategy,
		  buffer_before_minutes, buffer_after_minutes,
		  min_notice_minutes, max_future_days, max_active_bookings, seat_limit,
		  is_active, is_public, show_taken_slots, archived_at,
		  msg_confirmation, msg_cancellation, msg_reschedule, msg_reminder, msg_greeting,
		  subj_confirmation, subj_cancellation, subj_reschedule, subj_reminder,
		  price_cents, currency)
		SELECT
		  ?, user_id, team_id, ?, name, description,
		  duration_minutes, slot_interval_minutes,
		  location_type, location_value,
		  routing_mode, rr_strategy,
		  buffer_before_minutes, buffer_after_minutes,
		  min_notice_minutes, max_future_days, max_active_bookings, seat_limit,
		  0, is_public, show_taken_slots, NULL,
		  msg_confirmation, msg_cancellation, msg_reschedule, msg_reminder, msg_greeting,
		  subj_confirmation, subj_cancellation, subj_reschedule, subj_reminder,
		  price_cents, currency
		FROM event_types WHERE id = ?`, newID, newSlug, srcID); err != nil {
		h.logger.ErrorContext(r.Context(), "duplicate event type: copy row", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err := insertEventTypeChildren(r.Context(), tx, newID, children); err != nil {
		h.logger.ErrorContext(r.Context(), "duplicate event type: copy children", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err := tx.Commit(); err != nil {
		h.logger.ErrorContext(r.Context(), "duplicate event type: commit", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Answer with the copy in the same shape as CreateEventType, so the admin UI can
	// drop the caller straight into the new event type's editor.
	row := h.db.QueryRowContext(r.Context(), selectETCols+" WHERE id = ?", newID)
	et, err := scanEventType(row)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "duplicate event type: fetch copy", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := h.loadReminders(r.Context(), newID, et); err != nil {
		h.logger.ErrorContext(r.Context(), "duplicate event type: load reminders", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	et.Owned = true // the copy belongs to the caller, like a freshly created one
	h.writeJSON(w, http.StatusCreated, et)
}

// uniqueCopySlug returns the first free "<slug>-copy", "<slug>-copy-2", … for a copy of
// srcSlug.
//
// event_types.slug is UNIQUE across the whole instance rather than per user, so the
// lookup is deliberately not owner-scoped: a slug taken by another user's event type is
// still taken.
//
// It runs inside the caller's transaction, so the check and the INSERT that consumes its
// answer are one atomic unit. Choosing the slug beforehand would leave a window in which
// a concurrent duplicate claims it and the INSERT dies on the constraint instead.
func uniqueCopySlug(ctx context.Context, tx *sql.Tx, srcSlug string) (string, error) {
	for i := 1; i <= maxCopySlugAttempts; i++ {
		candidate := srcSlug + copySlugSuffix
		if i > 1 {
			candidate = fmt.Sprintf("%s%s-%d", srcSlug, copySlugSuffix, i)
		}
		var exists int
		err := tx.QueryRowContext(ctx,
			`SELECT 1 FROM event_types WHERE slug = ?`, candidate).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", errNoFreeCopySlug
}

// loadEventTypeChildren reads every child row of srcID into memory.
//
// Each cursor is drained and closed before the next statement runs, and nothing is
// written until all of them are closed. The pool is MaxOpenConns(1) (ARCHITECTURE §4),
// so a query issued while a cursor is open waits for the connection that cursor is
// holding — a deadlock that surfaces as a confusing "context deadline exceeded" rather
// than as a lock error. Same shape as loadHostSchedule and the calendar reconciler.
func loadEventTypeChildren(ctx context.Context, tx *sql.Tx, srcID string) (*eventTypeChildren, error) {
	var c eventTypeChildren

	qRows, err := tx.QueryContext(ctx, `
		SELECT label, type, options, required, position
		FROM event_type_questions WHERE event_type_id = ? ORDER BY position`, srcID)
	if err != nil {
		return nil, err
	}
	for qRows.Next() {
		var q questionCopy
		if err := qRows.Scan(&q.label, &q.qType, &q.options, &q.required, &q.position); err != nil {
			qRows.Close() // #nosec G104 -- already returning the scan error; nothing more actionable
			return nil, err
		}
		c.questions = append(c.questions, q)
	}
	qRows.Close() // #nosec G104 -- rows already fully consumed; nothing actionable on close error
	if err := qRows.Err(); err != nil {
		return nil, err
	}

	hRows, err := tx.QueryContext(ctx, `
		SELECT user_id, role, priority
		FROM event_type_hosts WHERE event_type_id = ? ORDER BY priority, user_id`, srcID)
	if err != nil {
		return nil, err
	}
	for hRows.Next() {
		var hc hostCopy
		if err := hRows.Scan(&hc.userID, &hc.role, &hc.priority); err != nil {
			hRows.Close() // #nosec G104 -- already returning the scan error; nothing more actionable
			return nil, err
		}
		c.hosts = append(c.hosts, hc)
	}
	hRows.Close() // #nosec G104 -- rows already fully consumed; nothing actionable on close error
	if err := hRows.Err(); err != nil {
		return nil, err
	}

	// Event-specific rules only. A rule with event_type_id IS NULL is the host's global
	// default and already applies to every event type they host, including this copy —
	// copying it would silently promote a global rule into an event-specific one, and
	// the original's later edits would then stop reaching the copy.
	rRows, err := tx.QueryContext(ctx, `
		SELECT user_id, day_of_week, start_time, end_time
		FROM availability_rules WHERE event_type_id = ?
		ORDER BY user_id, day_of_week, start_time`, srcID)
	if err != nil {
		return nil, err
	}
	for rRows.Next() {
		var ar availabilityRuleCopy
		if err := rRows.Scan(&ar.userID, &ar.dayOfWeek, &ar.startTime, &ar.endTime); err != nil {
			rRows.Close() // #nosec G104 -- already returning the scan error; nothing more actionable
			return nil, err
		}
		c.rules = append(c.rules, ar)
	}
	rRows.Close() // #nosec G104 -- rows already fully consumed; nothing actionable on close error
	if err := rRows.Err(); err != nil {
		return nil, err
	}

	remRows, err := tx.QueryContext(ctx, `
		SELECT hours_before FROM event_type_reminders
		WHERE event_type_id = ? ORDER BY hours_before DESC`, srcID)
	if err != nil {
		return nil, err
	}
	for remRows.Next() {
		var hb int
		if err := remRows.Scan(&hb); err != nil {
			remRows.Close() // #nosec G104 -- already returning the scan error; nothing more actionable
			return nil, err
		}
		c.reminders = append(c.reminders, hb)
	}
	remRows.Close() // #nosec G104 -- rows already fully consumed; nothing actionable on close error
	if err := remRows.Err(); err != nil {
		return nil, err
	}

	return &c, nil
}

// insertEventTypeChildren writes the materialised child rows against newID, each with a
// fresh id. Runs in the same transaction as the parent row copy, so any failure here
// takes the whole duplicate with it.
func insertEventTypeChildren(ctx context.Context, tx *sql.Tx, newID string, c *eventTypeChildren) error {
	for _, q := range c.questions {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO event_type_questions (id, event_type_id, label, type, options, required, position)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			uid.New(), newID, q.label, q.qType, q.options, q.required, q.position); err != nil {
			return fmt.Errorf("copy question: %w", err)
		}
	}
	for _, hc := range c.hosts {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO event_type_hosts (id, event_type_id, user_id, role, priority)
			VALUES (?, ?, ?, ?, ?)`,
			uid.New(), newID, hc.userID, hc.role, hc.priority); err != nil {
			return fmt.Errorf("copy host: %w", err)
		}
	}
	for _, ar := range c.rules {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO availability_rules (id, user_id, event_type_id, day_of_week, start_time, end_time)
			VALUES (?, ?, ?, ?, ?, ?)`,
			uid.New(), ar.userID, newID, ar.dayOfWeek, ar.startTime, ar.endTime); err != nil {
			return fmt.Errorf("copy availability rule: %w", err)
		}
	}
	for _, hb := range c.reminders {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO event_type_reminders (id, event_type_id, hours_before)
			VALUES (?, ?, ?)`, uid.New(), newID, hb); err != nil {
			return fmt.Errorf("copy reminder: %w", err)
		}
	}
	return nil
}
