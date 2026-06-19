package webhook

import "testing"

func TestBuildData_defaultSetMatchesOriginalShape(t *testing.T) {
	bd := enrichedBooking{
		core: BookingPayload{
			ID: "b1", EventTypeSlug: "call", HostID: "h1",
			StartAt: "2026-06-15T09:00:00Z", EndAt: "2026-06-15T09:30:00Z",
			Status: "confirmed", CreatedAt: "2026-06-14T00:00:00Z",
			// optional fields empty → omitted
		},
		// enrichment present but must NOT leak via the default set
		attendeeEmail: "bob@example.com",
		hostEmail:     "host@example.com",
		answers:       []map[string]string{{"question": "Topic", "answer": "Demo"}},
	}
	d := buildData(bd, defaultFields)
	for _, k := range []string{"id", "event_type_slug", "host_id", "start_at", "end_at", "status", "created_at"} {
		if _, ok := d[k]; !ok {
			t.Errorf("default payload missing %q", k)
		}
	}
	for _, k := range []string{"location_value", "cancellation_reason", "previous_start_at", "previous_end_at"} {
		if _, ok := d[k]; ok {
			t.Errorf("default payload should omit empty optional %q", k)
		}
	}
	for _, k := range []string{"attendee_email", "attendee_name", "answers", "host_email", "event_type_name"} {
		if _, ok := d[k]; ok {
			t.Errorf("default payload must not include %q (backwards-compat)", k)
		}
	}
}

func TestBuildData_includesOnlySelected(t *testing.T) {
	bd := enrichedBooking{
		core:          BookingPayload{ID: "b1", Status: "confirmed"},
		attendeeEmail: "bob@example.com",
		answers:       []map[string]string{{"question": "Topic", "answer": "Demo"}},
	}
	d := buildData(bd, []string{FieldAttendeeEmail, FieldAnswers})
	if len(d) != 2 {
		t.Fatalf("expected exactly 2 fields, got %d: %v", len(d), d)
	}
	if d[FieldAttendeeEmail] != "bob@example.com" {
		t.Errorf("attendee_email = %v", d[FieldAttendeeEmail])
	}
	if _, ok := d[FieldID]; ok {
		t.Error("id must not be present when not selected")
	}
	ans, ok := d[FieldAnswers].([]map[string]string)
	if !ok || len(ans) != 1 || ans[0]["answer"] != "Demo" {
		t.Errorf("answers not carried correctly: %v", d[FieldAnswers])
	}
}

func TestBuildData_omitsEmptyOptionalEvenWhenSelected(t *testing.T) {
	bd := enrichedBooking{core: BookingPayload{ID: "b1"}}
	d := buildData(bd, []string{FieldID, FieldCancelReason, FieldLocation})
	if _, ok := d[FieldCancelReason]; ok {
		t.Error("empty cancellation_reason should be omitted")
	}
	if _, ok := d[FieldID]; !ok {
		t.Error("id should be present")
	}
}

func TestValidFields_dropsUnknown(t *testing.T) {
	got := ValidFields([]string{"id", "bogus", "attendee_email", ""})
	if len(got) != 2 || got[0] != "id" || got[1] != "attendee_email" {
		t.Errorf("ValidFields = %v; want [id attendee_email]", got)
	}
}
