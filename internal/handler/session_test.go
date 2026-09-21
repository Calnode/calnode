package handler_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/handler"
)

// seedSessionID inserts a live session row under a caller-chosen id (the cookie value),
// so one user can be given several and a test can name the one it presents.
func seedSessionID(t *testing.T, database *sql.DB, id, userID string) string {
	t.Helper()
	if _, err := database.Exec(`INSERT INTO sessions (id, user_id, expires_at) VALUES (?, ?, ?)`,
		id, userID, time.Now().UTC().Add(24*time.Hour).Format(time.RFC3339)); err != nil {
		t.Fatalf("seed session %s: %v", id, err)
	}
	return id
}

// seedMCPToken inserts an MCP OAuth access token for userID.
func seedMCPToken(t *testing.T, database *sql.DB, id, userID string) {
	t.Helper()
	if _, err := database.Exec(`
		INSERT INTO oauth_access_tokens (id, token_hash, client_id, user_id, expires_at, created_at)
		VALUES (?, ?, 'client-1', ?, ?, ?)`,
		id, "hash-"+id, userID,
		time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
		time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("seed mcp token %s: %v", id, err)
	}
}

func countSessions(t *testing.T, database *sql.DB, userID string) int {
	t.Helper()
	var n int
	if err := database.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id = ?`, userID).Scan(&n); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	return n
}

func countMCPTokens(t *testing.T, database *sql.DB, userID string) int {
	t.Helper()
	var n int
	if err := database.QueryRow(`SELECT COUNT(*) FROM oauth_access_tokens WHERE user_id = ?`, userID).Scan(&n); err != nil {
		t.Fatalf("count mcp tokens: %v", err)
	}
	return n
}

// revokeAll drives the handler through RequireAuth. cookie is the session cookie value
// to present (empty for none); apiKey authenticates when no cookie is given.
func revokeAll(h *handler.Handler, body, apiKey, cookie string) *httptest.ResponseRecorder {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(http.MethodPost, "/v1/auth/sessions/revoke-all", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(http.MethodPost, "/v1/auth/sessions/revoke-all", nil)
	}
	if apiKey != "" {
		r.Header.Set("X-API-Key", apiKey)
	}
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: "calnode_session", Value: cookie})
	}
	rec := httptest.NewRecorder()
	h.RequireAuth(h.RevokeAllSessions)(rec, r)
	return rec
}

// Revoking your own keeps the session that asked. That is the difference between this
// endpoint and Logout, and the reason it is the default with no body.
func TestRevokeAllSessions_selfKeepsTheCallingSession(t *testing.T) {
	h, database, _, ownerID := setupWorkspaceWithDB(t)
	current := seedSessionID(t, database, "sess-current", ownerID)
	seedSessionID(t, database, "sess-laptop", ownerID)
	seedSessionID(t, database, "sess-phone", ownerID)

	rec := revokeAll(h, "", "", current)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 — %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		SessionsRevoked int `json:"sessions_revoked"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.SessionsRevoked != 2 {
		t.Errorf("sessions_revoked = %d; want 2", resp.SessionsRevoked)
	}
	if n := countSessions(t, database, ownerID); n != 1 {
		t.Fatalf("sessions left = %d; want 1 (the calling one)", n)
	}
	var left string
	if err := database.QueryRow(`SELECT id FROM sessions WHERE user_id = ?`, ownerID).Scan(&left); err != nil {
		t.Fatalf("read surviving session: %v", err)
	}
	if left != current {
		t.Errorf("surviving session = %q; want %q", left, current)
	}
}

// An API-key caller has no current session, so there is none to spare.
func TestRevokeAllSessions_apiKeyCallerRevokesEveryone(t *testing.T) {
	h, database, ownerKey, ownerID := setupWorkspaceWithDB(t)
	seedSessionID(t, database, "sess-a", ownerID)
	seedSessionID(t, database, "sess-b", ownerID)

	rec := revokeAll(h, "", ownerKey, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 — %s", rec.Code, rec.Body.String())
	}
	if n := countSessions(t, database, ownerID); n != 0 {
		t.Errorf("sessions left = %d; want 0", n)
	}
}

// ⛔ An API-key request that ALSO carries a cookie is still an API-key request —
// RequireAuth tries the key first, so the cookie played no part in authenticating it and
// there is no current session to spare. The test above sends a key and no cookie, so it
// passes whether the handler reads the cookie or not; this is the case that separates
// them. Sparing a session on the strength of a header the caller was not authenticated by
// would leave a script that asked to end all its sessions with one alive, and the response
// counts what was deleted, not what was kept, so nothing would say so.
func TestRevokeAllSessions_apiKeyCallerWithAStaleCookieStillRevokesEveryone(t *testing.T) {
	h, database, ownerKey, ownerID := setupWorkspaceWithDB(t)
	seedSessionID(t, database, "sess-stale", ownerID)
	seedSessionID(t, database, "sess-other", ownerID)

	rec := revokeAll(h, "", ownerKey, "sess-stale")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 — %s", rec.Code, rec.Body.String())
	}
	if n := countSessions(t, database, ownerID); n != 0 {
		t.Errorf("sessions left = %d; want 0 — the cookie is not what authenticated this "+
			"caller, so no session is the \"current\" one", n)
	}
}

// An MCP connector holds a bearer token, not a cookie. Revoking sessions and leaving it
// would hand back exactly the access that was just withdrawn.
func TestRevokeAllSessions_cutsMCPTokensToo(t *testing.T) {
	h, database, ownerKey, ownerID := setupWorkspaceWithDB(t)
	seedMCPToken(t, database, "tok-1", ownerID)
	seedMCPToken(t, database, "tok-2", ownerID)

	rec := revokeAll(h, "", ownerKey, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 — %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		OAuthTokensRevoked int `json:"oauth_tokens_revoked"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.OAuthTokensRevoked != 2 {
		t.Errorf("oauth_tokens_revoked = %d; want 2", resp.OAuthTokensRevoked)
	}
	if n := countMCPTokens(t, database, ownerID); n != 0 {
		t.Errorf("mcp tokens left = %d; want 0", n)
	}
}

// seedRoleUser inserts a user with the given flags plus an API key for them.
func seedRoleUser(t *testing.T, database *sql.DB, id, email string, isAdmin, isOwner int, apiKey string) {
	t.Helper()
	if _, err := database.Exec(
		`INSERT INTO users (id,email,name,iana_timezone,is_admin,is_owner) VALUES (?,?,?,'UTC',?,?)`,
		id, email, id, isAdmin, isOwner); err != nil {
		t.Fatalf("seed user %s: %v", id, err)
	}
	if apiKey != "" {
		if _, err := database.Exec(
			`INSERT INTO api_keys (id,user_id,name,key_hash,created_at) VALUES (?,?,'t',?,'2024-01-01')`,
			"key-"+id, id, sha256HexForTest(apiKey)); err != nil {
			t.Fatalf("seed api key for %s: %v", id, err)
		}
	}
}

func TestRevokeAllSessions_memberCannotTargetAnotherUser(t *testing.T) {
	h, database, _, _ := setupWorkspaceWithDB(t)
	seedRoleUser(t, database, "member-1", "m1@example.com", 0, 0, "member-1-key")
	seedRoleUser(t, database, "member-2", "m2@example.com", 0, 0, "")
	seedSessionID(t, database, "sess-victim", "member-2")

	rec := revokeAll(h, `{"user_id":"member-2"}`, "member-1-key", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d; want 403 — %s", rec.Code, rec.Body.String())
	}
	if n := countSessions(t, database, "member-2"); n != 1 {
		t.Errorf("victim sessions = %d; want 1 (untouched)", n)
	}
}

// ⛔ A member gets the SAME answer for a user that exists and one that does not, which is
// what stops this endpoint being a user-id oracle. The handler checks the actor's tier
// before it loads the target for exactly this reason, and the test above cannot see that
// — it names a real user, so it would pass just as well with the checks in either order.
func TestRevokeAllSessions_memberCannotProbeForUserIDs(t *testing.T) {
	h, database, _, _ := setupWorkspaceWithDB(t)
	seedRoleUser(t, database, "member-1", "m1@example.com", 0, 0, "member-1-key")
	seedRoleUser(t, database, "member-2", "m2@example.com", 0, 0, "")

	real := revokeAll(h, `{"user_id":"member-2"}`, "member-1-key", "")
	fake := revokeAll(h, `{"user_id":"no-such-user-at-all"}`, "member-1-key", "")

	if real.Code != http.StatusForbidden || fake.Code != http.StatusForbidden {
		t.Fatalf("existing user = %d, unknown user = %d; want 403 for both, or the status "+
			"code tells a member which ids exist", real.Code, fake.Code)
	}
	// The body has to match too: a differing message is the same oracle in prose.
	if real.Body.String() != fake.Body.String() {
		t.Errorf("bodies differ between an existing and an unknown user:\n  existing: %s\n  unknown:  %s",
			real.Body.String(), fake.Body.String())
	}
}

func TestRevokeAllSessions_adminRevokesAMember(t *testing.T) {
	h, database, _, _ := setupWorkspaceWithDB(t)
	seedRoleUser(t, database, "admin-1", "a1@example.com", 1, 0, "admin-1-key")
	seedRoleUser(t, database, "member-1", "m1@example.com", 0, 0, "")
	seedSessionID(t, database, "sess-m1a", "member-1")
	seedSessionID(t, database, "sess-m1b", "member-1")
	seedMCPToken(t, database, "tok-m1", "member-1")

	rec := revokeAll(h, `{"user_id":"member-1"}`, "admin-1-key", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 — %s", rec.Code, rec.Body.String())
	}
	if n := countSessions(t, database, "member-1"); n != 0 {
		t.Errorf("member sessions = %d; want 0", n)
	}
	if n := countMCPTokens(t, database, "member-1"); n != 0 {
		t.Errorf("member mcp tokens = %d; want 0", n)
	}
}

func TestRevokeAllSessions_onlyTheOwnerRevokesAnAdmin(t *testing.T) {
	h, database, ownerKey, _ := setupWorkspaceWithDB(t)
	seedRoleUser(t, database, "admin-1", "a1@example.com", 1, 0, "admin-1-key")
	seedRoleUser(t, database, "admin-2", "a2@example.com", 1, 0, "")
	seedSessionID(t, database, "sess-a2", "admin-2")

	// Admin → admin is refused.
	rec := revokeAll(h, `{"user_id":"admin-2"}`, "admin-1-key", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("admin targeting admin: status = %d; want 403 — %s", rec.Code, rec.Body.String())
	}
	if n := countSessions(t, database, "admin-2"); n != 1 {
		t.Fatalf("admin-2 sessions = %d; want 1 (untouched)", n)
	}

	// Owner → admin is allowed.
	rec = revokeAll(h, `{"user_id":"admin-2"}`, ownerKey, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("owner targeting admin: status = %d; want 200 — %s", rec.Code, rec.Body.String())
	}
	if n := countSessions(t, database, "admin-2"); n != 0 {
		t.Errorf("admin-2 sessions = %d; want 0", n)
	}
}

// Nobody signs the owner out but the owner, mirroring roles.go refusing to change the
// owner's role.
func TestRevokeAllSessions_ownerIsOffLimitsToAdmins(t *testing.T) {
	h, database, _, ownerID := setupWorkspaceWithDB(t)
	seedRoleUser(t, database, "admin-1", "a1@example.com", 1, 0, "admin-1-key")
	seedSessionID(t, database, "sess-owner", ownerID)

	rec := revokeAll(h, `{"user_id":"`+ownerID+`"}`, "admin-1-key", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d; want 403 — %s", rec.Code, rec.Body.String())
	}
	if n := countSessions(t, database, ownerID); n != 1 {
		t.Errorf("owner sessions = %d; want 1 (untouched)", n)
	}
}

func TestRevokeAllSessions_unknownUserIs404(t *testing.T) {
	h, _, ownerKey, _ := setupWorkspaceWithDB(t)

	rec := revokeAll(h, `{"user_id":"nobody"}`, ownerKey, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404 — %s", rec.Code, rec.Body.String())
	}
}

// Naming yourself is the self branch, not the admin branch: a member may do it.
func TestRevokeAllSessions_namingYourselfIsSelf(t *testing.T) {
	h, database, _, _ := setupWorkspaceWithDB(t)
	seedRoleUser(t, database, "member-1", "m1@example.com", 0, 0, "member-1-key")
	seedSessionID(t, database, "sess-m1", "member-1")

	rec := revokeAll(h, `{"user_id":"member-1"}`, "member-1-key", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 — %s", rec.Code, rec.Body.String())
	}
	if n := countSessions(t, database, "member-1"); n != 0 {
		t.Errorf("sessions left = %d; want 0 (api-key caller has no current session)", n)
	}
}
