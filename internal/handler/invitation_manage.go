package handler

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/calnode/calnode/internal/booking"
)

func (h *Handler) bookingInvitationContext(ctx context.Context, b *booking.Booking) (schedulingContext, error) {
	var creator string
	if err := h.db.QueryRowContext(ctx, `SELECT created_by FROM scheduling_invitations WHERE id=? AND booking_id=? AND status='booked'`, b.SchedulingInvitationID, b.ID).Scan(&creator); err != nil {
		return schedulingContext{}, errSlotUnavailable
	}
	inv, err := h.loadSchedulingInvitation(ctx, b.SchedulingInvitationID, creator)
	if err != nil {
		return schedulingContext{}, err
	}
	schedule, err := h.invitationEffectiveContext(ctx, inv)
	if err != nil {
		return schedulingContext{}, errSlotUnavailable
	}
	if b.EndAt.Sub(b.StartAt) != time.Duration(inv.DurationMinutes)*time.Minute {
		return schedulingContext{}, errSlotUnavailable
	}
	rows, err := h.db.QueryContext(ctx, `SELECT user_id FROM booking_hosts WHERE booking_id=? ORDER BY is_primary DESC,user_id`, b.ID)
	if err != nil {
		return schedulingContext{}, err
	}
	hosts := []EventHost{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return schedulingContext{}, err
		}
		hosts = append(hosts, EventHost{UserID: id, Role: "required"})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return schedulingContext{}, err
	}
	if len(hosts) == 0 {
		return schedulingContext{}, errSlotUnavailable
	}
	schedule.Hosts = hosts
	schedule.Event.RoutingMode = "collective"
	schedule.ExcludeBookingID = b.ID
	return schedule, nil
}

func (h *Handler) validateInvitationReschedule(ctx context.Context, b *booking.Booking, start, end time.Time) error {
	if end.Sub(start) != b.EndAt.Sub(b.StartAt) {
		return errSlotUnavailable
	}
	schedule, err := h.bookingInvitationContext(ctx, b)
	if err != nil {
		return err
	}
	if _, err := recheckSchedulingContext(ctx, h.db, schedule, start); err != nil {
		return err
	}
	hosts, _, _ := resolveBookingHostPool(schedule.Hosts, "collective")
	_, _, err = h.calendarFreeHosts(ctx, &bookableEventType{ID: b.EventTypeID, RoutingMode: "collective", BufferBeforeMinutes: schedule.Event.BufferBeforeMinutes, BufferAfterMinutes: schedule.Event.BufferAfterMinutes}, hosts, nil, nil, start, end)
	return err
}

func (h *Handler) rescheduleBooking(ctx context.Context, id string, start, end time.Time) (*booking.Booking, error) {
	b, err := h.bookingSvc.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if b.SchedulingInvitationID == "" {
		return h.bookingSvc.Reschedule(ctx, id, start, end)
	}
	schedule, err := h.bookingInvitationContext(ctx, b)
	if err != nil {
		return nil, err
	}
	return h.bookingSvc.RescheduleValidated(ctx, id, start, end, func(ctx context.Context, tx *sql.Tx) error {
		_, err := recheckSchedulingContext(ctx, tx, schedule, start)
		return err
	})
}

func (h *Handler) GetManageInvitationSlots(w http.ResponseWriter, r *http.Request) {
	invitationHeaders(w)
	b, err := h.bookingSvc.ValidateManageToken(r.Context(), r.PathValue("token"))
	if err != nil {
		h.writeError(w, 404, "Invalid booking-management link.")
		return
	}
	if b.Status == "cancelled" {
		h.writeError(w, 410, "This booking has been cancelled.")
		return
	}
	if b.SchedulingInvitationID == "" {
		h.writeError(w, 404, "Invitation booking not found.")
		return
	}
	schedule, err := h.bookingInvitationContext(r.Context(), b)
	if err != nil {
		h.writeError(w, 410, "Rescheduling is currently unavailable. Contact the host.")
		return
	}
	h.writeInvitationSlots(w, r, schedule)
}
