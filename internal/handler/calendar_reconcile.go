package handler

import (
	"context"
	"time"

	"github.com/calnode/calnode/internal/calendar"
)

// calReconcileInterval is how often the reconciler sweeps for divergence between
// booking state and Google Calendar. Failed inline ops also nudge it to run sooner.
const calReconcileInterval = 2 * time.Minute

// StartCalendarReconciler launches the calendar reconciler: a pass at startup,
// then on a ticker, plus whenever an inline calendar op fails (nudge). It heals
// divergence between booking state and the calendar — deleting events left behind
// by cancelled bookings, and creating events missing from upcoming confirmed
// bookings (e.g. when the original attempt failed due to a network blip). It
// stops when ctx is cancelled. Safe to call when calendar is unconfigured — each
// pass no-ops until a client is set.
func (h *Handler) StartCalendarReconciler(ctx context.Context) {
	go func() {
		h.reconcileCalendar()
		ticker := time.NewTicker(calReconcileInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				h.reconcileCalendar()
			case <-h.calNudge:
				h.reconcileCalendar()
			}
		}
	}()
}

// nudgeCalendarReconcile asks the reconciler to run soon. Non-blocking and
// coalesced (a pending nudge is enough); safe to call from inline op goroutines.
func (h *Handler) nudgeCalendarReconcile() {
	select {
	case h.calNudge <- struct{}{}:
	default:
	}
}

func (h *Handler) reconcileCalendar() {
	gc := h.getCal()
	if gc == nil {
		return // calendar not configured — nothing to reconcile
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	h.reconcileCancellations(ctx, gc)
	h.reconcileCreations(ctx, gc)
	h.reconcileReschedules(ctx, gc)
}

// reconcileReschedules re-applies the calendar time for booking_hosts rows flagged
// needs_sync — i.e. an inline reschedule move (UpdateEvent) failed and the event is
// stranded at the old time. This is the time-drift counterpart to the presence/
// absence passes: drift can't be inferred from booking state, so it's signalled by
// the flag. Idempotent — re-applying the same time is a no-op on Google.
func (h *Handler) reconcileReschedules(ctx context.Context, gc *calendar.Service) {
	now := time.Now().UTC().Format(time.RFC3339)
	type drift struct {
		bookingID, userID, eventID, startStr, endStr string
	}
	// Read fully before acting — the single DB connection can't serve the inner
	// UpdateEvent/UPDATE while a cursor is open (would deadlock). Only upcoming
	// confirmed bookings matter; past ones need no calendar correction.
	var items []drift
	rows, err := h.db.QueryContext(ctx, `
		SELECT bh.booking_id, bh.user_id, bh.external_event_id, b.start_at, b.end_at
		FROM booking_hosts bh JOIN bookings b ON b.id = bh.booking_id
		WHERE bh.needs_sync = 1 AND b.status = 'confirmed'
		  AND COALESCE(bh.external_event_id, '') != '' AND b.end_at > ?`, now)
	if err != nil {
		h.logger.Error("reconcile: query drifted events", "error", err)
		return
	}
	for rows.Next() {
		var d drift
		if err := rows.Scan(&d.bookingID, &d.userID, &d.eventID, &d.startStr, &d.endStr); err == nil {
			items = append(items, d)
		}
	}
	rows.Close() // #nosec G104 -- rows already fully consumed above; nothing actionable on close error

	for _, d := range items {
		start, err1 := time.Parse(time.RFC3339Nano, d.startStr)
		end, err2 := time.Parse(time.RFC3339Nano, d.endStr)
		if err1 != nil || err2 != nil {
			continue
		}
		if err := gc.UpdateEvent(ctx, d.userID, d.eventID, start, end); err != nil {
			h.logger.Error("reconcile: re-apply event time", "error", err, "booking_id", d.bookingID, "host", d.userID)
			continue // leave the flag set; retry next sweep
		}
		if _, err := h.db.ExecContext(ctx,
			`UPDATE booking_hosts SET needs_sync = 0 WHERE booking_id = ? AND user_id = ?`,
			d.bookingID, d.userID); err != nil {
			h.logger.Error("reconcile: clear needs_sync", "error", err, "booking_id", d.bookingID)
		}
	}
}

// reconcileCancellations deletes calendar events still attached to cancelled
// bookings (the inline cancel never reached Google). On success the per-host
// event id is cleared so the row drops out of the next sweep.
func (h *Handler) reconcileCancellations(ctx context.Context, gc *calendar.Service) {
	type orphan struct{ bookingID, userID, eventID string }
	// Read fully before acting — the single DB connection can't serve the inner
	// CancelEvent/UPDATE queries while a cursor is open (would deadlock).
	var orphans []orphan
	rows, err := h.db.QueryContext(ctx, `
		SELECT bh.booking_id, bh.user_id, bh.external_event_id
		FROM booking_hosts bh JOIN bookings b ON b.id = bh.booking_id
		WHERE b.status = 'cancelled' AND COALESCE(bh.external_event_id, '') != ''`)
	if err != nil {
		h.logger.Error("reconcile: query orphaned events", "error", err)
		return
	}
	for rows.Next() {
		var o orphan
		if err := rows.Scan(&o.bookingID, &o.userID, &o.eventID); err == nil {
			orphans = append(orphans, o)
		}
	}
	rows.Close() // #nosec G104 -- rows already fully consumed above; nothing actionable on close error

	for _, o := range orphans {
		if err := gc.CancelEvent(ctx, o.userID, o.eventID); err != nil {
			h.logger.Error("reconcile: cancel orphaned event", "error", err, "booking_id", o.bookingID, "host", o.userID)
			continue // leave the id in place; retry next sweep
		}
		if _, err := h.db.ExecContext(ctx,
			`UPDATE booking_hosts SET external_event_id = NULL WHERE booking_id = ? AND user_id = ?`,
			o.bookingID, o.userID); err != nil {
			h.logger.Error("reconcile: clear orphaned event id", "error", err, "booking_id", o.bookingID)
		}
	}
}

// reconcileCreations creates calendar events missing from upcoming confirmed
// bookings — hosts who should have an event but don't (the inline create failed).
// Hosts without a destination calendar are skipped so we don't retry forever.
func (h *Handler) reconcileCreations(ctx context.Context, gc *calendar.Service) {
	nowT := time.Now().UTC()
	now := nowT.Format(time.RFC3339)
	// Grace period: skip very recent bookings so we don't race (and duplicate) an
	// inline CreateEvent that simply hasn't stored its id yet.
	cutoff := nowT.Add(-5 * time.Minute).Format(time.RFC3339)
	type missing struct {
		bookingID, userID, etName, orgName, orgEmail, startStr, endStr string
		locationType, bookingLoc                                       string
		isPrimary                                                      bool
	}
	var items []missing
	rows, err := h.db.QueryContext(ctx, `
		SELECT bh.booking_id, bh.user_id, bh.is_primary, et.name, et.location_type,
		       COALESCE(b.location_value, ''),
		       COALESCE(o.name, ''), COALESCE(o.email, ''), b.start_at, b.end_at
		FROM booking_hosts bh
		JOIN bookings b ON b.id = bh.booking_id
		JOIN event_types et ON et.id = b.event_type_id
		LEFT JOIN booking_attendees o ON o.booking_id = b.id AND o.is_organizer = 1
		WHERE b.status = 'confirmed' AND b.end_at > ? AND b.created_at < ?
		  AND COALESCE(bh.external_event_id, '') = ''`, now, cutoff)
	if err != nil {
		h.logger.Error("reconcile: query missing events", "error", err)
		return
	}
	for rows.Next() {
		var m missing
		var primary int
		if err := rows.Scan(&m.bookingID, &m.userID, &primary, &m.etName, &m.locationType,
			&m.bookingLoc, &m.orgName, &m.orgEmail, &m.startStr, &m.endStr); err == nil {
			m.isPrimary = primary != 0
			items = append(items, m)
		}
	}
	rows.Close() // #nosec G104 -- rows already fully consumed above; nothing actionable on close error

	for _, m := range items {
		has, err := gc.HasDestination(ctx, m.userID)
		if err != nil {
			h.logger.Error("reconcile: check destination", "error", err, "host", m.userID)
			continue
		}
		if !has {
			continue // host has no calendar to write to — nothing to heal
		}
		start, err1 := time.Parse(time.RFC3339, m.startStr)
		end, err2 := time.Parse(time.RFC3339, m.endStr)
		if err1 != nil || err2 != nil {
			continue
		}
		// Online meeting (Meet/Teams): the primary's healed event mints the link only
		// when its connected provider natively matches the platform (Meet↔Google,
		// Teams↔Microsoft) and the booking has no link yet; otherwise keep the
		// existing/manual link via Location and never fabricate the wrong kind.
		autoGenMeet := false
		if m.isPrimary && m.bookingLoc == "" && onlineMeetingLocation(m.locationType) {
			if _, provider, perr := gc.Connected(ctx, m.userID); perr == nil {
				autoGenMeet = providerMintsPlatform(m.locationType, provider)
			}
		}
		eventID, link, err := gc.CreateEvent(ctx, m.userID, calendar.CreateEventParams{
			Summary:        m.etName + " with " + m.orgName,
			Description:    "Booking ID: " + m.bookingID,
			Location:       m.bookingLoc,
			Start:          start,
			End:            end,
			OrganizerName:  m.orgName,
			OrganizerEmail: m.orgEmail,
			AddMeet:        autoGenMeet,
		})
		if err != nil {
			h.logger.Error("reconcile: create missing event", "error", err, "booking_id", m.bookingID, "host", m.userID)
			continue
		}
		if eventID == "" {
			continue
		}
		if _, err := h.db.ExecContext(ctx,
			`UPDATE booking_hosts SET external_event_id = ? WHERE booking_id = ? AND user_id = ?`,
			eventID, m.bookingID, m.userID); err != nil {
			h.logger.Error("reconcile: save healed event id", "error", err, "booking_id", m.bookingID)
		}
		if m.isPrimary {
			if _, err := h.db.ExecContext(ctx,
				`UPDATE bookings SET external_event_id = ? WHERE id = ?`, eventID, m.bookingID); err != nil {
				h.logger.Error("reconcile: save healed booking event id", "error", err, "booking_id", m.bookingID)
			}
			if link != "" {
				if _, err := h.db.ExecContext(ctx,
					`UPDATE bookings SET location_value = ? WHERE id = ?`, link, m.bookingID); err != nil {
					h.logger.Error("reconcile: save healed meet link", "error", err, "booking_id", m.bookingID)
				}
			}
		}
	}
}
