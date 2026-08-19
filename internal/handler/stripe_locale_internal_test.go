package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/db"
	"github.com/calnode/calnode/internal/mailer"
)

// captureMailer records every Send call so the test can assert on the confirmation
// email's resolved locale (mailer.BookingData.Locale, surfaced via <html lang="…">).
type captureMailer struct {
	msgs []mailer.Message
}

func (c *captureMailer) Send(_ context.Context, msg mailer.Message) error {
	c.msgs = append(c.msgs, msg)
	return nil
}

// TestConfirmPaidBooking_usesAttendeeLocale is a regression test: the Stripe webhook path
// (confirmPaidBooking) rebuilds its bookingConfirmationInput from a raw SQL query that used
// to omit booking_attendees.locale, so paid bookings always confirmed in English regardless
// of the language the attendee actually booked in — even though the free-booking path (same
// dispatchBookingConfirmation) got this right. See stripe_booking.go's SELECT.
func TestConfirmPaidBooking_usesAttendeeLocale(t *testing.T) {
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer database.Close()
	if err := db.Migrate(database); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}

	h := New(database, slog.Default())
	cap := &captureMailer{}
	h.SetMailer(cap, "https://calnode.example.com")

	setupRec := httptest.NewRecorder()
	h.Setup(setupRec, httptest.NewRequest(http.MethodPost, "/v1/setup",
		strings.NewReader(`{"name":"Test Host","email":"host@example.com","timezone":"UTC"}`)))
	if setupRec.Code != http.StatusCreated {
		t.Fatalf("setup: %d — %s", setupRec.Code, setupRec.Body.String())
	}
	var setupResp struct {
		APIKey string `json:"api_key"`
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(setupRec.Body.Bytes(), &setupResp); err != nil {
		t.Fatalf("decode setup: %v", err)
	}

	etReq := httptest.NewRequest(http.MethodPost, "/v1/event-types", strings.NewReader(
		`{"slug":"paid-call","name":"Paid Call","duration_minutes":30,"location_type":"phone","location_value":"+1 555 000 1111","max_future_days":0}`))
	etReq.Header.Set("X-API-Key", setupResp.APIKey)
	etRec := httptest.NewRecorder()
	h.RequireAuth(h.CreateEventType)(etRec, etReq)
	if etRec.Code != http.StatusCreated {
		t.Fatalf("create event type: %d — %s", etRec.Code, etRec.Body.String())
	}
	var et struct{ ID string `json:"id"` }
	json.Unmarshal(etRec.Body.Bytes(), &et) //nolint:errcheck

	// Create the booking through booking.Service.Create directly, NOT through the
	// CreateBooking HTTP handler. That matters: for a free event type, CreateBooking
	// itself fires dispatchBookingConfirmation immediately, which would send its own
	// (correct) Spanish confirmation and mask a bug in confirmPaidBooking's separate,
	// independent path — the exact thing this test exists to catch. Going through the
	// service layer creates the booking + attendee row (with locale, per the earlier
	// plumbing fix) without sending any email, so the only email in this test is the
	// one confirmPaidBooking sends below.
	b, err := h.bookingSvc.Create(context.Background(), booking.CreateParams{
		EventTypeID: et.ID,
		HostIDs:     []string{setupResp.UserID},
		RoutingMode: "fixed",
		StartAt:     time.Now().Add(48 * time.Hour).UTC(),
		EndAt:       time.Now().Add(48*time.Hour + 30*time.Minute).UTC(),
		Organizer: booking.Attendee{
			Name:         "Ana Attendee",
			Email:        "ana@example.com",
			IANATimezone: "UTC",
			Locale:       "es",
		},
	})
	if err != nil {
		t.Fatalf("bookingSvc.Create: %v", err)
	}

	// Simulate the pending-payment hold a real paid booking is created with (see
	// CreateBooking's et.PriceCents > 0 branch) — confirmPaidBooking's conditional UPDATE
	// requires payment_status = 'pending' to proceed.
	if _, err := database.Exec(`UPDATE bookings SET payment_status = 'pending' WHERE id = ?`, b.ID); err != nil {
		t.Fatalf("set pending: %v", err)
	}

	// Sanity: the locale really did land in booking_attendees (the earlier plumbing fix).
	var storedLocale string
	if err := database.QueryRow(`SELECT locale FROM booking_attendees WHERE booking_id = ? AND is_organizer = 1`, b.ID).
		Scan(&storedLocale); err != nil {
		t.Fatalf("query stored locale: %v", err)
	}
	if storedLocale != "es" {
		t.Fatalf("stored locale = %q, want \"es\" (test setup problem, not the fix under test)", storedLocale)
	}

	h.confirmPaidBooking(context.Background(), b.ID, "pi_test_123", 5000, "usd")

	// dispatchBookingConfirmation fires in a goroutine and sends both a host notification
	// (always English by design — hosts are the operator, out of i18n scope) and the
	// attendee confirmation; poll for the attendee's specifically, by recipient, rather
	// than assume ordering or that only one email goes out.
	var msg *mailer.Message
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for i := range cap.msgs {
			if len(cap.msgs[i].To) > 0 && cap.msgs[i].To[0] == "ana@example.com" {
				msg = &cap.msgs[i]
				break
			}
		}
		if msg != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if msg == nil {
		t.Fatalf("no confirmation email was sent to the attendee (ana@example.com); sent: %+v", cap.msgs)
	}
	if !strings.Contains(msg.HTML, `lang="es"`) {
		t.Errorf("paid booking confirmation should render in the attendee's stored locale (es); got HTML head: %.300s", msg.HTML)
	}
	if !strings.Contains(msg.Subject, "Reserva confirmada") {
		t.Errorf("paid booking confirmation subject = %q, want the Spanish subject", msg.Subject)
	}
}
