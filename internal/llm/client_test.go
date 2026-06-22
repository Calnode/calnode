package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChat_sendsRequestAndParsesReply(t *testing.T) {
	var gotPath, gotAuth, gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		json.Unmarshal(body, &req)
		gotModel, _ = req["model"].(string)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"hi there"}}]}`)
	}))
	defer srv.Close()

	c := New(Config{Endpoint: srv.URL + "/v1", Model: "test-model", APIKey: "sk-abc"})
	res, err := c.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if res.Message.Content != "hi there" {
		t.Errorf("content = %q; want %q", res.Message.Content, "hi there")
	}
	if gotPath != "/v1/chat/completions" {
		t.Errorf("path = %q; want /v1/chat/completions", gotPath)
	}
	if gotAuth != "Bearer sk-abc" {
		t.Errorf("auth = %q; want Bearer sk-abc", gotAuth)
	}
	if gotModel != "test-model" {
		t.Errorf("model = %q; want test-model", gotModel)
	}
}

func TestChat_parsesToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","tool_calls":[
			{"id":"call_1","type":"function","function":{"name":"get_available_slots","arguments":"{\"event_type_id\":\"intro\"}"}}]}}]}`)
	}))
	defer srv.Close()

	c := New(Config{Endpoint: srv.URL, Model: "m"})
	res, err := c.Chat(context.Background(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "book me"}},
		Tools:    []Tool{{Type: "function", Function: ToolFunction{Name: "get_available_slots"}}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if len(res.Message.ToolCalls) != 1 || res.Message.ToolCalls[0].Function.Name != "get_available_slots" {
		t.Fatalf("tool calls = %+v; want one get_available_slots call", res.Message.ToolCalls)
	}
	if !strings.Contains(res.Message.ToolCalls[0].Function.Arguments, "intro") {
		t.Errorf("tool args = %q; want intro", res.Message.ToolCalls[0].Function.Arguments)
	}
}

func TestChat_surfacesEndpointError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"message":"bad key"}}`)
	}))
	defer srv.Close()

	c := New(Config{Endpoint: srv.URL, Model: "m", APIKey: "nope"})
	if err := c.Ping(context.Background()); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("Ping err = %v; want a 401 error", err)
	}
}
