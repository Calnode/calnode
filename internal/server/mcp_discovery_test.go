package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// discoverReq mirrors what a SEP-2575-aware client sends: the version and method
// also travel as headers so the transport can route the request before parsing.
func discoverReq(t *testing.T, protocolVersion string) *http.Request {
	t.Helper()
	body := `{
		"jsonrpc": "2.0",
		"id": "discovery-diagnostic",
		"method": "server/discover",
		"params": {
			"_meta": {
				"io.modelcontextprotocol/protocolVersion": "` + protocolVersion + `",
				"io.modelcontextprotocol/clientInfo": {"name": "test", "version": "1"},
				"io.modelcontextprotocol/clientCapabilities": {}
			}
		}
	}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Protocol-Version", protocolVersion)
	req.Header.Set("Mcp-Method", "server/discover")
	return req
}

// discoverResult pulls the JSON-RPC payload out of a Streamable HTTP response,
// which may arrive as SSE ("data: {...}") or as plain JSON.
func discoverResult(t *testing.T, body string) map[string]any {
	t.Helper()
	payload := body
	for _, line := range strings.Split(body, "\n") {
		if after, ok := strings.CutPrefix(strings.TrimSpace(line), "data:"); ok {
			payload = strings.TrimSpace(after)
			break
		}
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(payload), &out); err != nil {
		t.Fatalf("decode discovery response: %v (body: %q)", err, body)
	}
	return out
}

// The transport must be stateless so the go-sdk advertises 2026-07-28. On a
// stateful transport it filters the modern version out and ChatGPT's connector
// discovery fails without ever falling back (#170).
func TestMCPDiscoverAdvertisesProtocol20260728(t *testing.T) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "calnode", Version: "test"}, nil)
	h := newMCPStreamableHandler(srv)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, discoverReq(t, "2026-07-28"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	res := discoverResult(t, rec.Body.String())
	result, ok := res["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result in discovery response: %s", rec.Body.String())
	}
	versions, ok := result["supportedVersions"].([]any)
	if !ok {
		t.Fatalf("no supportedVersions in discovery response: %s", rec.Body.String())
	}

	var got []string
	for _, v := range versions {
		if s, ok := v.(string); ok {
			got = append(got, s)
		}
	}
	if !contains(got, "2026-07-28") {
		t.Errorf("supportedVersions = %v; want it to include 2026-07-28", got)
	}
	// The older versions must remain: clients that cannot do 2026-07-28 negotiate
	// down to these, and that path has to keep working.
	for _, want := range []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"} {
		if !contains(got, want) {
			t.Errorf("supportedVersions = %v; missing %s", got, want)
		}
	}
}

// GET opens the standalone notification stream on a stateful transport. Stateless
// has no session to stream, so it must answer 405 rather than hang.
func TestMCPStatelessRejectsNotificationStream(t *testing.T) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "calnode", Version: "test"}, nil)
	h := newMCPStreamableHandler(srv)

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /mcp status = %d; want 405 in stateless mode", rec.Code)
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// Stateless must remain transparent to ordinary MCP clients: they connect,
// initialize (negotiating a version below 2026-07-28) and call tools over HTTP.
// This is the path MCP Inspector, MiniMax and other SDK clients take, and it has
// to keep working after #170's transport change.
func TestMCPStatelessServesNormalClientSession(t *testing.T) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "calnode", Version: "test"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "ping", Description: "health check"},
		func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, struct{}, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "pong"}},
			}, struct{}{}, nil
		})

	ts := httptest.NewServer(newMCPStreamableHandler(srv))
	defer ts.Close()

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	cs, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	tools, err := cs.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools.Tools) != 1 || tools.Tools[0].Name != "ping" {
		t.Fatalf("tools = %+v; want the ping tool", tools.Tools)
	}

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "ping"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %+v", res.Content)
	}
}
