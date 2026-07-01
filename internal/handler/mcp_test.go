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

func TestMCP_getEventType_exposesQuestions(t *testing.T) {
	h, database, apiKey, _ := setupWorkspaceWithDB(t)
	body := `{"slug":"mcp-q","name":"Q Call","duration_minutes":30,"location_type":"phone","location_value":"+1 555 000 1111"}`
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateEventType)(rec, authReq(http.MethodPost, "/v1/event-types", body, apiKey))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create event type: %d — %s", rec.Code, rec.Body.String())
	}
	var etID string
	if err := database.QueryRow(`SELECT id FROM event_types WHERE slug = 'mcp-q'`).Scan(&etID); err != nil {
		t.Fatalf("event type id: %v", err)
	}
	if _, err := database.Exec(`
		INSERT INTO event_type_questions (id, event_type_id, label, type, required, position)
		VALUES (?, ?, ?, ?, 1, 0)`, "q1", etID, "What's the agenda?", "text"); err != nil {
		t.Fatalf("insert question: %v", err)
	}

	cs := connectMCP(t, h)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "get_event_type", Arguments: map[string]any{"event_type_id": "mcp-q"}})
	if err != nil {
		t.Fatalf("get_event_type: %v", err)
	}
	if res.IsError {
		t.Fatalf("get_event_type errored: %+v", res.Content)
	}
	blob, _ := json.Marshal(res.StructuredContent)
	if s := string(blob); !strings.Contains(s, "What's the agenda?") || !strings.Contains(s, `"required":true`) {
		t.Errorf("get_event_type didn't expose the required question: %s", s)
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
	for _, want := range []string{"list_event_types", "get_event_type", "get_available_slots", "get_booking", "list_bookings"} {
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

// bookingResult is the subset of the booking JSON the mutation tools return that the
// test inspects (bookingJSON itself is unexported in package handler).
type bookingResult struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	StartAt string `json:"start_at"`
}

func decodeBooking(t *testing.T, res *mcp.CallToolResult) bookingResult {
	t.Helper()
	blob, _ := json.Marshal(res.StructuredContent)
	var b bookingResult
	if err := json.Unmarshal(blob, &b); err != nil {
		t.Fatalf("decode booking result: %v (%s)", err, blob)
	}
	return b
}

func TestMCP_createRescheduleCancel(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)
	body := `{"slug":"mcp-call","name":"MCP Call","duration_minutes":30,"location_type":"phone","location_value":"+1 555 000 1111","max_future_days":0}`
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateEventType)(rec, authReq(http.MethodPost, "/v1/event-types", body, apiKey))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create event type: %d — %s", rec.Code, rec.Body.String())
	}
	seedFullAvailability(t, h, apiKey)

	cs := connectMCP(t, h)
	ctx := context.Background()

	// create_booking
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "create_booking", Arguments: map[string]any{
		"event_type_id":  "mcp-call",
		"slot_start":     "2027-01-15T10:00:00Z",
		"attendee_name":  "Pat Booker",
		"attendee_email": "pat@example.com",
	}})
	if err != nil {
		t.Fatalf("create_booking: %v", err)
	}
	if res.IsError {
		t.Fatalf("create_booking errored: %+v", res.Content)
	}
	created := decodeBooking(t, res)
	if created.ID == "" || created.Status != "confirmed" {
		t.Fatalf("create_booking returned %+v; want a confirmed booking with an id", created)
	}

	// reschedule_booking
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "reschedule_booking", Arguments: map[string]any{
		"booking_id":     created.ID,
		"new_slot_start": "2027-01-16T11:00:00Z",
	}})
	if err != nil {
		t.Fatalf("reschedule_booking: %v", err)
	}
	if res.IsError {
		t.Fatalf("reschedule_booking errored: %+v", res.Content)
	}
	moved := decodeBooking(t, res)
	if !strings.HasPrefix(moved.StartAt, "2027-01-16T11:00") {
		t.Errorf("reschedule_booking start = %q; want the new 2027-01-16T11:00 time", moved.StartAt)
	}

	// cancel_booking
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "cancel_booking", Arguments: map[string]any{
		"booking_id": created.ID,
		"reason":     "testing",
	}})
	if err != nil {
		t.Fatalf("cancel_booking: %v", err)
	}
	if res.IsError {
		t.Fatalf("cancel_booking errored: %+v", res.Content)
	}
	if cancelled := decodeBooking(t, res); cancelled.Status != "cancelled" {
		t.Errorf("cancel_booking status = %q; want cancelled", cancelled.Status)
	}
}
