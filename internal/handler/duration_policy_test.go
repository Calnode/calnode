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

func TestDurationPolicyPatchPersistsEffectiveValues(t *testing.T) {
	for _, tc := range []struct {
		name, policy, patch string
		status, duration    int
		min, max, increment any
	}{
		{"unchanged fixed duration", `"min_duration_minutes":30,"max_duration_minutes":30,"duration_increment_minutes":1`, `{"duration_minutes":30}`, 200, 30, 30, 30, 1},
		{"changed fixed duration", `"min_duration_minutes":30,"max_duration_minutes":30,"duration_increment_minutes":1`, `{"duration_minutes":45}`, 200, 45, nil, nil, nil},
		{"omitted duration", `"min_duration_minutes":30,"max_duration_minutes":30,"duration_increment_minutes":1`, `{"name":"Renamed"}`, 200, 30, 30, 30, 1},
		{"unchanged range duration", `"min_duration_minutes":15,"max_duration_minutes":90,"duration_increment_minutes":15`, `{"duration_minutes":30}`, 200, 30, 15, 90, 15},
		{"changed range duration", `"min_duration_minutes":15,"max_duration_minutes":90,"duration_increment_minutes":15`, `{"duration_minutes":45}`, 200, 45, 15, 90, 15},
		{"invalid range duration", `"min_duration_minutes":15,"max_duration_minutes":90,"duration_increment_minutes":15`, `{"duration_minutes":31}`, 400, 30, 15, 90, 15},
		{"explicit reset", `"min_duration_minutes":30,"max_duration_minutes":30,"duration_increment_minutes":1`, `{"min_duration_minutes":null,"max_duration_minutes":null,"duration_increment_minutes":null}`, 200, 30, nil, nil, nil},
		{"explicit policy update", `"min_duration_minutes":30,"max_duration_minutes":30,"duration_increment_minutes":1`, `{"duration_minutes":45,"min_duration_minutes":45,"max_duration_minutes":45}`, 200, 45, 45, 45, 1},
		{"explicit unchanged bounds reject changed duration", `"min_duration_minutes":30,"max_duration_minutes":30,"duration_increment_minutes":1`, `{"duration_minutes":45,"min_duration_minutes":30}`, 400, 30, 30, 30, 1},
		{"partial reset rejected", `"min_duration_minutes":15,"max_duration_minutes":90,"duration_increment_minutes":15`, `{"min_duration_minutes":null}`, 400, 30, 15, 90, 15},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, database, key, _ := setupWorkspaceWithDB(t)
			createEventType(t, h, key, `{"slug":"policy","name":"Policy","duration_minutes":30,`+tc.policy+`}`)
			mustStatus(t, patchInvitationEvent(t, h, "policy", key, tc.patch), tc.status, "patch policy")
			var duration int
			var min, max, increment *int
			if err := database.QueryRow(`SELECT duration_minutes,min_duration_minutes,max_duration_minutes,duration_increment_minutes FROM event_types WHERE slug='policy'`).Scan(&duration, &min, &max, &increment); err != nil {
				t.Fatal(err)
			}
			if duration != tc.duration {
				t.Fatalf("persisted duration=%d, want %d", duration, tc.duration)
			}
			for _, field := range []struct {
				name string
				got  *int
				want any
			}{{"minimum", min, tc.min}, {"maximum", max, tc.max}, {"increment", increment, tc.increment}} {
				if field.want == nil {
					if field.got != nil {
						t.Errorf("%s=%d, want NULL", field.name, *field.got)
					}
				} else if field.got == nil || *field.got != field.want.(int) {
					t.Errorf("%s=%v, want %v", field.name, field.got, field.want)
				}
			}
		})
	}
}
