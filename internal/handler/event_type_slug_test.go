package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calnode/calnode/internal/handler"
)

// patchSlug sends a slug rename through the admin API and returns the status and body.
func patchSlug(t *testing.T, h *handler.Handler, apiKey, slug, newSlug string) (int, map[string]any) {
	t.Helper()
	req := authReq(http.MethodPatch, "/v1/event-types/"+slug,
		fmt.Sprintf(`{"slug": %q}`, newSlug), apiKey)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.PatchEventType)(rec, req)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

// bookOnce creates one booking against slug, which is what closes the rename window.
func bookOnce(t *testing.T, h *handler.Handler, slug string) {
	t.Helper()
	body := fmt.Sprintf(
		`{"event_type_slug":%q,"start_at":"2026-06-15T10:00:00Z","name":"Bob","email":"bob@example.com"}`, slug)
	req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateBooking(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed booking: %d - %s", rec.Code, rec.Body.String())
	}
}

// TestPatchEventType_renamesWhileUnbooked is the case this exists for: a duplicate lands
// as "<slug>-copy" and there was previously no way to give it a real name (#22).
func TestPatchEventType_renamesWhileUnbooked(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, apiKey)

	code, body := patchSlug(t, h, apiKey, slug, "intro-call-v2")
	if code != http.StatusOK {
		t.Fatalf("rename: %d - %v", code, body)
	}
	if got := body["slug"]; got != "intro-call-v2" {
		t.Errorf("slug = %v, want intro-call-v2", got)
	}

	// The response must describe the row as it now is. The handler re-reads after the
	// UPDATE, and re-reading under the name the request arrived on would 404 a patch
	// that actually succeeded.
	if got := body["name"]; got == nil || got == "" {
		t.Errorf("response lost the rest of the event type after a rename: %v", body)
	}

	// And the new slug is the one that resolves from here on.
	if code, _ := patchSlug(t, h, apiKey, "intro-call-v2", "intro-call-v3"); code != http.StatusOK {
		t.Errorf("second rename through the new slug: %d; want 200", code)
	}
}

// A booking means the URL demonstrably reached someone, so the rename is refused rather
// than silently breaking their manage link.
func TestPatchEventType_refusesRenameOnceBooked(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, apiKey)
	bookOnce(t, h, slug)

	code, body := patchSlug(t, h, apiKey, slug, "too-late")
	if code != http.StatusConflict {
		t.Fatalf("rename after booking: %d; want 409 - %v", code, body)
	}
	// The old slug must still be the live one; a refused rename that half-applied would
	// be worse than either outcome.
	req := httptest.NewRequest(http.MethodGet, "/v1/event-types/"+slug+"/slots", nil)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.GetSlots(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("original slug stopped resolving after a refused rename: %d", rec.Code)
	}
}

// Patching other fields on a booked event type must keep working; only the slug is fenced.
func TestPatchEventType_bookedEventTypeStillEditable(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, apiKey)
	bookOnce(t, h, slug)

	req := authReq(http.MethodPatch, "/v1/event-types/"+slug, `{"name": "Renamed, not re-slugged"}`, apiKey)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.RequireAuth(h.PatchEventType)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch name on a booked event type: %d - %s", rec.Code, rec.Body.String())
	}
}

// Sending the slug it already has is a no-op, not a 409. The editor submits every field,
// so a booked event type would otherwise become uneditable from the UI entirely.
func TestPatchEventType_unchangedSlugOnBookedEventTypeIsFine(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, apiKey)
	bookOnce(t, h, slug)

	code, body := patchSlug(t, h, apiKey, slug, slug)
	if code != http.StatusOK {
		t.Fatalf("resubmitting the same slug: %d; want 200 - %v", code, body)
	}
}

func TestPatchEventType_renameRejectsCollisionAndEmpty(t *testing.T) {
	h, apiKey, _ := setupWorkspace(t)
	slugA, _ := seedEventTypeHTTP(t, h, apiKey)
	slugB, _ := seedEventTypeHTTP(t, h, apiKey)

	if code, _ := patchSlug(t, h, apiKey, slugA, slugB); code != http.StatusConflict {
		t.Errorf("rename onto an existing slug: %d; want 409", code)
	}
	// slugify strips everything usable out of this, so it is empty rather than invalid.
	if code, _ := patchSlug(t, h, apiKey, slugA, "!!!"); code != http.StatusBadRequest {
		t.Errorf("rename to an unusable slug: %d; want 400", code)
	}
	// And it is normalised rather than taken literally, matching team slugs.
	if code, body := patchSlug(t, h, apiKey, slugA, "  Intro Call  "); code != http.StatusOK {
		t.Errorf("rename with spaces and caps: %d - %v", code, body)
	} else if got := body["slug"]; got != "intro-call" {
		t.Errorf("slug = %v, want intro-call (slugified)", got)
	}
}
