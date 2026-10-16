package handler

import (
	"context"
	"database/sql"
	"fmt"
)

// ReminderAllowed checks the primary host's calendar event without changing the
// booking lifecycle. Deleting a secondary host's copy is not a booking cancellation.
func (h *Handler) ReminderAllowed(ctx context.Context, bookingID string) (bool, error) {
	var hostID, eventID, calendarID, provider string
	err := h.db.QueryRowContext(ctx, `
  SELECT bh.user_id, bh.external_event_id, COALESCE(bh.external_calendar_id, ''), COALESCE(bh.external_provider, '')
  FROM booking_hosts bh JOIN bookings b ON b.id = bh.booking_id
  WHERE b.id = ? AND bh.user_id = b.host_id AND COALESCE(bh.external_event_id, '') != ''`, bookingID).
		Scan(&hostID, &eventID, &calendarID, &provider)
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("reminder: load calendar event: %w", err)
	}
	gc := h.getCal()
	if gc == nil {
		return false, fmt.Errorf("reminder: calendar service unavailable")
	}
	cancelled, err := gc.EventCancelled(ctx, hostID, calendarID, eventID, provider)
	if err != nil {
		return false, err
	}
	if cancelled {
		h.logger.Info("reminder: skipped externally cancelled event", "booking_id", bookingID)
	}
	return !cancelled, nil
}
