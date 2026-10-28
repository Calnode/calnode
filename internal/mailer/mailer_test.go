package mailer

import (
	"context"
	"errors"
	"net/mail"
	"net/url"
	"regexp"
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
// Add-to-calendar links
// ---------------------------------------------------------------------------

func TestGoogleCalURL_encodesEventTimesAndLocation(t *testing.T) {
	d := testBookingData()
	u := d.GoogleCalURL()
	if !strings.Contains(u, "calendar.google.com/calendar/render") {
		t.Errorf("not a Google render link: %s", u)
	}
	if !strings.Contains(u, "action=TEMPLATE") {
		t.Errorf("missing action=TEMPLATE: %s", u)
	}
	if !strings.Contains(u, "20260615T090000Z") || !strings.Contains(u, "20260615T093000Z") {
		t.Errorf("missing UTC basic start/end: %s", u)
	}
	if !strings.Contains(u, "30-Minute+Call") {
		t.Errorf("event name not URL-encoded: %s", u)
	}
	if !strings.Contains(u, url.QueryEscape(d.LocationValue)) {
		t.Errorf("location not carried/encoded: %s", u)
	}
}

func TestCalendarLinks_inAttendeeBodies(t *testing.T) {
	for _, tc := range []struct {
		name string
		send func(context.Context, Mailer, BookingData) error
	}{
		{"confirmation", SendConfirmationToAttendee},
		{"reschedule", SendRescheduleToAttendee},
		{"reminder", SendReminder},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cap := &captureMailer{}
			if err := tc.send(context.Background(), cap, testBookingData()); err != nil {
				t.Fatalf("send: %v", err)
			}
			body := cap.all()[0].Text
			if !strings.Contains(body, "calendar.google.com/calendar/render") {
				t.Error("attendee body missing Google calendar link")
			}
			if !strings.Contains(body, "outlook.office.com/calendar") {
				t.Error("attendee body missing Outlook calendar link")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Custom subjects
// ---------------------------------------------------------------------------

func TestSubjectOverride_usedWhenSet(t *testing.T) {
	cap := &captureMailer{}
	d := testBookingData()
	d.SubjectOverride = "Your strategy call is locked in"
	SendConfirmationToAttendee(context.Background(), cap, d) //nolint:errcheck
	if got := cap.all()[0].Subject; got != d.SubjectOverride {
		t.Errorf("subject = %q; want override %q", got, d.SubjectOverride)
	}
}

func TestSubjectOverride_defaultWhenEmpty(t *testing.T) {
	cap := &captureMailer{}
	SendConfirmationToAttendee(context.Background(), cap, testBookingData()) //nolint:errcheck
	if got := cap.all()[0].Subject; !strings.Contains(got, "Booking confirmed") {
		t.Errorf("subject = %q; want the default 'Booking confirmed: …'", got)
	}
}

// ---------------------------------------------------------------------------
// buildRaw — security: header injection prevention
// ---------------------------------------------------------------------------

func smtpForTest() *SMTP {
	return &SMTP{from: "noreply@example.com", fromName: "Calnode"}
}

// mustBuildRaw builds msg and fails the test if the recipients are refused — for the
// cases below, which are about header encoding rather than recipient validation.
func mustBuildRaw(t *testing.T, s *SMTP, msg Message) []byte {
	t.Helper()
	raw, err := s.buildRaw(msg)
	if err != nil {
		t.Fatalf("buildRaw: %v", err)
	}
	return raw
}

func TestBuildRaw_subjectInjectionPrevented(t *testing.T) {
	s := smtpForTest()
	msg := Message{
		To:      []string{"user@example.com"},
		Subject: "Evil\r\nBcc: attacker@evil.com",
		Text:    "body",
	}
	raw := string(mustBuildRaw(t, s, msg))

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
	raw := string(mustBuildRaw(t, s, msg))

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
	raw := string(mustBuildRaw(t, s, msg))

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
	raw := string(mustBuildRaw(t, s, msg))

	if !strings.Contains(raw, "Calnode") {
		t.Error("From: header missing sender name")
	}
	if !strings.Contains(raw, "noreply@example.com") {
		t.Error("From: header missing sender address")
	}
}

// TestBuildRaw_recipientAddresses covers the To: header, which is the one header field
// assembled from caller-supplied input without an encoder in front of it. Subject and the
// From display name go through mime.QEncoding and attachment filenames through %q; a
// recipient that mail.ParseAddress refuses used to be appended verbatim, so a CR/LF inside
// it would have ended the To: line and started a header of the sender's choosing.
func TestBuildRaw_recipientAddresses(t *testing.T) {
	for _, tc := range []struct {
		name string
		to   []string
		// want is the expected To: header line. Empty means buildRaw must refuse.
		want string
	}{
		{"plain address", []string{"bob@example.com"}, "To: <bob@example.com>"},
		{"display name is normalised into the header", []string{"Bob <bob@example.com>"}, `To: "Bob" <bob@example.com>`},
		{"two recipients", []string{"bob@example.com", "eve@example.com"}, "To: <bob@example.com>, <eve@example.com>"},
		{"CRLF in the address", []string{"a@b.example\r\nBcc: attacker@example.com"}, ""},
		{"not an address at all", []string{"not-an-address"}, ""},
		{"no recipient", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := smtpForTest().buildRaw(Message{To: tc.to, Subject: "Hi", Text: "body"})

			if tc.want == "" {
				if err == nil {
					t.Fatalf("buildRaw accepted %q; want a refusal", tc.to)
				}
				if !errors.Is(err, ErrInvalidRecipient) {
					t.Errorf("error = %v; want it to wrap ErrInvalidRecipient", err)
				}
				if raw != nil {
					t.Errorf("buildRaw returned %d bytes alongside its error; want none", len(raw))
				}
				// The offending value is attacker-controlled and this error is logged,
				// so it must not carry the raw bytes back out.
				if strings.Contains(err.Error(), "attacker@example.com") {
					t.Errorf("error %q quotes the rejected address back", err)
				}
				return
			}

			if err != nil {
				t.Fatalf("buildRaw(%q): %v", tc.to, err)
			}
			var got string
			for _, line := range strings.Split(string(raw), "\r\n") {
				if strings.HasPrefix(line, "To:") {
					got = line
					break
				}
			}
			if got != tc.want {
				t.Errorf("To: header = %q; want %q", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// buildRaw — RFC 5322 Date and Message-ID
// ---------------------------------------------------------------------------

// headerLine returns the value of the first occurrence of the named header in a raw
// message, or "" when it is absent.
func headerLine(raw, name string) string {
	prefix := name + ": "
	for _, line := range strings.Split(raw, "\r\n") {
		if line == "" {
			break // end of the header block
		}
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, prefix)
		}
	}
	return ""
}

// TestBuildRaw_dateHeader checks the header against the parser receiving servers use
// rather than against the layout string that produced it: RFC 5322 requires a date-time
// a reader can actually parse, and a format change that stops satisfying mail.ParseDate
// would otherwise only show up as mail scoring badly at the far end.
func TestBuildRaw_dateHeader(t *testing.T) {
	before := time.Now().Truncate(time.Second)
	raw := string(mustBuildRaw(t, smtpForTest(), Message{
		To: []string{"user@example.com"}, Subject: "Hi", Text: "body",
	}))

	got := headerLine(raw, "Date")
	if got == "" {
		t.Fatal("Date: header missing")
	}
	parsed, err := mail.ParseDate(got)
	if err != nil {
		t.Fatalf("mail.ParseDate(%q): %v", got, err)
	}
	if parsed.Before(before) || parsed.After(time.Now().Add(time.Minute)) {
		t.Errorf("Date: header = %q; want the time of sending", got)
	}
	// A numeric zone ("+0000"), not "UTC" or another alphabetic abbreviation: RFC 5322
	// moved those to the obsolete syntax, and time.RFC1123 - one character away from the
	// layout used here - produces exactly that.
	if !regexp.MustCompile(`[+-]\d{4}$`).MatchString(got) {
		t.Errorf("Date: header = %q; want it to end in a numeric zone offset", got)
	}
}

func TestBuildRaw_messageIDHeader(t *testing.T) {
	raw := string(mustBuildRaw(t, smtpForTest(), Message{
		To: []string{"user@example.com"}, Subject: "Hi", Text: "body",
	}))

	got := headerLine(raw, "Message-ID")
	if !strings.HasPrefix(got, "<") || !strings.HasSuffix(got, ">") {
		t.Fatalf("Message-ID: header = %q; want it wrapped in angle brackets", got)
	}
	id := strings.Trim(got, "<>")
	local, domain, ok := strings.Cut(id, "@")
	if !ok {
		t.Fatalf("Message-ID %q has no @; want id@domain", id)
	}
	if local == "" {
		t.Errorf("Message-ID %q has an empty local part", id)
	}
	if domain != "example.com" {
		t.Errorf("Message-ID domain = %q; want the From domain %q", domain, "example.com")
	}

	// Two messages must not share an identifier — threading clients collapse messages
	// that do.
	other := string(mustBuildRaw(t, smtpForTest(), Message{
		To: []string{"user@example.com"}, Subject: "Hi", Text: "body",
	}))
	if headerLine(other, "Message-ID") == got {
		t.Errorf("two messages share the Message-ID %q", got)
	}
}

// TestBuildRaw_messageIDDomain covers the From values that reach buildRaw unvalidated -
// the settings endpoint does not require email_from, let alone check it. Each must still
// produce a well-formed right-hand side, since a malformed Message-ID works against the
// deliverability the header exists for.
func TestBuildRaw_messageIDDomain(t *testing.T) {
	for _, tc := range []struct {
		name string
		from string
		host string
		want string
	}{
		{"plain address", "noreply@example.com", "smtp.example.net", "example.com"},
		{"address with a display name", "Calnode <noreply@example.com>", "smtp.example.net", "example.com"},
		{"uppercase domain is normalised", "noreply@EXAMPLE.COM", "", "example.com"},
		{"subdomain is kept", "noreply@mail.team.example.com", "", "mail.team.example.com"},
		{"empty From falls back to the host", "", "smtp.example.net", "smtp.example.net"},
		{"unparseable From falls back to the host", "not-an-address", "smtp.example.net", "smtp.example.net"},
		{"no From and no host", "", "", "localhost"},
		{"unusable host is not written through", "", "smtp example net", "localhost"},
		{"CRLF in the host cannot start a header", "", "smtp.example.net\r\nBcc: attacker@evil.example", "localhost"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &SMTP{from: tc.from, fromName: "Calnode", host: tc.host}
			if got := s.messageIDDomain(); got != tc.want {
				t.Fatalf("messageIDDomain() = %q; want %q", got, tc.want)
			}

			raw := string(mustBuildRaw(t, s, Message{
				To: []string{"user@example.com"}, Subject: "Hi", Text: "body",
			}))
			if got := headerLine(raw, "Message-ID"); !strings.HasSuffix(got, "@"+tc.want+">") {
				t.Errorf("Message-ID: header = %q; want it to end in @%s>", got, tc.want)
			}
			for _, line := range strings.Split(raw, "\r\n") {
				if strings.HasPrefix(line, "Bcc:") {
					t.Errorf("header injected through the Message-ID domain: %q", line)
				}
			}
		})
	}
}
