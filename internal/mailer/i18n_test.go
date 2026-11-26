package mailer

import (
	"context"
	"strings"
	"testing"
	"time"

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

// TestHostEmails_followHostLocale: host-facing emails use BookingData.HostLocale (the
// operator's configured fallback language), never the attendee's. nil keeps them English,
// which is what the test above covers.
func TestHostEmails_followHostLocale(t *testing.T) {
	cap := &captureMailer{}
	d := testBookingData()
	d.Locale = i18n.Get("es")
	d.HostLocale = i18n.Get("de")
	d.CancellationReason = "Terminkonflikt"
	d.PreviousStartAt = d.StartAt.Add(-24 * time.Hour)
	for _, send := range []func(context.Context, Mailer, BookingData) error{SendConfirmationToHost, SendCancellationToHost, SendRescheduleToHost} {
		if err := send(context.Background(), cap, d); err != nil {
			t.Fatalf("send: %v", err)
		}
	}
	msgs := cap.all()
	if len(msgs) != 3 {
		t.Fatalf("got %d messages; want 3", len(msgs))
	}
	wants := []struct{ subject, lead string }{
		{"Neue Buchung: ", "Sie haben eine neue Buchung."},
		{"Buchung storniert: ", "Eine Buchung wurde storniert."},
		{"Termin verschoben: ", "Ein Termin wurde verschoben."},
	}
	for i, w := range wants {
		m := msgs[i]
		if !strings.HasPrefix(m.Subject, w.subject) || !strings.Contains(m.Subject, " mit ") {
			t.Errorf("msg %d subject = %q; want German host subject", i, m.Subject)
		}
		for _, body := range []string{m.Text, m.HTML} {
			if !strings.Contains(body, w.lead) {
				t.Errorf("msg %d body missing German lead %q", i, w.lead)
			}
			if strings.Contains(body, "Hola") || strings.Contains(body, "Hi ") {
				t.Errorf("msg %d body not in the host's language: %q", i, body)
			}
		}
		if !strings.Contains(m.HTML, `lang="de"`) {
			t.Errorf("msg %d HTML missing lang=de", i)
		}
	}
}
