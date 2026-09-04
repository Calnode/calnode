package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/calnode/calnode/internal/uid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type slotsBody struct {
	Slots []struct {
		Start   string   `json:"start"`
		HostIDs []string `json:"host_ids"`
	} `json:"slots"`
	// Taken is a pointer so "absent" (not opted in) stays distinguishable from
	// "present but empty" (opted in, nothing booked). That distinction is the
	// difference between a page that greys slots and one that does not.
	Taken *[]struct {
		Start   string   `json:"start"`
		HostIDs []string `json:"host_ids"`
	} `json:"taken"`
	// MinNotice is a pointer for the same reason: absent means the event type sets no
	// minimum notice, present-but-empty means it does and this range lost nothing to it
	// (see slots_notice_test.go).
	MinNotice *struct {
		Minutes int      `json:"minutes"`
		Dates   []string `json:"dates"`
	} `json:"min_notice"`
}

func getSlots(t *testing.T, h interface {
	GetSlots(http.ResponseWriter, *http.Request)
}, slug, query string) slotsBody {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/event-types/"+slug+"/slots"+query, nil)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.GetSlots(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET slots: %d - %s", rec.Code, rec.Body.String())
	}
	var out slotsBody
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
	return out
}

// setShowTaken flips the per-event-type opt-in through the admin API, the way the
// editor does.
func setShowTaken(t *testing.T, h interface {
	RequireAuth(http.HandlerFunc) http.HandlerFunc
	PatchEventType(http.ResponseWriter, *http.Request)
}, apiKey, slug string, on bool) {
	t.Helper()
	body := `{"show_taken_slots": false}`
	if on {
		body = `{"show_taken_slots": true}`
	}
	req := authReq(http.MethodPatch, "/v1/event-types/"+slug, body, apiKey)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.PatchEventType)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch show_taken_slots: %d - %s", rec.Code, rec.Body.String())
	}
}

// TestSlots_takenAbsentUntilOptedIn is the guard on the decision that made this
// feature shippable at all. The slots endpoint is public and unauthenticated, so
// showing which hours are booked has to be something a host chooses per event type,
// never a default anyone inherits by upgrading.
func TestSlots_takenAbsentUntilOptedIn(t *testing.T) {
	h, _, apiKey, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, apiKey)

	if got := getSlots(t, h, slug, ""); got.Taken != nil {
		t.Errorf("taken was present by default (%d entries); it must be absent until "+
			"the event type opts in, or upgrading would start disclosing booked hours",
			len(*got.Taken))
	}

	setShowTaken(t, h, apiKey, slug, true)
	if got := getSlots(t, h, slug, ""); got.Taken == nil {
		t.Error("taken still absent after opting in")
	}

	// And it goes away again when switched back off.
	setShowTaken(t, h, apiKey, slug, false)
	if got := getSlots(t, h, slug, ""); got.Taken != nil {
		t.Error("taken survived the opt-in being turned off")
	}
}

// New event types must not have it on. Belt and braces with the schema default: the
// create path builds its own INSERT and could set it independently.
func TestCreateEventType_doesNotShowTakenByDefault(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)
	got := createEventType(t, h, apiKey, `{
		"slug": "taken-`+uid.New()[:8]+`", "name": "Default off", "duration_minutes": 30
	}`)
	if v, ok := got["show_taken_slots"].(bool); !ok || v {
		t.Errorf("show_taken_slots = %v on a new event type, want false", got["show_taken_slots"])
	}
}

// TestMCPGetAvailableSlots_neverReturnsTakenSlots is the invariant that matters most
// here, and it is asserted at the surface rather than trusted from the call site.
//
// The MCP tool and the booking assistant exist to hand an agent times it can book. A
// taken slot mixed into that list is one the agent will eventually offer, and nothing
// in the payload distinguishes it. So even with the event type opted in - the page IS
// greying slots - the agent-facing tool must still see only bookable times.
func TestMCPGetAvailableSlots_neverReturnsTakenSlots(t *testing.T) {
	h, _, apiKey, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, apiKey)
	setShowTaken(t, h, apiKey, slug, true)

	res, err := connectMCP(t, h).CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_available_slots",
		Arguments: map[string]any{"event_type_id": slug},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if _, present := payload["taken"]; present {
		t.Errorf("get_available_slots exposed a taken field: %s\n"+
			"An agent cannot tell those apart from bookable times and will offer one", raw)
	}
}
