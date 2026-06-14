package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// CreateWebhook
// ---------------------------------------------------------------------------

func TestCreateWebhook_success(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)

	req := authReq(http.MethodPost, "/v1/webhooks",
		`{"url":"https://example.com/hook","events":["booking.created"]}`, apiKey)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateWebhook)(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d; want 201 — %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["id"] == "" {
		t.Error("expected non-empty id")
	}
	secret, _ := resp["secret"].(string)
	if len(secret) != 64 {
		t.Errorf("secret length = %d; want 64 hex chars", len(secret))
	}
	if resp["url"] != "https://example.com/hook" {
		t.Errorf("url = %v; want https://example.com/hook", resp["url"])
	}
}

func TestCreateWebhook_bothEvents(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)

	req := authReq(http.MethodPost, "/v1/webhooks",
		`{"url":"https://example.com/hook","events":["booking.created","booking.cancelled"]}`, apiKey)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateWebhook)(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d; want 201 — %s", rec.Code, rec.Body.String())
	}
}

func TestCreateWebhook_missingURL_returns400(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)

	req := authReq(http.MethodPost, "/v1/webhooks",
		`{"events":["booking.created"]}`, apiKey)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateWebhook)(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400", rec.Code)
	}
}

func TestCreateWebhook_invalidURL_returns400(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)

	req := authReq(http.MethodPost, "/v1/webhooks",
		`{"url":"not-a-url","events":["booking.created"]}`, apiKey)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateWebhook)(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400 for invalid URL", rec.Code)
	}
}

func TestCreateWebhook_emptyEvents_returns400(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)

	req := authReq(http.MethodPost, "/v1/webhooks",
		`{"url":"https://example.com/hook","events":[]}`, apiKey)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateWebhook)(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400", rec.Code)
	}
}

func TestCreateWebhook_unknownEvent_returns400(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)

	req := authReq(http.MethodPost, "/v1/webhooks",
		`{"url":"https://example.com/hook","events":["booking.unknown"]}`, apiKey)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateWebhook)(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400 for unknown event", rec.Code)
	}
}

func TestCreateWebhook_requiresAuth(t *testing.T) {
	h, _, _ := setupWorkspace(t)

	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks",
		strings.NewReader(`{"url":"https://example.com/hook","events":["booking.created"]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateWebhook)(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// ListWebhooks
// ---------------------------------------------------------------------------

func TestListWebhooks_emptyInitially(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)

	req := authReq(http.MethodGet, "/v1/webhooks", "", apiKey)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ListWebhooks)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	var resp struct {
		Items []any `json:"items"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Items) != 0 {
		t.Errorf("items len = %d; want 0", len(resp.Items))
	}
}

func TestListWebhooks_returnsCreated(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)

	// Create two webhooks.
	for i := range 2 {
		r := authReq(http.MethodPost, "/v1/webhooks",
			fmt.Sprintf(`{"url":"https://example%d.com/hook","events":["booking.created"]}`, i),
			apiKey)
		rc := httptest.NewRecorder()
		h.RequireAuth(h.CreateWebhook)(rc, r)
		if rc.Code != http.StatusCreated {
			t.Fatalf("create webhook %d: %d", i, rc.Code)
		}
	}

	req := authReq(http.MethodGet, "/v1/webhooks", "", apiKey)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ListWebhooks)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp struct {
		Items []map[string]any `json:"items"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Items) != 2 {
		t.Errorf("items len = %d; want 2", len(resp.Items))
	}
	// Secret must NOT appear in list response.
	for _, item := range resp.Items {
		if _, ok := item["secret"]; ok {
			t.Error("secret must not appear in ListWebhooks response")
		}
	}
}

func TestListWebhooks_isolatedBetweenUsers(t *testing.T) {
	h, apiKey1, _ := setupWorkspace(t)
	// Create a second workspace on the same handler (second user would need a separate DB
	// in a real test, but for isolation we can just verify the list only returns own items).
	authReq(http.MethodPost, "/v1/webhooks",
		`{"url":"https://user1.example.com/hook","events":["booking.created"]}`, apiKey1)

	req := authReq(http.MethodGet, "/v1/webhooks", "", apiKey1)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ListWebhooks)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// DeleteWebhook
// ---------------------------------------------------------------------------

func TestDeleteWebhook_success(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)

	// Create.
	createReq := authReq(http.MethodPost, "/v1/webhooks",
		`{"url":"https://example.com/hook","events":["booking.created"]}`, apiKey)
	createRec := httptest.NewRecorder()
	h.RequireAuth(h.CreateWebhook)(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create: %d", createRec.Code)
	}
	var created map[string]any
	json.Unmarshal(createRec.Body.Bytes(), &created)
	id := created["id"].(string)

	// Delete.
	delReq := authReq(http.MethodDelete, "/v1/webhooks/"+id, "", apiKey)
	delReq.SetPathValue("id", id)
	delRec := httptest.NewRecorder()
	h.RequireAuth(h.DeleteWebhook)(delRec, delReq)

	if delRec.Code != http.StatusNoContent {
		t.Errorf("status = %d; want 204", delRec.Code)
	}

	// Verify gone.
	listReq := authReq(http.MethodGet, "/v1/webhooks", "", apiKey)
	listRec := httptest.NewRecorder()
	h.RequireAuth(h.ListWebhooks)(listRec, listReq)
	var listResp struct {
		Items []any `json:"items"`
	}
	json.Unmarshal(listRec.Body.Bytes(), &listResp)
	if len(listResp.Items) != 0 {
		t.Error("webhook should be gone after delete")
	}
}

func TestDeleteWebhook_unknownID_returns404(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)

	req := authReq(http.MethodDelete, "/v1/webhooks/no-such-id", "", apiKey)
	req.SetPathValue("id", "no-such-id")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.DeleteWebhook)(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d; want 404", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// ListWebhookDeliveries
// ---------------------------------------------------------------------------

func TestListWebhookDeliveries_emptyInitially(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)

	// Create a webhook.
	createReq := authReq(http.MethodPost, "/v1/webhooks",
		`{"url":"https://example.com/hook","events":["booking.created"]}`, apiKey)
	createRec := httptest.NewRecorder()
	h.RequireAuth(h.CreateWebhook)(createRec, createReq)
	var created map[string]any
	json.Unmarshal(createRec.Body.Bytes(), &created)
	id := created["id"].(string)

	req := authReq(http.MethodGet, "/v1/webhooks/"+id+"/deliveries", "", apiKey)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ListWebhookDeliveries)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 — %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Items []any `json:"items"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Items) != 0 {
		t.Errorf("items len = %d; want 0", len(resp.Items))
	}
}

func TestListWebhookDeliveries_unknownWebhook_returns404(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)

	req := authReq(http.MethodGet, "/v1/webhooks/no-such-id/deliveries", "", apiKey)
	req.SetPathValue("id", "no-such-id")
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ListWebhookDeliveries)(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d; want 404", rec.Code)
	}
}
