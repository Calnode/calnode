package handler

import (
	"bytes"
	"strings"
	"testing"
)

// TestConsentChromeSharedAcrossSurfaces renders the book and manage templates from
// the same shared chrome partials and asserts both gate analytics + the cookie banner
// on a configured GTM/GA4 id, and surface the legal footer links. This is the guard
// against the two public surfaces drifting apart (the reason the chrome was shared).
func TestConsentChromeSharedAcrossSurfaces(t *testing.T) {
	surfaces := []struct {
		name   string
		render func(t *testing.T, gtm, privacy, terms string) string
	}{
		{"book", func(t *testing.T, gtm, privacy, terms string) string {
			var b bytes.Buffer
			if err := bookTmpl.Execute(&b, bookPageData{
				GTMContainerID: gtm, PrivacyURL: privacy, TermsURL: terms,
			}); err != nil {
				t.Fatalf("book render: %v", err)
			}
			return b.String()
		}},
		{"manage", func(t *testing.T, gtm, privacy, terms string) string {
			var b bytes.Buffer
			if err := manageTmpl.Execute(&b, managePageData{
				GTMContainerID: gtm, PrivacyURL: privacy, TermsURL: terms,
			}); err != nil {
				t.Fatalf("manage render: %v", err)
			}
			return b.String()
		}},
	}

	for _, s := range surfaces {
		s := s
		t.Run(s.name+"/tracking_on", func(t *testing.T) {
			out := s.render(t, "GTM-TEST123", "https://example.com/privacy", "https://example.com/terms")
			for _, want := range []string{
				`id="cookie-banner"`,           // consent banner present
				"__CALNODE_TRACK",              // tracking loader present
				"GTM-TEST123",                  // the configured id is wired in
				"window.__calnodeLoadTracking", // gated loader (not auto-injected)
				"Cookie settings",              // footer reopen control
				"https://example.com/privacy",  // legal links
				"https://example.com/terms",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("%s tracking-on: output missing %q", s.name, want)
				}
			}
			// Crucially, the real Google script must NOT be in the static HTML — it loads
			// only after consent, from JS.
			if strings.Contains(out, "googletagmanager.com/gtm.js?id=") &&
				!strings.Contains(out, "j.src='https://www.googletagmanager.com/gtm.js") {
				t.Errorf("%s tracking-on: GTM script appears pre-injected (should be consent-gated)", s.name)
			}
		})

		t.Run(s.name+"/tracking_off", func(t *testing.T) {
			out := s.render(t, "", "", "")
			for _, notWant := range []string{
				`id="cookie-banner"`,
				"__CALNODE_TRACK",
				"Cookie settings",
			} {
				if strings.Contains(out, notWant) {
					t.Errorf("%s tracking-off: output should not contain %q", s.name, notWant)
				}
			}
		})

		// The structural partials (calendarGrid, eventMeta) render on both surfaces
		// regardless of tracking — the JS hooks booking-logic.js depends on must be
		// present and identical.
		t.Run(s.name+"/structural", func(t *testing.T) {
			out := s.render(t, "", "", "")
			for _, want := range []string{
				`id="cal"`, `class="cal-grid"`, `id="month-label"`, // calendarGrid
				`id="prev-btn"`, `id="next-btn"`,
				`class="meta"`, // eventMeta
			} {
				if !strings.Contains(out, want) {
					t.Errorf("%s structural: output missing %q", s.name, want)
				}
			}
		})
	}
}

// TestDemoChromeSharedAcrossSurfaces mirrors TestConsentChromeSharedAcrossSurfaces for
// the demo-mode banner + noindex meta tag — both surfaces render from the same
// "demoBanner"/"demoNoindex" partials, so a regression in one shows up in both.
func TestDemoChromeSharedAcrossSurfaces(t *testing.T) {
	surfaces := []struct {
		name   string
		render func(t *testing.T, demoMode bool) string
	}{
		{"book", func(t *testing.T, demoMode bool) string {
			var b bytes.Buffer
			if err := bookTmpl.Execute(&b, bookPageData{DemoMode: demoMode}); err != nil {
				t.Fatalf("book render: %v", err)
			}
			return b.String()
		}},
		{"manage", func(t *testing.T, demoMode bool) string {
			var b bytes.Buffer
			if err := manageTmpl.Execute(&b, managePageData{DemoMode: demoMode}); err != nil {
				t.Fatalf("manage render: %v", err)
			}
			return b.String()
		}},
	}

	for _, s := range surfaces {
		s := s
		t.Run(s.name+"/demo_on", func(t *testing.T) {
			out := s.render(t, true)
			for _, want := range []string{
				`<meta name="robots" content="noindex,nofollow">`,
				`class="demo-banner"`,
				"Public demo",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("%s demo-on: output missing %q", s.name, want)
				}
			}
		})

		t.Run(s.name+"/demo_off", func(t *testing.T) {
			out := s.render(t, false)
			for _, notWant := range []string{
				`name="robots"`,
				`class="demo-banner"`,
			} {
				if strings.Contains(out, notWant) {
					t.Errorf("%s demo-off: output should not contain %q", s.name, notWant)
				}
			}
		})
	}
}
