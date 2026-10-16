package handler_test

import (
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublicPages_businessBranding(t *testing.T) {
	for _, tc := range []struct {
		name, businessName, logoURL string
		height, opacity             int
		wantHeight, wantOpacity     string
	}{
		{"logo", `Acme "Studio" <design> & Co`, "/branding/logo?v=123", 40, 60, "52", "0.6"},
		{"logoWithoutName", "", "/branding/logo?v=123", 28, 100, "36", "1"},
		{"nameOnly", `Acme "Studio" <design> & Co`, "", 28, 100, "", ""},
		{"neither", "", "", 28, 100, "", ""},
		{"normalizedSettings", "Acme", "/branding/logo?v=123", 0, 200, "36", "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, database, key, userID := setupWorkspaceWithDB(t)
			if _, err := database.Exec(`UPDATE server_settings SET business_name = ?, logo_url = ?, logo_height = ?, logo_opacity = ? WHERE id = 1`,
				tc.businessName, tc.logoURL, tc.height, tc.opacity); err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(`UPDATE users SET handle = 'host', avatar_url = '/avatars/host.png' WHERE id = ?`, userID); err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(`INSERT INTO teams (id, name, slug) VALUES ('brand-team', 'Design', 'design')`); err != nil {
				t.Fatal(err)
			}
			slug, etID := seedEventTypeHTTP(t, h, key)
			seedBooking(t, database, "brand-booking", etID, userID, "2026-06-20T09:00:00Z", "2026-06-20T09:30:00Z", "confirmed")
			token := issueTestToken(t, database, "brand-booking")
			for _, page := range []struct {
				name, path, pathKey, pathValue string
				serve                          http.HandlerFunc
			}{
				{"person", "/u/host", "handle", "host", h.PersonPage},
				{"team", "/team/design", "slug", "design", h.TeamPage},
				{"booking", "/book/" + slug, "slug", slug, h.BookPage},
				{"manage", "/manage/" + token, "token", token, h.ManagePage},
			} {
				t.Run(page.name, func(t *testing.T) {
					rec := httptest.NewRecorder()
					req := httptest.NewRequest(http.MethodGet, page.path, nil)
					req.SetPathValue(page.pathKey, page.pathValue)
					page.serve(rec, req)
					body := rec.Body.String()
					if rec.Code != http.StatusOK || !strings.Contains(body, "</html>") {
						t.Fatalf("incomplete page: status %d, body %.200s", rec.Code, body)
					}
					start := strings.Index(body, "<header ")
					if tc.logoURL == "" && tc.businessName == "" {
						if start != -1 {
							t.Error("unconfigured branding rendered a header")
						}
					} else {
						if start == -1 {
							t.Fatal("missing business header")
						}
						end := strings.Index(body[start:], "</header>")
						if end == -1 {
							t.Fatal("incomplete business header")
						}
						header := body[start : start+end]
						if tc.logoURL != "" {
							alt := tc.businessName
							if alt == "" {
								alt = "Business logo"
							}
							for _, want := range []string{
								`src="` + tc.logoURL + `"`, `alt="` + html.EscapeString(alt) + `"`,
								"height:" + tc.wantHeight + "px;width:auto;max-width:100%;opacity:" + tc.wantOpacity,
								"max-width:100%;box-sizing:border-box;",
							} {
								if !strings.Contains(header, want) {
									t.Errorf("header missing %q: %s", want, header)
								}
							}
						} else if strings.Contains(header, "<img") || !strings.Contains(header, ">"+html.EscapeString(tc.businessName)+"</span>") {
							t.Errorf("incorrect name-only header: %s", header)
						}
					}
					if page.name == "person" && !strings.Contains(body, `<span class="dir-avatar"><img src="/avatars/host.png" alt=""></span>`) {
						t.Error("person avatar missing from directory card")
					}
					if page.name == "team" && !strings.Contains(body, `<span class="dir-avatar" aria-hidden="true">D</span>`) {
						t.Error("team avatar missing from directory card")
					}
					if (page.name == "person" || page.name == "team") && start > strings.Index(body, `<main class="dir-card">`) {
						t.Error("business header should precede the directory card")
					}
				})
			}
		})
	}
}
