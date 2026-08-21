package mailer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubResend swaps resendEndpoint for a test server and returns the decoded payload of the
// last request it received.
func stubResend(t *testing.T, status int, respBody string) (*Resend, *resendPayload, *http.Header) {
	t.Helper()
	var got resendPayload
	var hdr http.Header

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr = r.Header.Clone()
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)

	orig := resendEndpoint
	resendEndpoint = srv.URL
	t.Cleanup(func() { resendEndpoint = orig })

	return NewResend("re_test_key", "bookings@example.com", "Calnode"), &got, &hdr
}

func TestResend_sendsExpectedPayload(t *testing.T) {
	m, got, hdr := stubResend(t, 200, `{"id":"abc-123"}`)

	err := m.Send(context.Background(), Message{
		To:      []string{"guest@example.com"},
		Subject: "Booking confirmed",
		Text:    "plain body",
		HTML:    "<p>html body</p>",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if hdr.Get("Authorization") != "Bearer re_test_key" {
		t.Errorf("Authorization = %q, want a bearer token", hdr.Get("Authorization"))
	}
	if got.From != `"Calnode" <bookings@example.com>` {
		t.Errorf("From = %q, want the display name and address", got.From)
	}
	if len(got.To) != 1 || got.To[0] != "guest@example.com" {
		t.Errorf("To = %v", got.To)
	}
	if got.Text != "plain body" || got.HTML != "<p>html body</p>" {
		t.Errorf("both alternatives must be sent; got text=%q html=%q", got.Text, got.HTML)
	}
}

// TestICSAttachmentKeepsItsMethodParameter is the important one. The `method=REQUEST`
// parameter on the calendar attachment is what makes Gmail and Outlook render an
// RSVP-able event rather than a downloadable file, so it must survive the trip through
// the JSON payload intact - parameters and all, not just the bare "text/calendar".
//
// This pins OUR half of the contract. Whether Resend then preserves it end to end is not
// something a unit test can prove; if invites ever start arriving as plain file
// attachments, this is the first place to look and the payload here is what to compare
// against what actually landed.
func TestICSAttachmentKeepsItsMethodParameter(t *testing.T) {
	m, got, _ := stubResend(t, 200, `{"id":"x"}`)

	ics := []byte("BEGIN:VCALENDAR\r\nMETHOD:REQUEST\r\nEND:VCALENDAR\r\n")
	err := m.Send(context.Background(), Message{
		To:      []string{"guest@example.com"},
		Subject: "Invite",
		Text:    "body",
		Attachments: []Attachment{{
			Filename:    "invite.ics",
			ContentType: "text/calendar; charset=utf-8; method=REQUEST",
			Content:     ics,
		}},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(got.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(got.Attachments))
	}
	a := got.Attachments[0]
	if a.Type != "text/calendar; charset=utf-8; method=REQUEST" {
		t.Errorf("content_type = %q; the method parameter must survive verbatim, or the "+
			"invite degrades to a plain file attachment with no RSVP", a.Type)
	}
	if a.Filename != "invite.ics" {
		t.Errorf("filename = %q", a.Filename)
	}
	decoded, err := base64.StdEncoding.DecodeString(a.Content)
	if err != nil {
		t.Fatalf("attachment content is not valid base64: %v", err)
	}
	if string(decoded) != string(ics) {
		t.Errorf("attachment round-trip changed the bytes:\n got %q\nwant %q", decoded, ics)
	}
}

func TestResend_classifiesFailures(t *testing.T) {
	cases := []struct {
		name        string
		status      int
		body        string
		wantReject  bool // a 4xx the provider understood and refused
		wantMessage string
	}{
		{
			name:        "422 unverified domain is a permanent rejection",
			status:      422,
			body:        `{"name":"validation_error","message":"The example.com domain is not verified"}`,
			wantReject:  true,
			wantMessage: "domain is not verified",
		},
		{
			name:       "401 bad key is a permanent rejection",
			status:     401,
			body:       `{"name":"missing_api_key","message":"Invalid API key"}`,
			wantReject: true,
		},
		{
			name:       "429 is retryable, not a permanent rejection",
			status:     429,
			body:       `{"message":"Too many requests"}`,
			wantReject: false,
		},
		{
			name:       "500 is retryable, not a permanent rejection",
			status:     500,
			body:       `{"message":"Internal error"}`,
			wantReject: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, _, _ := stubResend(t, c.status, c.body)
			err := m.Send(context.Background(), Message{To: []string{"a@example.com"}, Subject: "s", Text: "t"})
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := errors.Is(err, ErrEmailRejected); got != c.wantReject {
				t.Errorf("errors.Is(err, ErrEmailRejected) = %v, want %v (err: %v)", got, c.wantReject, err)
			}
			if c.wantMessage != "" && !strings.Contains(err.Error(), c.wantMessage) {
				t.Errorf("error %q should surface the provider's explanation %q", err, c.wantMessage)
			}
		})
	}
}

// A dead endpoint must report as unreachable, so the admin gets the same "your platform
// may be blocking this" style advice the SMTP path gives.
func TestResend_transportFailureIsUnreachable(t *testing.T) {
	orig := resendEndpoint
	resendEndpoint = "http://127.0.0.1:1" // nothing listens here
	t.Cleanup(func() { resendEndpoint = orig })

	m := NewResend("re_key", "a@example.com", "Calnode")
	err := m.Send(context.Background(), Message{To: []string{"b@example.com"}, Subject: "s", Text: "t"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, ErrUnreachable) {
		t.Errorf("a transport failure should map to ErrUnreachable, got %v", err)
	}
}

func TestResend_requiresAPIKey(t *testing.T) {
	m := NewResend("", "a@example.com", "Calnode")
	if err := m.Send(context.Background(), Message{To: []string{"b@example.com"}}); err == nil {
		t.Error("expected an error when no API key is configured")
	}
}
