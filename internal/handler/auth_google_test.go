package handler_test

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/db"
	"github.com/calnode/calnode/internal/handler"
)

// authTestSetup opens an in-memory DB, runs migrations, seeds one user, and
// returns the handler, database handle, and the seeded user ID.
func authTestSetup(t *testing.T) (*handler.Handler, *sql.DB, string) {
	t.Helper()
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := db.Migrate(database); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	userID := "user-auth-test"
	database.ExecContext(context.Background(),
		`INSERT INTO users (id, email, name, is_admin) VALUES (?, 'admin@example.com', 'Admin', 1)`,
		userID)

	return handler.New(database, slog.Default()), database, userID
}

// seedSession inserts a session row and returns the session id (cookie value).
func seedSession(t *testing.T, db *sql.DB, userID string, ttl time.Duration) string {
	t.Helper()
	sessID := "test-session-" + userID
	expiresAt := time.Now().UTC().Add(ttl).Format(time.RFC3339)
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO sessions (id, user_id, expires_at) VALUES (?, ?, ?)`,
		sessID, userID, expiresAt); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return sessID
}

// ---------------------------------------------------------------------------
// LoginGoogle
// ---------------------------------------------------------------------------

func TestLoginGoogle_returns503WhenNotConfigured(t *testing.T) {
	h, _, _ := authTestSetup(t)
	// googleAuth is nil by default — SetGoogleAuth not called.
	req := httptest.NewRequest(http.MethodGet, "/v1/auth/login", nil)
	rec := httptest.NewRecorder()
	h.LoginGoogle(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d; want 503", rec.Code)
	}
}

func TestLoginGoogle_setsStateCookieAndRedirects(t *testing.T) {
	h, _, _ := authTestSetup(t)
	h.SetGoogleAuth("client-id", "client-secret", "http://localhost/v1/auth/callback", false)

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/login", nil)
	rec := httptest.NewRecorder()
	h.LoginGoogle(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d; want 302", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc == "" {
		t.Fatal("Location header missing")
	}
	if len(loc) < 33 || loc[:33] != "https://accounts.google.com/o/oau" {
		t.Errorf("Location = %q; want Google auth URL", loc)
	}

	var stateCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "calnode_oauth_state" {
			stateCookie = c
		}
	}
	if stateCookie == nil {
		t.Fatal("calnode_oauth_state cookie not set")
	}
	if stateCookie.Value == "" {
		t.Error("state cookie value is empty")
	}
	if !stateCookie.HttpOnly {
		t.Error("state cookie must be HttpOnly")
	}
}

// ---------------------------------------------------------------------------
// CallbackGoogle — state validation (no real Google token exchange)
// ---------------------------------------------------------------------------

func TestCallbackGoogle_rejectsMissingStateCookie(t *testing.T) {
	h, _, _ := authTestSetup(t)
	h.SetGoogleAuth("client-id", "client-secret", "http://localhost/v1/auth/callback", false)

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/callback?state=abc&code=xyz", nil)
	rec := httptest.NewRecorder()
	h.CallbackGoogle(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d; want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/admin/login?error=state" {
		t.Errorf("Location = %q; want /admin/login?error=state", loc)
	}
}

func TestCallbackGoogle_rejectsStateMismatch(t *testing.T) {
	h, _, _ := authTestSetup(t)
	h.SetGoogleAuth("client-id", "client-secret", "http://localhost/v1/auth/callback", false)

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/callback?state=wrong&code=xyz", nil)
	req.AddCookie(&http.Cookie{Name: "calnode_oauth_state", Value: "correct"})
	rec := httptest.NewRecorder()
	h.CallbackGoogle(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d; want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/admin/login?error=state" {
		t.Errorf("Location = %q; want /admin/login?error=state", loc)
	}
}

func TestCallbackGoogle_redirectsToDeniedOnGoogleError(t *testing.T) {
	h, _, _ := authTestSetup(t)
	h.SetGoogleAuth("client-id", "client-secret", "http://localhost/v1/auth/callback", false)

	req := httptest.NewRequest(http.MethodGet,
		"/v1/auth/callback?state=abc&error=access_denied", nil)
	req.AddCookie(&http.Cookie{Name: "calnode_oauth_state", Value: "abc"})
	rec := httptest.NewRecorder()
	h.CallbackGoogle(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d; want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/admin/login?error=denied" {
		t.Errorf("Location = %q; want /admin/login?error=denied", loc)
	}
}

// ---------------------------------------------------------------------------
// Logout
// ---------------------------------------------------------------------------

func TestLogout_clearsCookieAndRedirects(t *testing.T) {
	h, database, userID := authTestSetup(t)
	sessID := seedSession(t, database, userID, time.Hour)

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: "calnode_session", Value: sessID})
	rec := httptest.NewRecorder()
	h.Logout(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d; want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/admin/login" {
		t.Errorf("Location = %q; want /admin/login", loc)
	}

	// Cookie must be cleared (MaxAge == -1).
	var cleared bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == "calnode_session" && c.MaxAge == -1 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("calnode_session cookie was not cleared")
	}

	// Session row must be deleted from DB.
	var count int
	database.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM sessions WHERE id = ?`, sessID).Scan(&count)
	if count != 0 {
		t.Errorf("session row count = %d; want 0 (should be deleted)", count)
	}
}

func TestLogout_worksWithNoCookie(t *testing.T) {
	h, _, _ := authTestSetup(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/logout", nil)
	rec := httptest.NewRecorder()
	h.Logout(rec, req)
	if rec.Code != http.StatusFound {
		t.Errorf("status = %d; want 302", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// RequireAuth — session cookie path
// ---------------------------------------------------------------------------

func TestRequireAuth_acceptsValidSessionCookie(t *testing.T) {
	h, database, userID := authTestSetup(t)
	sessID := seedSession(t, database, userID, time.Hour)

	called := false
	protected := h.RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/users/me", nil)
	req.AddCookie(&http.Cookie{Name: "calnode_session", Value: sessID})
	rec := httptest.NewRecorder()
	protected(rec, req)

	if !called {
		t.Error("handler was not called with a valid session cookie")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d; want 200", rec.Code)
	}
}

func TestRequireAuth_rejectsExpiredSession(t *testing.T) {
	h, database, userID := authTestSetup(t)
	sessID := seedSession(t, database, userID, -time.Hour) // expired in the past

	called := false
	protected := h.RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/users/me", nil)
	req.AddCookie(&http.Cookie{Name: "calnode_session", Value: sessID})
	rec := httptest.NewRecorder()
	protected(rec, req)

	if called {
		t.Error("handler must not be called with expired session")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401", rec.Code)
	}
}

func TestRequireAuth_rejectsNoCookieNoKey(t *testing.T) {
	h, _, _ := authTestSetup(t)
	protected := h.RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/users/me", nil)
	rec := httptest.NewRecorder()
	protected(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401", rec.Code)
	}
}

func TestRequireAuth_invalidAPIKeyDoesNotFallThroughToSession(t *testing.T) {
	h, database, userID := authTestSetup(t)
	sessID := seedSession(t, database, userID, time.Hour)

	called := false
	protected := h.RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	// Both a bad API key AND a valid session cookie — the bad key must win (reject).
	req := httptest.NewRequest(http.MethodGet, "/v1/users/me", nil)
	req.Header.Set("X-Api-Key", "bad-key")
	req.AddCookie(&http.Cookie{Name: "calnode_session", Value: sessID})
	rec := httptest.NewRecorder()
	protected(rec, req)

	if called {
		t.Error("handler must not be called: invalid API key should not fall through to session")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401", rec.Code)
	}
}
