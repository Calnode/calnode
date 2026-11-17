// Package payments defines the seam between Calnode's booking flow and whichever
// payment gateway actually takes the money.
//
// Step 1 of #246: the interface exists, Stripe is its only implementation, and the
// booking columns are provider-neutral. No behaviour changes. The point is that the
// handler no longer knows what "Stripe" is — it asks a Provider for a checkout, hands
// it a webhook payload, and asks for a refund.
//
// Two decisions are fixed here rather than left to each implementation:
//
//   - Money crosses the boundary as an integer count of the currency's minor units
//     (Stripe's model: 1050 USD = $10.50) plus an ISO currency code. An adapter whose
//     gateway wants a decimal string converts at its own edge, once. Nothing else in
//     Calnode should ever see a decimal amount.
//   - Refunds are a declared capability, not an assumption. A gateway without a refund
//     API returns Capabilities{Refunds: false} and the UI must say so rather than offer
//     a button that fails.
//
// Webhook verification is deliberately per-provider: the signature scheme and the
// event vocabulary differ, so each adapter normalizes its own payload into the small
// Event type below. The handler then switches on provider-neutral kinds.
package payments

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// ErrRefundNotSupported is returned by Refund when the provider has no refund API.
var ErrRefundNotSupported = errors.New("payments: provider does not support refunds")

// Capabilities describes what an implementation can actually do.
type Capabilities struct {
	// Refunds reports whether Refund() is implemented. False means the UI must not
	// offer a refund action for this provider.
	Refunds bool
	// Webhooks reports whether the provider is configured to verify webhook payloads
	// (a signing secret is present). Without it the endpoint refuses to act rather
	// than accepting unverified events.
	Webhooks bool
}

// CheckoutRequest is a one-off payment for a held booking.
type CheckoutRequest struct {
	AmountMinor   int64  // price in the currency's minor units (see the package doc)
	Currency      string // ISO 4217, any case; adapters normalize
	ProductName   string // shown on the hosted payment page
	CustomerEmail string
	SuccessURL    string
	CancelURL     string
	ExpiresAt     time.Time
	// Metadata is echoed back on the webhook. Calnode puts booking_id here and relies
	// on it to join the payment to the booking, so adapters must round-trip it.
	Metadata map[string]string
}

// CheckoutSession is the provider-neutral view of a hosted payment session.
type CheckoutSession struct {
	ID   string // provider session id, stored as bookings.payment_session_id
	URL  string // where to redirect the payer
	Paid bool   // whether the provider reports it settled
	// PaymentRef is the provider's reference for the money movement (Stripe's
	// PaymentIntent id). Stored as bookings.payment_ref; required to refund.
	PaymentRef  string
	AmountMinor int64
	Currency    string
	Metadata    map[string]string
}

// EventKind is the normalized webhook vocabulary. Providers map their own event
// names onto these; anything Calnode does not act on becomes EventOther.
type EventKind int

const (
	EventOther EventKind = iota
	EventCheckoutCompleted
	EventCheckoutExpired
)

// Event is a provider webhook reduced to what the booking flow reacts to.
type Event struct {
	Kind    EventKind
	Session *CheckoutSession // nil when the provider could not include the session
}

// RefundRequest asks for a full refund of one payment.
type RefundRequest struct {
	PaymentRef     string // CheckoutSession.PaymentRef
	IdempotencyKey string // e.g. "refund:<booking_id>"; makes retries safe
}

// Provider is one payment gateway.
//
// Implementations must be safe for concurrent use: a single Provider is shared by
// every request and hot-swapped from the settings page.
type Provider interface {
	// Name is the stable identifier persisted on a booking (e.g. "stripe").
	Name() string
	Capabilities() Capabilities
	CreateCheckout(ctx context.Context, req CheckoutRequest) (*CheckoutSession, error)
	GetSession(ctx context.Context, sessionID string) (*CheckoutSession, error)
	// ParseWebhook verifies the provider's signature over payload and normalizes the
	// event. headers carries the whole request header set because signature header
	// names differ per provider. A verification failure must return an error, never a
	// zero-value Event, so a forged call can never be mistaken for "nothing to do".
	ParseWebhook(payload []byte, headers http.Header, now time.Time) (*Event, error)
	Refund(ctx context.Context, req RefundRequest) error
}
