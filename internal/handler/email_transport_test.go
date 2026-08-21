package handler

import (
	"testing"

	"github.com/calnode/calnode/internal/mailer"
)

// TestBuildMailer_picksTransportFromCredentials pins the rule: the transport follows the
// credentials the admin supplied, with no probing and no silent fallback.
func TestBuildMailer_picksTransportFromCredentials(t *testing.T) {
	cases := []struct {
		name          string
		cfg           SMTPConfig
		wantTransport EmailTransport
		wantType      any
	}{
		{
			name:          "api key selects the HTTPS transport",
			cfg:           SMTPConfig{ResendAPIKey: "re_x", From: "a@example.com"},
			wantTransport: TransportResend,
			wantType:      &mailer.Resend{},
		},
		{
			name:          "api key wins over SMTP when both are configured",
			cfg:           SMTPConfig{Host: "smtp.example.com", Port: "587", ResendAPIKey: "re_x"},
			wantTransport: TransportResend,
			wantType:      &mailer.Resend{},
		},
		{
			name:          "smtp host alone selects SMTP",
			cfg:           SMTPConfig{Host: "smtp.example.com", Port: "587"},
			wantTransport: TransportSMTP,
			wantType:      &mailer.SMTP{},
		},
		{
			name: "an api key with no from address still selects Resend rather than " +
				"silently ignoring a key the admin pasted in",
			cfg:           SMTPConfig{ResendAPIKey: "re_x"},
			wantTransport: TransportResend,
			wantType:      &mailer.Resend{},
		},
		{
			name:          "nothing configured is a no-op mailer",
			cfg:           SMTPConfig{},
			wantTransport: TransportNone,
			wantType:      &mailer.Noop{},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, transport := BuildMailer(c.cfg)
			if transport != c.wantTransport {
				t.Errorf("transport = %q, want %q", transport, c.wantTransport)
			}
			if got, want := typeName(m), typeName(c.wantType); got != want {
				t.Errorf("mailer type = %s, want %s", got, want)
			}
		})
	}
}

func typeName(v any) string {
	switch v.(type) {
	case *mailer.Resend:
		return "*mailer.Resend"
	case *mailer.SMTP:
		return "*mailer.SMTP"
	case *mailer.Noop:
		return "*mailer.Noop"
	default:
		return "unknown"
	}
}

// TestBuildMailer_apiKeyOnlyIsAConfiguredInstall guards the specific trap this change had
// to step around: the settings loader used to treat an empty smtp_host as "email is not
// configured". On a platform where SMTP is blocked, an admin configures ONLY an API key -
// and would have been told email was unconfigured while holding valid credentials.
func TestBuildMailer_apiKeyOnlyIsAConfiguredInstall(t *testing.T) {
	m, transport := BuildMailer(SMTPConfig{ResendAPIKey: "re_x", From: "a@example.com"})
	if transport == TransportNone {
		t.Fatal("an API key with no SMTP host must count as configured")
	}
	if _, isNoop := m.(*mailer.Noop); isNoop {
		t.Error("an API-key-only config must not produce a Noop mailer, which discards mail silently")
	}
}
