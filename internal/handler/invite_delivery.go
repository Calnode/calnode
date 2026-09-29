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
// The mode is copied onto the booking when it is created and read from there afterwards,
// so a booking's reschedule and cancel follow the channel its invite actually went out on.

var errInviteSenderMissing = errors.New(
	"set up email in Settings → Email before letting Calnode send invites: the invite is delivered by email, from that sender")

func validInviteDelivery(v string) bool {
	return v == booking.InviteByCalendar || v == booking.InviteByCalnode
}

// bookingInviteDelivery returns the invite delivery a booking was created with. A lookup
// failure reads as InviteByCalendar, the behaviour every booking had before the setting
// existed.
func (h *Handler) bookingInviteDelivery(ctx context.Context, bookingID string) string {
	var mode string
	if err := h.db.QueryRowContext(ctx,
		`SELECT invite_delivery FROM bookings WHERE id = ?`, bookingID).Scan(&mode); err != nil {
		h.logger.ErrorContext(ctx, "load booking invite delivery", "error", err, "booking_id", bookingID)
		return booking.InviteByCalendar
	}
	return mode
}

// calendarInvitee is the guest to put on a host's calendar event: the booker when the
// hosts' calendars send the invite, nobody when Calnode does. An empty address is what
// every provider (Google, Microsoft, CalDAV) reads as "no attendee", so the provider has
// no one to email and the event stays private to the host.
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

// inviteSenderReady reports whether Calnode can deliver invites itself: a sender address
// and a transport (SMTP host or Resend API key) are configured. Checked when an event type
// switches to InviteByCalnode, since with no email there is no invite at all.
func (h *Handler) inviteSenderReady(ctx context.Context) bool {
	var from, host, resendKey string
	if err := h.db.QueryRowContext(ctx,
		`SELECT email_from, smtp_host, resend_api_key_enc FROM server_settings WHERE id = 1`).
		Scan(&from, &host, &resendKey); err != nil {
		h.logger.ErrorContext(ctx, "check invite sender", "error", err)
		return false
	}
	return from != "" && (host != "" || resendKey != "")
}

// applyInviteDelivery prepares an attendee-facing email's .ics for the booking's invite
// mode. Calnode-sent: always attach, organized by the instance sender, never the host.
// Calendar-sent: attach only when the primary host's calendar will not invite the booker
// itself (noConnectedDestination), with the host as organizer, exactly as before.
func (h *Handler) applyInviteDelivery(ctx context.Context, d *mailer.BookingData, mode, primaryHostID string) {
	if mode == booking.InviteByCalnode {
		d.AttachICS = true
		d.HideHostInInvite = true
		d.InviteOrganizerName, d.InviteOrganizerEmail = h.inviteSender(ctx)
		return
	}
	d.AttachICS = h.noConnectedDestination(ctx, primaryHostID)
}
