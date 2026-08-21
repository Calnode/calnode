package handler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/calnode/calnode/internal/mailer"
)

// TestTestEmailErrorMessage checks that the one failure an admin cannot diagnose on their
// own - the platform silently blocking outbound SMTP - is named explicitly, and that the
// other cases are not swallowed into the same advice.
func TestTestEmailErrorMessage(t *testing.T) {
	cases := []struct {
		name        string
		err         error
		transport   EmailTransport
		wantContain string
		wantAbsent  string
	}{
		{
			name: "unreachable over SMTP names the platform-block possibility",
			// Matches how smtp.go wraps a failed dial.
			err:         fmt.Errorf("mailer: dial smtp.resend.com:587: %w: %w", mailer.ErrUnreachable, context.DeadlineExceeded),
			transport:   TransportSMTP,
			wantContain: "block outbound SMTP",
		},
		{
			name:        "unreachable over SMTP points at the HTTPS alternative",
			err:         fmt.Errorf("mailer: dial x:587: %w: boom", mailer.ErrUnreachable),
			transport:   TransportSMTP,
			wantContain: "API key",
		},
		{
			// The bug this guards: ErrUnreachable is raised by BOTH transports. Telling an
			// admin already sending over the Resend API to "add an API key" is nonsense.
			name:        "unreachable over the API path does NOT tell them to add an API key",
			err:         fmt.Errorf("mailer: resend post: %w: dial tcp: i/o timeout", mailer.ErrUnreachable),
			transport:   TransportResend,
			wantContain: "api.resend.com",
			wantAbsent:  "add an API key",
		},
		{
			name:        "a provider rejection surfaces the provider's own explanation",
			err:         fmt.Errorf("mailer: resend api 422: %w: the example.com domain is not verified", mailer.ErrEmailRejected),
			transport:   TransportResend,
			wantContain: "domain is not verified",
			wantAbsent:  "block outbound SMTP",
		},
		{
			name:        "a timeout after connecting blames the port/TLS mode, not the platform",
			err:         fmt.Errorf("mailer: quit: %w", context.DeadlineExceeded),
			transport:   TransportSMTP,
			wantContain: "implicit TLS",
			wantAbsent:  "block outbound SMTP",
		},
		{
			name:        "an auth rejection is passed through verbatim",
			err:         errors.New("mailer: auth: 535 authentication failed"),
			transport:   TransportSMTP,
			wantContain: "535 authentication failed",
			wantAbsent:  "block outbound SMTP",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := testEmailErrorMessage(c.err, c.transport)
			if !strings.Contains(got, c.wantContain) {
				t.Errorf("message = %q, want it to mention %q", got, c.wantContain)
			}
			if c.wantAbsent != "" && strings.Contains(got, c.wantAbsent) {
				t.Errorf("message = %q, should NOT mention %q", got, c.wantAbsent)
			}
		})
	}
}

// TestSMTPUnreachableIsDetectable pins the contract the handler depends on: a failed dial
// must be matchable with errors.Is. If smtp.go ever stops wrapping ErrUnreachable, the
// admin silently loses the specific advice and gets a generic failure instead, so this
// guards the seam rather than the message.
func TestSMTPUnreachableIsDetectable(t *testing.T) {
	wrapped := fmt.Errorf("mailer: dial host:587: %w: %w", mailer.ErrUnreachable, context.DeadlineExceeded)
	if !errors.Is(wrapped, mailer.ErrUnreachable) {
		t.Error("errors.Is(err, mailer.ErrUnreachable) = false; the handler cannot classify dial failures")
	}
	if !errors.Is(wrapped, context.DeadlineExceeded) {
		t.Error("wrapping ErrUnreachable must not hide the underlying cause")
	}
}
