package handler_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLLMSettings_configureEnableAndTest(t *testing.T) {
	// Mock OpenAI-compatible endpoint.
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer mock.Close()

	h, apiKey, _ := setupWorkspace(t)

	// Configure + enable.
	body := `{"enabled":true,"endpoint":"` + mock.URL + `","model":"test-model","api_key":"sk-secret"}`
	rec := httptest.NewRecorder()
	h.RequireAuth(h.PatchLLMSettings)(rec, authReq(http.MethodPatch, "/v1/settings/llm", body, apiKey))
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d — %s", rec.Code, rec.Body.String())
	}
	s := rec.Body.String()
	if !strings.Contains(s, `"active":true`) || !strings.Contains(s, `"api_key_set":true`) || !strings.Contains(s, `"enabled":true`) {
		t.Errorf("after patch expected active+api_key_set+enabled: %s", s)
	}
	// The secret must never be echoed back.
	if strings.Contains(s, "sk-secret") {
		t.Errorf("settings response leaked the api key: %s", s)
	}

	// Test-connection against the mock (omits the key → reuses the stored one).
	trec := httptest.NewRecorder()
	h.RequireAuth(h.TestLLMSettings)(trec, authReq(http.MethodPost, "/v1/settings/llm/test",
		`{"endpoint":"`+mock.URL+`","model":"test-model"}`, apiKey))
	if trec.Code != http.StatusOK || !strings.Contains(trec.Body.String(), `"ok":true`) {
		t.Errorf("test connection = %d %s; want ok:true", trec.Code, trec.Body.String())
	}

	// Disable → no longer active.
	drec := httptest.NewRecorder()
	h.RequireAuth(h.PatchLLMSettings)(drec, authReq(http.MethodPatch, "/v1/settings/llm", `{"enabled":false}`, apiKey))
	if !strings.Contains(drec.Body.String(), `"active":false`) {
		t.Errorf("after disable expected active:false: %s", drec.Body.String())
	}
}
