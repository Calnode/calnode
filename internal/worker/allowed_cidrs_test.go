package worker_test

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/calnode/calnode/internal/webhook"
	"github.com/calnode/calnode/internal/worker"
)

// The production transport (no WithHTTPClient bypass) refuses a private receiver,
// and delivers to it once its network is in WEBHOOK_ALLOWED_CIDRS.
func TestWorker_webhookAllowedCIDRs(t *testing.T) {
	_, loopback, _ := net.ParseCIDR("127.0.0.0/8")
	_, other, _ := net.ParseCIDR("10.200.50.0/24")

	for _, tc := range []struct {
		name    string
		allowed []*net.IPNet
		want    string
	}{
		{"default guard", nil, "failed"},
		{"other network listed", []*net.IPNet{other}, "failed"},
		{"receiver's network listed", []*net.IPNet{loopback}, "success"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			database, svc := setup(t)
			ctx := context.Background()

			hits := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits++
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()

			wh, _, err := svc.Create(ctx, "host-01", srv.URL, []string{"booking.created"})
			if err != nil {
				t.Fatalf("create webhook: %v", err)
			}
			svc.Enqueue(ctx, "booking.created", webhook.BookingPayload{HostID: "host-01", Status: "confirmed"})

			w := worker.New(database, svc, slog.Default(), worker.WithWebhookAllowedCIDRs(tc.allowed))
			w.Poll(ctx)

			var status string
			database.QueryRowContext(ctx,
				`SELECT status FROM webhook_deliveries WHERE webhook_id = ?`, wh.ID).Scan(&status)
			if tc.want == "success" && (status != "success" || hits != 1) {
				t.Errorf("delivery status = %q, receiver hits = %d; want success, 1", status, hits)
			}
			if tc.want == "failed" && (status == "success" || hits != 0) {
				t.Errorf("delivery status = %q, receiver hits = %d; want refused before connecting", status, hits)
			}
		})
	}
}
