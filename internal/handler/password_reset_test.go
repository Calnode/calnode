package handler_test

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func resetReq(token, newPassword string) *http.Request {
	body := `{"token":"` + token + `","new_password":"` + newPassword + `"}`
	return httptest.NewRequest(http.MethodPost, "/v1/auth/password/reset", strings.NewReader(body))
}

func seedResetToken(t *testing.T, db *sql.DB, raw, userID string, expiresAt time.Time) string {
	t.Helper()
	sum := sha256.Sum256([]byte(raw))
	hash := hex.EncodeToString(sum[:])
	if _, err := db.Exec(`INSERT INTO password_reset_tokens (token_hash,user_id,expires_at) VALUES (?,?,?)`,
		hash, userID, expiresAt.UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("seed reset token: %v", err)
	}
	return hash
}

func seedPasswordUser(t *testing.T, db *sql.DB, id, email, password string, emailLogin int) {
	t.Helper()
	hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if _, err := db.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin,email_login,password_hash) VALUES (?,?,?, 'UTC',0,?,?)`,
		id, email, "Test User", emailLogin, string(hash)); err != nil {
		t.Fatalf("seed user: %v", err)
	}
}

func TestPasswordReset_requestUnknownEmailGeneric(t *testing.T) {
	h, db := newTestHandlerDB(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/password/forgot",
		strings.NewReader(`{"email":"nobody@example.com"}`))
	h.RequestPasswordReset(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 (generic)", rec.Code)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM password_reset_tokens`).Scan(&n) //nolint:errcheck
	if n != 0 {
		t.Errorf("token rows = %d; want 0 for unknown email", n)
	}
}

func TestPasswordReset_requestKnownEmailCreatesToken(t *testing.T) {
	h, db := newTestHandlerDB(t)
	seedPasswordUser(t, db, "u1", "known@example.com", "oldpassword", 1)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/password/forgot",
		strings.NewReader(`{"email":"known@example.com"}`))
	h.RequestPasswordReset(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		db.QueryRow(`SELECT COUNT(*) FROM password_reset_tokens WHERE user_id = 'u1'`).Scan(&n) //nolint:errcheck
		if n == 1 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no token row for u1 after waiting (background send never completed)")
}

func TestPasswordReset_requestSkipsArchivedAndDisabled(t *testing.T) {
	h, db := newTestHandlerDB(t)
	seedPasswordUser(t, db, "u1", "archived@example.com", "oldpassword", 1)
	db.Exec(`UPDATE users SET archived_at = '2026-01-01T00:00:00Z' WHERE id = 'u1'`) //nolint:errcheck
	seedPasswordUser(t, db, "u2", "disabled@example.com", "oldpassword", 0)

	for _, email := range []string{"archived@example.com", "disabled@example.com"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/password/forgot",
			strings.NewReader(`{"email":"`+email+`"}`))
		h.RequestPasswordReset(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d; want generic 200", email, rec.Code)
		}
	}
	time.Sleep(100 * time.Millisecond) // no goroutine should have been spawned; fail loudly if one was
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM password_reset_tokens`).Scan(&n) //nolint:errcheck
	if n != 0 {
		t.Errorf("token rows = %d; want 0 (archived and email-login-disabled get no email)", n)
	}
}

func TestPasswordReset_happyPath(t *testing.T) {
	h, db := newTestHandlerDB(t)
	seedPasswordUser(t, db, "u1", "alice@example.com", "oldpassword", 1)
	seedResetToken(t, db, "rawtoken123", "u1", time.Now().Add(time.Hour))

	rec := httptest.NewRecorder()
	h.ResetPassword(rec, resetReq("rawtoken123", "newpassword1"))
	if rec.Code != http.StatusOK {
		t.Fatalf("reset status = %d; want 200 — %s", rec.Code, rec.Body.String())
	}
	found := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == "calnode_session" && c.Value != "" {
			found = true
		}
	}
	if !found {
		t.Error("reset did not set a session cookie (user should be signed straight in)")
	}

	// New password works, old one doesn't.
	rec2 := httptest.NewRecorder()
	h.LoginEmail(rec2, loginEmailReq("alice@example.com", "newpassword1"))
	if rec2.Code != http.StatusOK {
		t.Errorf("login with new password: got %d; want 200", rec2.Code)
	}
	rec3 := httptest.NewRecorder()
	h.LoginEmail(rec3, loginEmailReq("alice@example.com", "oldpassword"))
	if rec3.Code != http.StatusUnauthorized {
		t.Errorf("login with old password: got %d; want 401", rec3.Code)
	}
}

func TestPasswordReset_tokenSingleUse(t *testing.T) {
	h, db := newTestHandlerDB(t)
	seedPasswordUser(t, db, "u1", "a@example.com", "oldpassword", 1)
	seedResetToken(t, db, "oncetoken", "u1", time.Now().Add(time.Hour))

	rec := httptest.NewRecorder()
	h.ResetPassword(rec, resetReq("oncetoken", "newpassword1"))
	if rec.Code != http.StatusOK {
		t.Fatalf("first reset: got %d; want 200", rec.Code)
	}
	rec2 := httptest.NewRecorder()
	h.ResetPassword(rec2, resetReq("oncetoken", "anotherpw2"))
	if rec2.Code != http.StatusBadRequest {
		t.Errorf("second reset with same token: got %d; want 400", rec2.Code)
	}
}

func TestPasswordReset_expiredToken(t *testing.T) {
	h, db := newTestHandlerDB(t)
	seedPasswordUser(t, db, "u1", "a@example.com", "oldpassword", 1)
	seedResetToken(t, db, "expiredtok", "u1", time.Now().Add(-time.Minute))

	rec := httptest.NewRecorder()
	h.ResetPassword(rec, resetReq("expiredtok", "newpassword1"))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expired reset: got %d; want 400", rec.Code)
	}
}

func TestPasswordReset_weakPasswordDoesNotConsume(t *testing.T) {
	h, db := newTestHandlerDB(t)
	seedPasswordUser(t, db, "u1", "a@example.com", "oldpassword", 1)
	hash := seedResetToken(t, db, "retrytoken", "u1", time.Now().Add(time.Hour))

	rec := httptest.NewRecorder()
	h.ResetPassword(rec, resetReq("retrytoken", "short"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("weak password reset: got %d; want 400", rec.Code)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM password_reset_tokens WHERE token_hash = ? AND used_at IS NULL`, hash).Scan(&n) //nolint:errcheck
	if n != 1 {
		t.Fatalf("weak attempt consumed the token; want it still usable")
	}

	// Same link works with a good password.
	rec2 := httptest.NewRecorder()
	h.ResetPassword(rec2, resetReq("retrytoken", "goodpassword1"))
	if rec2.Code != http.StatusOK {
		t.Errorf("retry with good password: got %d; want 200", rec2.Code)
	}
}

func TestPasswordReset_archivedAndDisabledForbidden(t *testing.T) {
	h, db := newTestHandlerDB(t)
	seedPasswordUser(t, db, "u1", "archived@example.com", "oldpassword", 1)
	db.Exec(`UPDATE users SET archived_at = '2026-01-01T00:00:00Z' WHERE id = 'u1'`) //nolint:errcheck
	seedResetToken(t, db, "archtok", "u1", time.Now().Add(time.Hour))
	seedPasswordUser(t, db, "u2", "disabled@example.com", "oldpassword", 0)
	seedResetToken(t, db, "distok", "u2", time.Now().Add(time.Hour))

	for _, tc := range []struct{ raw string }{{"archtok"}, {"distok"}} {
		rec := httptest.NewRecorder()
		h.ResetPassword(rec, resetReq(tc.raw, "newpassword1"))
		if rec.Code != http.StatusForbidden {
			t.Errorf("token %s: got %d; want 403", tc.raw, rec.Code)
		}
		for _, c := range rec.Result().Cookies() {
			if c.Name == "calnode_session" {
				t.Errorf("token %s: must not create a session", tc.raw)
			}
		}
	}
}

func TestPasswordReset_revokesOldSessions(t *testing.T) {
	h, db := newTestHandlerDB(t)
	seedPasswordUser(t, db, "u1", "a@example.com", "oldpassword", 1)
	future := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	db.Exec(`INSERT INTO sessions (id,user_id,expires_at) VALUES ('old1','u1',?),('old2','u1',?)`, future, future) //nolint:errcheck
	seedResetToken(t, db, "sesstoken", "u1", time.Now().Add(time.Hour))

	rec := httptest.NewRecorder()
	h.ResetPassword(rec, resetReq("sesstoken", "newpassword1"))
	if rec.Code != http.StatusOK {
		t.Fatalf("reset: got %d; want 200", rec.Code)
	}
	var ids []string
	rows, err := db.Query(`SELECT id FROM sessions WHERE user_id = 'u1'`) //nolint:errcheck
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		rows.Scan(&id) //nolint:errcheck
		ids = append(ids, id)
	}
	if len(ids) != 1 {
		t.Fatalf("sessions after reset = %v; want exactly the fresh one", ids)
	}
	if ids[0] == "old1" || ids[0] == "old2" {
		t.Errorf("pre-reset session %q survived the reset", ids[0])
	}
}
