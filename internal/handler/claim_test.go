package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calnode/calnode/internal/handler"
)

// newEmptyHandler returns a handler backed by a migrated but empty database
// (no users, no API keys). Used to test first-boot flows.
func newEmptyHandler(t *testing.T) *handler.Handler {
	t.Helper()
	h, _ := newTestHandlerDB(t)
	return h
}

// ---------------------------------------------------------------------------
// GET /v1/auth/status
// ---------------------------------------------------------------------------

func TestAuthStatus_unclaimed(t *testing.T) {
	h, _, _, _ := setupWorkspaceWithDB(t)
	// setupWorkspaceWithDB creates one user — we need a fresh empty DB instead.
	// Use a separate empty-DB helper.
	h2 := newEmptyHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/status", nil)
	rec := httptest.NewRecorder()
	h2.AuthStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d; want 200", rec.Code)
	}
	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if claimed, _ := resp["claimed"].(bool); claimed {
		t.Error("claimed = true on empty DB; want false")
	}
	_ = h // prevent unused warning
}

func TestAuthStatus_claimed(t *testing.T) {
	h, _, _, _ := setupWorkspaceWithDB(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/status", nil)
	rec := httptest.NewRecorder()
	h.AuthStatus(rec, req)

	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if claimed, _ := resp["claimed"].(bool); !claimed {
		t.Error("claimed = false; want true when users exist")
	}
}

// ---------------------------------------------------------------------------
// POST /v1/auth/claim
// ---------------------------------------------------------------------------

func TestClaim_firstUser(t *testing.T) {
	h := newEmptyHandler(t)

	body := `{"name":"Alice","email":"alice@example.com","password":"strongpass1","timezone":"UTC"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/claim", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.Claim(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("got %d; want 201 — %s", rec.Code, rec.Body.String())
	}
	// Session cookie must be set.
	if rec.Result().Cookies() == nil {
		t.Error("no cookies set after claim")
	}
	found := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == "calnode_session" {
			found = true
		}
	}
	if !found {
		t.Error("calnode_session cookie not set after claim")
	}
}

func TestClaim_alreadyClaimed(t *testing.T) {
	// setupWorkspaceWithDB pre-seeds one user.
	h, _, _, _ := setupWorkspaceWithDB(t)

	body := `{"name":"Bob","email":"bob@example.com","password":"strongpass1","timezone":"UTC"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/claim", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.Claim(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("got %d; want 409", rec.Code)
	}
}

func TestClaim_shortPassword(t *testing.T) {
	h := newEmptyHandler(t)

	body := `{"name":"Alice","email":"alice@example.com","password":"short","timezone":"UTC"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/claim", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.Claim(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d; want 400 for short password", rec.Code)
	}
}

func TestClaim_missingFields(t *testing.T) {
	h := newEmptyHandler(t)

	body := `{"name":"Alice"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/claim", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.Claim(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d; want 400", rec.Code)
	}
}
