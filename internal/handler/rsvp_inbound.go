package handler

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/mailer"
	"github.com/calnode/calnode/internal/secret"
	"github.com/calnode/calnode/internal/webhook"
)

// RSVP tracking for Calnode-sent invites (migration 00069).
//
// A booker's Yes/No/Maybe is an iTIP REPLY their mail client emails to the invite's
// ORGANIZER. When RSVP tracking is set up, that organizer is a private per-booking address
// on a domain Resend receives for (rsvp+<token>@reply.example.com). Resend calls
// POST /v1/email/inbound/resend with the message's metadata; Calnode checks the signature,
// fetches the message, reads the REPLY and records the answer on the booking.
//
// Three checks stand between a stranger and a forged RSVP: the Resend signature, the
// unguessable per-booking address, and the REPLY's attendee having to be that booking's
// booker. A forwarded invite answered by someone else is therefore ignored.

// fetchReceivedRaw downloads a received message from Resend; a var so tests can stand in
// for Resend.
var fetchReceivedRaw = mailer.FetchReceivedRaw

// rsvpStatuses are the PARTSTAT answers recorded; anything else in a REPLY is ignored.
var rsvpStatuses = map[string]bool{"accepted": true, "declined": true, "tentative": true}

// rsvpAddress returns the configured RSVP inbox (e.g. rsvp@reply.example.com) when RSVP
// tracking can actually work end to end: an address, the Resend webhook secret to trust
// deliveries, and a Resend API key to fetch them. "" otherwise, and invites fall back to
// the plain sender, which is what they would get anyway.
func (h *Handler) rsvpAddress(ctx context.Context) string {
	var addr, secretEnc, apiKeyEnc string
	if err := h.db.QueryRowContext(ctx,
		`SELECT rsvp_address, resend_webhook_secret_enc, resend_api_key_enc FROM server_settings WHERE id = 1`).
		Scan(&addr, &secretEnc, &apiKeyEnc); err != nil {
		h.logger.ErrorContext(ctx, "load rsvp settings", "error", err)
		return ""
	}
	if addr == "" || secretEnc == "" || apiKeyEnc == "" {
		return ""
	}
	return addr
}

// replyToken is 128 bits of randomness, lower-case base32 so it survives case-folding
// mail systems and stays well inside a 64-octet local part.
var replyToken = base32.StdEncoding.WithPadding(base32.NoPadding)

// newReplyAddress derives a private per-booking address from the RSVP inbox:
// rsvp@reply.example.com → rsvp+<token>@reply.example.com. Resend receives every local
// part at its domain, so no mailbox has to exist for it.
func newReplyAddress(base string) (string, error) {
	local, domain, ok := strings.Cut(base, "@")
	if !ok || local == "" || domain == "" {
		return "", errors.New("rsvp address is not local@domain")
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return local + "+" + strings.ToLower(replyToken.EncodeToString(b)) + "@" + domain, nil
}

// isReplyAddressFor reports whether addr is a per-booking address minted from base. Only
// those can route to a booking: a plain email to the sender or the inbox itself never does.
func isReplyAddressFor(addr, base string) bool {
	local, domain, ok := strings.Cut(strings.ToLower(addr), "@")
	baseLocal, baseDomain, bok := strings.Cut(strings.ToLower(base), "@")
	if !ok || !bok || domain != baseDomain {
		return false
	}
	token, found := strings.CutPrefix(local, baseLocal+"+")
	return found && token != ""
}

// validRSVPAddress accepts a bare local@domain with no plus tag of its own (the per-booking
// tag is appended to it) and no display name.
func validRSVPAddress(v string) bool {
	a, err := mail.ParseAddress(v)
	if err != nil || a.Name != "" || a.Address != v {
		return false
	}
	local, _, _ := strings.Cut(v, "@")
	return !strings.Contains(local, "+")
}

// InboundEmailResend handles POST /v1/email/inbound/resend, Resend's email.received webhook.
// Public, authenticated by the webhook signature. Answers 200 for anything that is not an
// RSVP to act on, so Resend does not retry it, and 5xx only when fetching the message
// failed and a retry could succeed.
func (h *Handler) InboundEmailResend(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil {
		h.writeError(w, http.StatusRequestEntityTooLarge, "payload too large")
		return
	}

	var addr, secretEnc, apiKeyEnc string
	if err := h.db.QueryRowContext(ctx,
		`SELECT rsvp_address, resend_webhook_secret_enc, resend_api_key_enc FROM server_settings WHERE id = 1`).
		Scan(&addr, &secretEnc, &apiKeyEnc); err != nil || secretEnc == "" {
		h.writeError(w, http.StatusNotFound, "inbound email is not configured")
		return
	}
	whSecret, err := secret.Decrypt(h.encKey, secretEnc)
	if err != nil {
		h.logger.ErrorContext(ctx, "inbound email: decrypt webhook secret", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := mailer.VerifyResendWebhook(whSecret, r.Header, body, time.Now()); err != nil {
		h.logger.WarnContext(ctx, "inbound email: rejected webhook", "error", err)
		h.writeError(w, http.StatusUnauthorized, "invalid signature")
		return
	}

	var ev mailer.ResendInboundEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	ignore := func(reason string) {
		h.writeJSON(w, http.StatusOK, map[string]string{"ignored": reason})
	}
	if ev.Type != "email.received" {
		ignore("not an email.received event")
		return
	}
	bookingID, bookerEmail := h.bookingForReplyAddress(ctx, ev.Recipients(), addr)
	if bookingID == "" {
		ignore("not addressed to a booking's reply address")
		return
	}

	apiKey, err := secret.Decrypt(h.encKey, apiKeyEnc)
	if err != nil || apiKey == "" {
		h.logger.ErrorContext(ctx, "inbound email: resend api key unavailable", "error", err)
		h.writeError(w, http.StatusServiceUnavailable, "resend api key not configured")
		return
	}
	raw, err := fetchReceivedRaw(ctx, apiKey, ev.Data.EmailID)
	if err != nil {
		h.logger.ErrorContext(ctx, "inbound email: fetch message", "error", err, "booking_id", bookingID)
		h.writeError(w, http.StatusBadGateway, "could not fetch the received email")
		return
	}
	reply, err := mailer.ParseICSReply(raw)
	if err != nil {
		ignore("no calendar reply in message")
		return
	}
	if !strings.EqualFold(reply.UID, bookingID+"@calnode") {
		ignore("reply is for a different event")
		return
	}
	status := ""
	for _, a := range reply.Attendees {
		if strings.EqualFold(a.Email, bookerEmail) {
			status = a.PartStat
			break
		}
	}
	if !rsvpStatuses[status] {
		ignore("no answer from this booking's booker")
		return
	}

	changed, err := h.recordRSVP(ctx, bookingID, status)
	if err != nil {
		h.logger.ErrorContext(ctx, "inbound email: record rsvp", "error", err, "booking_id", bookingID)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]string{"rsvp_status": status})
	if changed {
		h.enqueueRSVPWebhook(ctx, bookingID, status)
	}
}

// bookingForReplyAddress finds the booking one of the recipients was minted for, and that
// booking's booker address.
func (h *Handler) bookingForReplyAddress(ctx context.Context, recipients []string, base string) (bookingID, bookerEmail string) {
	if base == "" {
		return "", ""
	}
	for _, rcpt := range recipients {
		a, err := mail.ParseAddress(rcpt)
		if err != nil || !isReplyAddressFor(a.Address, base) {
			continue
		}
		err = h.db.QueryRowContext(ctx, `
			SELECT b.id, COALESCE(o.email, '')
			FROM bookings b
			LEFT JOIN booking_attendees o ON o.booking_id = b.id AND o.is_organizer = 1
			WHERE b.invite_organizer = ? COLLATE NOCASE`, a.Address).Scan(&bookingID, &bookerEmail)
		if err == nil {
			return bookingID, bookerEmail
		}
	}
	return "", ""
}

// recordRSVP stores the booker's answer, reporting whether it changed. Resend retries and
// clients re-send the same answer, so an unchanged answer is a no-op, not a new event.
func (h *Handler) recordRSVP(ctx context.Context, bookingID, status string) (bool, error) {
	res, err := h.db.ExecContext(ctx, `
		UPDATE booking_attendees SET rsvp_status = ?, rsvp_at = ?
		WHERE booking_id = ? AND is_organizer = 1 AND rsvp_status != ?`,
		status, time.Now().UTC().Format(time.RFC3339), bookingID, status)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// enqueueRSVPWebhook fires booking.rsvp for the booking's hosts' subscribed webhooks.
func (h *Handler) enqueueRSVPWebhook(ctx context.Context, bookingID, status string) {
	if h.webhookSvc == nil {
		return
	}
	b, err := h.bookingSvc.Get(ctx, bookingID)
	if err != nil {
		h.logger.ErrorContext(ctx, "rsvp webhook: load booking", "error", err, "booking_id", bookingID)
		return
	}
	var slug string
	_ = h.db.QueryRowContext(ctx, `SELECT slug FROM event_types WHERE id = ?`, b.EventTypeID).Scan(&slug)
	if err := h.webhookSvc.Enqueue(ctx, "booking.rsvp", webhook.BookingPayload{
		ID:            b.ID,
		EventTypeSlug: slug,
		HostID:        b.HostID,
		StartAt:       b.StartAt.UTC().Format(time.RFC3339),
		EndAt:         b.EndAt.UTC().Format(time.RFC3339),
		Status:        b.Status,
		LocationValue: b.LocationValue,
		CreatedAt:     b.CreatedAt.UTC().Format(time.RFC3339),
		RSVPStatus:    status,
	}); err != nil {
		h.logger.ErrorContext(ctx, "enqueue booking.rsvp webhook", "error", err, "booking_id", bookingID)
	}
}
