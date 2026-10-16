package config

import "testing"

func TestLoadSMTPConnectAddress(t *testing.T) {
	t.Setenv("EMAIL_SMTP_HOST", "smtp.example.com")
	t.Setenv("EMAIL_SMTP_CONNECT_HOST", "relay.example.com")
	t.Setenv("EMAIL_SMTP_CONNECT_PORT", "2525")
	cfg := Load()
	if cfg.SMTPHost != "smtp.example.com" || cfg.SMTPConnectHost != "relay.example.com" || cfg.SMTPConnectPort != "2525" {
		t.Fatal("SMTP identity and relay address were not loaded separately")
	}
}
