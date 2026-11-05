package handler

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/calnode/calnode/internal/db"
	stripe "github.com/calnode/calnode/internal/makecommerce"
)

func newRefundTestSetup(t *testing.T) (*Handler, *sql.DB, string) {
	t.Helper()
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	h := New(database, slog.Default())
	setupRec := httptest.NewRecorder()
	h.Setup(setupRec, httptest.NewRequest(http.MethodPost, "/v1/setup",
		strings.NewReader(`{"name":"Test Host","email":"host@example.com","timezone":"UTC"}`)))
	if setupRec.Code != http.StatusCreated {
		t.Fatalf("setup: %d — %s", setupRec.Code, setupRec.Body.String())
	}
	var ownerID string
	if err := database.QueryRow(`SELECT id FROM users WHERE email='host@example.com'`).Scan(&ownerID); err != nil {
		t.Fatalf("owner lookup: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO event_types (id,user_id,slug,name,duration_minutes) VALUES ('et-pay',?,'et-pay','E',30)`, ownerID); err != nil {
		t.Fatalf("event type: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO bookings (id,event_type_id,host_id,start_at,end_at,status,payment_status,stripe_payment_intent_id)
		VALUES ('b-pay','et-pay',?,'2099-01-01T10:00:00Z','2099-01-01T10:30:00Z','confirmed','paid','pi_test_123')`, ownerID); err != nil {
		t.Fatalf("booking: %v", err)
	}
	return h, database, ownerID
}

func refundTestClient(t *testing.T, h *Handler, handler func(w http.ResponseWriter, r *http.Request)) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(srv.Close)
	sc, err := stripe.New("sk_test_x", "", "")
	if err != nil {
		t.Fatal(err)
	}
	sc.SetAPIBase(srv.URL)
	h.SetStripe(sc)
}

// TestRefundBookingPayment_claimsOnce proves two cancels for the same paid booking
// produce a single refund call: the conditional claim UPDATE lets exactly one
// claimant through, and the refund call carries an idempotency key.
// For MakeCommerce, this also requires mocking the GetCheckoutSession endpoint.
func TestRefundBookingPayment_claimsOnce(t *testing.T) {
	h, database, _ := newRefundTestSetup(t)

	var refundCalls atomic.Int32
	var idemKey string
	refundTestClient(t, h, func(w http.ResponseWriter, r *http.Request) {
		// Only count POST /v1/refunds requests (not GET /v1/transactions/{id})
		if r.Method == http.MethodPost && r.URL.Path == "/v1/refunds" {
			refundCalls.Add(1)
			idemKey = r.Header.Get("Idempotency-Key")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"re_test"}`))
			return
		}
		// For GET /v1/transactions/{id}, return a mock transaction
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"pi_test_123","status":"COMPLETED","amount":"100.00","currency":"EUR"}`))
			return
		}
		// For other requests, return empty
		w.WriteHeader(http.StatusOK)
	})

	ctx := context.Background()
	h.refundBookingPayment(ctx, "b-pay")
	h.refundBookingPayment(ctx, "b-pay") // second cancel: must not hit refund endpoint again

	if refundCalls.Load() != 1 {
		t.Fatalf("refund endpoint called %d times; want 1", refundCalls.Load())
	}
	if idemKey != "refund:b-pay" {
		t.Fatalf("Idempotency-Key = %q; want refund:b-pay", idemKey)
	}
	var status string
	database.QueryRow(`SELECT payment_status FROM bookings WHERE id='b-pay'`).Scan(&status)
	if status != "refunded" {
		t.Fatalf("payment_status = %q; want refunded", status)
	}
}

// TestRefundBookingPayment_failureRevertsClaim proves a refund error leaves the
// booking payable-again: the claim reverts to paid so a later cancel retries
// instead of silently keeping the money.
func TestRefundBookingPayment_failureRevertsClaim(t *testing.T) {
	h, database, _ := newRefundTestSetup(t)

	refundTestClient(t, h, func(w http.ResponseWriter, r *http.Request) {
		// Mock GetCheckoutSession
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"pi_test_123","status":"COMPLETED","amount":"100.00","currency":"EUR"}`))
			return
		}
		// Mock Refund - return error
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
	})

	h.refundBookingPayment(context.Background(), "b-pay")
	var status string
	database.QueryRow(`SELECT payment_status FROM bookings WHERE id='b-pay'`).Scan(&status)
	if status != "paid" {
		t.Fatalf("payment_status = %q after refund failure; want paid (claim reverted)", status)
	}
}
