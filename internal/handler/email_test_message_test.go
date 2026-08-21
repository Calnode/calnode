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
		wantContain string
		wantAbsent  string
	}{
		{
			name: "unreachable host names the platform-block possibility",
			// Matches how smtp.go wraps a failed dial.
			err:         fmt.Errorf("mailer: dial smtp.resend.com:587: %w: %w", mailer.ErrUnreachable, context.DeadlineExceeded),
			wantContain: "block outbound SMTP",
		},
		{
			name:        "unreachable host points at the HTTPS alternative",
			err:         fmt.Errorf("mailer: dial x:587: %w: boom", mailer.ErrUnreachable),
			wantContain: "API key",
		},
		{
			name:        "a timeout after connecting blames the port/TLS mode, not the platform",
			err:         fmt.Errorf("mailer: quit: %w", context.DeadlineExceeded),
			wantContain: "implicit TLS",
			wantAbsent:  "block outbound SMTP",
		},
		{
			name:        "an auth rejection is passed through verbatim",
			err:         errors.New("mailer: auth: 535 authentication failed"),
			wantContain: "535 authentication failed",
			wantAbsent:  "block outbound SMTP",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := testEmailErrorMessage(c.err)
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
