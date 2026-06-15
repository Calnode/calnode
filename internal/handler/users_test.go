package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ---------------------------------------------------------------------------
// GET /v1/users
// ---------------------------------------------------------------------------

func TestListUsers_returnsAllUsers(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)

	// Add a second non-admin user.
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin,email_login)
		VALUES ('u2','member@example.com','Member','UTC',0,1)`)

	req := authReq(http.MethodGet, "/v1/users", "", key)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ListUsers)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d; want 200 — %s", rec.Code, rec.Body.String())
	}
	var users []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &users); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("got %d users; want 2", len(users))
	}
}

func TestListUsers_requiresAdmin(t *testing.T) {
	h, database, _, _ := setupWorkspaceWithDB(t)

	rawKey := "non-admin-list-key"
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u2','other@example.com','Other','UTC',0)`)
	database.Exec(`INSERT INTO api_keys (id,user_id,name,key_hash,created_at) VALUES ('k2','u2','test',?,'2024-01-01')`, sha256HexForTest(rawKey))

	req := authReq(http.MethodGet, "/v1/users", "", rawKey)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ListUsers)(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("got %d; want 403", rec.Code)
	}
}

func TestListUsers_responseShape(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)

	req := authReq(http.MethodGet, "/v1/users", "", key)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ListUsers)(rec, req)

	var users []map[string]any
	json.Unmarshal(rec.Body.Bytes(), &users)

	if len(users) == 0 {
		t.Fatal("expected at least one user")
	}
	u := users[0]
	for _, field := range []string{"id", "email", "name", "timezone", "is_admin", "email_login", "created_at"} {
		if _, ok := u[field]; !ok {
			t.Errorf("response missing field %q", field)
		}
	}
}

// ---------------------------------------------------------------------------
// DELETE /v1/users/{id}
// ---------------------------------------------------------------------------

func TestDeleteUser_removesUser(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)

	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u2','bye@example.com','Bye','UTC',0)`)

	req := authReq(http.MethodDelete, "/v1/users/u2", "", key)
	req.SetPathValue("id", "u2")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.DeleteUser)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d; want 200 — %s", rec.Code, rec.Body.String())
	}

	var count int
	database.QueryRow(`SELECT COUNT(*) FROM users WHERE id = 'u2'`).Scan(&count)
	if count != 0 {
		t.Error("user still exists after delete")
	}
}

func TestDeleteUser_cannotDeleteSelf(t *testing.T) {
	h, _, key, userID := setupWorkspaceWithDB(t)

	req := authReq(http.MethodDelete, "/v1/users/"+userID, "", key)
	req.SetPathValue("id", userID)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.DeleteUser)(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d; want 400 (self-delete blocked)", rec.Code)
	}
}

func TestDeleteUser_notFound(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)

	req := authReq(http.MethodDelete, "/v1/users/nonexistent", "", key)
	req.SetPathValue("id", "nonexistent")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.DeleteUser)(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d; want 404", rec.Code)
	}
}

func TestDeleteUser_requiresAdmin(t *testing.T) {
	h, database, _, _ := setupWorkspaceWithDB(t)

	rawKey := "non-admin-del-key"
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u2','other@example.com','Other','UTC',0)`)
	database.Exec(`INSERT INTO api_keys (id,user_id,name,key_hash,created_at) VALUES ('k2','u2','test',?,'2024-01-01')`, sha256HexForTest(rawKey))

	req := authReq(http.MethodDelete, "/v1/users/u2", "", rawKey)
	req.SetPathValue("id", "u2")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.DeleteUser)(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("got %d; want 403", rec.Code)
	}
}
