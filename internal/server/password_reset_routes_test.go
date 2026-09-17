package server

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calnode/calnode/internal/config"
	"github.com/calnode/calnode/internal/db"
)

// TestPasswordResetRoutes_publicAndRateLimitedPerIP drives the real mux from New, so it
// proves the routes are registered, reachable without a session, and behind a per-IP
// limit whose bucket the three reset routes share and the login route does not.
func TestPasswordResetRoutes_publicAndRateLimitedPerIP(t *testing.T) {
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	srv, drain := New(ctx, &config.Config{BaseURL: "http://app.example.com", DataDir: t.TempDir()},
		database, slog.New(slog.DiscardHandler))
	t.Cleanup(func() { cancel(); drain() })

	post := func(path, ip, body string) int {
		req := httptest.NewRequest(http.MethodPost, "http://app.example.com"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = ip + ":40000"
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		return rec.Code
	}
	const (
		request = "/v1/auth/password-reset/request"
		check   = "/v1/auth/password-reset/check"
		confirm = "/v1/auth/password-reset/confirm"
		ip      = "192.0.2.50"
	)
	emailBody := `{"email":"nobody@example.com"}`

	if got := post(check, ip, `{"token":"nope"}`); got != http.StatusNotFound {
		t.Fatalf("check without a session: %d; want 404 from the handler (public route)", got)
	}
	for i := 2; i <= 10; i++ {
		if got := post(request, ip, emailBody); got != http.StatusOK {
			t.Fatalf("request %d of 10: %d; want 200", i, got)
		}
	}
	for _, path := range []string{request, check, confirm} {
		if got := post(path, ip, emailBody); got != http.StatusTooManyRequests {
			t.Errorf("%s after the shared budget is spent: %d; want 429", path, got)
		}
	}
	if got := post(request, "192.0.2.51", emailBody); got != http.StatusOK {
		t.Errorf("another IP: %d; want 200 (buckets are per IP)", got)
	}
	// A separate bucket from login: exhausting reset must not lock anyone out of signing
	// in, and the reverse is the case the separate bucket exists for.
	if got := post("/v1/auth/login/email", ip, `{"email":"nobody@example.com","password":"wrongpassword"}`); got != http.StatusUnauthorized {
		t.Errorf("login from the same IP: %d; want 401 (not rate limited by reset)", got)
	}
}
