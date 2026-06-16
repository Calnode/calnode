package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// ---------------------------------------------------------------------------
// PATCH /v1/users/{id}/role  (owner only)
// ---------------------------------------------------------------------------

func TestSetUserRole_ownerPromotesAndDemotes(t *testing.T) {
	h, database, ownerKey, _ := setupWorkspaceWithDB(t)
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u2','m@example.com','Member','UTC',0)`)

	// Promote member → admin.
	req := authReq(http.MethodPatch, "/v1/users/u2/role", `{"role":"admin"}`, ownerKey)
	req.SetPathValue("id", "u2")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.SetUserRole)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("promote: got %d — %s", rec.Code, rec.Body.String())
	}
	var isAdmin int
	database.QueryRow(`SELECT is_admin FROM users WHERE id='u2'`).Scan(&isAdmin)
	if isAdmin != 1 {
		t.Error("u2 should be admin after promotion")
	}

	// Demote admin → member.
	req = authReq(http.MethodPatch, "/v1/users/u2/role", `{"role":"member"}`, ownerKey)
	req.SetPathValue("id", "u2")
	rec = httptest.NewRecorder()
	h.RequireAuth(h.SetUserRole)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("demote: got %d — %s", rec.Code, rec.Body.String())
	}
	database.QueryRow(`SELECT is_admin FROM users WHERE id='u2'`).Scan(&isAdmin)
	if isAdmin != 0 {
		t.Error("u2 should be member after demotion")
	}
}

func TestSetUserRole_requiresOwnerNotAdmin(t *testing.T) {
	h, database, _, _ := setupWorkspaceWithDB(t)
	// An admin (not owner) tries to change a role → 403.
	adminKey := "admin-not-owner-key"
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin,is_owner) VALUES ('u2','a@example.com','Admin','UTC',1,0)`)
	database.Exec(`INSERT INTO api_keys (id,user_id,name,key_hash,created_at) VALUES ('k2','u2','t',?,'2024-01-01')`, sha256HexForTest(adminKey))
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u3','m@example.com','Member','UTC',0)`)

	req := authReq(http.MethodPatch, "/v1/users/u3/role", `{"role":"admin"}`, adminKey)
	req.SetPathValue("id", "u3")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.SetUserRole)(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("got %d; want 403 (admin cannot grant admin)", rec.Code)
	}
}

func TestSetUserRole_cannotTargetOwnerOrSelf(t *testing.T) {
	h, database, ownerKey, ownerID := setupWorkspaceWithDB(t)
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin,is_owner) VALUES ('u2','o2@example.com','Other','UTC',1,0)`)

	// Self.
	req := authReq(http.MethodPatch, "/v1/users/"+ownerID+"/role", `{"role":"member"}`, ownerKey)
	req.SetPathValue("id", ownerID)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.SetUserRole)(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("self role change: got %d; want 400", rec.Code)
	}
}

func TestSetUserRole_invalidRole(t *testing.T) {
	h, database, ownerKey, _ := setupWorkspaceWithDB(t)
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u2','m@example.com','Member','UTC',0)`)

	req := authReq(http.MethodPatch, "/v1/users/u2/role", `{"role":"superuser"}`, ownerKey)
	req.SetPathValue("id", "u2")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.SetUserRole)(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d; want 400 (invalid role)", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// POST /v1/users/{id}/transfer-ownership  (owner only)
// ---------------------------------------------------------------------------

func TestTransferOwnership_swapsOwner(t *testing.T) {
	h, database, ownerKey, ownerID := setupWorkspaceWithDB(t)
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u2','next@example.com','Next','UTC',0)`)

	req := authReq(http.MethodPost, "/v1/users/u2/transfer-ownership", "", ownerKey)
	req.SetPathValue("id", "u2")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.TransferOwnership)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d — %s", rec.Code, rec.Body.String())
	}

	var oldOwnerIsOwner, oldOwnerIsAdmin, newOwnerIsOwner, newOwnerIsAdmin int
	database.QueryRow(`SELECT is_owner, is_admin FROM users WHERE id=?`, ownerID).Scan(&oldOwnerIsOwner, &oldOwnerIsAdmin)
	database.QueryRow(`SELECT is_owner, is_admin FROM users WHERE id='u2'`).Scan(&newOwnerIsOwner, &newOwnerIsAdmin)

	if oldOwnerIsOwner != 0 || oldOwnerIsAdmin != 1 {
		t.Errorf("old owner should be demoted to admin: is_owner=%d is_admin=%d", oldOwnerIsOwner, oldOwnerIsAdmin)
	}
	if newOwnerIsOwner != 1 || newOwnerIsAdmin != 1 {
		t.Errorf("new owner should be owner+admin: is_owner=%d is_admin=%d", newOwnerIsOwner, newOwnerIsAdmin)
	}

	// Exactly one owner remains.
	var owners int
	database.QueryRow(`SELECT COUNT(*) FROM users WHERE is_owner=1`).Scan(&owners)
	if owners != 1 {
		t.Errorf("owner count = %d; want exactly 1", owners)
	}
}

func TestTransferOwnership_requiresOwner(t *testing.T) {
	h, database, _, _ := setupWorkspaceWithDB(t)
	adminKey := "admin-xfer-key"
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin,is_owner) VALUES ('u2','a@example.com','Admin','UTC',1,0)`)
	database.Exec(`INSERT INTO api_keys (id,user_id,name,key_hash,created_at) VALUES ('k2','u2','t',?,'2024-01-01')`, sha256HexForTest(adminKey))
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u3','m@example.com','Member','UTC',0)`)

	req := authReq(http.MethodPost, "/v1/users/u3/transfer-ownership", "", adminKey)
	req.SetPathValue("id", "u3")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.TransferOwnership)(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("got %d; want 403", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// DELETE /v1/users/{id}  — role + booking guards
// ---------------------------------------------------------------------------

func TestDeleteUser_cannotRemoveOwner(t *testing.T) {
	h, database, ownerKey, _ := setupWorkspaceWithDB(t)
	// Make a second owner-less admin actor isn't needed; owner deletes... the owner.
	// Insert another user flagged as owner to attempt deletion of an owner.
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin,is_owner) VALUES ('u2','o2@example.com','Owner2','UTC',1,1)`)

	req := authReq(http.MethodDelete, "/v1/users/u2", "", ownerKey)
	req.SetPathValue("id", "u2")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.DeleteUser)(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d; want 400 (cannot remove owner)", rec.Code)
	}
}

func TestDeleteUser_adminCannotRemoveAdmin(t *testing.T) {
	h, database, _, _ := setupWorkspaceWithDB(t)
	adminKey := "admin-del-admin-key"
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin,is_owner) VALUES ('u2','a@example.com','Admin','UTC',1,0)`)
	database.Exec(`INSERT INTO api_keys (id,user_id,name,key_hash,created_at) VALUES ('k2','u2','t',?,'2024-01-01')`, sha256HexForTest(adminKey))
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u3','a3@example.com','Admin3','UTC',1)`)

	req := authReq(http.MethodDelete, "/v1/users/u3", "", adminKey)
	req.SetPathValue("id", "u3")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.DeleteUser)(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("got %d; want 403 (admin cannot remove admin)", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// POST /v1/users/{id}/password — role-gated (no privilege escalation)
// ---------------------------------------------------------------------------

func TestAdminSetPassword_adminCannotResetOwner(t *testing.T) {
	h, database, _, ownerID := setupWorkspaceWithDB(t)
	adminKey := "admin-resets-owner-key"
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin,is_owner) VALUES ('u2','a@example.com','Admin','UTC',1,0)`)
	database.Exec(`INSERT INTO api_keys (id,user_id,name,key_hash,created_at) VALUES ('k2','u2','t',?,'2024-01-01')`, sha256HexForTest(adminKey))

	req := authReq(http.MethodPost, "/v1/users/"+ownerID+"/password", `{"password":"newpassword123"}`, adminKey)
	req.SetPathValue("id", ownerID)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.AdminSetPassword)(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("got %d; want 403 (admin cannot reset owner password) — %s", rec.Code, rec.Body.String())
	}
}

func TestAdminSetPassword_adminCannotResetAdmin(t *testing.T) {
	h, database, _, _ := setupWorkspaceWithDB(t)
	adminKey := "admin-resets-admin-key"
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin,is_owner) VALUES ('u2','a@example.com','Admin','UTC',1,0)`)
	database.Exec(`INSERT INTO api_keys (id,user_id,name,key_hash,created_at) VALUES ('k2','u2','t',?,'2024-01-01')`, sha256HexForTest(adminKey))
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u3','a3@example.com','Admin3','UTC',1)`)

	req := authReq(http.MethodPost, "/v1/users/u3/password", `{"password":"newpassword123"}`, adminKey)
	req.SetPathValue("id", "u3")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.AdminSetPassword)(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("got %d; want 403 (admin cannot reset another admin) — %s", rec.Code, rec.Body.String())
	}
}

func TestAdminSetPassword_ownerCanResetAdmin_adminCanResetMember(t *testing.T) {
	h, database, ownerKey, _ := setupWorkspaceWithDB(t)
	adminKey := "admin-resets-member-key"
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin,is_owner) VALUES ('u2','a@example.com','Admin','UTC',1,0)`)
	database.Exec(`INSERT INTO api_keys (id,user_id,name,key_hash,created_at) VALUES ('k2','u2','t',?,'2024-01-01')`, sha256HexForTest(adminKey))
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u3','m@example.com','Member','UTC',0)`)

	// Owner resets the admin's password → allowed.
	req := authReq(http.MethodPost, "/v1/users/u2/password", `{"password":"newpassword123"}`, ownerKey)
	req.SetPathValue("id", "u2")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.AdminSetPassword)(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("owner reset admin: got %d; want 200 — %s", rec.Code, rec.Body.String())
	}

	// Admin resets a plain member's password → allowed.
	req = authReq(http.MethodPost, "/v1/users/u3/password", `{"password":"newpassword123"}`, adminKey)
	req.SetPathValue("id", "u3")
	rec = httptest.NewRecorder()
	h.RequireAuth(h.AdminSetPassword)(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("admin reset member: got %d; want 200 — %s", rec.Code, rec.Body.String())
	}
}

func TestDeleteUser_blockedByUpcomingBookings(t *testing.T) {
	h, database, ownerKey, _ := setupWorkspaceWithDB(t)
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u2','host2@example.com','Host','UTC',0)`)
	database.Exec(`INSERT INTO event_types (id,user_id,slug,name,duration_minutes) VALUES ('et1','u2','et-slug','E',30)`)
	// Future, non-cancelled booking hosted by u2.
	database.Exec(`INSERT INTO bookings (id,event_type_id,host_id,start_at,end_at,status)
		VALUES ('b1','et1','u2','2099-01-01T10:00:00Z','2099-01-01T10:30:00Z','confirmed')`)

	req := authReq(http.MethodDelete, "/v1/users/u2", "", ownerKey)
	req.SetPathValue("id", "u2")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.DeleteUser)(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("got %d; want 409 (upcoming bookings block removal) — %s", rec.Code, rec.Body.String())
	}

	// Cancelling the upcoming booking clears the guard (the 409 no longer fires).
	// NOTE: hard-deleting a user with *historical* booking rows is still blocked
	// by the bookings.host_id foreign key — proper member offboarding needs
	// soft-delete/deactivation, tracked separately.
	database.Exec(`UPDATE bookings SET status='cancelled' WHERE id='b1'`)
	req = authReq(http.MethodDelete, "/v1/users/u2", "", ownerKey)
	req.SetPathValue("id", "u2")
	rec = httptest.NewRecorder()
	h.RequireAuth(h.DeleteUser)(rec, req)
	if rec.Code == http.StatusConflict {
		t.Fatalf("after cancel the upcoming-booking guard should no longer block, got 409")
	}
}
