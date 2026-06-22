package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// connectMCP runs the handler's MCP server over an in-memory transport and returns
// a connected client session for calling tools.
func connectMCP(t *testing.T, h interface{ MCPServer() *mcp.Server }) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	srvT, cliT := mcp.NewInMemoryTransports()
	go func() { _ = h.MCPServer().Run(ctx, srvT) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	cs, err := client.Connect(ctx, cliT, nil)
	if err != nil {
		t.Fatalf("MCP connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func TestMCP_listEventTypes(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)

	// Two event types; the second is then deactivated so list_event_types must skip it.
	for _, body := range []string{
		`{"slug":"mcp-intro","name":"MCP Intro","duration_minutes":30,"location_type":"phone","location_value":"+1 555 000 1111"}`,
		`{"slug":"mcp-hidden","name":"Hidden One","duration_minutes":15,"location_type":"phone","location_value":"+1 555 222 3333"}`,
	} {
		rec := httptest.NewRecorder()
		h.RequireAuth(h.CreateEventType)(rec, authReq(http.MethodPost, "/v1/event-types", body, apiKey))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create event type: %d — %s", rec.Code, rec.Body.String())
		}
	}
	// Deactivate the second one.
	preq := authReq(http.MethodPatch, "/v1/event-types/mcp-hidden", `{"is_active":false}`, apiKey)
	preq.SetPathValue("slug", "mcp-hidden")
	prec := httptest.NewRecorder()
	h.RequireAuth(h.PatchEventType)(prec, preq)
	if prec.Code != http.StatusOK {
		t.Fatalf("deactivate event type: %d — %s", prec.Code, prec.Body.String())
	}

	cs := connectMCP(t, h)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_event_types"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %+v", res.Content)
	}
	blob, _ := json.Marshal(res)
	s := string(blob)
	if !strings.Contains(s, "mcp-intro") || !strings.Contains(s, "MCP Intro") {
		t.Errorf("list_event_types missing the active event type: %s", s)
	}
	if strings.Contains(s, "mcp-hidden") {
		t.Errorf("list_event_types leaked an inactive event type: %s", s)
	}
}

func TestMCP_readTools_registeredAndBehave(t *testing.T) {
	h, _, _ := setupWorkspace(t)
	cs := connectMCP(t, h)
	ctx := context.Background()

	// All read tools advertised.
	lt, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	have := map[string]bool{}
	for _, tool := range lt.Tools {
		have[tool.Name] = true
	}
	for _, want := range []string{"list_event_types", "get_available_slots", "get_booking", "list_bookings"} {
		if !have[want] {
			t.Errorf("tool %q not registered (have %v)", want, have)
		}
	}

	// list_bookings on an empty workspace returns an empty list, not an error.
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "list_bookings"})
	if err != nil {
		t.Fatalf("list_bookings: %v", err)
	}
	if res.IsError {
		t.Errorf("list_bookings errored on empty workspace: %+v", res.Content)
	}

	// get_booking for a missing id surfaces a tool error.
	res2, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "get_booking", Arguments: map[string]any{"booking_id": "nope"}})
	if err == nil && !res2.IsError {
		t.Errorf("get_booking on a missing id should error; got %+v", res2.Content)
	}
}
