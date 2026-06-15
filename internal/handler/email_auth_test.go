package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func seedEmailUser(t *testing.T, h interface{ GetDB() interface{ Exec(string, ...any) (interface{ RowsAffected() (int64, error) }, error) } }) {
	t.Helper()
}

// loginEmailReq builds a POST /v1/auth/login/email request.
func loginEmailReq(email, password string) *http.Request {
	body := `{"email":"` + email + `","password":"` + password + `"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login/email", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestLoginEmail_success(t *testing.T) {
	h, database := newTestHandlerDB(t)

	hash, _ := bcrypt.GenerateFromPassword([]byte("mypassword1"), bcrypt.MinCost)
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin,email_login,password_hash) VALUES ('u1','alice@example.com','Alice','UTC',1,1,?)`, string(hash))

	rec := httptest.NewRecorder()
	h.LoginEmail(rec, loginEmailReq("alice@example.com", "mypassword1"))

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d; want 200 — %s", rec.Code, rec.Body.String())
	}
	found := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == "calnode_session" {
			found = true
		}
	}
	if !found {
		t.Error("session cookie not set on successful email login")
	}
}

func TestLoginEmail_wrongPassword(t *testing.T) {
	h, database := newTestHandlerDB(t)

	hash, _ := bcrypt.GenerateFromPassword([]byte("mypassword1"), bcrypt.MinCost)
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin,email_login,password_hash) VALUES ('u1','alice@example.com','Alice','UTC',1,1,?)`, string(hash))

	rec := httptest.NewRecorder()
	h.LoginEmail(rec, loginEmailReq("alice@example.com", "wrongpassword"))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got %d; want 401", rec.Code)
	}
}

func TestLoginEmail_unknownEmail(t *testing.T) {
	h := newEmptyHandler(t)

	rec := httptest.NewRecorder()
	h.LoginEmail(rec, loginEmailReq("nobody@example.com", "anypassword"))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got %d; want 401", rec.Code)
	}
}

func TestLoginEmail_emailLoginDisabled(t *testing.T) {
	h, database := newTestHandlerDB(t)

	hash, _ := bcrypt.GenerateFromPassword([]byte("mypassword1"), bcrypt.MinCost)
	// email_login = 0: user exists but cannot use password login
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin,email_login,password_hash) VALUES ('u1','alice@example.com','Alice','UTC',1,0,?)`, string(hash))

	rec := httptest.NewRecorder()
	h.LoginEmail(rec, loginEmailReq("alice@example.com", "mypassword1"))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got %d; want 401 when email_login=0", rec.Code)
	}
}

func TestLoginEmail_caseInsensitiveEmail(t *testing.T) {
	h, database := newTestHandlerDB(t)

	hash, _ := bcrypt.GenerateFromPassword([]byte("mypassword1"), bcrypt.MinCost)
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin,email_login,password_hash) VALUES ('u1','alice@example.com','Alice','UTC',1,1,?)`, string(hash))

	// Send email in uppercase — should still match.
	rec := httptest.NewRecorder()
	h.LoginEmail(rec, loginEmailReq("ALICE@EXAMPLE.COM", "mypassword1"))

	if rec.Code != http.StatusOK {
		t.Errorf("got %d; want 200 — email login should be case-insensitive", rec.Code)
	}
}

func TestChangePassword_success(t *testing.T) {
	h, database := newTestHandlerDB(t)

	hash, _ := bcrypt.GenerateFromPassword([]byte("oldpassword"), bcrypt.MinCost)
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin,email_login,password_hash) VALUES ('u1','alice@example.com','Alice','UTC',1,1,?)`, string(hash))
	database.Exec(`INSERT INTO api_keys (id,user_id,name,key_hash,created_at) VALUES ('k1','u1','test',?,'2024-01-01')`, sha256HexForTest("testkey"))

	body := `{"current_password":"oldpassword","new_password":"newpassword123"}`
	req := authReq(http.MethodPost, "/v1/users/me/password", body, "testkey")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ChangePassword)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d; want 200 — %s", rec.Code, rec.Body.String())
	}

	// Old password should no longer work.
	rec2 := httptest.NewRecorder()
	h.LoginEmail(rec2, loginEmailReq("alice@example.com", "oldpassword"))
	if rec2.Code != http.StatusUnauthorized {
		t.Error("old password still works after change")
	}
}

func TestChangePassword_wrongCurrent(t *testing.T) {
	h, database := newTestHandlerDB(t)

	hash, _ := bcrypt.GenerateFromPassword([]byte("oldpassword"), bcrypt.MinCost)
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin,email_login,password_hash) VALUES ('u1','alice@example.com','Alice','UTC',1,1,?)`, string(hash))
	database.Exec(`INSERT INTO api_keys (id,user_id,name,key_hash,created_at) VALUES ('k1','u1','test',?,'2024-01-01')`, sha256HexForTest("testkey"))

	body := `{"current_password":"wrongcurrent","new_password":"newpassword123"}`
	req := authReq(http.MethodPost, "/v1/users/me/password", body, "testkey")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ChangePassword)(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got %d; want 401", rec.Code)
	}
}
