package booking

import (
	"context"
	"database/sql"
	"slices"
	"time"
)

// Validate persisted authorization even when a caller bypasses the HTTP layer.
// Local slot rechecks must be supplied; they run on this same transaction.
func validateInvitationCreate(ctx context.Context, tx *sql.Tx, p *CreateParams) error {
	var eventID, name, email, mode, locationType, locationValue string
	var duration, before, after int
	var from, until sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT event_type_id,recipient_name,recipient_email,routing_mode,duration_minutes,buffer_before_minutes,buffer_after_minutes,available_from,available_until,location_type,location_value FROM scheduling_invitations WHERE id=?`, p.SchedulingInvitationID).Scan(&eventID, &name, &email, &mode, &duration, &before, &after, &from, &until, &locationType, &locationValue); err != nil {
		return err
	}
	if p.ValidateTx == nil || p.EventTypeID != eventID || p.Organizer.Name != name || p.Organizer.Email != email || p.RoutingMode != mode || p.EndAt.Sub(p.StartAt) != time.Duration(duration)*time.Minute || !insideInvitationWindow(from, until, p.StartAt, p.EndAt) {
		return ErrInvitationUnavailable
	}
	p.BufferBeforeMinutes, p.BufferAfterMinutes = before, after
	p.LocationType, p.LocationValue = locationType, locationValue
	rows, err := tx.QueryContext(ctx, `SELECT user_id,role FROM scheduling_invitation_hosts WHERE invitation_id=?`, p.SchedulingInvitationID)
	if err != nil {
		return err
	}
	roles := map[string]string{}
	for rows.Next() {
		var id, role string
		if err := rows.Scan(&id, &role); err != nil {
			rows.Close()
			return err
		}
		roles[id] = role
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range p.HostIDs {
		expected := "required"
		if mode == "round_robin" {
			expected = "rotation"
		}
		if roles[id] != expected {
			return ErrInvitationUnavailable
		}
	}
	for _, id := range p.OptionalHosts {
		if roles[id] != "optional" {
			return ErrInvitationUnavailable
		}
	}
	for _, id := range p.RequiredHosts {
		if mode != "round_robin" || roles[id] != "required" {
			return ErrInvitationUnavailable
		}
	}
	for id, role := range roles {
		if role == "required" && !slices.Contains(p.HostIDs, id) && !slices.Contains(p.RequiredHosts, id) {
			return ErrInvitationUnavailable
		}
	}
	return nil
}

func insideInvitationWindow(from, until sql.NullString, start, end time.Time) bool {
	if !end.After(start) {
		return false
	}
	if from.Valid {
		bound, err := time.Parse(time.RFC3339Nano, from.String)
		if err != nil || start.Before(bound) {
			return false
		}
	}
	if until.Valid {
		bound, err := time.Parse(time.RFC3339Nano, until.String)
		if err != nil || end.After(bound) {
			return false
		}
	}
	return true
}

// Expiration and consumed status authorize new bookings, not management of an
// existing appointment. Retained interval/window and buffers still apply.
func invitationRescheduleBounds(ctx context.Context, tx *sql.Tx, b *Booking, start, end time.Time) (int, int, error) {
	var duration, before, after int
	var from, until sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT duration_minutes,buffer_before_minutes,buffer_after_minutes,available_from,available_until FROM scheduling_invitations WHERE id=? AND booking_id=? AND status='booked'`, b.SchedulingInvitationID, b.ID).Scan(&duration, &before, &after, &from, &until); err != nil {
		return 0, 0, ErrInvitationUnavailable
	}
	if end.Sub(start) != b.EndAt.Sub(b.StartAt) || end.Sub(start) != time.Duration(duration)*time.Minute || !insideInvitationWindow(from, until, start, end) {
		return 0, 0, ErrInvitationUnavailable
	}
	return before, after, nil
}
