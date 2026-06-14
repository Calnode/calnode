package handler_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/db"
	"github.com/calnode/calnode/internal/handler"
)

// newTestHandlerDB creates a test handler and returns both the handler and the
// underlying DB so tests can interact with the DB directly (e.g. to issue tokens).
func newTestHandlerDB(t *testing.T) (*handler.Handler, *sql.DB) {
	t.Helper()
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := db.Migrate(database); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return handler.New(database, slog.Default()), database
}

// setupWorkspaceWithDB bootstraps a workspace and returns (handler, db, apiKey, userID).
func setupWorkspaceWithDB(t *testing.T) (*handler.Handler, *sql.DB, string, string) {
	t.Helper()
	h, database := newTestHandlerDB(t)

	body := `{"name":"Test Host","email":"host@example.com","timezone":"UTC"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/setup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.Setup(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup: got %d — %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		APIKey string `json:"api_key"`
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("setup: decode: %v", err)
	}
	return h, database, resp.APIKey, resp.UserID
}

// createBookingViaHTTP creates a booking via the HTTP handler and returns its ID.
func createBookingViaHTTP(t *testing.T, h *handler.Handler, slug, startAt string) string {
	t.Helper()
	body := fmt.Sprintf(`{
		"event_type_slug": %q,
		"start_at": %q,
		"name": "Test Attendee",
		"email": "attendee@example.com",
		"timezone": "UTC"
	}`, slug, startAt)
	req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateBooking(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("createBooking: got %d — %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		ID string `json:"id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	return resp.ID
}

// issueTestToken issues a manage token for bookingID via the booking service.
func issueTestToken(t *testing.T, database *sql.DB, bookingID string) string {
	t.Helper()
	svc := booking.New(database)
	tok, err := svc.IssueManageToken(context.Background(), bookingID)
	if err != nil {
		t.Fatalf("IssueManageToken: %v", err)
	}
	return tok
}

// ---------------------------------------------------------------------------
// ManagePage
// ---------------------------------------------------------------------------

func TestManagePage_invalidToken(t *testing.T) {
	h, _, _, _ := setupWorkspaceWithDB(t)

	req := httptest.NewRequest(http.MethodGet, "/manage/badtoken", nil)
	req.SetPathValue("token", "badtoken")
	rec := httptest.NewRecorder()
	h.ManagePage(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d; want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "expired or invalid") {
		t.Error("body should contain 'expired or invalid' for invalid token")
	}
}

func TestManagePage_validToken(t *testing.T) {
	h, database, apiKey, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, apiKey)

	bookingID := createBookingViaHTTP(t, h, slug, "2026-06-20T09:00:00Z")
	tok := issueTestToken(t, database, bookingID)

	req := httptest.NewRequest(http.MethodGet, "/manage/"+tok, nil)
	req.SetPathValue("token", tok)
	rec := httptest.NewRecorder()
	h.ManagePage(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 — body: %s", rec.Code, rec.Body.String())
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q; want text/html", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Test Meeting") {
		t.Error("body should contain event type name 'Test Meeting'")
	}
	if !strings.Contains(body, "Reschedule") {
		t.Error("body should contain 'Reschedule' action")
	}
	if strings.Contains(body, "expired or invalid") {
		t.Error("body should not show 'expired or invalid' for valid token")
	}
}

func TestManagePage_cancelledBooking(t *testing.T) {
	h, database, apiKey, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, apiKey)

	bookingID := createBookingViaHTTP(t, h, slug, "2026-06-20T09:00:00Z")

	// Cancel the booking directly via the booking service.
	svc := booking.New(database)
	var hostID string
	database.QueryRowContext(context.Background(),
		`SELECT host_id FROM bookings WHERE id = ?`, bookingID).Scan(&hostID)
	if err := svc.Cancel(context.Background(), hostID, bookingID, "test cancel"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	tok := issueTestToken(t, database, bookingID)

	req := httptest.NewRequest(http.MethodGet, "/manage/"+tok, nil)
	req.SetPathValue("token", tok)
	rec := httptest.NewRecorder()
	h.ManagePage(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "already cancelled") || !strings.Contains(body, "cancelled") {
		t.Error("body should indicate booking is cancelled")
	}
}

// ---------------------------------------------------------------------------
// RescheduleByToken
// ---------------------------------------------------------------------------

func TestRescheduleByToken_success(t *testing.T) {
	h, database, apiKey, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, apiKey)

	bookingID := createBookingViaHTTP(t, h, slug, "2026-06-20T09:00:00Z")
	tok := issueTestToken(t, database, bookingID)

	body := `{"start_at":"2026-06-20T10:00:00Z"}`
	req := httptest.NewRequest(http.MethodPost, "/manage/"+tok+"/reschedule", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("token", tok)
	rec := httptest.NewRecorder()
	h.RescheduleByToken(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 — body: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		StartAt string `json:"start_at"`
		Status  string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !strings.HasPrefix(resp.StartAt, "2026-06-20T10:00:00") {
		t.Errorf("start_at = %q; want 2026-06-20T10:00:00...", resp.StartAt)
	}
	if resp.Status != "confirmed" {
		t.Errorf("status = %q; want confirmed", resp.Status)
	}
}

func TestRescheduleByToken_invalidToken(t *testing.T) {
	h, _, _, _ := setupWorkspaceWithDB(t)

	body := `{"start_at":"2026-06-20T10:00:00Z"}`
	req := httptest.NewRequest(http.MethodPost, "/manage/badtoken/reschedule", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("token", "badtoken")
	rec := httptest.NewRecorder()
	h.RescheduleByToken(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d; want 404 for invalid token", rec.Code)
	}
}

func TestRescheduleByToken_missingStartAt(t *testing.T) {
	h, database, apiKey, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, apiKey)

	bookingID := createBookingViaHTTP(t, h, slug, "2026-06-20T09:00:00Z")
	tok := issueTestToken(t, database, bookingID)

	req := httptest.NewRequest(http.MethodPost, "/manage/"+tok+"/reschedule", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("token", tok)
	rec := httptest.NewRecorder()
	h.RescheduleByToken(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400 when start_at missing", rec.Code)
	}
}

func TestRescheduleByToken_invalidStartAtFormat(t *testing.T) {
	h, database, apiKey, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, apiKey)

	bookingID := createBookingViaHTTP(t, h, slug, "2026-06-20T09:00:00Z")
	tok := issueTestToken(t, database, bookingID)

	body := `{"start_at":"not-a-date"}`
	req := httptest.NewRequest(http.MethodPost, "/manage/"+tok+"/reschedule", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("token", tok)
	rec := httptest.NewRecorder()
	h.RescheduleByToken(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400 for invalid date format", rec.Code)
	}
}

func TestRescheduleByToken_conflict(t *testing.T) {
	h, database, apiKey, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, apiKey)

	// Create booking A and booking B at different times.
	bookingAID := createBookingViaHTTP(t, h, slug, "2026-06-20T09:00:00Z")
	createBookingViaHTTP(t, h, slug, "2026-06-20T11:00:00Z") // B occupies 11:00-11:30

	tok := issueTestToken(t, database, bookingAID)

	// Try to reschedule A to 11:00 — conflict with B.
	body := `{"start_at":"2026-06-20T11:00:00Z"}`
	req := httptest.NewRequest(http.MethodPost, "/manage/"+tok+"/reschedule", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("token", tok)
	rec := httptest.NewRecorder()
	h.RescheduleByToken(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d; want 409 for conflicting slot", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// CancelByToken
// ---------------------------------------------------------------------------

func TestCancelByToken_success(t *testing.T) {
	h, database, apiKey, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, apiKey)

	bookingID := createBookingViaHTTP(t, h, slug, "2026-06-20T09:00:00Z")
	tok := issueTestToken(t, database, bookingID)

	body := `{"reason":"changed plans"}`
	req := httptest.NewRequest(http.MethodPost, "/manage/"+tok+"/cancel", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("token", tok)
	rec := httptest.NewRecorder()
	h.CancelByToken(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 — body: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Status             string `json:"status"`
		CancellationReason string `json:"cancellation_reason"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != "cancelled" {
		t.Errorf("status = %q; want cancelled", resp.Status)
	}
	if resp.CancellationReason != "changed plans" {
		t.Errorf("cancellation_reason = %q; want %q", resp.CancellationReason, "changed plans")
	}
}

func TestCancelByToken_noReason(t *testing.T) {
	h, database, apiKey, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, apiKey)

	bookingID := createBookingViaHTTP(t, h, slug, "2026-06-20T09:00:00Z")
	tok := issueTestToken(t, database, bookingID)

	req := httptest.NewRequest(http.MethodPost, "/manage/"+tok+"/cancel", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("token", tok)
	rec := httptest.NewRecorder()
	h.CancelByToken(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 — body: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Status string `json:"status"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Status != "cancelled" {
		t.Errorf("status = %q; want cancelled", resp.Status)
	}
}

func TestCancelByToken_invalidToken(t *testing.T) {
	h, _, _, _ := setupWorkspaceWithDB(t)

	req := httptest.NewRequest(http.MethodPost, "/manage/badtoken/cancel", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("token", "badtoken")
	rec := httptest.NewRecorder()
	h.CancelByToken(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d; want 404 for invalid token", rec.Code)
	}
}

func TestCancelByToken_alreadyCancelled(t *testing.T) {
	h, database, apiKey, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, apiKey)

	bookingID := createBookingViaHTTP(t, h, slug, "2026-06-20T09:00:00Z")
	tok := issueTestToken(t, database, bookingID)

	// First cancel.
	svc := booking.New(database)
	var hostID string
	database.QueryRowContext(context.Background(),
		`SELECT host_id FROM bookings WHERE id = ?`, bookingID).Scan(&hostID)
	svc.Cancel(context.Background(), hostID, bookingID, "first") //nolint:errcheck

	// Second cancel via token should return 409.
	req := httptest.NewRequest(http.MethodPost, "/manage/"+tok+"/cancel", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("token", tok)
	rec := httptest.NewRecorder()
	h.CancelByToken(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d; want 409 for already-cancelled booking", rec.Code)
	}
}
