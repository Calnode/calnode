package handler

import (
	"context"
	"errors"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/mailer"
)

// Invite delivery decides who sends the booker's calendar invite (migration 00068):
//
//   - booking.InviteByCalendar: each host's connected calendar invites the booker, so
//     Google/Microsoft email the invite from that host's own account. The default.
//   - booking.InviteByCalnode: the hosts' events are still written (they block time and
//     mint the Meet/Teams link) but with no guests, so the provider emails nobody.
//     Calnode sends the invite itself on the confirmation email, organized by the
//     instance's sender identity (Settings → Email). No host's address reaches the booker,
//     and a team books under one name.
//
// The mode is copied onto the booking when it is created (booking.Booking.InviteDelivery)
// and read from there afterwards, so a booking's reschedule and cancel follow the channel
// its invite actually went out on.

var errInviteSenderMissing = errors.New(
	"set up email in Settings → Email before letting Calnode send invites: the invite is delivered by email, from that sender")

// normalizeInviteDelivery maps the API value to a stored mode. Empty means the default,
// matching booking.CreateParams; anything else unknown is rejected.
func normalizeInviteDelivery(v string) (string, bool) {
	switch v {
	case "", booking.InviteByCalendar:
		return booking.InviteByCalendar, true
	case booking.InviteByCalnode:
		return booking.InviteByCalnode, true
	}
	return "", false
}

// calendarInvitee is the address passed as CreateEventParams.OrganizerEmail for a host's
// calendar event: the booker when the hosts' calendars send the invite, "" when Calnode
// does. Google and Microsoft add that address as the event's attendee, so "" leaves them
// no one to email and the event private to the host. CalDAV never sends invites; there it
// only decides whether the stored event names the booker at all.
func calendarInvitee(mode, bookerEmail string) string {
	if mode == booking.InviteByCalnode {
		return ""
	}
	return bookerEmail
}

// inviteSender returns the instance's sender identity, the From name and address in
// Settings → Email. It organizes invites Calnode sends, which puts replies (RSVPs) where
// the operator already reads mail rather than in any one host's inbox.
func (h *Handler) inviteSender(ctx context.Context) (name, email string) {
	if err := h.db.QueryRowContext(ctx,
		`SELECT email_from_name, email_from FROM server_settings WHERE id = 1`).Scan(&name, &email); err != nil {
		h.logger.ErrorContext(ctx, "load invite sender", "error", err)
		return "", ""
	}
	return name, email
}

// inviteSenderReady reports whether Calnode can deliver invites itself: a sender address,
// and a mailer that actually sends (the transport choice stays BuildMailer's alone).
// Checked when an event type switches to InviteByCalnode, since with no email there is
// no invite at all.
func (h *Handler) inviteSenderReady(ctx context.Context) bool {
	_, from := h.inviteSender(ctx)
	return from != "" && h.isEmailEnabled()
}

// applyInviteDelivery prepares the booker's email .ics for the booking's invite mode.
// Calnode-sent: always attach, organized by the booking's fixed invite organizer, never
// the host. Calendar-sent: attach only when the primary host's calendar will not invite
// the booker itself (noConnectedDestination), with the host as organizer, exactly as before.
func (h *Handler) applyInviteDelivery(ctx context.Context, d *mailer.BookingData, mode, primaryHostID string) {
	if mode == booking.InviteByCalnode {
		d.AttachICS = true
		d.HideHostInInvite = true
		d.InviteOrganizerName, d.InviteOrganizerEmail = h.bookingInviteOrganizer(ctx, d.BookingID)
		return
	}
	d.AttachICS = h.noConnectedDestination(ctx, primaryHostID)
}

// applyHostInvite turns booking email data into one host's own copy: organized by that
// host, attached only when their calendar does not already hold the event, and - for a
// Calnode-invited booking - without the booker as a guest (see ICSWithoutAttendee).
func (h *Handler) applyHostInvite(ctx context.Context, d *mailer.BookingData, mode, hostID string) {
	d.HideHostInInvite = false
	d.ICSWithoutAttendee = mode == booking.InviteByCalnode
	d.AttachICS = h.noConnectedDestination(ctx, hostID)
}

// bookingInviteOrganizer returns the name and address a Calnode-sent invite is organized
// by. The address is fixed on the booking at its first send (bookings.invite_organizer):
// calendar clients match a reschedule or cancellation by UID and organizer, so editing the
// sender or switching RSVP tracking on later must not change it for invites already out.
// New bookings get a private reply address when RSVP tracking is on, else the sender.
func (h *Handler) bookingInviteOrganizer(ctx context.Context, bookingID string) (name, email string) {
	name, sender := h.inviteSender(ctx)
	var stored string
	if err := h.db.QueryRowContext(ctx,
		`SELECT invite_organizer FROM bookings WHERE id = ?`, bookingID).Scan(&stored); err != nil {
		h.logger.ErrorContext(ctx, "load invite organizer", "error", err, "booking_id", bookingID)
		return name, sender
	}
	if stored != "" {
		return name, stored
	}
	addr := sender
	if base := h.rsvpAddress(ctx); base != "" {
		if reply, err := newReplyAddress(base); err == nil {
			addr = reply
		} else {
			h.logger.ErrorContext(ctx, "mint rsvp reply address", "error", err, "booking_id", bookingID)
		}
	}
	if addr == "" {
		return name, ""
	}
	// Only the first send writes; a concurrent first send reads the winner back.
	if _, err := h.db.ExecContext(ctx,
		`UPDATE bookings SET invite_organizer = ? WHERE id = ? AND invite_organizer = ''`, addr, bookingID); err != nil {
		h.logger.ErrorContext(ctx, "fix invite organizer", "error", err, "booking_id", bookingID)
		return name, addr
	}
	if err := h.db.QueryRowContext(ctx,
		`SELECT invite_organizer FROM bookings WHERE id = ?`, bookingID).Scan(&stored); err != nil || stored == "" {
		return name, addr
	}
	return name, stored
}
