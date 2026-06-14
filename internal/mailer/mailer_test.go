package mailer

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Capture mailer — records every message sent, safe for concurrent use.
// ---------------------------------------------------------------------------

type captureMailer struct {
	mu   sync.Mutex
	msgs []Message
}

func (c *captureMailer) Send(_ context.Context, msg Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.msgs = append(c.msgs, msg)
	return nil
}

func (c *captureMailer) all() []Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Message, len(c.msgs))
	copy(out, c.msgs)
	return out
}

// ---------------------------------------------------------------------------
// Shared test fixture.
// ---------------------------------------------------------------------------

func testBookingData() BookingData {
	return BookingData{
		BookingID:         "01J4TEST",
		EventTypeName:     "30-Minute Call",
		EventTypeSlug:     "30-min-call",
		HostName:          "Alice Host",
		HostEmail:         "host@example.com",
		OrganizerName:     "Bob Booker",
		OrganizerEmail:    "bob@example.com",
		OrganizerTimezone: "UTC",
		StartAt:           time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC),
		EndAt:             time.Date(2026, 6, 15, 9, 30, 0, 0, time.UTC),
		LocationValue:     "https://meet.example.com/abc",
		BaseURL:           "https://calnode.example.com",
	}
}

// ---------------------------------------------------------------------------
// SendConfirmation
// ---------------------------------------------------------------------------

func TestSendConfirmation_sendsToBothParties(t *testing.T) {
	cap := &captureMailer{}
	if err := SendConfirmation(context.Background(), cap, testBookingData()); err != nil {
		t.Fatalf("SendConfirmation: %v", err)
	}
	msgs := cap.all()
	if len(msgs) != 2 {
		t.Fatalf("got %d messages; want 2 (organizer + host)", len(msgs))
	}
}

func TestSendConfirmation_organizerEmail(t *testing.T) {
	cap := &captureMailer{}
	d := testBookingData()
	SendConfirmation(context.Background(), cap, d) //nolint:errcheck

	org := cap.all()[0]
	if len(org.To) != 1 || org.To[0] != d.OrganizerEmail {
		t.Errorf("organizer To = %v; want [%s]", org.To, d.OrganizerEmail)
	}
	if !strings.Contains(org.Subject, d.EventTypeName) {
		t.Errorf("organizer Subject %q missing event type name", org.Subject)
	}
	if !strings.Contains(org.Text, d.OrganizerName) {
		t.Errorf("organizer body missing organizer name")
	}
	if !strings.Contains(org.Text, d.BookingID) {
		t.Errorf("organizer body missing booking ID")
	}
	if !strings.Contains(org.Text, d.LocationValue) {
		t.Errorf("organizer body missing location")
	}
}

func TestSendConfirmation_hostEmail(t *testing.T) {
	cap := &captureMailer{}
	d := testBookingData()
	SendConfirmation(context.Background(), cap, d) //nolint:errcheck

	host := cap.all()[1]
	if len(host.To) != 1 || host.To[0] != d.HostEmail {
		t.Errorf("host To = %v; want [%s]", host.To, d.HostEmail)
	}
	if !strings.Contains(host.Subject, d.OrganizerName) {
		t.Errorf("host Subject %q missing organizer name", host.Subject)
	}
	if !strings.Contains(host.Text, d.OrganizerEmail) {
		t.Errorf("host body missing organizer email")
	}
}

func TestSendConfirmation_noHostEmail_skipsHostSend(t *testing.T) {
	cap := &captureMailer{}
	d := testBookingData()
	d.HostEmail = ""
	SendConfirmation(context.Background(), cap, d) //nolint:errcheck

	msgs := cap.all()
	if len(msgs) != 1 {
		t.Errorf("got %d messages; want 1 (organizer only, host email empty)", len(msgs))
	}
}

// ---------------------------------------------------------------------------
// SendCancellation
// ---------------------------------------------------------------------------

func TestSendCancellation_sendsToBothParties(t *testing.T) {
	cap := &captureMailer{}
	d := testBookingData()
	d.CancellationReason = "can't make it"
	if err := SendCancellation(context.Background(), cap, d); err != nil {
		t.Fatalf("SendCancellation: %v", err)
	}
	msgs := cap.all()
	if len(msgs) != 2 {
		t.Fatalf("got %d messages; want 2", len(msgs))
	}
}

func TestSendCancellation_organizerBodyContainsReason(t *testing.T) {
	cap := &captureMailer{}
	d := testBookingData()
	d.CancellationReason = "unexpected conflict"
	SendCancellation(context.Background(), cap, d) //nolint:errcheck

	org := cap.all()[0]
	if !strings.Contains(org.Text, d.CancellationReason) {
		t.Errorf("organizer cancellation body missing reason %q", d.CancellationReason)
	}
	if !strings.Contains(org.Text, d.EventTypeSlug) {
		t.Errorf("organizer cancellation body missing rebook link (slug %q)", d.EventTypeSlug)
	}
}

func TestSendCancellation_noReason_omitsReasonLine(t *testing.T) {
	cap := &captureMailer{}
	d := testBookingData()
	d.CancellationReason = ""
	SendCancellation(context.Background(), cap, d) //nolint:errcheck

	org := cap.all()[0]
	if strings.Contains(org.Text, "Reason:") {
		t.Errorf("organizer cancellation body should not contain Reason: when reason is empty")
	}
}

// ---------------------------------------------------------------------------
// buildRaw — security: header injection prevention
// ---------------------------------------------------------------------------

func smtpForTest() *SMTP {
	return &SMTP{from: "noreply@example.com", fromName: "Calnode"}
}

func TestBuildRaw_subjectInjectionPrevented(t *testing.T) {
	s := smtpForTest()
	msg := Message{
		To:      []string{"user@example.com"},
		Subject: "Evil\r\nBcc: attacker@evil.com",
		Text:    "body",
	}
	raw := string(s.buildRaw(msg))

	if strings.Contains(raw, "Bcc: attacker@evil.com") {
		t.Error("header injection: injected Bcc header found in raw message")
	}
	// The CRLF sequence that would end the Subject line and start a new
	// header should not appear literally in the encoded subject value.
	lines := strings.Split(raw, "\r\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "Bcc:") {
			t.Errorf("injected Bcc header found as a separate line: %q", line)
		}
	}
}

func TestBuildRaw_nonASCIISubjectEncoded(t *testing.T) {
	s := smtpForTest()
	msg := Message{
		To:      []string{"user@example.com"},
		Subject: "Réunion d'équipe",
		Text:    "body",
	}
	raw := string(s.buildRaw(msg))

	// The raw Subject: line must not contain bare UTF-8 bytes (> 0x7E).
	for _, line := range strings.Split(raw, "\r\n") {
		if strings.HasPrefix(line, "Subject:") {
			for _, b := range []byte(line) {
				if b > 0x7E {
					t.Errorf("Subject header contains non-ASCII byte 0x%02x; want RFC 2047 encoding", b)
				}
			}
			// Encoded words must be present.
			if !strings.Contains(line, "=?utf-8?") {
				t.Errorf("Subject header %q missing RFC 2047 encoded-word prefix", line)
			}
		}
	}
}

func TestBuildRaw_pureASCIISubjectNotEncoded(t *testing.T) {
	s := smtpForTest()
	msg := Message{
		To:      []string{"user@example.com"},
		Subject: "Booking confirmed: 30-min call",
		Text:    "body",
	}
	raw := string(s.buildRaw(msg))

	for _, line := range strings.Split(raw, "\r\n") {
		if strings.HasPrefix(line, "Subject:") {
			// Pure ASCII subjects should not be wrapped in encoded-word syntax.
			if strings.Contains(line, "=?") {
				t.Errorf("pure ASCII subject was unnecessarily encoded: %q", line)
			}
		}
	}
}

func TestBuildRaw_fromNameFormatted(t *testing.T) {
	s := smtpForTest()
	msg := Message{To: []string{"x@example.com"}, Subject: "Hi", Text: "body"}
	raw := string(s.buildRaw(msg))

	if !strings.Contains(raw, "Calnode") {
		t.Error("From: header missing sender name")
	}
	if !strings.Contains(raw, "noreply@example.com") {
		t.Error("From: header missing sender address")
	}
}
