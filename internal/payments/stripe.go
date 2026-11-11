package payments

import (
	"context"
	"net/http"
	"time"

	"github.com/calnode/calnode/internal/stripe"
)

// StripeProvider adapts internal/stripe to the payments seam. It is the only
// implementation in step 1 of #246; a second gateway is step 4 and is gated on a real
// account to test against.
type StripeProvider struct {
	client *stripe.Client
}

// NewStripe builds the adapter. The signature mirrors stripe.New, so callers that
// already hold the three credentials do not change shape.
func NewStripe(secretKey, publishableKey, webhookSecret string) (*StripeProvider, error) {
	c, err := stripe.New(secretKey, publishableKey, webhookSecret)
	if err != nil {
		return nil, err
	}
	return &StripeProvider{client: c}, nil
}

// Name is the value persisted in bookings.payment_provider.
func (p *StripeProvider) Name() string { return "stripe" }

// Capabilities: Stripe's refund API is always available on a configured account, and
// webhooks are verifiable only once a signing secret is stored.
func (p *StripeProvider) Capabilities() Capabilities {
	return Capabilities{Refunds: true, Webhooks: p.client.WebhookConfigured()}
}

// WebhookConfigured reports whether a signing secret is present. Stripe-specific: the
// settings page uses it to warn before payments are enabled without one.
func (p *StripeProvider) WebhookConfigured() bool { return p.client.WebhookConfigured() }

// SetAPIBase overrides the Stripe API endpoint. Test-only, mirroring
// stripe.Client.SetAPIBase so handler tests can point at a local fake.
func (p *StripeProvider) SetAPIBase(url string) { p.client.SetAPIBase(url) }

// PublishableKey is the public key the settings page stores.
func (p *StripeProvider) PublishableKey() string { return p.client.PublishableKey() }

func (p *StripeProvider) CreateCheckout(ctx context.Context, req CheckoutRequest) (*CheckoutSession, error) {
	sess, err := p.client.CreateCheckoutSession(ctx, stripe.CheckoutParams{
		AmountCents:   req.AmountMinor,
		Currency:      req.Currency,
		ProductName:   req.ProductName,
		CustomerEmail: req.CustomerEmail,
		SuccessURL:    req.SuccessURL,
		CancelURL:     req.CancelURL,
		ExpiresAt:     req.ExpiresAt,
		Metadata:      req.Metadata,
	})
	if err != nil {
		return nil, err
	}
	return normalizeSession(sess), nil
}

func (p *StripeProvider) GetSession(ctx context.Context, sessionID string) (*CheckoutSession, error) {
	sess, err := p.client.GetCheckoutSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return normalizeSession(sess), nil
}

// ParseWebhook verifies the Stripe-Signature header and maps the two events Calnode
// acts on. Everything else becomes EventOther, which the handler acknowledges and
// ignores.
func (p *StripeProvider) ParseWebhook(payload []byte, headers http.Header, now time.Time) (*Event, error) {
	ev, err := p.client.VerifyWebhook(payload, headers.Get("Stripe-Signature"), now)
	if err != nil {
		return nil, err
	}

	kind := EventOther
	switch ev.Type {
	case "checkout.session.completed":
		kind = EventCheckoutCompleted
	case "checkout.session.expired":
		kind = EventCheckoutExpired
	default:
		return &Event{Kind: EventOther}, nil
	}

	event := &Event{Kind: kind}
	// A completed/expired session should carry one; if the payload cannot be decoded we
	// still return the kind so the handler can decide, exactly as before.
	if sess, serr := ev.Session(); serr == nil && sess != nil {
		event.Session = normalizeSession(sess)
	}
	return event, nil
}

func (p *StripeProvider) Refund(ctx context.Context, req RefundRequest) error {
	return p.client.Refund(ctx, req.PaymentRef, req.IdempotencyKey)
}

// normalizeSession maps Stripe's session onto the neutral shape. PaymentStatus is a
// string on Stripe's side; only "paid" means settled.
func normalizeSession(s *stripe.CheckoutSession) *CheckoutSession {
	if s == nil {
		return nil
	}
	return &CheckoutSession{
		ID:          s.ID,
		URL:         s.URL,
		Paid:        s.PaymentStatus == "paid",
		PaymentRef:  s.PaymentIntent,
		AmountMinor: s.AmountTotal,
		Currency:    s.Currency,
		Metadata:    s.Metadata,
	}
}
