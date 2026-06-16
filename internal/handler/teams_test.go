package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/calnode/calnode/internal/handler"
)

// createTeamHTTP creates a team via the handler and returns its id.
func createTeamHTTP(t *testing.T, h *handler.Handler, key, body string) (string, *httptest.ResponseRecorder) {
	t.Helper()
	req := authReq(http.MethodPost, "/v1/teams", body, key)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateTeam)(rec, req)
	var resp struct {
		ID string `json:"id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	return resp.ID, rec
}

func TestCreateTeam_derivesSlugAndRejectsDuplicate(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)

	id, rec := createTeamHTTP(t, h, key, `{"name":"Sales Team"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: got %d — %s", rec.Code, rec.Body.String())
	}
	if id == "" {
		t.Fatal("expected a team id")
	}
	var resp struct {
		Slug string `json:"slug"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Slug != "sales-team" {
		t.Errorf("slug = %q; want sales-team", resp.Slug)
	}

	// Same derived slug → 409.
	_, rec2 := createTeamHTTP(t, h, key, `{"name":"Sales Team"}`)
	if rec2.Code != http.StatusConflict {
		t.Errorf("duplicate slug: got %d; want 409", rec2.Code)
	}
}

func TestCreateTeam_requiresAdmin(t *testing.T) {
	h, database, _, _ := setupWorkspaceWithDB(t)
	memberKey := "team-member-key"
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u2','m@example.com','Member','UTC',0)`)
	database.Exec(`INSERT INTO api_keys (id,user_id,name,key_hash,created_at) VALUES ('k2','u2','t',?,'2024-01-01')`, sha256HexForTest(memberKey))

	req := authReq(http.MethodPost, "/v1/teams", `{"name":"X"}`, memberKey)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateTeam)(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("got %d; want 403", rec.Code)
	}
}

func TestTeamMembers_addUpdateRemove(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u2','m@example.com','Member','UTC',0)`)
	teamID, _ := createTeamHTTP(t, h, key, `{"name":"Support"}`)

	// Add member with a routing priority.
	req := authReq(http.MethodPost, "/v1/teams/"+teamID+"/members", `{"user_id":"u2","routing_priority":5}`, key)
	req.SetPathValue("id", teamID)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.AddTeamMember)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("add member: got %d — %s", rec.Code, rec.Body.String())
	}
	var team struct {
		MemberCount int `json:"member_count"`
		Members     []struct {
			ID              string `json:"id"`
			RoutingPriority int    `json:"routing_priority"`
		} `json:"members"`
	}
	json.Unmarshal(rec.Body.Bytes(), &team)
	if team.MemberCount != 1 || team.Members[0].ID != "u2" || team.Members[0].RoutingPriority != 5 {
		t.Fatalf("unexpected team after add: %+v", team)
	}

	// Adding again → 409.
	req = authReq(http.MethodPost, "/v1/teams/"+teamID+"/members", `{"user_id":"u2"}`, key)
	req.SetPathValue("id", teamID)
	rec = httptest.NewRecorder()
	h.RequireAuth(h.AddTeamMember)(rec, req)
	if rec.Code != http.StatusConflict {
		t.Errorf("duplicate member: got %d; want 409", rec.Code)
	}

	// Update routing priority.
	req = authReq(http.MethodPatch, "/v1/teams/"+teamID+"/members/u2", `{"routing_priority":2}`, key)
	req.SetPathValue("id", teamID)
	req.SetPathValue("userId", "u2")
	rec = httptest.NewRecorder()
	h.RequireAuth(h.UpdateTeamMember)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update member: got %d — %s", rec.Code, rec.Body.String())
	}
	var prio int
	database.QueryRow(`SELECT routing_priority FROM team_members WHERE team_id=? AND user_id='u2'`, teamID).Scan(&prio)
	if prio != 2 {
		t.Errorf("routing_priority = %d; want 2", prio)
	}

	// Remove member.
	req = authReq(http.MethodDelete, "/v1/teams/"+teamID+"/members/u2", "", key)
	req.SetPathValue("id", teamID)
	req.SetPathValue("userId", "u2")
	rec = httptest.NewRecorder()
	h.RequireAuth(h.RemoveTeamMember)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove member: got %d — %s", rec.Code, rec.Body.String())
	}
	var count int
	database.QueryRow(`SELECT COUNT(*) FROM team_members WHERE team_id=?`, teamID).Scan(&count)
	if count != 0 {
		t.Errorf("member count after remove = %d; want 0", count)
	}
}

func TestAddTeamMember_rejectsArchived(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin,archived_at) VALUES ('u2','m@example.com','Member','UTC',0,'2026-01-01T00:00:00Z')`)
	teamID, _ := createTeamHTTP(t, h, key, `{"name":"Ops"}`)

	req := authReq(http.MethodPost, "/v1/teams/"+teamID+"/members", `{"user_id":"u2"}`, key)
	req.SetPathValue("id", teamID)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.AddTeamMember)(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d; want 400 (archived member) — %s", rec.Code, rec.Body.String())
	}
}

func TestDeleteTeam_cascadesMembership(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u2','m@example.com','Member','UTC',0)`)
	teamID, _ := createTeamHTTP(t, h, key, `{"name":"Temp"}`)
	database.Exec(`INSERT INTO team_members (id,team_id,user_id,role,routing_priority) VALUES ('tm1',?,'u2','member',0)`, teamID)

	req := authReq(http.MethodDelete, "/v1/teams/"+teamID, "", key)
	req.SetPathValue("id", teamID)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.DeleteTeam)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete team: got %d — %s", rec.Code, rec.Body.String())
	}
	var count int
	database.QueryRow(`SELECT COUNT(*) FROM team_members WHERE team_id=?`, teamID).Scan(&count)
	if count != 0 {
		t.Errorf("team_members after team delete = %d; want 0 (cascade)", count)
	}
}

func TestListUsers_includesTeams(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	database.Exec(`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES ('u2','m@example.com','Member','UTC',0)`)
	teamID, _ := createTeamHTTP(t, h, key, `{"name":"Growth"}`)
	database.Exec(`INSERT INTO team_members (id,team_id,user_id,role,routing_priority) VALUES ('tm1',?,'u2','member',0)`, teamID)

	req := authReq(http.MethodGet, "/v1/users", "", key)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ListUsers)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list users: got %d — %s", rec.Code, rec.Body.String())
	}
	var users []struct {
		ID    string `json:"id"`
		Teams []struct {
			Name string `json:"name"`
		} `json:"teams"`
	}
	json.Unmarshal(rec.Body.Bytes(), &users)
	var found bool
	for _, u := range users {
		if u.ID == "u2" {
			if len(u.Teams) != 1 || u.Teams[0].Name != "Growth" {
				t.Errorf("u2 teams = %+v; want [Growth]", u.Teams)
			}
			found = true
		}
	}
	if !found {
		t.Error("u2 not in user list")
	}
}
