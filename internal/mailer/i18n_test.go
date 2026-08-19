package mailer

import (
	"context"
	"strings"
	"testing"

	"github.com/calnode/calnode/internal/i18n"
)

// TestSendConfirmation_attendeeLocaleTranslatesButHostStaysEnglish is the core i18n
// contract for attendee-facing emails: the organizer/attendee email follows d.Locale, but
// the host email always stays English regardless — see BookingData.Locale's doc comment
// and internal-docs/i18n-plan.md ("public-facing only": the host is the operator, out of
// scope, same as the admin UI).
func TestSendConfirmation_attendeeLocaleTranslatesButHostStaysEnglish(t *testing.T) {
	cap := &captureMailer{}
	d := testBookingData()
	d.Locale = i18n.Get("es")
	if err := SendConfirmation(context.Background(), cap, d); err != nil {
		t.Fatalf("SendConfirmation: %v", err)
	}
	msgs := cap.all()
	if len(msgs) != 2 {
		t.Fatalf("got %d messages; want 2", len(msgs))
	}
	org, host := msgs[0], msgs[1]

	if !strings.Contains(org.Subject, "Reserva confirmada") {
		t.Errorf("organizer subject = %q; want Spanish", org.Subject)
	}
	if !strings.Contains(org.Text, "Hola Bob Booker,") {
		t.Errorf("organizer body missing Spanish greeting: %q", org.Text)
	}
	if !strings.Contains(org.Text, "Evento:") || !strings.Contains(org.Text, "Con:") {
		t.Errorf("organizer body missing Spanish labels: %q", org.Text)
	}
	if !strings.Contains(org.HTML, `lang="es"`) {
		t.Errorf("organizer HTML missing lang=es: %q", org.HTML)
	}
	// 2026-06-15 09:00 UTC is a Monday — Spanish clock_format is 24h, so no "AM"/"PM".
	if strings.Contains(org.Text, "AM") || strings.Contains(org.Text, "PM") {
		t.Errorf("organizer body should use a 24h clock in Spanish: %q", org.Text)
	}
	if !strings.Contains(org.Text, "lun 15 jun 2026") {
		t.Errorf("organizer body missing Spanish weekday/month names: %q", org.Text)
	}

	if !strings.Contains(host.Subject, "New booking") {
		t.Errorf("host subject = %q; want English regardless of attendee locale", host.Subject)
	}
	if !strings.Contains(host.Text, "Hi Alice Host,") {
		t.Errorf("host body should stay English: %q", host.Text)
	}
	if strings.Contains(host.HTML, `lang="es"`) {
		t.Errorf("host HTML should not carry the attendee's lang: %q", host.HTML)
	}
}

// TestSendConfirmation_nilLocaleDefaultsToEnglish covers the zero-value case (bookings
// created before locale capture existed, or paths that never set it).
func TestSendConfirmation_nilLocaleDefaultsToEnglish(t *testing.T) {
	cap := &captureMailer{}
	d := testBookingData() // Locale left nil
	if err := SendConfirmationToAttendee(context.Background(), cap, d); err != nil {
		t.Fatalf("SendConfirmationToAttendee: %v", err)
	}
	org := cap.all()[0]
	if !strings.Contains(org.Text, "Hi Bob Booker,") {
		t.Errorf("nil Locale should default to English: %q", org.Text)
	}
}
