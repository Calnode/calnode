package handler_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The info panel is 240px wide with 1.5rem padding (192px of content). Each face
// is 56px and the stack overlaps by 16px, so six hosts is 256px — it spilled into
// the calendar column (#166). The page must draw at most three faces plus a "+N"
// chip, matching the label which already names only three hosts.
func TestBookPageCapsHostFacesAtThree(t *testing.T) {
	h, database, key, ownerID := setupWorkspaceWithDB(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	parts := []string{
		fmt.Sprintf(`{"user_id":%q,"role":"required","priority":0}`, ownerID),
	}
	for i := 2; i <= 6; i++ {
		hostID := fmt.Sprintf("u%d", i)
		if _, err := database.Exec(
			`INSERT INTO users (id,email,name,iana_timezone,is_admin) VALUES (?,?,?, 'UTC',0)`,
			hostID, hostID+"@example.com", "Host "+hostID); err != nil {
			t.Fatalf("seed %s: %v", hostID, err)
		}
		parts = append(parts, fmt.Sprintf(`{"user_id":%q,"role":"required","priority":%d}`, hostID, i-1))
	}
	if rec := putHosts(t, h, slug, key, `{"hosts":[`+strings.Join(parts, ",")+`]}`); rec.Code != http.StatusOK {
		t.Fatalf("set hosts: %d — %s", rec.Code, rec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/book/"+slug, nil)
	req.SetPathValue("slug", slug)
	rec := httptest.NewRecorder()
	h.BookPage(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("book page: %d", rec.Code)
	}

	block := hostFacesBlock(t, rec.Body.String())
	if got := strings.Count(block, `class="face"`); got != 3 {
		t.Errorf("rendered %d avatar faces; want 3", got)
	}
	if !strings.Contains(block, `class="face face-more"`) {
		t.Error("missing the \"+N\" overflow chip for 6 hosts")
	}
	if !strings.Contains(block, ">+3</span>") {
		t.Error("overflow chip should read +3 for 6 hosts")
	}
}

// hostFacesBlock returns the rendered #host-faces element only — the page's inline
// script contains the same class names in template literals.
func hostFacesBlock(t *testing.T, page string) string {
	t.Helper()
	start := strings.Index(page, `id="host-faces"`)
	if start < 0 {
		t.Fatal("no #host-faces element in rendered page")
	}
	end := strings.Index(page[start:], "</div>")
	if end < 0 {
		t.Fatal("unterminated #host-faces element")
	}
	return page[start : start+end]
}
