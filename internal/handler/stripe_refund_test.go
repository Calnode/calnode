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
	"github.com/calnode/calnode/internal/stripe"
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
// produce a single Stripe refund call: the conditional claim UPDATE lets exactly one
// claimant through, and the Stripe call carries an idempotency key.
func TestRefundBookingPayment_claimsOnce(t *testing.T) {
	h, database, _ := newRefundTestSetup(t)

	var calls atomic.Int32
	var idemKey string
	refundTestClient(t, h, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		idemKey = r.Header.Get("Idempotency-Key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"re_test"}`))
	})

	ctx := context.Background()
	h.refundBookingPayment(ctx, "b-pay")
	h.refundBookingPayment(ctx, "b-pay") // second cancel: must not hit Stripe again

	if calls.Load() != 1 {
		t.Fatalf("Stripe refund called %d times; want 1", calls.Load())
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

// TestRefundBookingPayment_failureRevertsClaim proves a Stripe error leaves the
// booking payable-again: the claim reverts to paid so a later cancel retries
// instead of silently keeping the money.
func TestRefundBookingPayment_failureRevertsClaim(t *testing.T) {
	h, database, _ := newRefundTestSetup(t)

	refundTestClient(t, h, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
	})

	h.refundBookingPayment(context.Background(), "b-pay")
	var status string
	database.QueryRow(`SELECT payment_status FROM bookings WHERE id='b-pay'`).Scan(&status)
	if status != "paid" {
		t.Fatalf("payment_status = %q after Stripe failure; want paid (claim reverted)", status)
	}
}
