package handler

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/calnode/calnode/internal/db"
)

func newInternalHandler(t *testing.T) *Handler {
	t.Helper()
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := db.Migrate(database); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return New(database, slog.Default())
}

func TestFinishOAuthLogin_allowedSignupDomainsJITProvisioning(t *testing.T) {
	t.Run("empty allowed domains preserves default redirect", func(t *testing.T) {
		h := newInternalHandler(t)
		req := httptest.NewRequest(http.MethodGet, "/v1/auth/microsoft/callback", nil)
		rec := httptest.NewRecorder()

		h.finishOAuthLogin(rec, req, "newuser@example.com")
		if rec.Code != http.StatusFound {
			t.Fatalf("status = %d; want 302", rec.Code)
		}
		if got := rec.Header().Get("Location"); got != "/admin/login?error=no_account" {
			t.Errorf("Location = %q; want /admin/login?error=no_account", got)
		}
	})

	t.Run("unclaimed instance skips JIT provisioning so claim remains available", func(t *testing.T) {
		h := newInternalHandler(t)
		h.SetAllowedSignupDomains([]string{"example.com"})

		req := httptest.NewRequest(http.MethodGet, "/v1/auth/microsoft/callback", nil)
		rec := httptest.NewRecorder()
		h.finishOAuthLogin(rec, req, "Member@example.com")

		if rec.Code != http.StatusFound {
			t.Fatalf("status = %d; want 302", rec.Code)
		}
		if got := rec.Header().Get("Location"); got != "/admin/login?error=no_account" {
			t.Errorf("Location = %q; want /admin/login?error=no_account", got)
		}
	})

	t.Run("matching domain provisions member and creates session", func(t *testing.T) {
		h := newInternalHandler(t)
		if _, err := h.db.Exec(`INSERT INTO users (id, email, name, iana_timezone, is_admin, is_owner, email_login) VALUES ('owner-1', 'owner@example.com', 'Owner', 'UTC', 1, 1, 1)`); err != nil {
			t.Fatalf("seed owner: %v", err)
		}
		h.SetAllowedSignupDomains([]string{"Example.COM"})

		req := httptest.NewRequest(http.MethodGet, "/v1/auth/microsoft/callback", nil)
		rec := httptest.NewRecorder()
		h.finishOAuthLogin(rec, req, "Member@example.com")

		if rec.Code != http.StatusFound {
			t.Fatalf("status = %d; want 302 — body: %s", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Location"); got != "/admin" {
			t.Errorf("Location = %q; want /admin", got)
		}

		var hasSession bool
		for _, c := range rec.Result().Cookies() {
			if c.Name == sessionCookieName && c.Value != "" {
				hasSession = true
			}
		}
		if !hasSession {
			t.Fatal("expected calnode_session cookie after JIT provisioning")
		}

		var name string
		var isAdmin, isOwner int
		if err := h.db.QueryRow(`SELECT name, is_admin, is_owner FROM users WHERE email = ?`, "member@example.com").
			Scan(&name, &isAdmin, &isOwner); err != nil {
			t.Fatalf("query provisioned user: %v", err)
		}
		if name != "member" {
			t.Errorf("name = %q; want member", name)
		}
		if isAdmin != 0 || isOwner != 0 {
			t.Errorf("role flags = (%d, %d); want (0, 0) for member", isAdmin, isOwner)
		}
	})

	t.Run("non-matching domain returns 403 Forbidden", func(t *testing.T) {
		h := newInternalHandler(t)
		if _, err := h.db.Exec(`INSERT INTO users (id, email, name, iana_timezone, is_admin, is_owner, email_login) VALUES ('owner-1', 'owner@example.com', 'Owner', 'UTC', 1, 1, 1)`); err != nil {
			t.Fatalf("seed owner: %v", err)
		}
		h.SetAllowedSignupDomains([]string{"example.com"})

		req := httptest.NewRequest(http.MethodGet, "/v1/auth/microsoft/callback", nil)
		rec := httptest.NewRecorder()
		h.finishOAuthLogin(rec, req, "intruder@gmail.com")

		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d; want 403", rec.Code)
		}
		var count int
		if err := h.db.QueryRow(`SELECT COUNT(*) FROM users WHERE email = ?`, "intruder@gmail.com").Scan(&count); err != nil {
			t.Fatalf("count users: %v", err)
		}
		if count != 0 {
			t.Errorf("count = %d; want 0 for rejected domain", count)
		}
	})
}
