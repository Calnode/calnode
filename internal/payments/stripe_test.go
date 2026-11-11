package payments

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"
)

// signStripe builds a valid Stripe-Signature header for payload, so the adapter can be
// tested through its real verification path rather than a stub.
func signStripe(t *testing.T, secret string, payload []byte, at time.Time) string {
	t.Helper()
	ts := strconv.FormatInt(at.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(payload)
	return fmt.Sprintf("t=%s,v1=%s", ts, hex.EncodeToString(mac.Sum(nil)))
}

const testWebhookSecret = "whsec_test"

func webhookPayload(eventType, sessionJSON string) []byte {
	return []byte(fmt.Sprintf(`{"id":"evt_1","type":%q,"data":{"object":%s}}`, eventType, sessionJSON))
}

const paidSession = `{
	"id":"cs_test_1",
	"url":"https://checkout.stripe.test/cs_test_1",
	"payment_status":"paid",
	"payment_intent":"pi_test_1",
	"amount_total":2500,
	"currency":"eur",
	"metadata":{"booking_id":"bk_1"}
}`

func newTestProvider(t *testing.T) *StripeProvider {
	t.Helper()
	p, err := NewStripe("sk_test_x", "pk_test_x", testWebhookSecret)
	if err != nil {
		t.Fatalf("NewStripe: %v", err)
	}
	return p
}

func TestStripeProvider_Capabilities(t *testing.T) {
	withSecret := newTestProvider(t)
	got := withSecret.Capabilities()
	if !got.Refunds || !got.Webhooks {
		t.Errorf("Capabilities = %+v; want refunds and webhooks true", got)
	}

	// Without a signing secret the endpoint must refuse to act rather than accept
	// unverified events.
	noSecret, err := NewStripe("sk_test_x", "pk_test_x", "")
	if err != nil {
		t.Fatalf("NewStripe: %v", err)
	}
	if noSecret.Capabilities().Webhooks {
		t.Error("Webhooks = true without a signing secret; want false")
	}
	if noSecret.Name() != "stripe" {
		t.Errorf("Name = %q; want stripe", noSecret.Name())
	}
}

func TestStripeProvider_ParseWebhook_checkoutCompleted(t *testing.T) {
	p := newTestProvider(t)
	now := time.Unix(1_800_000_000, 0)
	payload := webhookPayload("checkout.session.completed", paidSession)

	ev, err := p.ParseWebhook(payload, http.Header{
		"Stripe-Signature": {signStripe(t, testWebhookSecret, payload, now)},
	}, now)
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if ev.Kind != EventCheckoutCompleted {
		t.Fatalf("Kind = %v; want EventCheckoutCompleted", ev.Kind)
	}
	if ev.Session == nil {
		t.Fatal("Session is nil")
	}
	if !ev.Session.Paid {
		t.Error("Paid = false; payment_status was paid")
	}
	if ev.Session.PaymentRef != "pi_test_1" {
		t.Errorf("PaymentRef = %q; want pi_test_1", ev.Session.PaymentRef)
	}
	if ev.Session.AmountMinor != 2500 {
		t.Errorf("AmountMinor = %d; want 2500", ev.Session.AmountMinor)
	}
	if ev.Session.Currency != "eur" {
		t.Errorf("Currency = %q; want eur", ev.Session.Currency)
	}
	if ev.Session.Metadata["booking_id"] != "bk_1" {
		t.Errorf("metadata booking_id = %q; want bk_1", ev.Session.Metadata["booking_id"])
	}
}

func TestStripeProvider_ParseWebhook_expiredAndOther(t *testing.T) {
	p := newTestProvider(t)
	now := time.Unix(1_800_000_000, 0)
	expired := `{"id":"cs_test_2","payment_status":"unpaid","metadata":{"booking_id":"bk_2"}}`

	payload := webhookPayload("checkout.session.expired", expired)
	ev, err := p.ParseWebhook(payload, http.Header{
		"Stripe-Signature": {signStripe(t, testWebhookSecret, payload, now)},
	}, now)
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if ev.Kind != EventCheckoutExpired {
		t.Fatalf("Kind = %v; want EventCheckoutExpired", ev.Kind)
	}
	if ev.Session == nil || ev.Session.Metadata["booking_id"] != "bk_2" {
		t.Errorf("Session = %+v; want booking_id bk_2", ev.Session)
	}

	// An event Calnode does not act on is acknowledged, not treated as a session.
	payload = webhookPayload("payment_intent.created", `{"id":"pi_x"}`)
	ev, err = p.ParseWebhook(payload, http.Header{
		"Stripe-Signature": {signStripe(t, testWebhookSecret, payload, now)},
	}, now)
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if ev.Kind != EventOther || ev.Session != nil {
		t.Errorf("got Kind=%v Session=%v; want EventOther with no session", ev.Kind, ev.Session)
	}
}

// A forged or replayed payload must fail verification and yield no event, never a
// zero-value one the handler could mistake for "nothing to do".
func TestStripeProvider_ParseWebhook_rejectsBadSignature(t *testing.T) {
	p := newTestProvider(t)
	now := time.Unix(1_800_000_000, 0)
	payload := webhookPayload("checkout.session.completed", paidSession)

	cases := map[string]http.Header{
		"missing header": {},
		"wrong secret":   {"Stripe-Signature": {signStripe(t, "whsec_other", payload, now)}},
		"wrong payload":  {"Stripe-Signature": {signStripe(t, testWebhookSecret, []byte(`{"id":"evt_evil"}`), now)}},
		"stale timestamp": {"Stripe-Signature": {
			signStripe(t, testWebhookSecret, payload, now.Add(-10*time.Minute)),
		}},
	}
	for name, headers := range cases {
		t.Run(name, func(t *testing.T) {
			ev, err := p.ParseWebhook(payload, headers, now)
			if err == nil {
				t.Fatalf("ParseWebhook accepted a bad signature: ev=%+v", ev)
			}
			if ev != nil {
				t.Errorf("event returned on verification failure: %+v", ev)
			}
		})
	}
}
