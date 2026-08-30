package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/calnode/calnode/internal/handler"
	"github.com/calnode/calnode/internal/uid"
)

func itoa(n int) string { return strconv.Itoa(n) }

// JSON numbers decode as float64.
func toInt(v any) int {
	if f, ok := v.(float64); ok {
		return int(f)
	}
	return -1
}

// createEventType posts one event type and returns the decoded response.
func createEventType(t *testing.T, h *handler.Handler, apiKey, body string) map[string]any {
	t.Helper()
	req := authReq(http.MethodPost, "/v1/event-types", body, apiKey)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateEventType)(rec, req)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("create event type: %d - %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

// TestCreateEventType_slotIntervalDefaultsToDuration covers issue #13. Slot interval and
// duration are deliberately separate - interval is how often a booking may START, duration
// is how long it runs, and a 45-minute meeting offered on the hour is a legitimate setup.
// But the old default was a fixed 30 regardless, so a 15-minute event offered slots every
// 30 minutes (wasting half the host's day) and a 90-minute one offered starts it could
// mostly not honour. From the booker's side that looked like duration being ignored.
func TestCreateEventType_slotIntervalDefaultsToDuration(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)

	for _, dur := range []int{15, 45, 90} {
		got := createEventType(t, h, apiKey, `{
			"slug": "`+uid.New()[:8]+`",
			"name": "Test",
			"duration_minutes": `+itoa(dur)+`
		}`)
		if iv := toInt(got["slot_interval_minutes"]); iv != dur {
			t.Errorf("duration %d: slot_interval_minutes = %d, want it to match the duration", dur, iv)
		}
	}
}

// An explicit interval must still win - the two being independent is the point, and
// defaulting must not turn into forcing.
func TestCreateEventType_explicitSlotIntervalIsKept(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)

	got := createEventType(t, h, apiKey, `{
		"slug": "`+uid.New()[:8]+`",
		"name": "Hourly starts",
		"duration_minutes": 45,
		"slot_interval_minutes": 60
	}`)
	if iv := toInt(got["slot_interval_minutes"]); iv != 60 {
		t.Errorf("slot_interval_minutes = %d, want the explicit 60 to survive", iv)
	}
	if d := toInt(got["duration_minutes"]); d != 45 {
		t.Errorf("duration_minutes = %d, want 45 - the two must stay independent", d)
	}
}

// slots.Generate rejects a non-positive interval, so an unvalidated 0 would leave the event
// type with no bookable times at all and nothing explaining why.
func TestEventType_rejectsNonPositiveSlotInterval(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)

	t.Run("on create", func(t *testing.T) {
		req := authReq(http.MethodPost, "/v1/event-types", `{
			"slug": "`+uid.New()[:8]+`", "name": "Bad", "duration_minutes": 30,
			"slot_interval_minutes": 0
		}`, apiKey)
		rec := httptest.NewRecorder()
		h.RequireAuth(h.CreateEventType)(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400 - a zero interval produces no slots at all", rec.Code)
		}
	})

	t.Run("on patch", func(t *testing.T) {
		slug, _ := seedEventTypeHTTP(t, h, apiKey)
		req := authReq(http.MethodPatch, "/v1/event-types/"+slug, `{"slot_interval_minutes": 0}`, apiKey)
		req.SetPathValue("slug", slug)
		rec := httptest.NewRecorder()
		h.RequireAuth(h.PatchEventType)(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})
}
