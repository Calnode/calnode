package handler_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// scriptedLLM returns the given chat-completion JSON bodies in sequence (last one repeats).
func scriptedLLM(t *testing.T, bodies ...string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	n := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		i := n
		n++
		mu.Unlock()
		if i >= len(bodies) {
			i = len(bodies) - 1
		}
		io.WriteString(w, bodies[i])
	}))
}

func TestBookingAssistant_findThenBook(t *testing.T) {
	// The model: (1) calls find_available_slots, (2) calls book, (3) confirms in text.
	mock := scriptedLLM(t,
		`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"find_available_slots","arguments":"{}"}}]}}]}`,
		`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"c2","type":"function","function":{"name":"book","arguments":"{\"slot_start\":\"2027-05-03T10:00:00Z\",\"name\":\"Pat\",\"email\":\"pat@example.com\"}"}}]}}]}`,
		`{"choices":[{"message":{"role":"assistant","content":"<think>I booked it; now confirm briefly.</think>All set — booked for May 3 at 10:00. See you then!"}}]}`,
	)
	defer mock.Close()

	h, apiKey, _ := setupWorkspace(t)

	// Event type (active + public by default).
	erec := httptest.NewRecorder()
	h.RequireAuth(h.CreateEventType)(erec, authReq(http.MethodPost, "/v1/event-types",
		`{"slug":"ai-call","name":"AI Call","duration_minutes":30,"location_type":"phone","location_value":"+1 555 000 1111","max_future_days":0}`, apiKey))
	if erec.Code != http.StatusCreated {
		t.Fatalf("create event type: %d — %s", erec.Code, erec.Body.String())
	}
	seedFullAvailability(t, h, apiKey)

	// Turn on the LLM, pointed at the scripted mock.
	prec := httptest.NewRecorder()
	h.RequireAuth(h.PatchLLMSettings)(prec, authReq(http.MethodPatch, "/v1/settings/llm",
		`{"enabled":true,"endpoint":"`+mock.URL+`","model":"m","api_key":"k"}`, apiKey))
	if prec.Code != http.StatusOK {
		t.Fatalf("enable llm: %d — %s", prec.Code, prec.Body.String())
	}

	// Chat the assistant.
	areq := httptest.NewRequest(http.MethodPost, "/v1/event-types/ai-call/assistant",
		strings.NewReader(`{"messages":[{"role":"user","content":"book me a call"}],"timezone":"UTC"}`))
	areq.SetPathValue("slug", "ai-call")
	arec := httptest.NewRecorder()
	h.BookingAssistant(arec, areq)
	if arec.Code != http.StatusOK {
		t.Fatalf("assistant: %d — %s", arec.Code, arec.Body.String())
	}

	var resp struct {
		Reply   string `json:"reply"`
		Booking *struct {
			ID, StartAt, Status string
		} `json:"booking"`
		Fallback bool `json:"fallback"`
	}
	if err := json.Unmarshal(arec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, arec.Body.String())
	}
	if resp.Booking == nil || resp.Booking.Status != "confirmed" || resp.Booking.ID == "" {
		t.Fatalf("expected a confirmed booking; got %+v (reply %q)", resp.Booking, resp.Reply)
	}
	if !strings.Contains(resp.Reply, "booked for May 3") {
		t.Errorf("reply = %q; want the final confirmation", resp.Reply)
	}
	if strings.Contains(resp.Reply, "<think>") || strings.Contains(resp.Reply, "now confirm briefly") {
		t.Errorf("reply leaked model reasoning: %q", resp.Reply)
	}
}

func TestBookingAssistant_fallbackWhenLLMOff(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)
	erec := httptest.NewRecorder()
	h.RequireAuth(h.CreateEventType)(erec, authReq(http.MethodPost, "/v1/event-types",
		`{"slug":"no-ai","name":"No AI","duration_minutes":30,"location_type":"phone","location_value":"+1 555 000 1111"}`, apiKey))
	if erec.Code != http.StatusCreated {
		t.Fatalf("create event type: %d — %s", erec.Code, erec.Body.String())
	}

	// LLM never enabled → assistant tells the client to fall back to the picker.
	areq := httptest.NewRequest(http.MethodPost, "/v1/event-types/no-ai/assistant",
		strings.NewReader(`{"messages":[{"role":"user","content":"hi"}],"timezone":"UTC"}`))
	areq.SetPathValue("slug", "no-ai")
	arec := httptest.NewRecorder()
	h.BookingAssistant(arec, areq)
	if !strings.Contains(arec.Body.String(), `"fallback":true`) {
		t.Errorf("expected fallback:true when LLM is off; got %s", arec.Body.String())
	}
}
