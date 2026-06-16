package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestArchiveUser_archivesAndBlocksLogin(t *testing.T) {
	h, database, ownerKey, _ := setupWorkspaceWithDB(t)
	memberKey := "member-archive-key"
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u2','m@example.com','Member','UTC',0)`)
	database.Exec(`INSERT INTO api_keys (id,user_id,name,key_hash,created_at) VALUES ('k2','u2','t',?,'2024-01-01')`, sha256HexForTest(memberKey))
	database.Exec(`INSERT INTO event_types (id,user_id,slug,name,duration_minutes,is_active) VALUES ('et1','u2','et-slug','E',30,1)`)

	// Archive u2.
	req := authReq(http.MethodPost, "/v1/users/u2/archive", "", ownerKey)
	req.SetPathValue("id", "u2")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ArchiveUser)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("archive: got %d — %s", rec.Code, rec.Body.String())
	}

	// archived_at set; event types deactivated.
	var archivedAt *string
	database.QueryRow(`SELECT archived_at FROM users WHERE id='u2'`).Scan(&archivedAt)
	if archivedAt == nil {
		t.Error("archived_at should be set")
	}
	var active int
	database.QueryRow(`SELECT is_active FROM event_types WHERE id='et1'`).Scan(&active)
	if active != 0 {
		t.Error("archived member's event types should be deactivated")
	}

	// The archived member's API key no longer authenticates.
	req = authReq(http.MethodGet, "/v1/users/me", "", memberKey)
	rec = httptest.NewRecorder()
	h.RequireAuth(h.GetMe)(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("archived member auth: got %d; want 401", rec.Code)
	}
}

func TestArchiveUser_hiddenFromDefaultListShownWithFlag(t *testing.T) {
	h, database, ownerKey, _ := setupWorkspaceWithDB(t)
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin,archived_at) VALUES ('u2','m@example.com','Member','UTC',0,'2026-01-01T00:00:00Z')`)

	// Default list excludes archived.
	req := authReq(http.MethodGet, "/v1/users", "", ownerKey)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ListUsers)(rec, req)
	if rec.Body.String() == "" || rec.Code != http.StatusOK {
		t.Fatalf("list: got %d — %s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); strings.Contains(got, "u2") {
		t.Error("archived member should be hidden from default list")
	}

	// With include_archived=true they appear.
	req = authReq(http.MethodGet, "/v1/users?include_archived=true", "", ownerKey)
	rec = httptest.NewRecorder()
	h.RequireAuth(h.ListUsers)(rec, req)
	if got := rec.Body.String(); !strings.Contains(got, "u2") {
		t.Error("archived member should appear with include_archived=true")
	}
}

func TestArchiveUser_blockedByUpcomingBookings(t *testing.T) {
	h, database, ownerKey, _ := setupWorkspaceWithDB(t)
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u2','m@example.com','Member','UTC',0)`)
	database.Exec(`INSERT INTO event_types (id,user_id,slug,name,duration_minutes) VALUES ('et1','u2','et-slug','E',30)`)
	database.Exec(`INSERT INTO bookings (id,event_type_id,host_id,start_at,end_at,status)
		VALUES ('b1','et1','u2','2099-01-01T10:00:00Z','2099-01-01T10:30:00Z','confirmed')`)

	req := authReq(http.MethodPost, "/v1/users/u2/archive", "", ownerKey)
	req.SetPathValue("id", "u2")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ArchiveUser)(rec, req)
	if rec.Code != http.StatusConflict {
		t.Errorf("got %d; want 409 (upcoming bookings) — %s", rec.Code, rec.Body.String())
	}
}

func TestArchiveUser_cannotArchiveOwner(t *testing.T) {
	h, _, ownerKey, ownerID := setupWorkspaceWithDB(t)
	req := authReq(http.MethodPost, "/v1/users/"+ownerID+"/archive", "", ownerKey)
	req.SetPathValue("id", ownerID)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ArchiveUser)(rec, req)
	// Owner archiving self → self guard (400). Also owner-guard covers others.
	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d; want 400", rec.Code)
	}
}

func TestRestoreUser_adminOnlyRestoresOwnArchives(t *testing.T) {
	h, database, ownerKey, _ := setupWorkspaceWithDB(t)
	adminAKey := "adminA-restore-key"
	adminBKey := "adminB-restore-key"
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin,is_owner) VALUES ('a','aa@example.com','AdminA','UTC',1,0)`)
	database.Exec(`INSERT INTO api_keys (id,user_id,name,key_hash,created_at) VALUES ('ka','a','t',?,'2024-01-01')`, sha256HexForTest(adminAKey))
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin,is_owner) VALUES ('b','bb@example.com','AdminB','UTC',1,0)`)
	database.Exec(`INSERT INTO api_keys (id,user_id,name,key_hash,created_at) VALUES ('kb','b','t',?,'2024-01-01')`, sha256HexForTest(adminBKey))
	// Member archived by AdminA.
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin,archived_at,archived_by) VALUES ('m','m@example.com','Member','UTC',0,'2026-01-01T00:00:00Z','a')`)

	// AdminB cannot restore AdminA's archive.
	req := authReq(http.MethodPost, "/v1/users/m/restore", "", adminBKey)
	req.SetPathValue("id", "m")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.RestoreUser)(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("adminB restoring adminA's archive: got %d; want 403 — %s", rec.Code, rec.Body.String())
	}

	// AdminA (the archiver) can.
	req = authReq(http.MethodPost, "/v1/users/m/restore", "", adminAKey)
	req.SetPathValue("id", "m")
	rec = httptest.NewRecorder()
	h.RequireAuth(h.RestoreUser)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("adminA restoring own archive: got %d; want 200 — %s", rec.Code, rec.Body.String())
	}

	// Re-archive (by AdminA) and confirm the owner can always restore.
	database.Exec(`UPDATE users SET archived_at='2026-01-01T00:00:00Z', archived_by='a' WHERE id='m'`)
	req = authReq(http.MethodPost, "/v1/users/m/restore", "", ownerKey)
	req.SetPathValue("id", "m")
	rec = httptest.NewRecorder()
	h.RequireAuth(h.RestoreUser)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner restoring adminA's archive: got %d; want 200 — %s", rec.Code, rec.Body.String())
	}
}

func TestRestoreUser_reenablesLogin(t *testing.T) {
	h, database, ownerKey, _ := setupWorkspaceWithDB(t)
	memberKey := "member-restore-key"
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin,archived_at) VALUES ('u2','m@example.com','Member','UTC',0,'2026-01-01T00:00:00Z')`)
	database.Exec(`INSERT INTO api_keys (id,user_id,name,key_hash,created_at) VALUES ('k2','u2','t',?,'2024-01-01')`, sha256HexForTest(memberKey))

	// Archived member can't auth yet.
	req := authReq(http.MethodGet, "/v1/users/me", "", memberKey)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.GetMe)(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("pre-restore auth: got %d; want 401", rec.Code)
	}

	// Restore.
	req = authReq(http.MethodPost, "/v1/users/u2/restore", "", ownerKey)
	req.SetPathValue("id", "u2")
	rec = httptest.NewRecorder()
	h.RequireAuth(h.RestoreUser)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("restore: got %d — %s", rec.Code, rec.Body.String())
	}

	// Now the member can auth again.
	req = authReq(http.MethodGet, "/v1/users/me", "", memberKey)
	rec = httptest.NewRecorder()
	h.RequireAuth(h.GetMe)(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("post-restore auth: got %d; want 200", rec.Code)
	}
}
