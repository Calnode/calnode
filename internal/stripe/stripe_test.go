package stripe

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"
)

// sign produces a valid Stripe-Signature header for payload at time t.
func sign(payload []byte, secret string, t time.Time) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(t.Unix(), 10)))
	mac.Write([]byte("."))
	mac.Write(payload)
	return fmt.Sprintf("t=%d,v1=%s", t.Unix(), hex.EncodeToString(mac.Sum(nil)))
}

func TestConstructEvent_valid(t *testing.T) {
	secret := "whsec_test"
	now := time.Unix(1_700_000_000, 0)
	payload := []byte(`{"id":"evt_1","type":"checkout.session.completed","data":{"object":{"id":"cs_1","payment_status":"paid","payment_intent":"pi_1","metadata":{"booking_id":"bk_1"}}}}`)
	ev, err := ConstructEvent(payload, sign(payload, secret, now), secret, now)
	if err != nil {
		t.Fatalf("ConstructEvent: %v", err)
	}
	if ev.Type != "checkout.session.completed" {
		t.Errorf("type = %q", ev.Type)
	}
	s, err := ev.Session()
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if s.PaymentStatus != "paid" || s.PaymentIntent != "pi_1" || s.Metadata["booking_id"] != "bk_1" {
		t.Errorf("session parsed wrong: %+v", s)
	}
}

func TestConstructEvent_badSignature(t *testing.T) {
	secret := "whsec_test"
	now := time.Unix(1_700_000_000, 0)
	payload := []byte(`{"id":"evt_1"}`)
	// Signed with a different secret → must fail.
	if _, err := ConstructEvent(payload, sign(payload, "whsec_other", now), secret, now); err == nil {
		t.Error("expected signature verification to fail with wrong secret")
	}
	// Tampered payload → must fail.
	good := sign(payload, secret, now)
	if _, err := ConstructEvent([]byte(`{"id":"evt_TAMPERED"}`), good, secret, now); err == nil {
		t.Error("expected verification to fail for tampered payload")
	}
}

func TestConstructEvent_staleTimestamp(t *testing.T) {
	secret := "whsec_test"
	signedAt := time.Unix(1_700_000_000, 0)
	payload := []byte(`{"id":"evt_1"}`)
	header := sign(payload, secret, signedAt)
	// Verify 10 minutes later — outside the 5-minute tolerance.
	later := signedAt.Add(10 * time.Minute)
	if _, err := ConstructEvent(payload, header, secret, later); err == nil {
		t.Error("expected verification to fail for a stale timestamp")
	}
}

func TestCreateCheckoutSession(t *testing.T) {
	var gotPath, gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"cs_123","url":"https://checkout.stripe.com/c/pay/cs_123","payment_status":"unpaid"}`)
	}))
	defer srv.Close()

	c, _ := New("sk_test_abc", "pk_test_abc", "whsec_test")
	c.apiBase = srv.URL
	sess, err := c.CreateCheckoutSession(context.Background(), CheckoutParams{
		AmountCents: 5000, Currency: "USD", ProductName: "Intro call",
		CustomerEmail: "alex@example.com",
		SuccessURL:    "https://x/success", CancelURL: "https://x/cancel",
		Metadata: map[string]string{"booking_id": "bk_1"},
	})
	if err != nil {
		t.Fatalf("CreateCheckoutSession: %v", err)
	}
	if sess.ID != "cs_123" || sess.URL == "" {
		t.Errorf("session = %+v", sess)
	}
	if gotPath != "/v1/checkout/sessions" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth == "" {
		t.Error("missing basic auth")
	}
	// Form-encoded body should carry the amount (lowercased currency) and metadata.
	vals, _ := url.ParseQuery(gotBody)
	if vals.Get("line_items[0][price_data][unit_amount]") != "5000" {
		t.Errorf("unit_amount = %q", vals.Get("line_items[0][price_data][unit_amount]"))
	}
	if vals.Get("line_items[0][price_data][currency]") != "usd" {
		t.Errorf("currency = %q", vals.Get("line_items[0][price_data][currency]"))
	}
	if vals.Get("metadata[booking_id]") != "bk_1" {
		t.Errorf("metadata booking_id = %q", vals.Get("metadata[booking_id]"))
	}
}
