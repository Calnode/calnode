package makecommerce

import (
	"context"
	"crypto/sha512"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// signMC produces a valid MakeCommerce MAC signature for the given data.
// Format: UPPERCASE(HEX(SHA-512(string(JSON) + string(Secret Key))))
// This matches the MakeCommerce documentation at:
// https://developer.makecommerce.net/guides/custom-api/RegularPaymentFlow#validation-of-mac-value
func signMC(secret, jsonStr string) string {
	// MakeCommerce MAC: UPPERCASE(HEX(SHA-512(string(JSON) + string(Secret Key))))
	message := jsonStr + secret
	hash := sha512.Sum512([]byte(message))
	return fmt.Sprintf("%X", hash)
}

func TestNew(t *testing.T) {
	// Valid client creation
	c, err := New("secret_key_123", "pub_key_456", "whsec_789")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.secretKey != "secret_key_123" {
		t.Errorf("secretKey = %q", c.secretKey)
	}
	if c.publicKey != "pub_key_456" {
		t.Errorf("publicKey = %q", c.publicKey)
	}
	if c.webhookSecret != "whsec_789" {
		t.Errorf("webhookSecret = %q", c.webhookSecret)
	}

	// Empty secret key should fail
	if _, err := New("", "pub", "wh"); err == nil {
		t.Error("expected error with empty secret key")
	}
}

func TestPublishableKey(t *testing.T) {
	c, _ := New("secret", "pub_key", "whsec")
	if c.PublishableKey() != "pub_key" {
		t.Errorf("PublishableKey = %q", c.PublishableKey())
	}
}

func TestWebhookConfigured(t *testing.T) {
	c, _ := New("secret", "pub", "")
	if c.WebhookConfigured() {
		t.Error("expected WebhookConfigured to be false with empty webhook secret")
	}

	c, _ = New("secret", "pub", "whsec")
	if !c.WebhookConfigured() {
		t.Error("expected WebhookConfigured to be true with webhook secret")
	}
}

func TestSetAPIBase(t *testing.T) {
	c, _ := New("secret", "pub", "whsec")
	c.SetAPIBase("https://api.test.example.com")
	if c.apiBase != "https://api.test.example.com" {
		t.Errorf("apiBase = %q", c.apiBase)
	}
}

func TestMapStatus(t *testing.T) {
	tests := []struct {
		mcStatus string
		want     string
	}{
		{"COMPLETED", "paid"},
		{"PENDING", "pending"},
		{"CREATED", "pending"},
		{"CANCELLED", "unpaid"},
		{"EXPIRED", "unpaid"},
		{"UNKNOWN", "unpaid"},
	}

	for _, tt := range tests {
		t.Run(tt.mcStatus, func(t *testing.T) {
			if got := mapStatus(tt.mcStatus); got != tt.want {
				t.Errorf("mapStatus(%q) = %q, want %q", tt.mcStatus, got, tt.want)
			}
		})
	}
}

func TestMapWebhookType(t *testing.T) {
	tests := []struct {
		status string
		want   string
	}{
		{"COMPLETED", "checkout.session.completed"},
		{"EXPIRED", "checkout.session.expired"},
		{"CANCELLED", "checkout.session.expired"},
		{"PENDING", "checkout.session.unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			if got := mapWebhookType(tt.status); got != tt.want {
				t.Errorf("mapWebhookType(%q) = %q, want %q", tt.status, got, tt.want)
			}
		})
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
		// Return a mock MakeCommerce transaction response with other as array
		io.WriteString(w, `{
			"id": "txn_123",
			"status": "CREATED",
			"net_amount": "50.00",
			"payment_methods": {
				"other": [
					{"name": "redirect", "url": "https://payment.maksekeskus.ee/pay.html?trx=txn_123"}
				]
			}
		}`)
	}))
	defer srv.Close()

	c, _ := New("sk_test_abc", "pk_test_abc", "whsec_test")
	c.apiBase = srv.URL

	sess, err := c.CreateCheckoutSession(context.Background(), CheckoutParams{
		AmountCents:   5000,
		Currency:      "EUR",
		ProductName:   "Intro call",
		CustomerEmail: "alex@example.com",
		SuccessURL:    "https://x/success",
		CancelURL:     "https://x/cancel",
		Metadata:      map[string]string{"booking_id": "bk_1"},
	})
	if err != nil {
		t.Fatalf("CreateCheckoutSession: %v", err)
	}

	// Verify the session was created correctly
	if sess.ID != "txn_123" {
		t.Errorf("session ID = %q, want txn_123", sess.ID)
	}
	if sess.URL != "https://payment.maksekeskus.ee/pay.html?trx=txn_123" {
		t.Errorf("session URL = %q", sess.URL)
	}
	if sess.PaymentStatus != "pending" {
		t.Errorf("payment status = %q, want pending", sess.PaymentStatus)
	}
	if sess.PaymentIntent != "txn_123" {
		t.Errorf("payment intent = %q, want txn_123", sess.PaymentIntent)
	}
	if sess.AmountTotal != 5000 {
		t.Errorf("amount total = %d, want 5000", sess.AmountTotal)
	}
	if sess.Currency != "eur" {
		t.Errorf("currency = %q, want eur", sess.Currency)
	}

	// Verify the request was made correctly
	if gotPath != "/v1/transactions" {
		t.Errorf("path = %q, want /v1/transactions", gotPath)
	}
	if gotAuth == "" {
		t.Error("missing basic auth")
	}

	// Verify the request body contains the expected data
	var reqBody map[string]interface{}
	if err := json.Unmarshal([]byte(gotBody), &reqBody); err != nil {
		t.Fatalf("failed to parse request body: %v", err)
	}

	txn, ok := reqBody["transaction"].(map[string]interface{})
	if !ok {
		t.Fatal("transaction field missing or not an object")
	}

	if txn["amount"] != "50.00" {
		t.Errorf("amount = %v, want 50.00", txn["amount"])
	}
	if txn["currency"] != "EUR" {
		t.Errorf("currency = %v, want EUR", txn["currency"])
	}
}

func TestCreateCheckoutSession_DefaultCurrency(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{
			"id": "txn_1",
			"status": "CREATED",
			"net_amount": "10.00",
			"payment_methods": {"other": [{"name": "redirect", "url": "https://payment.example.com/pay.html?trx=txn_1"}]}
		}`)
	}))
	defer srv.Close()

	c, _ := New("secret", "pub", "whsec")
	c.apiBase = srv.URL

	// Test with empty currency - should still work
	_, err := c.CreateCheckoutSession(context.Background(), CheckoutParams{
		AmountCents:   1000,
		Currency:      "", // Empty - should use default or as-is
		ProductName:   "Test",
		CustomerEmail: "test@example.com",
		SuccessURL:    "https://x/success",
		CancelURL:     "https://x/cancel",
		Metadata:      map[string]string{"booking_id": "bk_1"},
	})
	if err != nil {
		t.Fatalf("CreateCheckoutSession with empty currency: %v", err)
	}
}

func TestGetCheckoutSession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/transactions/txn_123" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{
			"id": "txn_123",
			"status": "COMPLETED",
			"amount": "100.00",
			"currency": "EUR",
			"reference": "booking_1"
		}`)
	}))
	defer srv.Close()

	c, _ := New("secret", "pub", "whsec")
	c.apiBase = srv.URL

	sess, err := c.GetCheckoutSession(context.Background(), "txn_123")
	if err != nil {
		t.Fatalf("GetCheckoutSession: %v", err)
	}

	if sess.ID != "txn_123" {
		t.Errorf("session ID = %q", sess.ID)
	}
	if sess.PaymentStatus != "paid" {
		t.Errorf("payment status = %q, want paid", sess.PaymentStatus)
	}
	if sess.AmountTotal != 10000 {
		t.Errorf("amount total = %d, want 10000", sess.AmountTotal)
	}
	if sess.Currency != "eur" {
		t.Errorf("currency = %q, want eur", sess.Currency)
	}
}

func TestRefund(t *testing.T) {
	// First, mock GetCheckoutSession for the Refund method
	var getCalled, refundCalled bool
	var refundBody string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/transactions/txn_123" {
			getCalled = true
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{
				"id": "txn_123",
				"status": "COMPLETED",
				"amount": "25.00",
				"currency": "EUR"
			}`)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/v1/refunds" {
			refundCalled = true
			b, _ := io.ReadAll(r.Body)
			refundBody = string(b)
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, `{"id": "ref_123", "status": "CREATED"}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c, _ := New("secret", "pub", "whsec")
	c.apiBase = srv.URL

	// Test successful refund
	err := c.Refund(context.Background(), "txn_123", "refund:book_1")
	if err != nil {
		t.Fatalf("Refund: %v", err)
	}

	if !getCalled {
		t.Error("GetCheckoutSession was not called")
	}
	if !refundCalled {
		t.Error("refund request was not made")
	}

	// Verify the refund request body
	var reqBody map[string]interface{}
	if err := json.Unmarshal([]byte(refundBody), &reqBody); err != nil {
		t.Fatalf("failed to parse refund body: %v", err)
	}

	if reqBody["transaction_id"] != "txn_123" {
		t.Errorf("transaction_id = %v, want txn_123", reqBody["transaction_id"])
	}
	// The amount should be "25.00" with 2 decimal places
	if reqBody["amount"] != "25.00" {
		t.Errorf("amount = %v, want 25.00", reqBody["amount"])
	}
	if reqBody["currency"] != "EUR" {
		t.Errorf("currency = %v, want EUR", reqBody["currency"])
	}
}

func TestRefund_EmptyPaymentIntent(t *testing.T) {
	c, _ := New("secret", "pub", "whsec")
	c.apiBase = "https://api.example.com"

	// Should return nil without error
	err := c.Refund(context.Background(), "", "refund:123")
	if err != nil {
		t.Errorf("Refund with empty paymentIntentID should return nil, got: %v", err)
	}
}

func TestVerifyWebhook(t *testing.T) {
	secret := "whsec_test"
	txnID := "f291af44-8d3c-4dc8-b4a9-31592023aae3" // Use transaction ID as string
	amount := "90.95"
	currency := "EUR"
	status := "COMPLETED"

	// Create JSON payload matching MakeCommerce documentation format
	// Based on: https://developer.makecommerce.net/guides/custom-api/RegularPaymentFlow#validation-of-mac-value
	jsonPayload := fmt.Sprintf(`{"amount":"%s","currency":"%s","message_time":"2024-11-13T12:03:48+0000","message_type":"payment_return","reference":"booking_123","shop":"1ad3a943-6958-430b-b58b-e55524a4e360","status":"%s","transaction":"%s"}`, amount, currency, status, txnID)

	// Create MAC using MakeCommerce format: SHA-512(JSON + Secret)
	mac := signMC(secret, jsonPayload)

	c, _ := New(secret, "pub", secret) // webhookSecret same as secret for MC

	// Create form-encoded payload manually to ensure exact encoding
	// json=<url-encoded-json>&mac=<mac-value>
	formPayload := "json=" + url.QueryEscape(jsonPayload) + "&mac=" + mac

	// Test with correct signature
	ev, err := c.VerifyWebhook([]byte(formPayload), "", time.Now())
	if err != nil {
		t.Fatalf("VerifyWebhook with correct signature: %v", err)
	}

	if ev.ID != txnID {
		t.Errorf("event ID = %q, want %q", ev.ID, txnID)
	}
	if ev.Type != "checkout.session.completed" {
		t.Errorf("event type = %q, want checkout.session.completed", ev.Type)
	}

	// Verify session can be parsed
	sess, err := ev.Session()
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if sess.PaymentStatus != "paid" {
		t.Errorf("session payment status = %q, want paid", sess.PaymentStatus)
	}
}

func TestVerifyWebhook_BadSignature(t *testing.T) {
	secret := "whsec_test"
	// Create JSON payload with transaction as string (per MakeCommerce docs)
	jsonPayload := `{"amount":"10.00","currency":"EUR","message_type":"payment_return","status":"COMPLETED","transaction":"txn_1"}`
	// Signed with a different secret
	mac := signMC("wrong_secret", jsonPayload)

	c, _ := New(secret, "pub", secret)

	formPayload := "json=" + url.QueryEscape(jsonPayload) + "&mac=" + mac

	if _, err := c.VerifyWebhook([]byte(formPayload), "", time.Now()); err == nil {
		t.Error("expected signature verification to fail with wrong secret")
	}
}

func TestVerifyWebhook_TamperedPayload(t *testing.T) {
	secret := "whsec_test"

	// Sign the original payload with transaction as string
	jsonPayload := `{"amount":"10.00","currency":"EUR","message_type":"payment_return","status":"COMPLETED","transaction":"txn_1"}`
	mac := signMC(secret, jsonPayload)

	c, _ := New(secret, "pub", secret)

	// Create tampered form payload
	tamperedJSON := `{"amount":"10.00","currency":"EUR","message_type":"payment_return","status":"COMPLETED","transaction":"txn_TAMPERED"}`
	formPayload := "json=" + url.QueryEscape(tamperedJSON) + "&mac=" + mac

	if _, err := c.VerifyWebhook([]byte(formPayload), "", time.Now()); err == nil {
		t.Error("expected verification to fail for tampered payload")
	}
}

func TestVerifyWebhook_NoSecret(t *testing.T) {
	c, _ := New("secret", "pub", "") // Use valid secret but empty webhook secret

	if _, err := c.VerifyWebhook([]byte(`{"transaction_id":"txn_1"}`), "some_sig", time.Now()); err == nil {
		t.Error("expected error with no webhook secret configured")
	}
}

func TestEventSession(t *testing.T) {
	event := &Event{
		ID:   "evt_1",
		Type: "checkout.session.completed",
		Data: struct {
			Object json.RawMessage `json:"object"`
		}{
			Object: []byte(`{"id":"cs_1","payment_status":"paid","payment_intent":"pi_1","amount_total":5000,"currency":"eur","metadata":{"booking_id":"bk_1"}}`),
		},
	}

	sess, err := event.Session()
	if err != nil {
		t.Fatalf("Session: %v", err)
	}

	if sess.ID != "cs_1" {
		t.Errorf("session ID = %q", sess.ID)
	}
	if sess.PaymentStatus != "paid" {
		t.Errorf("payment status = %q", sess.PaymentStatus)
	}
	if sess.PaymentIntent != "pi_1" {
		t.Errorf("payment intent = %q", sess.PaymentIntent)
	}
	if sess.AmountTotal != 5000 {
		t.Errorf("amount total = %d", sess.AmountTotal)
	}
	if sess.Currency != "eur" {
		t.Errorf("currency = %q", sess.Currency)
	}
	if sess.Metadata["booking_id"] != "bk_1" {
		t.Errorf("metadata booking_id = %q", sess.Metadata["booking_id"])
	}
}

func TestConstructEvent(t *testing.T) {
	secret := "whsec_test"
	txnID := "txn_123"
	amount := "50.00"
	currency := "EUR"
	status := "COMPLETED"

	// Create JSON payload with transaction as string
	jsonPayload := fmt.Sprintf(`{"amount":"%s","currency":"%s","message_type":"payment_return","status":"%s","transaction":"%s"}`, amount, currency, status, txnID)

	mac := signMC(secret, jsonPayload)

	// Create form-encoded payload
	formPayload := "json=" + url.QueryEscape(jsonPayload) + "&mac=" + mac

	ev, err := ConstructEvent([]byte(formPayload), "", secret, time.Now())
	if err != nil {
		t.Fatalf("ConstructEvent: %v", err)
	}

	if ev.ID != txnID {
		t.Errorf("event ID = %q", ev.ID)
	}
	if ev.Type != "checkout.session.completed" {
		t.Errorf("event type = %q", ev.Type)
	}
}

func TestSignatureCalculation(t *testing.T) {

	//payload := `json={"amount":"1.0","currency":"EUR","customer_name":"Tõõger Leõpäöld","merchant_data":null,"message_time":"2026-11-05T20:51:06+0000","message_type":"payment_return","reference":"Test - 0fa5ed8a-5311-43c3-9705-ad78c456c58c","shop":"f7741ab2-7445-45f9-9af4-0d0408ef1e4c","signature":"F6891653FD35AF105F8D2251B7CC27DBD11E9D8B80B835CF4D2E6194E273F04EA4DD9D8CB27AD5A0DF4A26C49344714EEA4424E9E751A40A38E38B9193EBB435","status":"COMPLETED","transaction":"24cec1e3-3101-4093-bf3b-0aa3d40cec6b"}&mac=DF3B2B21201DF358501088C48B80B77978F22DA41EE5B55F23907D7EB4C5566E34FD3634D8E4E9E61B303BD77BD3EAE1A95C17800FA57A55C4F5BB636D64F26C`
	payload := "json=%7B%22amount%22%3A%2290.95%22%2C%22currency%22%3A%22EUR%22%2C%22customer_name%22%3A%22ec1adce4-b485-49c3-acf1-319bf80d5e29%22%2C%22merchant_data%22%3A%22internalid321%22%2C%22message_time%22%3A%222024-11-13T12%3A03%3A48%2B0000%22%2C%22message_type%22%3A%22payment_return%22%2C%22reference%22%3A%22123abc%22%2C%22shop%22%3A%221ad3a943-6958-430b-b58b-e55524a4e360%22%2C%22signature%22%3A%2275DCA7CF1050E045C574D423B5A2E4BB62BC12B399F7E4415D20D4FBDD0DD95E62FFF2DFAAF135D6FCBAF1B01CD925F9C098409215DDCD6995B7FA89917FD919%22%2C%22status%22%3A%22COMPLETED%22%2C%22transaction%22%3A%22f291af44-8d3c-4dc8-b4a9-31592023aae3%22%7D&mac=E4812E5D0EE8FE6A158826C52EF0E45D13E3746A4CA0BE43562F09758C956E9D6C09671EF641C02BB967E5AFE2F0AFD4BB3749EE36315F624763A2CBBD5DF34A"

	secretKey := "pfOsGD9oPaFEILwqFLHEHkPf7vZz4j3t36nAcufP1abqT9l99koyuC1IWAOcBeqt"

	// Parse the payload (simulating what VerifyWebhook does)
	formValues, err := url.ParseQuery(payload)
	if err != nil {
		t.Fatalf("payload parsing failed: %v", err)
	}

	receivedJson := formValues.Get("json")
	receivedMac := formValues.Get("mac")
	calculatedSignature := CalculateSignature(receivedJson, secretKey)

	// Verify the MAC matches
	if receivedMac != calculatedSignature {
		t.Errorf("MAC verification failed: expected %.10s..., got %.10s...", calculatedSignature, receivedMac)
	}
}
