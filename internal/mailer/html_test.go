package mailer

import (
	"strings"
	"testing"
	"time"
)

func sampleBookingData() BookingData {
	start := time.Date(2026, 6, 22, 21, 0, 0, 0, time.UTC) // 9am NZST
	return BookingData{
		BookingID:         "abc-123",
		EventTypeName:     "20-minute call",
		EventTypeSlug:     "intro",
		HostName:          "Wynne Pirini",
		HostEmail:         "host@example.com",
		OrganizerName:     "Alex Johnson",
		OrganizerEmail:    "alex@example.com",
		OrganizerTimezone: "Pacific/Auckland",
		StartAt:           start,
		EndAt:             start.Add(20 * time.Minute),
		PreviousStartAt:   start.AddDate(0, 0, -1),
		PreviousEndAt:     start.AddDate(0, 0, -1).Add(20 * time.Minute),
		ManageURL:         "https://booking.example.com/manage/tok",
		BaseURL:           "https://booking.example.com",
		BrandName:         "Orchestratr",
	}
}

// Every HTML template must render to non-empty output (renderHTML returns "" on
// any execution error, so empty means a broken template).
func TestRenderHTML_allTemplates(t *testing.T) {
	d := sampleBookingData()
	cases := map[string]string{
		"confirm-org":     renderHTML(htmlConfirmOrg, d),
		"confirm-host":    renderHTML(htmlConfirmHost, d),
		"cancel-org":      renderHTML(htmlCancelOrg, d),
		"cancel-host":     renderHTML(htmlCancelHost, d),
		"reschedule-org":  renderHTML(htmlRescheduleOrg, d),
		"reschedule-host": renderHTML(htmlRescheduleHost, d),
		"reminder-org":    renderHTML(htmlReminderOrg, d),
	}
	for name, out := range cases {
		if strings.TrimSpace(out) == "" {
			t.Errorf("%s: rendered empty HTML (template error)", name)
			continue
		}
		if !strings.Contains(out, "20-minute call") {
			t.Errorf("%s: missing event name", name)
		}
		// No logo set → no logo header bar, and no repeated brand wordmark in body.
		if strings.Contains(out, "<img") {
			t.Errorf("%s: rendered a logo <img> with no LogoURL set", name)
		}
	}

	// Attendee confirmation must carry the calendar buttons and manage link.
	conf := cases["confirm-org"]
	if !strings.Contains(conf, "calendar.google.com") || !strings.Contains(conf, "outlook.office.com") {
		t.Error("confirm-org: missing add-to-calendar links")
	}
	if !strings.Contains(conf, "/manage/tok") {
		t.Error("confirm-org: missing manage link")
	}

	// With a logo, the header renders the image with the brand as alt text.
	d.LogoURL = "https://cdn.example.com/logo.png"
	withLogo := renderHTML(htmlConfirmOrg, d)
	if !strings.Contains(withLogo, `src="https://cdn.example.com/logo.png"`) {
		t.Error("confirm-org: logo image not rendered when LogoURL set")
	}
	if !strings.Contains(withLogo, `alt="Orchestratr"`) {
		t.Error("confirm-org: logo alt should be the brand name")
	}
}

func TestBookingData_Brand(t *testing.T) {
	if got := (BookingData{}).Brand(); got != "Calnode" {
		t.Errorf("Brand() empty = %q; want Calnode", got)
	}
	if got := (BookingData{BrandName: "Acme"}).Brand(); got != "Acme" {
		t.Errorf("Brand() set = %q; want Acme", got)
	}
}
