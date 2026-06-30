package handler

import (
	"bytes"
	"strings"
	"testing"
)

// TestBookingSurfacesShareStructuralHooks pins the structural contract across the
// THREE booking surfaces — book.html and manage.html (Go templates) and embed.js
// (a vanilla-JS web component). All three load the shared booking.css and implement
// the same calendar/slot-picker, but their markup is authored separately (Go
// template vs JS DOM-building), so they drift — the exact hazard CLAUDE.md warns
// about. Go template partials can't reach the JS embed, so this is the cross-language
// safety net: change the calendar/slots structure on one surface and forget another,
// and CI fails here.
//
// The pages are rendered WITHOUT the shared booking-logic.js inlined (BookingLogicJS
// left empty) on purpose: each hook must be present in the surface's OWN markup/script,
// otherwise the shared module would mask per-surface drift.
//
// Hooks that legitimately differ are deliberately excluded: the mobile step-flow uses
// .cal-back buttons in the templates vs .step-cal/.step-right card classes in the
// embed, and month nav uses #prev-btn/#next-btn in the templates vs the embed's own.
func TestBookingSurfacesShareStructuralHooks(t *testing.T) {
	var bookBuf, manageBuf bytes.Buffer
	if err := bookTmpl.Execute(&bookBuf, bookPageData{}); err != nil {
		t.Fatalf("book render: %v", err)
	}
	// Zero value → TokenInvalid false + Status "" (not "cancelled"), so the reschedule
	// calendar branch renders.
	if err := manageTmpl.Execute(&manageBuf, managePageData{}); err != nil {
		t.Fatalf("manage render: %v", err)
	}

	surfaces := map[string]string{
		"book.html":   bookBuf.String(),
		"manage.html": manageBuf.String(),
		"embed.js":    string(embedJS),
	}

	// Shared calendar/slots hooks every surface must expose — booking.css styles these
	// and the pickers depend on them. Verified present in all three when authored.
	hooks := []string{
		"cal-nav",      // month-navigation row
		"cal-grid",     // the day grid
		"month-label",  // current-month label
		"cal-col",      // calendar column
		"right-col",    // slots/form column
		"slots-list",   // slot-button container
		"slots-header", // selected-day header
		"slot-btn",     // a time-slot button
	}

	for _, h := range hooks {
		for name, src := range surfaces {
			if !strings.Contains(src, h) {
				t.Errorf("structural hook %q missing from %s — the booking surfaces have drifted; "+
					"add it to all three (book.html, manage.html, embed.js) or adjust this contract", h, name)
			}
		}
	}
}
