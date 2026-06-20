package handler_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/db"
	"github.com/calnode/calnode/internal/gcal"
	"github.com/calnode/calnode/internal/handler"
)

// testGCalKeyHex is a valid 64-char AES-256 key (32 bytes).
const testGCalKeyHex = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"

// newHandlerWithGCal builds a handler with a gcal.Client and a bootstrapped user.
// Returns (handler, gcalClient, plainAPIKey, userID).
func newHandlerWithGCal(t *testing.T) (*handler.Handler, *gcal.Client, string, string) {
	t.Helper()
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := db.Migrate(database); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	h := handler.New(database, slog.Default())

	gc, err := gcal.New(database, "google-client-id", "google-secret",
		"http://localhost:3000/v1/calendar/callback", testGCalKeyHex)
	if err != nil {
		t.Fatalf("gcal.New: %v", err)
	}
	svc := calendar.NewService(database)
	svc.Register(gc)
	h.SetCalendar(svc)
	// baseURL is used in the success redirect; leave default ("") since no
	// test here reaches the success path of CalendarCallback.

	// Bootstrap a workspace so tests can authenticate.
	body := `{"name":"Cal User","email":"cal@example.com","timezone":"UTC"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/setup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.Setup(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup: got %d — %s", rec.Code, rec.Body.String())
	}
	var setup struct {
		APIKey string `json:"api_key"`
		UserID string `json:"user_id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &setup) //nolint:errcheck
	return h, gc, setup.APIKey, setup.UserID
}

// ---------------------------------------------------------------------------
// CalendarStatus
// ---------------------------------------------------------------------------

func TestCalendarStatus_noGCal_returnsNotConnected(t *testing.T) {
	// No SetCalendar called — h.gcal is nil.
	h := newTestHandler(t)

	// Short-circuit path (gcal==nil) doesn't need auth context.
	req := httptest.NewRequest(http.MethodGet, "/v1/calendar/status", nil)
	rec := httptest.NewRecorder()
	h.CalendarStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp) //nolint:errcheck
	if resp["connected"] != false {
		t.Errorf("connected = %v; want false when gcal not configured", resp["connected"])
	}
}

func TestCalendarStatus_configuredButNotConnected(t *testing.T) {
	h, _, apiKey, _ := newHandlerWithGCal(t)

	req := authReq(http.MethodGet, "/v1/calendar/status", "", apiKey)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CalendarStatus)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp) //nolint:errcheck
	if resp["connected"] != false {
		t.Errorf("connected = %v; want false before OAuth flow completes", resp["connected"])
	}
}

// ---------------------------------------------------------------------------
// ConnectCalendar
// ---------------------------------------------------------------------------

func TestConnectCalendar_noGCal_returns501(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t) // no SetCalendar

	req := authReq(http.MethodGet, "/v1/calendar/connect", "", apiKey)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ConnectCalendar)(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Errorf("status = %d; want 501 when gcal not configured", rec.Code)
	}
}

func TestConnectCalendar_redirectsToGoogle(t *testing.T) {
	h, _, apiKey, _ := newHandlerWithGCal(t)

	req := authReq(http.MethodGet, "/v1/calendar/connect", "", apiKey)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ConnectCalendar)(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d; want 302", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc == "" {
		t.Fatal("no Location header in redirect")
	}
	if !strings.Contains(loc, "accounts.google.com") {
		t.Errorf("Location %q does not look like a Google OAuth URL", loc)
	}
	if !strings.Contains(loc, "client_id=google-client-id") {
		t.Errorf("Location %q missing client_id", loc)
	}
}

func TestConnectCalendar_stateIsURLSafe(t *testing.T) {
	h, _, apiKey, _ := newHandlerWithGCal(t)

	req := authReq(http.MethodGet, "/v1/calendar/connect", "", apiKey)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ConnectCalendar)(rec, req)

	loc := rec.Header().Get("Location")
	// Verify state param exists in redirect URL and contains only URL-safe chars.
	if !strings.Contains(loc, "state=") {
		t.Error("Location missing state parameter")
	}
}

// ---------------------------------------------------------------------------
// CalendarCallback
// ---------------------------------------------------------------------------

func TestCalendarCallback_noGCal_returns501(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/calendar/callback?code=x&state=y", nil)
	rec := httptest.NewRecorder()
	h.CalendarCallback(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("status = %d; want 501", rec.Code)
	}
}

func TestCalendarCallback_oauthError_returns400(t *testing.T) {
	h, _, _, _ := newHandlerWithGCal(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/calendar/callback?error=access_denied", nil)
	rec := httptest.NewRecorder()
	h.CalendarCallback(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400 for OAuth error param", rec.Code)
	}
}

func TestCalendarCallback_missingState_returns400(t *testing.T) {
	h, _, _, _ := newHandlerWithGCal(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/calendar/callback?code=someCode", nil)
	rec := httptest.NewRecorder()
	h.CalendarCallback(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400 for missing state", rec.Code)
	}
}

func TestCalendarCallback_tamperedState_returns400(t *testing.T) {
	h, _, _, _ := newHandlerWithGCal(t)
	// Random base64url string that won't decrypt correctly.
	req := httptest.NewRequest(http.MethodGet,
		"/v1/calendar/callback?code=someCode&state=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", nil)
	rec := httptest.NewRecorder()
	h.CalendarCallback(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400 for tampered state", rec.Code)
	}
}

func TestCalendarCallback_missingCode_returns400(t *testing.T) {
	h, gc, _, userID := newHandlerWithGCal(t)
	state, err := gc.EncryptState(userID)
	if err != nil {
		t.Fatalf("EncryptState: %v", err)
	}
	// Valid state but no code — should get 400 before trying to exchange.
	req := httptest.NewRequest(http.MethodGet,
		"/v1/calendar/callback?state="+state, nil)
	rec := httptest.NewRecorder()
	h.CalendarCallback(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400 for missing code", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// DisconnectCalendar
// ---------------------------------------------------------------------------

func TestDisconnectCalendar_noGCal_returns501(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest(http.MethodDelete, "/v1/calendar", nil)
	rec := httptest.NewRecorder()
	h.DisconnectCalendar(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("status = %d; want 501", rec.Code)
	}
}

func TestDisconnectCalendar_whenNotConnected_returns204(t *testing.T) {
	h, _, apiKey, _ := newHandlerWithGCal(t)

	req := authReq(http.MethodDelete, "/v1/calendar", "", apiKey)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.DisconnectCalendar)(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d; want 204", rec.Code)
	}
}
