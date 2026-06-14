package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// createQuestion creates a question via the API and returns its ID.
func createQuestion(t *testing.T, h interface {
	RequireAuth(http.HandlerFunc) http.HandlerFunc
	CreateQuestion(http.ResponseWriter, *http.Request)
}, slug, apiKey, body string) string {
	t.Helper()
	req := authReq(http.MethodPost, "/v1/event-types/"+slug+"/questions", body, apiKey)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateQuestion)(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("createQuestion: got %d — %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		ID string `json:"id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	return resp.ID
}

// ---------------------------------------------------------------------------
// ListQuestions — public endpoint
// ---------------------------------------------------------------------------

func TestListQuestions_emptyWhenNone(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	req := httptest.NewRequest(http.MethodGet, "/v1/event-types/"+slug+"/questions", nil)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.ListQuestions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("list questions: got %d — %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Items []any `json:"items"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Items) != 0 {
		t.Errorf("expected 0 items; got %d", len(resp.Items))
	}
}

func TestListQuestions_notFoundForUnknownSlug(t *testing.T) {
	h, _, _, _ := setupWorkspaceWithDB(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/event-types/no-such-slug/questions", nil)
	req.SetPathValue("slug", "no-such-slug")
	rec := httptest.NewRecorder()
	h.ListQuestions(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown slug: got %d; want 404", rec.Code)
	}
}

func TestListQuestions_orderedByPosition(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	// Create two questions with explicit positions out of order.
	createQuestion(t, h, slug, key, `{"label":"Second","type":"text","position":1}`)
	createQuestion(t, h, slug, key, `{"label":"First","type":"text","position":0}`)

	req := httptest.NewRequest(http.MethodGet, "/v1/event-types/"+slug+"/questions", nil)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.ListQuestions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("list: got %d — %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Items []struct {
			Label    string `json:"label"`
			Position int    `json:"position"`
		} `json:"items"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Items) != 2 {
		t.Fatalf("got %d items; want 2", len(resp.Items))
	}
	if resp.Items[0].Label != "First" {
		t.Errorf("first item label = %q; want First", resp.Items[0].Label)
	}
}

// ---------------------------------------------------------------------------
// CreateQuestion
// ---------------------------------------------------------------------------

func TestCreateQuestion_text(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	qID := createQuestion(t, h, slug, key, `{"label":"What's your goal?","type":"text","required":true}`)
	if qID == "" {
		t.Fatal("expected non-empty question ID")
	}
}

func TestCreateQuestion_select(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	body := `{"label":"Preferred time","type":"select","options":["morning","afternoon"],"required":false}`
	req := authReq(http.MethodPost, "/v1/event-types/"+slug+"/questions", body, key)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateQuestion)(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("create select: got %d — %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Type    string   `json:"type"`
		Options []string `json:"options"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Type != "select" {
		t.Errorf("type = %q; want select", resp.Type)
	}
	if len(resp.Options) != 2 {
		t.Errorf("options len = %d; want 2", len(resp.Options))
	}
}

func TestCreateQuestion_selectMissingOptions(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	req := authReq(http.MethodPost, "/v1/event-types/"+slug+"/questions",
		`{"label":"Pick one","type":"select"}`, key)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateQuestion)(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("select without options: got %d; want 400", rec.Code)
	}
}

func TestCreateQuestion_invalidType(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	req := authReq(http.MethodPost, "/v1/event-types/"+slug+"/questions",
		`{"label":"Q","type":"radio"}`, key)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateQuestion)(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid type: got %d; want 400", rec.Code)
	}
}

func TestCreateQuestion_missingLabel(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	req := authReq(http.MethodPost, "/v1/event-types/"+slug+"/questions",
		`{"type":"text"}`, key)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateQuestion)(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing label: got %d; want 400", rec.Code)
	}
}

func TestCreateQuestion_wrongEventType(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)

	req := authReq(http.MethodPost, "/v1/event-types/does-not-exist/questions",
		`{"label":"Q","type":"text"}`, key)
	req.SetPathValue("slug", "does-not-exist")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateQuestion)(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("wrong event type: got %d; want 404", rec.Code)
	}
}

func TestCreateQuestion_autoPosition(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	// Create three questions without specifying position.
	for i := 0; i < 3; i++ {
		createQuestion(t, h, slug, key, fmt.Sprintf(`{"label":"Q%d","type":"text"}`, i))
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/event-types/"+slug+"/questions", nil)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.ListQuestions(rec, req)

	var resp struct {
		Items []struct{ Position int `json:"position"` } `json:"items"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Items) != 3 {
		t.Fatalf("got %d items; want 3", len(resp.Items))
	}
	for i, item := range resp.Items {
		if item.Position != i {
			t.Errorf("item[%d].position = %d; want %d", i, item.Position, i)
		}
	}
}

// ---------------------------------------------------------------------------
// UpdateQuestion
// ---------------------------------------------------------------------------

func TestUpdateQuestion_label(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	qID := createQuestion(t, h, slug, key, `{"label":"Old label","type":"text"}`)

	req := authReq(http.MethodPatch, "/v1/event-types/"+slug+"/questions/"+qID,
		`{"label":"New label"}`, key)
	req.SetPathValue("slug", slug)
	req.SetPathValue("id", qID)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.UpdateQuestion)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("update: got %d — %s", rec.Code, rec.Body.String())
	}
	var resp struct{ Label string `json:"label"` }
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Label != "New label" {
		t.Errorf("label = %q; want 'New label'", resp.Label)
	}
}

func TestUpdateQuestion_notFound(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	req := authReq(http.MethodPatch, "/v1/event-types/"+slug+"/questions/nonexistent",
		`{"label":"X"}`, key)
	req.SetPathValue("slug", slug)
	req.SetPathValue("id", "nonexistent")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.UpdateQuestion)(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("not found: got %d; want 404", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// DeleteQuestion
// ---------------------------------------------------------------------------

func TestDeleteQuestion_success(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	qID := createQuestion(t, h, slug, key, `{"label":"Temp","type":"text"}`)

	req := authReq(http.MethodDelete, "/v1/event-types/"+slug+"/questions/"+qID, "", key)
	req.SetPathValue("slug", slug)
	req.SetPathValue("id", qID)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.DeleteQuestion)(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("delete: got %d; want 204", rec.Code)
	}

	// Verify it's gone.
	req2 := httptest.NewRequest(http.MethodGet, "/v1/event-types/"+slug+"/questions", nil)
	req2.SetPathValue("slug", slug)
	rec2 := httptest.NewRecorder()
	h.ListQuestions(rec2, req2)
	var resp struct {
		Items []any `json:"items"`
	}
	json.Unmarshal(rec2.Body.Bytes(), &resp)
	if len(resp.Items) != 0 {
		t.Errorf("after delete: got %d items; want 0", len(resp.Items))
	}
}

func TestDeleteQuestion_notFound(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	req := authReq(http.MethodDelete, "/v1/event-types/"+slug+"/questions/no-such-id", "", key)
	req.SetPathValue("slug", slug)
	req.SetPathValue("id", "no-such-id")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.DeleteQuestion)(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("delete missing: got %d; want 404", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// CreateBooking with answers
// ---------------------------------------------------------------------------

func TestCreateBooking_withRequiredAnswer(t *testing.T) {
	h, database, key, _ := setupWorkspaceWithDB(t)
	ctx := context.Background()
	slug, _ := seedEventTypeHTTP(t, h, key)
	qID := createQuestion(t, h, slug, key, `{"label":"Your goal","type":"text","required":true}`)

	// Booking without the required answer → 400.
	body := fmt.Sprintf(`{"event_type_slug":%q,"start_at":"2026-06-20T10:00:00Z","name":"Bob","email":"bob@example.com"}`, slug)
	req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateBooking(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing required answer: got %d; want 400", rec.Code)
	}

	// Booking with the required answer → 201.
	body2 := fmt.Sprintf(`{"event_type_slug":%q,"start_at":"2026-06-20T10:00:00Z","name":"Bob","email":"bob@example.com","answers":[{"question_id":%q,"value":"Improve productivity"}]}`, slug, qID)
	req2 := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	h.CreateBooking(rec2, req2)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("with answer: got %d — %s", rec2.Code, rec2.Body.String())
	}

	// Verify the answer was stored.
	var bookingID string
	json.Unmarshal(rec2.Body.Bytes(), &struct{ ID *string `json:"id"` }{&bookingID})
	var count int
	database.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM booking_answers WHERE booking_id = ?`, bookingID).Scan(&count)
	if count != 1 {
		t.Errorf("booking_answers count = %d; want 1", count)
	}
}

func TestCreateBooking_invalidSelectOption(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	qID := createQuestion(t, h, slug, key, `{"label":"Size","type":"select","options":["small","large"]}`)

	body := fmt.Sprintf(`{"event_type_slug":%q,"start_at":"2026-06-20T10:00:00Z","name":"Alice","email":"a@example.com","answers":[{"question_id":%q,"value":"medium"}]}`, slug, qID)
	req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateBooking(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid select option: got %d; want 400", rec.Code)
	}
}

func TestCreateBooking_unknownQuestionID(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	body := fmt.Sprintf(`{"event_type_slug":%q,"start_at":"2026-06-20T10:00:00Z","name":"X","email":"x@x.com","answers":[{"question_id":"fake-id","value":"hi"}]}`, slug)
	req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateBooking(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown question_id: got %d; want 400", rec.Code)
	}
}

func TestCreateBooking_validSelectOption(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	qID := createQuestion(t, h, slug, key, `{"label":"Size","type":"select","options":["small","large"]}`)

	body := fmt.Sprintf(`{"event_type_slug":%q,"start_at":"2026-06-20T10:00:00Z","name":"Alice","email":"a@example.com","answers":[{"question_id":%q,"value":"small"}]}`, slug, qID)
	req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateBooking(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("valid select: got %d — %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// GetBookingAnswers
// ---------------------------------------------------------------------------

func TestGetBookingAnswers_success(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	qID := createQuestion(t, h, slug, key, `{"label":"Notes","type":"text"}`)

	body := fmt.Sprintf(`{"event_type_slug":%q,"start_at":"2026-06-20T10:00:00Z","name":"Charlie","email":"c@c.com","answers":[{"question_id":%q,"value":"Some notes"}]}`, slug, qID)
	req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateBooking(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create booking: %d — %s", rec.Code, rec.Body.String())
	}
	var bResp struct{ ID string `json:"id"` }
	json.Unmarshal(rec.Body.Bytes(), &bResp)

	// Retrieve answers.
	req2 := authReq(http.MethodGet, "/v1/bookings/"+bResp.ID+"/answers", "", key)
	req2.SetPathValue("id", bResp.ID)
	rec2 := httptest.NewRecorder()
	h.RequireAuth(h.GetBookingAnswers)(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("get answers: %d — %s", rec2.Code, rec2.Body.String())
	}

	var resp struct {
		Items []struct {
			QuestionID string `json:"question_id"`
			Label      string `json:"label"`
			Value      string `json:"value"`
		} `json:"items"`
	}
	json.Unmarshal(rec2.Body.Bytes(), &resp)
	if len(resp.Items) != 1 {
		t.Fatalf("got %d answer items; want 1", len(resp.Items))
	}
	if resp.Items[0].Value != "Some notes" {
		t.Errorf("value = %q; want 'Some notes'", resp.Items[0].Value)
	}
	if resp.Items[0].Label != "Notes" {
		t.Errorf("label = %q; want 'Notes'", resp.Items[0].Label)
	}
}

func TestGetBookingAnswers_requiresAuth(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)
	bookingID := createBookingViaHTTP(t, h, slug, "2026-06-20T10:00:00Z")

	req := httptest.NewRequest(http.MethodGet, "/v1/bookings/"+bookingID+"/answers", nil)
	req.SetPathValue("id", bookingID)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.GetBookingAnswers)(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no auth: got %d; want 401", rec.Code)
	}
}

func TestGetBookingAnswers_notFound(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)

	req := authReq(http.MethodGet, "/v1/bookings/nonexistent/answers", "", key)
	req.SetPathValue("id", "nonexistent")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.GetBookingAnswers)(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("not found: got %d; want 404", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Regression: ListQuestions must work even when the event type is inactive
// ---------------------------------------------------------------------------

func TestListQuestions_returns404ForInactiveEventType(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	createQuestion(t, h, slug, key, `{"label":"Name","type":"text"}`)

	// Deactivate the event type.
	patchReq := authReq(http.MethodPatch, "/v1/event-types/"+slug, `{"is_active":false}`, key)
	patchReq.SetPathValue("slug", slug)
	patchRec := httptest.NewRecorder()
	h.RequireAuth(h.PatchEventType)(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("deactivate event type: got %d — %s", patchRec.Code, patchRec.Body.String())
	}

	// Public ListQuestions must return 404 for inactive types.
	req := httptest.NewRequest(http.MethodGet, "/v1/event-types/"+slug+"/questions", nil)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.ListQuestions(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("public list questions on inactive type: got %d; want 404", rec.Code)
	}
}

func TestListQuestionsAdmin_worksOnInactiveEventType(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	createQuestion(t, h, slug, key, `{"label":"Name","type":"text"}`)

	// Deactivate the event type.
	patchReq := authReq(http.MethodPatch, "/v1/event-types/"+slug, `{"is_active":false}`, key)
	patchReq.SetPathValue("slug", slug)
	patchRec := httptest.NewRecorder()
	h.RequireAuth(h.PatchEventType)(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("deactivate event type: got %d — %s", patchRec.Code, patchRec.Body.String())
	}

	// Admin endpoint must return the question regardless of is_active.
	req := authReq(http.MethodGet, "/v1/event-types/"+slug+"/questions/admin", "", key)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ListQuestionsAdmin)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("admin list questions on inactive type: got %d — %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Items []struct {
			Label string `json:"label"`
		} `json:"items"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Items) != 1 {
		t.Fatalf("got %d items; want 1", len(resp.Items))
	}
	if resp.Items[0].Label != "Name" {
		t.Errorf("label = %q; want 'Name'", resp.Items[0].Label)
	}
}
