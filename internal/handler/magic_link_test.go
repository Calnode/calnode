package handler_test

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMagicLink_requestIsGenericForUnknownEmail(t *testing.T) {
	h, db := newTestHandlerDB(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/magic-link/request",
		strings.NewReader(`{"email":"nobody@example.com"}`))
	h.RequestMagicLink(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 (generic)", rec.Code)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM magic_link_tokens`).Scan(&n) //nolint:errcheck
	if n != 0 {
		t.Errorf("token rows = %d; want 0 for unknown email", n)
	}
}

func TestMagicLink_verifyConsumesTokenOnce(t *testing.T) {
	h, db := newTestHandlerDB(t)
	db.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u1','a@example.com','A','UTC',1)`) //nolint:errcheck

	raw := "rawtoken123"
	sum := sha256.Sum256([]byte(raw))
	hash := hex.EncodeToString(sum[:])
	exp := time.Now().UTC().Add(10 * time.Minute).Format(time.RFC3339)
	if _, err := db.Exec(`INSERT INTO magic_link_tokens (token_hash,user_id,expires_at) VALUES (?,?,?)`, hash, "u1", exp); err != nil {
		t.Fatalf("seed token: %v", err)
	}

	// First use: should set a session cookie + redirect into the app.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/auth/magic-link/verify?token="+raw, nil)
	h.VerifyMagicLink(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("first verify status = %d; want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "/admin/") || strings.Contains(loc, "error") {
		t.Errorf("first verify redirect = %q; want into the app", loc)
	}
	var gotSession bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == "calnode_session" && c.Value != "" {
			gotSession = true
		}
	}
	if !gotSession {
		t.Error("first verify did not set a session cookie")
	}

	// Second use of the same token: must fail (single-use) → redirect to login?error=.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/v1/auth/magic-link/verify?token="+raw, nil)
	h.VerifyMagicLink(rec2, req2)
	if loc := rec2.Header().Get("Location"); !strings.Contains(loc, "error") {
		t.Errorf("second verify redirect = %q; want an error (token already used)", loc)
	}
}

func TestMagicLink_verifyRejectsExpired(t *testing.T) {
	h, db := newTestHandlerDB(t)
	db.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u1','a@example.com','A','UTC',1)`) //nolint:errcheck
	raw := "expiredtok"
	sum := sha256.Sum256([]byte(raw))
	hash := hex.EncodeToString(sum[:])
	past := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	db.Exec(`INSERT INTO magic_link_tokens (token_hash,user_id,expires_at) VALUES (?,?,?)`, hash, "u1", past) //nolint:errcheck

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/auth/magic-link/verify?token="+raw, nil)
	h.VerifyMagicLink(rec, req)
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "error") {
		t.Errorf("expired verify redirect = %q; want an error", loc)
	}
}
