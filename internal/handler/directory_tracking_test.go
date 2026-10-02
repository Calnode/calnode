package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDirectoryPages_headHTMLAndCSP(t *testing.T) {
	const injectedHTML = `<style id="directory-custom-css">.dir-title { color: #123456; }</style>`
	const strictCSP = "default-src 'self'; script-src 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; connect-src 'self'; frame-ancestors 'none'"
	for _, tc := range []struct {
		name, headHTML, allow, gtm, ga4 string
		strict                          bool
	}{
		{"empty", "", "", "", "", true},
		{"whitespace", " \n\t", "https://custom.example", "", "", true},
		{"headHTML", injectedHTML, "", "", "", false},
		{"allowlistedHeadHTML", injectedHTML, "https://custom.example", "", "", false},
		{"nativeTags", "", "https://custom.example", "GTM-TEST123", "G-TEST123", false},
		{"headHTMLAndNativeTags", injectedHTML, "https://custom.example", "GTM-TEST123", "G-TEST123", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, database, key, userID := setupWorkspaceWithDB(t)
			// Use the authenticated admin setting endpoint, the only source of trusted HTML.
			settings, err := json.Marshal(map[string]string{
				"head_html": tc.headHTML, "csp_allow": tc.allow,
				"gtm_container_id": tc.gtm, "ga4_measurement_id": tc.ga4,
			})
			if err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			h.RequireAuth(h.PatchTrackingSettings)(rec, authReq(http.MethodPatch, "/v1/settings/tracking", string(settings), key))
			if rec.Code != http.StatusOK {
				t.Fatalf("configure tracking: %d — %s", rec.Code, rec.Body.String())
			}
			if _, err := database.Exec(`UPDATE users SET handle = 'host' WHERE id = ?`, userID); err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(`INSERT INTO teams (id, name, slug) VALUES ('t1', 'Design', 'design')`); err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(`INSERT INTO team_members (id, team_id, user_id) VALUES ('tm1', 't1', ?)`, userID); err != nil {
				t.Fatal(err)
			}
			slug, etID := seedEventTypeHTTP(t, h, key)
			if _, err := database.Exec(`UPDATE event_types SET team_id = 't1' WHERE id = ?`, etID); err != nil {
				t.Fatal(err)
			}

			bookRec := httptest.NewRecorder()
			bookReq := httptest.NewRequest(http.MethodGet, "/book/"+slug, nil)
			bookReq.SetPathValue("slug", slug)
			h.BookPage(bookRec, bookReq)
			manageRec := httptest.NewRecorder()
			manageReq := httptest.NewRequest(http.MethodGet, "/manage/invalid", nil)
			manageReq.SetPathValue("token", "invalid")
			h.ManagePage(manageRec, manageReq)
			wantCSP := bookRec.Header().Get("Content-Security-Policy")
			if bookRec.Code != http.StatusOK || manageRec.Code != http.StatusOK || wantCSP == "" || manageRec.Header().Get("Content-Security-Policy") != wantCSP {
				t.Fatal("booking/manage CSP baseline missing or inconsistent")
			}
			if tc.strict && wantCSP != strictCSP {
				t.Fatalf("empty settings must preserve strict CSP: %q", wantCSP)
			}

			for _, page := range []struct {
				name, path, pathKey, pathValue, title string
				serve                                 http.HandlerFunc
			}{
				{"person", "/u/host", "handle", "host", "Test Host", h.PersonPage},
				{"team", "/team/design", "slug", "design", "Design", h.TeamPage},
			} {
				t.Run(page.name, func(t *testing.T) {
					rec := httptest.NewRecorder()
					req := httptest.NewRequest(http.MethodGet, page.path+"?head_html=untrusted-url-marker", nil)
					req.SetPathValue(page.pathKey, page.pathValue)
					page.serve(rec, req)
					body := rec.Body.String()
					if rec.Code != http.StatusOK || !strings.Contains(body, "</html>") {
						t.Fatalf("incomplete directory: %d — %.200s", rec.Code, body)
					}
					if got := rec.Header().Get("Content-Security-Policy"); got != wantCSP {
						t.Errorf("CSP = %q; want booking/manage policy %q", got, wantCSP)
					}
					headStart, headEnd := strings.Index(body, "<head>"), strings.Index(body, "</head>")
					if headStart < 0 || headEnd <= headStart {
						t.Fatal("missing head element")
					}
					if strings.TrimSpace(tc.headHTML) != "" {
						if strings.Count(body, tc.headHTML) != 1 || !strings.Contains(body[headStart:headEnd], tc.headHTML) {
							t.Error("configured HTML must appear unescaped exactly once, inside head")
						}
						if strings.Index(body, tc.headHTML) > strings.Index(body, `<link rel="stylesheet"`) {
							t.Error("head HTML should precede the booking stylesheet")
						}
					} else if strings.Contains(body, "directory-custom-css") {
						t.Error("empty configuration rendered custom CSS")
					}
					if strings.Contains(body, "untrusted-url-marker") {
						t.Error("directory URL must not supply injected HTML")
					}
					for _, want := range []string{`<h1 class="dir-title">` + page.title + `</h1>`, `href="/book/` + slug + `"`} {
						if !strings.Contains(body, want) {
							t.Errorf("normal directory content missing %q", want)
						}
					}
					if page.name == "team" && !strings.Contains(body, `href="/u/host"`) {
						t.Error("team member directory link missing")
					}
					if tc.gtm != "" {
						for _, want := range []string{`gtm: "` + tc.gtm + `"`, `ga4: "` + tc.ga4 + `"`, `id="cookie-banner"`} {
							if !strings.Contains(body, want) {
								t.Errorf("native tracking markup missing %q", want)
							}
						}
						if tc.headHTML != "" && strings.Index(body, "window.__CALNODE_TRACK") > strings.Index(body, tc.headHTML) {
							t.Error("configured head HTML should follow native tracking markup")
						}
					}
				})
			}
		})
	}
}
