// Package stripe is a minimal, dependency-free client for the few Stripe REST calls the
// paid-booking flow needs: create a Checkout Session, verify a webhook signature, fetch a
// session, and refund a payment. One Stripe account per instance (instance-per-tenant), so
// there's no Connect/marketplace logic — the workspace keeps 100% of each charge.
package stripe

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const apiBaseDefault = "https://api.stripe.com"

// Client talks to one Stripe account using its secret key.
type Client struct {
	secretKey      string
	publishableKey string
	webhookSecret  string
	http           *http.Client
	apiBase        string // overridable in tests
}

// New builds a client from the account's secret + publishable keys and the webhook signing
// secret (whsec_…; may be empty until the admin registers the endpoint).
func New(secretKey, publishableKey, webhookSecret string) (*Client, error) {
	if secretKey == "" {
		return nil, fmt.Errorf("stripe: secret key required")
	}
	return &Client{
		secretKey:      secretKey,
		publishableKey: publishableKey,
		webhookSecret:  webhookSecret,
		http:           &http.Client{Timeout: 20 * time.Second},
		apiBase:        apiBaseDefault,
	}, nil
}

// PublishableKey returns the configured publishable key (safe to expose client-side).
func (c *Client) PublishableKey() string { return c.publishableKey }

// WebhookConfigured reports whether a signing secret is set (so the webhook can be verified).
func (c *Client) WebhookConfigured() bool { return c.webhookSecret != "" }

// VerifyWebhook verifies + parses a webhook payload using the configured signing secret.
func (c *Client) VerifyWebhook(payload []byte, sigHeader string, now time.Time) (*Event, error) {
	if c.webhookSecret == "" {
		return nil, fmt.Errorf("stripe: webhook signing secret not configured")
	}
	return ConstructEvent(payload, sigHeader, c.webhookSecret, now)
}

// CheckoutParams describes a one-off payment Checkout Session.
type CheckoutParams struct {
	AmountCents   int64
	Currency      string
	ProductName   string            // shown on the Checkout page (e.g. "30-min consultation")
	CustomerEmail string            // prefills the email field
	SuccessURL    string            // Stripe appends ?session_id={CHECKOUT_SESSION_ID} if present as a template
	CancelURL     string
	ExpiresAt     time.Time         // session expiry (Stripe requires 30 min – 24 h from now)
	Metadata      map[string]string // e.g. booking_id — echoed back on the webhook
}

// CheckoutSession is the subset of a Stripe Checkout Session we use.
type CheckoutSession struct {
	ID            string            `json:"id"`
	URL           string            `json:"url"`            // hosted Checkout page to redirect the booker to
	PaymentStatus string            `json:"payment_status"` // "paid" | "unpaid" | "no_payment_required"
	PaymentIntent string            `json:"payment_intent"` // set once paid (used for refunds)
	AmountTotal   int64             `json:"amount_total"`   // charged amount in minor units
	Currency      string            `json:"currency"`       // charged currency (lowercase)
	Metadata      map[string]string `json:"metadata"`
}

// CreateCheckoutSession creates a one-off payment session and returns it (incl. the hosted URL).
func (c *Client) CreateCheckoutSession(ctx context.Context, p CheckoutParams) (*CheckoutSession, error) {
	form := url.Values{}
	form.Set("mode", "payment")
	form.Set("success_url", p.SuccessURL)
	form.Set("cancel_url", p.CancelURL)
	if p.CustomerEmail != "" {
		form.Set("customer_email", p.CustomerEmail)
	}
	if !p.ExpiresAt.IsZero() {
		form.Set("expires_at", strconv.FormatInt(p.ExpiresAt.Unix(), 10))
	}
	form.Set("line_items[0][quantity]", "1")
	form.Set("line_items[0][price_data][currency]", strings.ToLower(p.Currency))
	form.Set("line_items[0][price_data][unit_amount]", strconv.FormatInt(p.AmountCents, 10))
	form.Set("line_items[0][price_data][product_data][name]", p.ProductName)
	for k, v := range p.Metadata {
		form.Set("metadata["+k+"]", v)
		// Also stamp the PaymentIntent so a refund/audit can trace back to the booking.
		form.Set("payment_intent_data[metadata]["+k+"]", v)
	}

	var sess CheckoutSession
	if err := c.do(ctx, http.MethodPost, "/v1/checkout/sessions", form, &sess); err != nil {
		return nil, err
	}
	return &sess, nil
}

// GetCheckoutSession fetches a session by id (used by the success page to confirm payment).
func (c *Client) GetCheckoutSession(ctx context.Context, id string) (*CheckoutSession, error) {
	var sess CheckoutSession
	if err := c.do(ctx, http.MethodGet, "/v1/checkout/sessions/"+url.PathEscape(id), nil, &sess); err != nil {
		return nil, err
	}
	return &sess, nil
}

// Refund issues a full refund for a PaymentIntent. A blank id is a no-op.
func (c *Client) Refund(ctx context.Context, paymentIntentID string) error {
	if paymentIntentID == "" {
		return nil
	}
	form := url.Values{}
	form.Set("payment_intent", paymentIntentID)
	var out struct {
		ID string `json:"id"`
	}
	return c.do(ctx, http.MethodPost, "/v1/refunds", form, &out)
}

// do performs a Stripe API call (form-encoded for POST) and decodes the JSON response.
func (c *Client) do(ctx context.Context, method, path string, form url.Values, out any) error {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, c.apiBase+path, body)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.secretKey, "")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("stripe: request: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		// Surface Stripe's error message when present.
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		msg := e.Error.Message
		if msg == "" {
			msg = strings.TrimSpace(string(raw))
		}
		return fmt.Errorf("stripe: %s %s returned %d: %s", method, path, resp.StatusCode, msg)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("stripe: decode response: %w", err)
		}
	}
	return nil
}

// Event is a minimal Stripe webhook event envelope.
type Event struct {
	ID   string          `json:"id"`
	Type string          `json:"type"`
	Data struct {
		Object json.RawMessage `json:"object"`
	} `json:"data"`
}

// Session parses the event's data.object as a Checkout Session (valid for
// checkout.session.* event types).
func (e *Event) Session() (*CheckoutSession, error) {
	var s CheckoutSession
	if err := json.Unmarshal(e.Data.Object, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// webhookTolerance is the max allowed clock skew between the signature timestamp and now.
const webhookTolerance = 5 * time.Minute

// ConstructEvent verifies a webhook payload against the Stripe-Signature header using the
// endpoint's signing secret (whsec_…), then parses it. Implements Stripe's signature scheme
// (HMAC-SHA256 over "<t>.<payload>", constant-time compare, timestamp tolerance) without the
// SDK. `now` is injected for testability — pass time.Now().
func ConstructEvent(payload []byte, sigHeader, secret string, now time.Time) (*Event, error) {
	var ts string
	var v1s []string
	for _, part := range strings.Split(sigHeader, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "t":
			ts = kv[1]
		case "v1":
			v1s = append(v1s, kv[1])
		}
	}
	if ts == "" || len(v1s) == 0 {
		return nil, fmt.Errorf("stripe: malformed signature header")
	}
	tsInt, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("stripe: bad signature timestamp")
	}
	if d := now.Sub(time.Unix(tsInt, 0)); d > webhookTolerance || d < -webhookTolerance {
		return nil, fmt.Errorf("stripe: signature timestamp outside tolerance")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(payload)
	expected := mac.Sum(nil)
	for _, v := range v1s {
		got, err := hex.DecodeString(v)
		if err != nil {
			continue
		}
		if hmac.Equal(got, expected) {
			var ev Event
			if err := json.Unmarshal(payload, &ev); err != nil {
				return nil, fmt.Errorf("stripe: decode event: %w", err)
			}
			return &ev, nil
		}
	}
	return nil, fmt.Errorf("stripe: signature verification failed")
}
