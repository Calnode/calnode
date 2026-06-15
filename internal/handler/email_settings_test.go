package handler_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calnode/calnode/internal/mailer"
)

// sha256HexForTest mirrors the hashing in auth.go so tests can mint valid API keys.
func sha256HexForTest(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// ---------------------------------------------------------------------------
// GET /v1/settings/email
// ---------------------------------------------------------------------------

func TestGetEmailSettings_unconfigured(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)

	req := authReq(http.MethodGet, "/v1/settings/email", "", key)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.GetEmailSettings)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d; want 200 — %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["smtp_host"] != "" {
		t.Errorf("smtp_host = %q; want empty", resp["smtp_host"])
	}
	if enabled, _ := resp["enabled"].(bool); enabled {
		t.Error("enabled = true; want false when no SMTP configured")
	}
	if passSet, _ := resp["smtp_pass_set"].(bool); passSet {
		t.Error("smtp_pass_set = true; want false when no password stored")
	}
}

func TestGetEmailSettings_requiresAuth(t *testing.T) {
	h, _, _, _ := setupWorkspaceWithDB(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/settings/email", nil)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.GetEmailSettings)(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got %d; want 401", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// PATCH /v1/settings/email
// ---------------------------------------------------------------------------

func TestPatchEmailSettings_savesSettings(t *testing.T) {
	h, db, key, _ := setupWorkspaceWithDB(t)

	// Attach a live mailer so the hot-swap path runs.
	live := mailer.NewLive(&mailer.Noop{})
	h.SetMailer(live, "http://localhost")

	body := `{
		"smtp_host": "smtp.example.com",
		"smtp_port": "587",
		"smtp_user": "user@example.com",
		"smtp_pass": "s3cr3t",
		"smtp_starttls": true,
		"email_from": "noreply@example.com",
		"email_from_name": "Acme"
	}`
	req := authReq(http.MethodPatch, "/v1/settings/email", body, key)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.PatchEmailSettings)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d; want 200 — %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["smtp_host"] != "smtp.example.com" {
		t.Errorf("smtp_host = %q; want smtp.example.com", resp["smtp_host"])
	}
	if passSet, _ := resp["smtp_pass_set"].(bool); !passSet {
		t.Error("smtp_pass_set = false after saving password; want true")
	}
	if enabled, _ := resp["enabled"].(bool); !enabled {
		t.Error("enabled = false after configuring SMTP; want true")
	}

	// Verify password is stored encrypted in the DB (not plaintext).
	var enc string
	db.QueryRow(`SELECT smtp_pass_enc FROM server_settings WHERE id = 1`).Scan(&enc)
	if enc == "" {
		t.Error("smtp_pass_enc is empty in DB after save")
	}
	if enc == "s3cr3t" {
		t.Error("password stored in plaintext; expected encrypted ciphertext")
	}
}

func TestPatchEmailSettings_keepExistingPassword(t *testing.T) {
	h, db, key, _ := setupWorkspaceWithDB(t)

	// First save — sets a password.
	body1 := `{"smtp_host":"smtp.example.com","smtp_pass":"original"}`
	req1 := authReq(http.MethodPatch, "/v1/settings/email", body1, key)
	rec1 := httptest.NewRecorder()
	h.RequireAuth(h.PatchEmailSettings)(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first patch: got %d — %s", rec1.Code, rec1.Body.String())
	}

	var firstEnc string
	db.QueryRow(`SELECT smtp_pass_enc FROM server_settings WHERE id = 1`).Scan(&firstEnc)
	if firstEnc == "" {
		t.Fatal("no password enc after first patch")
	}

	// Second save — omits smtp_pass; existing password must be kept.
	body2 := `{"smtp_host":"smtp.example.com","smtp_user":"changed@example.com"}`
	req2 := authReq(http.MethodPatch, "/v1/settings/email", body2, key)
	rec2 := httptest.NewRecorder()
	h.RequireAuth(h.PatchEmailSettings)(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("second patch: got %d — %s", rec2.Code, rec2.Body.String())
	}

	var secondEnc string
	db.QueryRow(`SELECT smtp_pass_enc FROM server_settings WHERE id = 1`).Scan(&secondEnc)
	if secondEnc != firstEnc {
		t.Errorf("password changed when smtp_pass was omitted; old=%q new=%q", firstEnc, secondEnc)
	}
}

func TestPatchEmailSettings_clearHostDisablesEmail(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	live := mailer.NewLive(&mailer.Noop{})
	h.SetMailer(live, "http://localhost")

	// Configure SMTP first.
	req1 := authReq(http.MethodPatch, "/v1/settings/email",
		`{"smtp_host":"smtp.example.com","smtp_pass":"x"}`, key)
	rec1 := httptest.NewRecorder()
	h.RequireAuth(h.PatchEmailSettings)(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("configure: got %d — %s", rec1.Code, rec1.Body.String())
	}

	// Now clear the host.
	req2 := authReq(http.MethodPatch, "/v1/settings/email", `{"smtp_host":""}`, key)
	rec2 := httptest.NewRecorder()
	h.RequireAuth(h.PatchEmailSettings)(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("clear: got %d — %s", rec2.Code, rec2.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(rec2.Body.Bytes(), &resp)
	if enabled, _ := resp["enabled"].(bool); enabled {
		t.Error("enabled = true after clearing smtp_host; want false")
	}
}

func TestPatchEmailSettings_invalidPort(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	for _, bad := range []string{"0", "65536", "not-a-port", "-1"} {
		body := `{"smtp_host":"smtp.example.com","smtp_port":"` + bad + `"}`
		req := authReq(http.MethodPatch, "/v1/settings/email", body, key)
		rec := httptest.NewRecorder()
		h.RequireAuth(h.PatchEmailSettings)(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("port=%q: got %d; want 400", bad, rec.Code)
		}
	}
}

func TestPatchEmailSettings_requiresAuth(t *testing.T) {
	h, _, _, _ := setupWorkspaceWithDB(t)
	req := httptest.NewRequest(http.MethodPatch, "/v1/settings/email",
		strings.NewReader(`{"smtp_host":"x"}`))
	rec := httptest.NewRecorder()
	h.RequireAuth(h.PatchEmailSettings)(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got %d; want 401", rec.Code)
	}
}

func TestPatchEmailSettings_nonAdminForbidden(t *testing.T) {
	h, db, _, _ := setupWorkspaceWithDB(t)

	// Insert a non-admin user + API key (SHA-256 of raw key, matching auth.go).
	rawKey := "non-admin-test-key-xyz"
	hash := sha256HexForTest(rawKey)
	db.Exec(`INSERT INTO users (id, email, name, iana_timezone, is_admin) VALUES ('u2','other@example.com','Other','UTC',0)`)
	db.Exec(`INSERT INTO api_keys (id, user_id, name, key_hash, created_at) VALUES ('k2','u2','test',?,datetime('now'))`, hash)

	req := authReq(http.MethodPatch, "/v1/settings/email", `{"smtp_host":"evil.smtp.example.com"}`, rawKey)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.PatchEmailSettings)(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("non-admin PATCH: got %d; want 403", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// POST /v1/settings/email/test
// ---------------------------------------------------------------------------

func TestTestEmailConnection_sends(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)

	// Wire a live mailer with a stub inner so Send() is captured.
	stub := &stubMailer{}
	live := mailer.NewLive(stub)
	h.SetMailer(live, "http://localhost")

	req := authReq(http.MethodPost, "/v1/settings/email/test", "", key)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.TestEmailConnection)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d; want 200 — %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if sent, _ := resp["sent"].(bool); !sent {
		t.Error("sent = false; want true")
	}
	if len(stub.lastMsg.To) == 0 {
		t.Error("stub.Send never called")
	}
	if !strings.HasPrefix(stub.lastMsg.Subject, "[TEST] ") {
		t.Errorf("subject %q; want [TEST] prefix", stub.lastMsg.Subject)
	}
}

func TestTestEmailConnection_notConfigured(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t) // default Noop mailer → 503

	req := authReq(http.MethodPost, "/v1/settings/email/test", "", key)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.TestEmailConnection)(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("got %d; want 503", rec.Code)
	}
}

func TestTestEmailConnection_requiresAuth(t *testing.T) {
	h, _, _, _ := setupWorkspaceWithDB(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/settings/email/test", nil)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.TestEmailConnection)(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got %d; want 401", rec.Code)
	}
}

func TestTestEmailConnection_nonAdminForbidden(t *testing.T) {
	h, db, _, _ := setupWorkspaceWithDB(t)

	rawKey := "non-admin-conn-test-key"
	hash := sha256HexForTest(rawKey)
	db.Exec(`INSERT INTO users (id, email, name, iana_timezone, is_admin) VALUES ('u3','other2@example.com','Other2','UTC',0)`)
	db.Exec(`INSERT INTO api_keys (id, user_id, name, key_hash, created_at) VALUES ('k3','u3','test',?,datetime('now'))`, hash)

	req := authReq(http.MethodPost, "/v1/settings/email/test", "", rawKey)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.TestEmailConnection)(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("non-admin test-email: got %d; want 403", rec.Code)
	}
}
