package handler_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/calnode/calnode/internal/uid"
)

func TestInvitationDurationPolicyCreateAndPatch(t *testing.T) {
	h, _, key, _ := setupWorkspaceWithDB(t)
	for _, fields := range []string{
		`"min_duration_minutes":15`,
		`"min_duration_minutes":15,"max_duration_minutes":60,"duration_increment_minutes":0`,
		`"min_duration_minutes":60,"max_duration_minutes":15,"duration_increment_minutes":15`,
		`"min_duration_minutes":15,"max_duration_minutes":60,"duration_increment_minutes":20`,
	} {
		body := fmt.Sprintf(`{"slug":%q,"name":"Range","duration_minutes":30,%s}`, "range-"+uid.New(), fields)
		rec := httptest.NewRecorder()
		h.RequireAuth(h.CreateEventType)(rec, authReq(http.MethodPost, "/v1/event-types", body, key))
		mustStatus(t, rec, http.StatusBadRequest, "invalid duration policy")
	}
	created := createEventType(t, h, key, `{"slug":"range-valid","name":"Range","duration_minutes":30,"min_duration_minutes":15,"max_duration_minutes":120,"duration_increment_minutes":15}`)
	if toInt(created["min_duration_minutes"]) != 15 || toInt(created["max_duration_minutes"]) != 120 {
		t.Fatal("create did not preserve policy")
	}
	mustStatus(t, patchInvitationEvent(t, h, "range-valid", key, `{"max_duration_minutes":180}`), http.StatusOK, "partial policy update")
	mustStatus(t, patchInvitationEvent(t, h, "range-valid", key, `{"duration_minutes":31}`), http.StatusBadRequest, "default outside increment")
	mustStatus(t, patchInvitationEvent(t, h, "range-valid", key, `{"max_duration_minutes":15}`), http.StatusBadRequest, "default outside range")
	slug, _ := seedEventTypeHTTP(t, h, key)
	mustStatus(t, patchInvitationEvent(t, h, slug, key, `{"duration_minutes":45}`), http.StatusOK, "legacy default change")
	mustStatus(t, patchInvitationEvent(t, h, slug, key, `{"min_duration_minutes":15}`), http.StatusBadRequest, "incomplete range")
	mustStatus(t, patchInvitationEvent(t, h, "range-valid", key, `{"min_duration_minutes":null}`), http.StatusBadRequest, "partial reset")
	mustStatus(t, patchInvitationEvent(t, h, "range-valid", key, `{"min_duration_minutes":null,"max_duration_minutes":null,"duration_increment_minutes":null}`), http.StatusOK, "reset range")
	mustStatus(t, patchInvitationEvent(t, h, "range-valid", key, `{"duration_minutes":31}`), http.StatusOK, "reset is fixed")
	mustStatus(t, patchInvitationEvent(t, h, slug, key, `{"min_duration_minutes":45,"max_duration_minutes":45,"duration_increment_minutes":1}`), http.StatusOK, "explicit fixed bounds")
	mustStatus(t, patchInvitationEvent(t, h, slug, key, `{"duration_minutes":60}`), http.StatusOK, "fixed bounds follow legacy edit")
}
