package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func createInviteReq(email, apiKey string) *http.Request {
	body := `{"email":"` + email + `"}`
	return authReq(http.MethodPost, "/v1/invites", body, apiKey)
}

// ---------------------------------------------------------------------------
// POST /v1/invites
// ---------------------------------------------------------------------------

func TestCreateInvite_adminCreates(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)

	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateInvite)(rec, createInviteReq("bob@example.com", key))

	if rec.Code != http.StatusCreated {
		t.Fatalf("got %d; want 201 — %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["invite_url"] == "" {
		t.Error("invite_url is empty")
	}
	if resp["email"] != "bob@example.com" {
		t.Errorf("email = %q; want bob@example.com", resp["email"])
	}
	if resp["note"] == "" {
		t.Error("note is empty — should warn about expiry and lock")
	}
}

func TestCreateInvite_requiresAdmin(t *testing.T) {
	h, database, _, _ := setupWorkspaceWithDB(t)

	rawKey := "non-admin-invite-key"
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u2','other@example.com','Other','UTC',0)`)
	database.Exec(`INSERT INTO api_keys (id,user_id,name,key_hash,created_at) VALUES ('k2','u2','test',?,'2024-01-01')`, sha256HexForTest(rawKey))

	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateInvite)(rec, createInviteReq("bob@example.com", rawKey))

	if rec.Code != http.StatusForbidden {
		t.Errorf("got %d; want 403", rec.Code)
	}
}

func TestCreateInvite_existingUserConflict(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)

	// host@example.com is already created by setupWorkspaceWithDB.
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateInvite)(rec, createInviteReq("host@example.com", key))

	if rec.Code != http.StatusConflict {
		t.Errorf("got %d; want 409", rec.Code)
	}
}

func TestCreateInvite_replacesExisting(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)

	// Create two invites for the same email — second should replace first.
	h.RequireAuth(h.CreateInvite)(httptest.NewRecorder(), createInviteReq("bob@example.com", key))

	rec2 := httptest.NewRecorder()
	h.RequireAuth(h.CreateInvite)(rec2, createInviteReq("bob@example.com", key))
	if rec2.Code != http.StatusCreated {
		t.Fatalf("second invite: got %d — %s", rec2.Code, rec2.Body.String())
	}

	// Only one pending invite should exist.
	var count int
	database.QueryRow(`SELECT COUNT(*) FROM invite_tokens WHERE email='bob@example.com' AND used_at IS NULL`).Scan(&count)
	if count != 1 {
		t.Errorf("pending invite count = %d; want 1 after replacement", count)
	}
}

// ---------------------------------------------------------------------------
// GET /v1/invites/{token}  and  POST /v1/invites/{token}/claim
// ---------------------------------------------------------------------------

func TestGetInvite_valid(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)

	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateInvite)(rec, createInviteReq("bob@example.com", key))
	var created map[string]any
	json.Unmarshal(rec.Body.Bytes(), &created)
	inviteURL := created["invite_url"].(string)
	// Extract token from URL (last path segment).
	parts := strings.Split(inviteURL, "/")
	token := parts[len(parts)-1]

	req := httptest.NewRequest(http.MethodGet, "/v1/invites/"+token, nil)
	req.SetPathValue("token", token)
	rec2 := httptest.NewRecorder()
	h.GetInvite(rec2, req)

	if rec2.Code != http.StatusOK {
		t.Fatalf("got %d; want 200 — %s", rec2.Code, rec2.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(rec2.Body.Bytes(), &resp)
	if resp["email"] != "bob@example.com" {
		t.Errorf("email = %q; want bob@example.com", resp["email"])
	}
}

func TestGetInvite_invalidToken(t *testing.T) {
	h, _, _, _ := setupWorkspaceWithDB(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/invites/badtoken", nil)
	req.SetPathValue("token", "badtoken")
	rec := httptest.NewRecorder()
	h.GetInvite(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d; want 404", rec.Code)
	}
}

func TestClaimInvite_success(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)

	// Create invite.
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateInvite)(rec, createInviteReq("bob@example.com", key))
	var created map[string]any
	json.Unmarshal(rec.Body.Bytes(), &created)
	parts := strings.Split(created["invite_url"].(string), "/")
	token := parts[len(parts)-1]

	// Claim it.
	body := `{"name":"Bob","password":"bobspassword1","timezone":"UTC"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/invites/"+token+"/claim", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("token", token)
	rec2 := httptest.NewRecorder()
	h.ClaimInvite(rec2, req)

	if rec2.Code != http.StatusCreated {
		t.Fatalf("got %d; want 201 — %s", rec2.Code, rec2.Body.String())
	}
	// Session cookie must be set.
	found := false
	for _, c := range rec2.Result().Cookies() {
		if c.Name == "calnode_session" {
			found = true
		}
	}
	if !found {
		t.Error("no session cookie set after claiming invite")
	}
}

func TestClaimInvite_cannotReuseToken(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)

	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateInvite)(rec, createInviteReq("bob@example.com", key))
	var created map[string]any
	json.Unmarshal(rec.Body.Bytes(), &created)
	parts := strings.Split(created["invite_url"].(string), "/")
	token := parts[len(parts)-1]

	claimBody := `{"name":"Bob","password":"bobspassword1","timezone":"UTC"}`
	claim := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/invites/"+token+"/claim", strings.NewReader(claimBody))
		req.Header.Set("Content-Type", "application/json")
		req.SetPathValue("token", token)
		rec := httptest.NewRecorder()
		h.ClaimInvite(rec, req)
		return rec
	}

	if r := claim(); r.Code != http.StatusCreated {
		t.Fatalf("first claim: got %d — %s", r.Code, r.Body.String())
	}
	if r := claim(); r.Code != http.StatusNotFound {
		t.Errorf("second claim: got %d; want 404 (token already used)", r.Code)
	}
}

func TestClaimInvite_expiredToken(t *testing.T) {
	h, database, _, userID := setupWorkspaceWithDB(t)

	// Insert an already-expired token directly.
	pastTime := time.Now().UTC().Add(-8 * 24 * time.Hour).Format(time.RFC3339)
	database.Exec(`INSERT INTO invite_tokens (id,email,token_hash,created_by,expires_at) VALUES ('inv1','bob@example.com','fakehash123',?,?)`, userID, pastTime)

	req := httptest.NewRequest(http.MethodGet, "/v1/invites/faketoken", nil)
	req.SetPathValue("token", "faketoken")
	rec := httptest.NewRecorder()
	h.GetInvite(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d; want 404 for expired token", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// DELETE /v1/invites/{id}
// ---------------------------------------------------------------------------

func TestRevokeInvite_success(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)

	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateInvite)(rec, createInviteReq("bob@example.com", key))
	var created map[string]any
	json.Unmarshal(rec.Body.Bytes(), &created)
	inviteID := created["id"].(string)

	req := authReq(http.MethodDelete, "/v1/invites/"+inviteID, "", key)
	req.SetPathValue("id", inviteID)
	rec2 := httptest.NewRecorder()
	h.RequireAuth(h.RevokeInvite)(rec2, req)

	if rec2.Code != http.StatusOK {
		t.Fatalf("got %d; want 200 — %s", rec2.Code, rec2.Body.String())
	}

	// Token should now be gone.
	req3 := httptest.NewRequest(http.MethodGet, "/v1/invites", nil)
	req3.Header.Set("X-API-Key", key)
	rec3 := httptest.NewRecorder()
	h.RequireAuth(h.ListInvites)(rec3, req3)
	var list []any
	json.Unmarshal(rec3.Body.Bytes(), &list)
	if len(list) != 0 {
		t.Errorf("invite list has %d items; want 0 after revoke", len(list))
	}
}

// ---------------------------------------------------------------------------
// POST /v1/invites/{id}/resend
// ---------------------------------------------------------------------------

func TestResendInvite_reissuesFreshLink(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)

	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateInvite)(rec, createInviteReq("bob@example.com", key))
	var created map[string]any
	json.Unmarshal(rec.Body.Bytes(), &created)
	origID := created["id"].(string)
	origURL := created["invite_url"].(string)

	req := authReq(http.MethodPost, "/v1/invites/"+origID+"/resend", "", key)
	req.SetPathValue("id", origID)
	rec2 := httptest.NewRecorder()
	h.RequireAuth(h.ResendInvite)(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Fatalf("resend: got %d; want 200 — %s", rec2.Code, rec2.Body.String())
	}
	var resent map[string]any
	json.Unmarshal(rec2.Body.Bytes(), &resent)
	if resent["email"] != "bob@example.com" {
		t.Errorf("resend email = %v; want bob@example.com", resent["email"])
	}
	if resent["invite_url"] == "" || resent["invite_url"] == origURL {
		t.Errorf("resend must mint a NEW link; got %v (orig %v)", resent["invite_url"], origURL)
	}
	if resent["id"] == origID {
		t.Error("resend must create a new invite id (the old one is invalidated)")
	}

	// Exactly one pending invite remains — the fresh one; the old token is gone.
	listReq := authReq(http.MethodGet, "/v1/invites", "", key)
	rec3 := httptest.NewRecorder()
	h.RequireAuth(h.ListInvites)(rec3, listReq)
	var list []map[string]any
	json.Unmarshal(rec3.Body.Bytes(), &list)
	if len(list) != 1 {
		t.Errorf("pending invites = %d; want 1 after resend", len(list))
	}
}

func TestResendInvite_notFound(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	req := authReq(http.MethodPost, "/v1/invites/nope/resend", "", key)
	req.SetPathValue("id", "nope")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ResendInvite)(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("resend nonexistent: got %d; want 404", rec.Code)
	}
}
