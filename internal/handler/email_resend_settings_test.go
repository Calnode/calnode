package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/calnode/calnode/internal/handler"
	"github.com/calnode/calnode/internal/mailer"
)

// setupEmailHandler wires a Live mailer onto the test handler, which the hot-swap path and
// the "enabled" flag both depend on. Without it h.live is nil and enabled reports on the
// injected stub instead of on the saved settings.
func setupEmailHandler(t *testing.T) (*handler.Handler, string) {
	t.Helper()
	h, _, key, _ := setupWorkspaceWithDB(t)
	h.SetMailer(mailer.NewLive(&mailer.Noop{}), "http://localhost")
	return h, key
}

// patchEmail PATCHes /v1/settings/email and returns the decoded response.
func patchEmail(t *testing.T, h *handler.Handler, key, body string) map[string]any {
	t.Helper()
	req := authReq(http.MethodPatch, "/v1/settings/email", body, key)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.PatchEmailSettings)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch email settings: %d - %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

// TestPatchEmailSettings_resendAPIKeySelectsHTTPSTransport covers the whole point of the
// feature: on a host that blocks SMTP, pasting an API key is the entire configuration.
func TestPatchEmailSettings_resendAPIKeySelectsHTTPSTransport(t *testing.T) {
	h, key := setupEmailHandler(t)

	resp := patchEmail(t, h, key, `{
		"resend_api_key": "re_live_testkey",
		"email_from": "bookings@example.com",
		"email_from_name": "Calnode"
	}`)

	if resp["transport"] != "resend_api" {
		t.Errorf("transport = %v, want resend_api", resp["transport"])
	}
	if set, _ := resp["resend_api_key_set"].(bool); !set {
		t.Error("resend_api_key_set should be true after saving a key")
	}
	if enabled, _ := resp["enabled"].(bool); !enabled {
		t.Error("an API key with no SMTP host must count as configured; " +
			"otherwise a blocked-SMTP install is told email is unconfigured while holding valid credentials")
	}
	// The key itself must never come back out.
	for k, v := range resp {
		if s, ok := v.(string); ok && s == "re_live_testkey" {
			t.Errorf("the API key leaked in field %q", k)
		}
	}
}

// TestPatchEmailSettings_resendKeyWinsOverSMTP documents the precedence when both are set.
func TestPatchEmailSettings_resendKeyWinsOverSMTP(t *testing.T) {
	h, key := setupEmailHandler(t)

	resp := patchEmail(t, h, key, `{
		"smtp_host": "smtp.example.com", "smtp_port": "587",
		"smtp_user": "u", "smtp_pass": "p", "smtp_starttls": true,
		"resend_api_key": "re_live_testkey",
		"email_from": "bookings@example.com"
	}`)
	if resp["transport"] != "resend_api" {
		t.Errorf("transport = %v, want resend_api to take precedence", resp["transport"])
	}
	if resp["smtp_host"] != "smtp.example.com" {
		t.Errorf("SMTP settings must still be persisted, not discarded; got %v", resp["smtp_host"])
	}
}

// TestPatchEmailSettings_clearingResendKeyFallsBackToSMTP is the reverse path. The key is
// optional-and-omittable, so "" has to mean "clear it" - otherwise an admin who moves to a
// host that permits SMTP can never switch back.
func TestPatchEmailSettings_clearingResendKeyFallsBackToSMTP(t *testing.T) {
	h, key := setupEmailHandler(t)

	patchEmail(t, h, key, `{
		"smtp_host": "smtp.example.com", "smtp_port": "587",
		"smtp_user": "u", "smtp_pass": "p", "smtp_starttls": true,
		"resend_api_key": "re_live_testkey",
		"email_from": "bookings@example.com"
	}`)

	resp := patchEmail(t, h, key, `{
		"smtp_host": "smtp.example.com", "smtp_port": "587",
		"smtp_user": "u", "smtp_starttls": true,
		"resend_api_key": "",
		"email_from": "bookings@example.com"
	}`)
	if resp["transport"] != "smtp" {
		t.Errorf("transport = %v, want smtp after clearing the key", resp["transport"])
	}
	if set, _ := resp["resend_api_key_set"].(bool); set {
		t.Error("resend_api_key_set should be false after clearing")
	}
	// Omitting smtp_pass must not have wiped the stored password.
	if set, _ := resp["smtp_pass_set"].(bool); !set {
		t.Error("omitting smtp_pass should keep the stored password, not clear it")
	}
}

// TestPatchEmailSettings_omittingResendKeyKeepsIt distinguishes "field absent" (keep) from
// "empty string" (clear). Saving unrelated settings must not silently drop the key.
func TestPatchEmailSettings_omittingResendKeyKeepsIt(t *testing.T) {
	h, key := setupEmailHandler(t)

	patchEmail(t, h, key, `{"resend_api_key": "re_live_testkey", "email_from": "a@example.com"}`)

	resp := patchEmail(t, h, key, `{"email_from": "changed@example.com"}`)
	if set, _ := resp["resend_api_key_set"].(bool); !set {
		t.Error("omitting resend_api_key must keep the stored key, not clear it")
	}
	if resp["transport"] != "resend_api" {
		t.Errorf("transport = %v, want resend_api to survive an unrelated save", resp["transport"])
	}
	if resp["email_from"] != "changed@example.com" {
		t.Errorf("email_from = %v, want the update to have applied", resp["email_from"])
	}
}
