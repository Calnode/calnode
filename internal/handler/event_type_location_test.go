package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/calnode/calnode/internal/handler"
	"github.com/calnode/calnode/internal/uid"
)

// patchET sends one PATCH and returns the status plus decoded body.
func patchET(t *testing.T, h *handler.Handler, apiKey, slug, body string) (int, map[string]any) {
	t.Helper()
	req := authReq(http.MethodPatch, "/v1/event-types/"+slug, body, apiKey)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.PatchEventType)(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// TestCreateEventType_defaultedLocationIsValidForTheOwner pins the invariant that makes
// the defaulting safe: whatever smartDefaultLocation returns must be a location this
// owner can actually be booked at.
//
// The create path skips validateLocation entirely when it defaults the location - there
// is no request field to blame an error on - so nothing else checks the answer. It used
// to end at an unconditional "zoom", so on an instance with no Zoom account every event
// type created without a location was born unable to mint a join link: the booking
// succeeds and the attendee is told "Zoom" with no URL.
//
// Tested by feeding the default back through the create path EXPLICITLY, which does
// validate. That keeps it independent of the PATCH change below - an earlier version of
// this test went through PATCH and passed either way, because the PATCH fix alone
// satisfied it.
func TestCreateEventType_defaultedLocationIsValidForTheOwner(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)

	// No calendar and no Zoom are connected in this workspace, which is the case that
	// used to break.
	created := createEventType(t, h, apiKey, `{
		"slug": "defaulted-`+uid.New()[:8]+`", "name": "No location given", "duration_minutes": 30
	}`)
	locType, _ := created["location_type"].(string)
	if locType == "" {
		t.Fatalf("no location_type on the created event type: %v", created)
	}

	req := authReq(http.MethodPost, "/v1/event-types", `{
		"slug": "explicit-`+uid.New()[:8]+`", "name": "Same location, stated", "duration_minutes": 30,
		"location_type": "`+locType+`"
	}`, apiKey)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.CreateEventType)(rec, req)
	if rec.Code != http.StatusCreated {
		t.Errorf("smartDefaultLocation returned %q, which validateLocation rejects for this "+
			"owner (%d - %s). A defaulted location is written unvalidated, so every branch "+
			"of smartDefaultLocation has to be one this owner can actually host at.",
			locType, rec.Code, rec.Body.String())
	}
}

// TestPatchEventType_unrelatedFieldOnAnInvalidLocation is the other half of the bug, and
// the half that matters for rows already in that state.
//
// The broken state is written DIRECTLY here, because after the create fix above there is
// no longer an API path into it - which is the point, and is also why an earlier version
// of this test could not fail: it tried to reach the state through the API, was correctly
// refused, and then asserted against a perfectly valid row.
//
// Rows in this state are real: anything created before smartDefaultLocation was fixed,
// the demo seeder (which inserts straight into the table the same way), a provider
// disconnected since, or a duplicate that inherited it (#22).
//
// The editor submits the whole form, so validation used to run whenever the request
// MENTIONED the location, which is always. That locked the operator out of every other
// field, reporting an error about a meeting URL they had not gone near. Validation now
// runs only when the effective location differs from what is stored.
func TestPatchEventType_unrelatedFieldOnAnInvalidLocation(t *testing.T) {
	h, database, apiKey, _ := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, apiKey)

	// 'link' with no URL: valid to store, rejected by validateLocation.
	if _, err := database.Exec(
		`UPDATE event_types SET location_type = 'link', location_value = NULL WHERE slug = ?`,
		slug); err != nil {
		t.Fatalf("put the row in the legacy state: %v", err)
	}

	// What the editor sends: the whole form, location included, nothing about it changed.
	// location_value is null because the editor sends `.trim() || null`.
	code, body := patchET(t, h, apiKey, slug, `{
		"name": "Renamed", "duration_minutes": 30,
		"location_type": "link", "location_value": null
	}`)
	if code != http.StatusOK {
		t.Fatalf("editing the name on an event type whose stored location is invalid "+
			"(%d - %v): the operator is locked out of every field by a location they "+
			"did not touch.", code, body)
	}
	if got := body["name"]; got != "Renamed" {
		t.Errorf("name = %v, want Renamed", got)
	}
}

// Changing the location still validates. Without this the fix above would be a hole
// rather than a narrowing: nothing may write a NEW invalid location.
func TestPatchEventType_changingToAnInvalidLocationIsStillRejected(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, apiKey)

	if code, _ := patchET(t, h, apiKey, slug, `{"location_type": "link", "location_value": "not-a-url"}`); code != http.StatusBadRequest {
		t.Errorf("link with a junk URL: %d; want 400", code)
	}
	if code, _ := patchET(t, h, apiKey, slug, `{"location_type": "phone", "location_value": ""}`); code != http.StatusBadRequest {
		t.Errorf("phone with no number: %d; want 400", code)
	}
}
