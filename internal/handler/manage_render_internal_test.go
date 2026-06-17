package handler

import (
	"bytes"
	"strings"
	"testing"
)

// The manage template injects JS constants from server data. Using
// {{printf "%q" .X}} inside an html/template <script> double-quotes the value
// (the value gets JS-escaped again), producing SLUG=`"20-min-call"` with literal
// quotes — which broke the reschedule slots fetch (404) and the "Invalid Date".
// Guard that the constants are single-layer quoted.
func TestManageTemplate_jsConstantsNotDoubleQuoted(t *testing.T) {
	var buf bytes.Buffer
	data := managePageData{
		Token:           "tok123",
		EventTypeSlug:   "20-min-call",
		EventTypeName:   "20-minute call",
		CurrentStartISO: "2026-06-18T10:00:00Z",
		OrganizerTZ:     "Pacific/Auckland",
		Status:          "confirmed",
		MaxFutureDays:   60,
		DurationMinutes: 20,
	}
	if err := manageTmpl.Execute(&buf, data); err != nil {
		t.Fatalf("render manage template: %v", err)
	}
	out := buf.String()

	if strings.Contains(out, `\"20-min-call\"`) || strings.Contains(out, `\"2026-06-18`) {
		t.Error("JS constant is double-quoted (literal quotes in the value)")
	}
	if !strings.Contains(out, `"20-min-call"`) {
		t.Error("SLUG not emitted as a clean quoted JS string")
	}
	if !strings.Contains(out, `"2026-06-18T10:00:00Z"`) {
		t.Error("CURRENT_ISO not emitted as a clean quoted JS string")
	}
}
