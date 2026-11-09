package handler

import (
	"context"
	"io"
	"net/http"
	"time"

	stripe "github.com/calnode/calnode/internal/makecommerce"
)

// checkoutHoldWindow is how long the slot is held while the booker pays.
// MakeCommerce uses a fixed 30-minute expiry, but we keep 31 minutes for safety.
const checkoutHoldWindow = 31 * time.Minute

// startBookingCheckout creates a payment transaction for a held (pending) booking, marks
// the booking pending_payment, and returns the hosted payment URL to redirect the booker to.
// For MakeCommerce, the transaction ID is used instead of Stripe's session ID.
func (h *Handler) startBookingCheckout(ctx context.Context, sc *stripe.Client, bookingID string, priceCents int, currency, productName, slug, email string, clientIp string) (string, error) {
	if currency == "" {
		currency = "eur" // MakeCommerce primarily uses EUR
	}
	base := h.publicURL()
	// For MakeCommerce, we pass the transaction reference in the customer_url
	// The transaction ID will be returned and can be used to query status
	sess, err := sc.CreateCheckoutSession(ctx, stripe.CheckoutParams{
		AmountCents:   int64(priceCents),
		Currency:      currency,
		ProductName:   productName,
		CustomerEmail: email,
		// MakeCommerce doesn't use template variables like Stripe's {CHECKOUT_SESSION_ID}
		// Instead, we include the booking_id in the reference and handle it on success
		SuccessURL:      base + "/book/" + slug + "?paid=1&session_id=" + bookingID,
		CancelURL:       base + "/book/" + slug,
		NotificationURL: base + "/v1/stripe/webhook?booking_id=" + bookingID,
		ExpiresAt:       time.Now().Add(checkoutHoldWindow),
		Metadata: map[string]string{
			"booking_id": bookingID,
			"client_ip":  clientIp,
		},
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

// StripeWebhook handles POST /v1/stripe/webhook — payment provider notifications.
// For MakeCommerce, this receives transaction status updates. Public, but authenticated
// by the signing secret (no session cookie, so CSRF middleware doesn't apply).
// MakeCommerce sends webhook data as URL-encoded form with json and mac fields.
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

	// For MakeCommerce, the MAC is in the form data itself, not in a header.
	// We pass the raw body which contains json={...}&mac=...
	// The VerifyWebhook implementation will parse and verify the MAC.
	ev, err := sc.VerifyWebhook(body, "", time.Now())
	if err != nil {
		// Bad signature → 400 so the payment provider surfaces the failure.
		h.logger.WarnContext(r.Context(), "payment webhook: verify failed", "error", err)
		h.writeError(w, http.StatusBadRequest, "signature verification failed")
		return
	}

	bookingId := r.URL.Query().Get("booking_id")
	if bookingId == "" {
		h.writeError(w, http.StatusBadRequest, "param missing")
	}

	switch ev.Type {
	case "checkout.session.completed":
		sess, perr := ev.Session()
		if perr != nil {
			break
		}
		if sess.PaymentStatus == "paid" {
			h.confirmPaidBooking(context.Background(), bookingId, sess.PaymentIntent, int(sess.AmountTotal), sess.Currency)
		}
	case "checkout.session.expired":
		if _, perr := ev.Session(); perr == nil {
			h.releaseUnpaidHold(context.Background(), bookingId)
		}
	}
	// Acknowledge so the payment provider stops retrying (we've durably acted, or chosen to ignore).
	w.WriteHeader(http.StatusOK)
}

// confirmPaidBooking flips a held booking to paid, records the charged amount, and fires the
// deferred confirmation side-effects. Idempotent: the conditional UPDATE (payment_status=
// 'pending') ensures only the first delivery of a (possibly retried) webhook dispatches.
func (h *Handler) confirmPaidBooking(ctx context.Context, bookingID, paymentIntentID string, amountCents int, currency string) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	// For MakeCommerce, paymentIntentID is actually the transaction ID
	res, err := h.db.ExecContext(ctx,
		`UPDATE bookings SET payment_status = 'paid', stripe_payment_intent_id = ?,
		     amount_paid_cents = ?, amount_paid_currency = ?
		 WHERE id = ? AND payment_status = 'pending'`,
		paymentIntentID, amountCents, currency, bookingID)
	if err != nil {
		h.logger.ErrorContext(ctx, "payment: mark booking paid", "error", err, "booking_id", bookingID)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return // already processed (or not a pending paid booking) — don't double-dispatch
	}

	b, err := h.bookingSvc.Get(ctx, bookingID)
	if err != nil || b == nil {
		h.logger.ErrorContext(ctx, "payment: load paid booking", "error", err, "booking_id", bookingID)
		return
	}
	// Reconstruct the confirmation context from the booking's event type + organizer.
	var in bookingConfirmationInput
	if err := h.db.QueryRowContext(ctx, `
		SELECT et.name, et.slug, COALESCE(NULLIF(b.location_type, ''), et.location_type), a.name, a.email, a.iana_timezone, a.locale
		FROM bookings b
		JOIN event_types et ON et.id = b.event_type_id
		JOIN booking_attendees a ON a.booking_id = b.id AND a.is_organizer = 1
		WHERE b.id = ?`, bookingID).
		Scan(&in.EventTypeName, &in.EventTypeSlug, &in.LocationType, &in.OrganizerName, &in.OrganizerEmail, &in.OrganizerTimezone, &in.OrganizerLocale); err != nil {
		h.logger.ErrorContext(ctx, "payment: load confirmation input", "error", err, "booking_id", bookingID)
		return
	}
	// Run the same side-effects a free booking gets (calendar, emails, Zoom/Meet, webhooks) in
	// the background, so the webhook is acknowledged immediately.
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

// refundBookingPayment issues a Stripe refund when a PAID booking is cancelled.
// Two concurrent cancels must not double-refund, and a Stripe failure must not
// silently keep the money: the row is claimed first with a conditional UPDATE
// (only one claimant wins), the Stripe call carries an idempotency key so a
// retried request returns the original refund, and a failed Stripe call reverts
// the claim so a later cancel retries instead of losing the refund.
// For MakeCommerce, intentID is actually the transaction ID.
func (h *Handler) refundBookingPayment(ctx context.Context, bookingID string) {
	sc := h.getStripe()
	if sc == nil {
		return
	}
	var intentID string
	// For MakeCommerce, we store the transaction ID in stripe_payment_intent_id
	if err := h.db.QueryRowContext(ctx,
		`SELECT COALESCE(stripe_payment_intent_id, '') FROM bookings WHERE id = ?`,
		bookingID).Scan(&intentID); err != nil {
		return
	}
	if intentID == "" {
		return
	}
	// Claim the refund: only a row still marked paid flips to refunding. A
	// concurrent cancel hitting the same row updates zero rows and stops.
	claimed, err := h.db.ExecContext(ctx,
		`UPDATE bookings SET payment_status = 'refunding'
		 WHERE id = ? AND payment_status = 'paid'`, bookingID)
	if err != nil {
		h.logger.ErrorContext(ctx, "payment: claim refund", "error", err, "booking_id", bookingID)
		return
	}
	if n, _ := claimed.RowsAffected(); n == 0 {
		return // already refunded/refunding, or never paid
	}
	if err := sc.Refund(ctx, intentID, "refund:"+bookingID); err != nil {
		h.logger.ErrorContext(ctx, "payment: refund on cancel", "error", err, "booking_id", bookingID)
		// Revert the claim so a later cancel retries; the money was not moved.
		// If the revert itself fails, the row stays 'refunding' — still safe:
		// no other cancel will claim it, and the error is logged above.
		_, _ = h.db.ExecContext(ctx,
			`UPDATE bookings SET payment_status = 'paid'
			 WHERE id = ? AND payment_status = 'refunding'`, bookingID)
		return
	}
	if _, err := h.db.ExecContext(ctx,
		`UPDATE bookings SET payment_status = 'refunded'
		 WHERE id = ? AND payment_status = 'refunding'`, bookingID); err != nil {
		h.logger.ErrorContext(ctx, "payment: mark refunded", "error", err, "booking_id", bookingID)
	}
}
