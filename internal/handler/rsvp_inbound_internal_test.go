package handler

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/mailer"
	"github.com/calnode/calnode/internal/secret"
)

const (
	rsvpInbox     = "rsvp@reply.team.example"
	rsvpSecretRaw = "calnode-test-webhook-signing-key"
)

var rsvpSecret = "whsec_" + base64.StdEncoding.EncodeToString([]byte(rsvpSecretRaw))

// newRSVPFixture is an invite fixture with RSVP tracking fully configured.
func newRSVPFixture(t *testing.T) *inviteFixture {
	t.Helper()
	f := newInviteFixture(t, true)
	whEnc, err := secret.Encrypt(f.h.encKey, rsvpSecret)
	if err != nil {
		t.Fatal(err)
	}
	keyEnc, err := secret.Encrypt(f.h.encKey, "re_test_key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.db.Exec(`UPDATE server_settings
		SET rsvp_address = ?, resend_webhook_secret_enc = ?, resend_api_key_enc = ? WHERE id = 1`,
		rsvpInbox, whEnc, keyEnc); err != nil {
		t.Fatalf("seed rsvp settings: %v", err)
	}
	return f
}

// stubResend stands in for Resend's Received emails API, serving msg for any email id.
func stubResend(t *testing.T, msg mailer.ReceivedEmail) {
	t.Helper()
	prev := fetchReceived
	fetchReceived = func(_ context.Context, apiKey, _ string) (mailer.ReceivedEmail, error) {
		if apiKey != "re_test_key" {
			return mailer.ReceivedEmail{}, errors.New("wrong api key")
		}
		return msg, nil
	}
	t.Cleanup(func() { fetchReceived = prev })
}

// sentBy is a received message whose From Resend verified (DKIM and DMARC pass).
func sentBy(from string, raw []byte) mailer.ReceivedEmail {
	return mailer.ReceivedEmail{Raw: raw, From: "Someone <" + from + ">", DKIM: "pass", DMARC: "pass"}
}

func replyMessage(uid, attendee, partstat string) []byte {
	return []byte("From: " + attendee + "\r\nSubject: Accepted\r\n" +
		"Content-Type: text/calendar; charset=UTF-8; method=REPLY\r\n\r\n" +
		"BEGIN:VCALENDAR\r\nMETHOD:REPLY\r\nBEGIN:VEVENT\r\nUID:" + uid + "\r\n" +
		"ATTENDEE;PARTSTAT=" + partstat + ":mailto:" + attendee + "\r\n" +
		"END:VEVENT\r\nEND:VCALENDAR\r\n")
}

// postInbound delivers an email.received webhook for a message to `to`, signed with sign.
func (f *inviteFixture) postInbound(t *testing.T, to, sign string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"type": "email.received",
		"data": map[string]any{"email_id": "em_1", "from": inviteBookerEmail, "to": []string{to}},
	})
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	key, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(sign, "whsec_"))
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("msg_1." + ts + "."))
	mac.Write(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/email/inbound/resend", strings.NewReader(string(body)))
	req.Header.Set("svix-id", "msg_1")
	req.Header.Set("svix-timestamp", ts)
	req.Header.Set("svix-signature", "v1,"+base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	rec := httptest.NewRecorder()
	f.h.InboundEmailResend(rec, req)
	return rec
}

func (f *inviteFixture) rsvpStatus(t *testing.T, bookingID string) string {
	t.Helper()
	var s string
	if err := f.h.db.QueryRow(`SELECT rsvp_status FROM booking_attendees WHERE booking_id = ? AND is_organizer = 1`,
		bookingID).Scan(&s); err != nil {
		t.Fatalf("read rsvp: %v", err)
	}
	return s
}

var organizerLine = regexp.MustCompile(`ORGANIZER;CN="[^"]*":mailto:(\S+)`)

// The whole loop: the booker's invite is organized by a private reply address, their
// "Yes" comes back through Resend, and Calnode records it on the booking.
func TestRSVP_bookerAnswerIsRecorded(t *testing.T) {
	f := newRSVPFixture(t)
	if rec := f.createEventType(t, "intro", booking.InviteByCalnode); rec.Code != http.StatusCreated {
		t.Fatalf("create event type: %d — %s", rec.Code, rec.Body.String())
	}
	id, msg := f.book(t, "intro")

	m := organizerLine.FindStringSubmatch(icsOf(msg))
	if m == nil || !isReplyAddressFor(m[1], rsvpInbox) {
		t.Fatalf("invite is not organized by a per-booking reply address:\n%s", icsOf(msg))
	}
	replyAddr := m[1]
	stubResend(t, sentBy(inviteBookerEmail, replyMessage(id+"@calnode", inviteBookerEmail, "ACCEPTED")))

	rec := f.postInbound(t, replyAddr, rsvpSecret)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "accepted") {
		t.Fatalf("inbound RSVP: %d — %s", rec.Code, rec.Body.String())
	}
	if got := f.rsvpStatus(t, id); got != "accepted" {
		t.Errorf("rsvp_status = %q; want accepted", got)
	}
}

// Each check must, on its own, stop an answer from being recorded: the Resend signature,
// a From that Resend verified and that is the booker, and the event UID. The REPLY body is
// written by whoever sends it, so its ATTENDEE line alone is never trusted.
func TestRSVP_rejectsWhatIsNotTheBookersAnswer(t *testing.T) {
	f := newRSVPFixture(t)
	if rec := f.createEventType(t, "intro", booking.InviteByCalnode); rec.Code != http.StatusCreated {
		t.Fatalf("create event type: %d — %s", rec.Code, rec.Body.String())
	}
	id, msg := f.book(t, "intro")
	replyAddr := organizerLine.FindStringSubmatch(icsOf(msg))[1]
	if !isReplyAddressFor(replyAddr, rsvpInbox) {
		t.Fatalf("invite organizer %q is not a reply address; the checks below would pass vacuously", replyAddr)
	}
	bookersDecline := replyMessage(id+"@calnode", inviteBookerEmail, "DECLINED")

	t.Run("forged webhook", func(t *testing.T) {
		stubResend(t, sentBy(inviteBookerEmail, bookersDecline))
		other := "whsec_" + base64.StdEncoding.EncodeToString([]byte("not-the-secret"))
		if rec := f.postInbound(t, replyAddr, other); rec.Code != http.StatusUnauthorized {
			t.Errorf("unsigned-by-Resend webhook: %d; want 401", rec.Code)
		}
	})
	t.Run("forwarded invite answered by someone else", func(t *testing.T) {
		stubResend(t, sentBy("colleague@elsewhere.example",
			replyMessage(id+"@calnode", "colleague@elsewhere.example", "DECLINED")))
		f.postInbound(t, replyAddr, rsvpSecret)
	})
	t.Run("booker's answer forged from another mailbox", func(t *testing.T) {
		stubResend(t, sentBy("attacker@evil.example", bookersDecline))
		f.postInbound(t, replyAddr, rsvpSecret)
	})
	t.Run("booker's address typed into an unverified From", func(t *testing.T) {
		spoofed := sentBy(inviteBookerEmail, bookersDecline)
		spoofed.DKIM, spoofed.DMARC = "fail", "fail"
		stubResend(t, spoofed)
		f.postInbound(t, replyAddr, rsvpSecret)
	})
	t.Run("reply for another event", func(t *testing.T) {
		stubResend(t, sentBy(inviteBookerEmail, replyMessage("someone-else@calnode", inviteBookerEmail, "DECLINED")))
		f.postInbound(t, replyAddr, rsvpSecret)
	})
	if got := f.rsvpStatus(t, id); got != "needs-action" {
		t.Errorf("rsvp_status = %q after only invalid answers; want needs-action", got)
	}
}

// A cancelled booking takes no answers: nothing can act on them, and it ends the reply
// address's usefulness.
func TestRSVP_cancelledBookingTakesNoAnswer(t *testing.T) {
	f := newRSVPFixture(t)
	if rec := f.createEventType(t, "intro", booking.InviteByCalnode); rec.Code != http.StatusCreated {
		t.Fatalf("create event type: %d — %s", rec.Code, rec.Body.String())
	}
	id, msg := f.book(t, "intro")
	replyAddr := organizerLine.FindStringSubmatch(icsOf(msg))[1]
	if err := f.h.bookingSvc.CancelByID(context.Background(), id, ""); err != nil {
		t.Fatal(err)
	}
	stubResend(t, sentBy(inviteBookerEmail, replyMessage(id+"@calnode", inviteBookerEmail, "ACCEPTED")))
	f.postInbound(t, replyAddr, rsvpSecret)
	if got := f.rsvpStatus(t, id); got != "needs-action" {
		t.Errorf("rsvp_status = %q on a cancelled booking; want needs-action", got)
	}
}

// Calendar clients match a cancellation to the invite by UID and organizer. Changing the
// RSVP inbox after a booking was made must not change the organizer its cancellation uses.
func TestRSVP_organizerStaysFixedForTheBookingsLifetime(t *testing.T) {
	f := newRSVPFixture(t)
	if rec := f.createEventType(t, "intro", booking.InviteByCalnode); rec.Code != http.StatusCreated {
		t.Fatalf("create event type: %d — %s", rec.Code, rec.Body.String())
	}
	id, msg := f.book(t, "intro")
	first := organizerLine.FindStringSubmatch(icsOf(msg))[1]
	if !isReplyAddressFor(first, rsvpInbox) {
		t.Fatalf("invite organizer %q is not a reply address", first)
	}

	if _, err := f.h.db.Exec(`UPDATE server_settings SET rsvp_address = 'answers@other.example', email_from = 'new@team.example' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := f.h.bookingSvc.CancelByID(ctx, id, ""); err != nil {
		t.Fatal(err)
	}
	b, err := f.h.bookingSvc.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	f.h.cancelSideEffects(*b)

	cancelICS := icsOf(f.waitFor(t, inviteBookerEmail, 2))
	m := organizerLine.FindStringSubmatch(cancelICS)
	if m == nil || m[1] != first {
		t.Errorf("cancellation organizer = %v; want the invite's original %s:\n%s", m, first, cancelICS)
	}
}

func (f *inviteFixture) setupWebhook(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	return f.do(t, f.h.SetupRSVPWebhook, http.MethodPost, "/v1/settings/email/rsvp-webhook", "")
}

func stubEnsureWebhook(t *testing.T, fn func(ctx context.Context, apiKey, endpoint string) (string, bool, error)) {
	t.Helper()
	prev := ensureInboundWebhook
	ensureInboundWebhook = fn
	t.Cleanup(func() { ensureInboundWebhook = prev })
}

// The one-click path: Calnode points Resend at itself and stores the secret, and RSVP
// tracking is on without the admin ever handling the secret.
func TestRSVPWebhookSetup_storesSecretFromResend(t *testing.T) {
	f := newRSVPFixture(t)
	if _, err := f.h.db.Exec(`UPDATE server_settings SET resend_webhook_secret_enc = '' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	var gotEndpoint string
	stubEnsureWebhook(t, func(_ context.Context, apiKey, endpoint string) (string, bool, error) {
		gotEndpoint = endpoint
		return rsvpSecret, true, nil
	})

	if rec := f.setupWebhook(t); rec.Code != http.StatusOK {
		t.Fatalf("setup: %d — %s", rec.Code, rec.Body.String())
	}
	if gotEndpoint != "https://book.team.example/v1/email/inbound/resend" {
		t.Errorf("webhook endpoint = %q; want this instance's inbound URL", gotEndpoint)
	}
	if f.h.rsvpAddress(context.Background()) != rsvpInbox {
		t.Error("RSVP tracking is still off after the webhook secret was stored")
	}
	// The stored secret is the one Resend signs with: a signed delivery now verifies.
	if rec := f.postInbound(t, rsvpInbox, rsvpSecret); rec.Code != http.StatusOK {
		t.Errorf("delivery signed with the stored secret: %d — %s", rec.Code, rec.Body.String())
	}
}

// When automatic setup cannot work, the admin is told why in words they can act on.
func TestRSVPWebhookSetup_explainsWhyItCannot(t *testing.T) {
	f := newRSVPFixture(t)
	stubEnsureWebhook(t, func(context.Context, string, string) (string, bool, error) {
		return "", false, mailer.ErrResendRestrictedKey
	})
	rec := f.setupWebhook(t)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "Full access") {
		t.Errorf("sending-only key: %d — %s; want 422 asking for a Full access key", rec.Code, rec.Body.String())
	}

	called := false
	stubEnsureWebhook(t, func(context.Context, string, string) (string, bool, error) {
		called = true
		return "", false, nil
	})
	f.h.SetMailer(f.mail, "http://localhost:3000")
	rec = f.setupWebhook(t)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "https") || called {
		t.Errorf("non-https instance: %d — %s (Resend called: %v); want 422 before calling Resend",
			rec.Code, rec.Body.String(), called)
	}
}
