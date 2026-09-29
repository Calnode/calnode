package mailer

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Svix's published verification example (docs.svix.com, "Verifying payloads manually"),
// the scheme Resend signs webhooks with. Matching it proves wire compatibility rather than
// agreement with our own signer.
func svixExample() (string, http.Header, []byte, time.Time) {
	h := http.Header{}
	h.Set("svix-id", "msg_p5jXN8AQM9LWM0D4loKWxJek")
	h.Set("svix-timestamp", "1614265330")
	h.Set("svix-signature", "v1,g0hM9SsE+OTPJTGt/tmIKtSyZlE3uFJELVlNIOLJ1OE=")
	return "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw", h, []byte(`{"test": 2432232314}`), time.Unix(1614265330, 0)
}

func TestVerifyResendWebhook_acceptsSvixExample(t *testing.T) {
	secret, h, body, now := svixExample()
	if err := VerifyResendWebhook(secret, h, body, now); err != nil {
		t.Fatalf("valid Svix signature rejected: %v", err)
	}
	// Secret rotation sends several signatures; any one matching is enough.
	h.Set("svix-signature", "v1,bm90LXRoZS1zaWduYXR1cmU= "+h.Get("svix-signature"))
	if err := VerifyResendWebhook(secret, h, body, now); err != nil {
		t.Errorf("valid signature among several rejected: %v", err)
	}
}

func TestVerifyResendWebhook_rejectsForgeries(t *testing.T) {
	secret, h, body, now := svixExample()
	cases := map[string]func() (string, http.Header, []byte, time.Time){
		"tampered body": func() (string, http.Header, []byte, time.Time) {
			return secret, h, []byte(`{"test": 1}`), now
		},
		"other secret": func() (string, http.Header, []byte, time.Time) {
			return "whsec_" + base64.StdEncoding.EncodeToString([]byte("another-secret")), h, body, now
		},
		"replayed later": func() (string, http.Header, []byte, time.Time) {
			return secret, h, body, now.Add(6 * time.Minute)
		},
		"unsigned": func() (string, http.Header, []byte, time.Time) {
			return secret, http.Header{}, body, now
		},
	}
	for name, c := range cases {
		s, hdr, b, at := c()
		if err := VerifyResendWebhook(s, hdr, b, at); !errors.Is(err, ErrWebhookSignature) {
			t.Errorf("%s: got %v; want ErrWebhookSignature", name, err)
		}
	}
}

// gmailStyleReply mirrors how Gmail answers an invite: multipart/mixed holding a
// multipart/alternative whose text/calendar part is the REPLY (base64), plus the same
// calendar again as an invite.ics attachment.
func gmailStyleReply(uid, attendee, partstat string) []byte {
	ics := "BEGIN:VCALENDAR\r\nMETHOD:REPLY\r\nBEGIN:VEVENT\r\n" +
		"ATTENDEE;PARTSTAT=" + partstat + ";CN=\"Guest; the: one\":mailto:" + attendee + "\r\n" +
		"UID:" + uid + "\r\nSEQUENCE:0\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	b64 := base64.StdEncoding.EncodeToString([]byte(ics))
	var wrapped strings.Builder
	for len(b64) > 76 {
		wrapped.WriteString(b64[:76] + "\r\n")
		b64 = b64[76:]
	}
	wrapped.WriteString(b64 + "\r\n")
	return []byte("From: Guest <" + attendee + ">\r\n" +
		"To: rsvp+tok@reply.example.com\r\n" +
		"Subject: Accepted: Intro\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"outer\"\r\n\r\n" +
		"--outer\r\n" +
		"Content-Type: multipart/alternative; boundary=\"inner\"\r\n\r\n" +
		"--inner\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\nGuest has accepted.\r\n" +
		"--inner\r\nContent-Type: text/calendar; charset=UTF-8; method=REPLY\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" + wrapped.String() +
		"--inner--\r\n" +
		"--outer\r\nContent-Type: application/ics; name=\"invite.ics\"\r\n" +
		"Content-Disposition: attachment; filename=\"invite.ics\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" + wrapped.String() +
		"--outer--\r\n")
}

func TestParseICSReply_gmailStyle(t *testing.T) {
	got, err := ParseICSReply(gmailStyleReply("b-1@calnode", "guest@booker.example", "ACCEPTED"))
	if err != nil {
		t.Fatalf("ParseICSReply: %v", err)
	}
	if got.UID != "b-1@calnode" {
		t.Errorf("UID = %q", got.UID)
	}
	if len(got.Attendees) != 1 || got.Attendees[0].Email != "guest@booker.example" || got.Attendees[0].PartStat != "accepted" {
		t.Errorf("attendees = %+v; want guest@booker.example accepted (a quoted CN with ; and : must not split)", got.Attendees)
	}
}

func TestParseICSReply_foldedQuotedPrintable(t *testing.T) {
	raw := "From: g@x.example\r\nContent-Type: text/calendar; method=REPLY\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n\r\n" +
		"BEGIN:VCALENDAR\r\nMETHOD:REPLY\r\nBEGIN:VEVENT\r\nUID:b-2@calnode\r\n" +
		"ATTENDEE;CN=3DGuest;PARTSTAT=3DDECLINED:MAILTO:guest@booker.exa\r\n mple\r\n" +
		"END:VEVENT\r\nEND:VCALENDAR\r\n"
	got, err := ParseICSReply([]byte(raw))
	if err != nil {
		t.Fatalf("ParseICSReply: %v", err)
	}
	if len(got.Attendees) != 1 || got.Attendees[0].Email != "guest@booker.example" || got.Attendees[0].PartStat != "declined" {
		t.Errorf("attendees = %+v; want the folded MAILTO unfolded and declined", got.Attendees)
	}
}

// Only a REPLY is an answer: an echoed invite or a typed reply must not read as one.
func TestParseICSReply_ignoresNonReplies(t *testing.T) {
	request := "From: g@x.example\r\nContent-Type: text/calendar; method=REQUEST\r\n\r\n" +
		"BEGIN:VCALENDAR\r\nMETHOD:REQUEST\r\nBEGIN:VEVENT\r\nUID:b-3@calnode\r\n" +
		"ATTENDEE;PARTSTAT=ACCEPTED:mailto:g@x.example\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	typed := "From: g@x.example\r\nContent-Type: text/plain\r\n\r\nSee you then!\r\n"
	for name, raw := range map[string]string{"request": request, "typed reply": typed} {
		if _, err := ParseICSReply([]byte(raw)); !errors.Is(err, ErrNoReply) {
			t.Errorf("%s: got %v; want ErrNoReply", name, err)
		}
	}
}
