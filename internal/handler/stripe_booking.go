package handler

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/calnode/calnode/internal/stripe"
)

// checkoutHoldWindow is how long the slot is held while the booker pays. Stripe requires a
// Checkout session to expire between 30 minutes and 24 hours from creation.
const checkoutHoldWindow = 31 * time.Minute

// startBookingCheckout creates a Stripe Checkout session for a held (pending) booking, marks
// the booking pending_payment, and returns the hosted Checkout URL to redirect the booker to.
func (h *Handler) startBookingCheckout(ctx context.Context, sc *stripe.Client, bookingID string, priceCents int, currency, productName, slug, email string) (string, error) {
	if currency == "" {
		currency = "usd"
	}
	base := h.publicURL()
	sess, err := sc.CreateCheckoutSession(ctx, stripe.CheckoutParams{
		AmountCents:   int64(priceCents),
		Currency:      currency,
		ProductName:   productName,
		CustomerEmail: email,
		// Stripe substitutes {CHECKOUT_SESSION_ID}; the page shows a "payment received" banner.
		SuccessURL: base + "/book/" + slug + "?paid=1&session_id={CHECKOUT_SESSION_ID}",
		CancelURL:  base + "/book/" + slug,
		ExpiresAt:  time.Now().Add(checkoutHoldWindow),
		Metadata:   map[string]string{"booking_id": bookingID},
	})
	if err != nil {
		return "", err
	}
	if _, err := h.db.ExecContext(ctx,
		`UPDATE bookings SET payment_status = 'pending', stripe_session_id = ? WHERE id = ?`,
		sess.ID, bookingID); err != nil {
		return "", err
	}
	return sess.URL, nil
}

// StripeWebhook handles POST /v1/stripe/webhook — Stripe's payment notifications. Public, but
// authenticated by the signing secret (no session cookie, so CSRF middleware doesn't apply).
func (h *Handler) StripeWebhook(w http.ResponseWriter, r *http.Request) {
	sc := h.getStripe()
	if sc == nil || !sc.WebhookConfigured() {
		h.writeError(w, http.StatusServiceUnavailable, "payments not configured")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "could not read body")
		return
	}
	ev, err := sc.VerifyWebhook(body, r.Header.Get("Stripe-Signature"), time.Now())
	if err != nil {
		// Bad signature → 400 so Stripe surfaces the failure (and a forged call is rejected).
		h.logger.WarnContext(r.Context(), "stripe webhook: verify failed", "error", err)
		h.writeError(w, http.StatusBadRequest, "signature verification failed")
		return
	}

	switch ev.Type {
	case "checkout.session.completed":
		sess, perr := ev.Session()
		if perr != nil {
			break
		}
		if sess.PaymentStatus == "paid" {
			if id := sess.Metadata["booking_id"]; id != "" {
				h.confirmPaidBooking(context.Background(), id, sess.PaymentIntent, int(sess.AmountTotal), sess.Currency)
			}
		}
	case "checkout.session.expired":
		if sess, perr := ev.Session(); perr == nil {
			if id := sess.Metadata["booking_id"]; id != "" {
				h.releaseUnpaidHold(context.Background(), id)
			}
		}
	}
	// Acknowledge so Stripe stops retrying (we've durably acted, or chosen to ignore).
	w.WriteHeader(http.StatusOK)
}

// confirmPaidBooking flips a held booking to paid, records the charged amount, and fires the
// deferred confirmation side-effects. Idempotent: the conditional UPDATE (payment_status=
// 'pending') ensures only the first delivery of a (possibly retried) webhook dispatches.
func (h *Handler) confirmPaidBooking(ctx context.Context, bookingID, paymentIntentID string, amountCents int, currency string) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	res, err := h.db.ExecContext(ctx,
		`UPDATE bookings SET payment_status = 'paid', stripe_payment_intent_id = ?,
		     amount_paid_cents = ?, amount_paid_currency = ?
		 WHERE id = ? AND payment_status = 'pending'`,
		paymentIntentID, amountCents, currency, bookingID)
	if err != nil {
		h.logger.ErrorContext(ctx, "stripe: mark booking paid", "error", err, "booking_id", bookingID)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return // already processed (or not a pending paid booking) — don't double-dispatch
	}

	b, err := h.bookingSvc.Get(ctx, bookingID)
	if err != nil || b == nil {
		h.logger.ErrorContext(ctx, "stripe: load paid booking", "error", err, "booking_id", bookingID)
		return
	}
	// Reconstruct the confirmation context from the booking's event type + organizer.
	var in bookingConfirmationInput
	if err := h.db.QueryRowContext(ctx, `
		SELECT et.name, et.slug, et.location_type, a.name, a.email, a.iana_timezone
		FROM bookings b
		JOIN event_types et ON et.id = b.event_type_id
		JOIN booking_attendees a ON a.booking_id = b.id AND a.is_organizer = 1
		WHERE b.id = ?`, bookingID).
		Scan(&in.EventTypeName, &in.EventTypeSlug, &in.LocationType, &in.OrganizerName, &in.OrganizerEmail, &in.OrganizerTimezone); err != nil {
		h.logger.ErrorContext(ctx, "stripe: load confirmation input", "error", err, "booking_id", bookingID)
		return
	}
	// Run the same side-effects a free booking gets (calendar, emails, Zoom/Meet, webhooks) in
	// the background, so the webhook is acknowledged immediately (well under Stripe's timeout).
	// dispatchBookingConfirmation manages its own context, so fire-and-forget is safe.
	go h.dispatchBookingConfirmation(b, in) // #nosec G118 -- deliberately its own context.Background(); see dispatchBookingConfirmation's doc comment
}

// releaseUnpaidHold cancels a still-pending booking whose Checkout session expired, freeing
// the slot. No-op if the booking already paid or was otherwise resolved.
func (h *Handler) releaseUnpaidHold(ctx context.Context, bookingID string) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	res, err := h.db.ExecContext(ctx,
		`UPDATE bookings SET status = 'cancelled', cancellation_reason = 'payment not completed'
		 WHERE id = ? AND status = 'confirmed' AND payment_status = 'pending'`, bookingID)
	if err != nil {
		h.logger.ErrorContext(ctx, "stripe: release unpaid hold", "error", err, "booking_id", bookingID)
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		h.logger.InfoContext(ctx, "stripe: released unpaid hold", "booking_id", bookingID)
	}
}

// refundBookingPayment issues a Stripe refund when a PAID booking is cancelled. Best-effort
// and idempotent (guards on payment_status='paid'); failures are logged, not fatal to cancel.
func (h *Handler) refundBookingPayment(ctx context.Context, bookingID string) {
	sc := h.getStripe()
	if sc == nil {
		return
	}
	var paymentStatus, intentID string
	if err := h.db.QueryRowContext(ctx,
		`SELECT payment_status, COALESCE(stripe_payment_intent_id, '') FROM bookings WHERE id = ?`,
		bookingID).Scan(&paymentStatus, &intentID); err != nil {
		return
	}
	if paymentStatus != "paid" || intentID == "" {
		return
	}
	if err := sc.Refund(ctx, intentID); err != nil {
		h.logger.ErrorContext(ctx, "stripe: refund on cancel", "error", err, "booking_id", bookingID)
		return
	}
	if _, err := h.db.ExecContext(ctx,
		`UPDATE bookings SET payment_status = 'refunded' WHERE id = ?`, bookingID); err != nil {
		h.logger.ErrorContext(ctx, "stripe: mark refunded", "error", err, "booking_id", bookingID)
	}
}
