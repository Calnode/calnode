package worker

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/calnode/calnode/internal/netutil"
	"github.com/calnode/calnode/internal/webhook"
)

// Worker polls the jobs table and processes pending webhook deliveries.
type Worker struct {
	db         *sql.DB
	svc        *webhook.Service
	logger     *slog.Logger
	httpClient *http.Client
}

// WithHTTPClient overrides the default SSRF-safe HTTP client. Intended for testing only.
func WithHTTPClient(c *http.Client) func(*Worker) {
	return func(w *Worker) { w.httpClient = c }
}

func New(db *sql.DB, svc *webhook.Service, logger *slog.Logger, opts ...func(*Worker)) *Worker {
	baseDialer := &net.Dialer{}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, fmt.Errorf("worker: split addr: %w", err)
			}
			addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, fmt.Errorf("worker: resolve %q: %w", host, err)
			}
			if len(addrs) == 0 {
				return nil, fmt.Errorf("worker: no addresses for %q", host)
			}
			for _, a := range addrs {
				if netutil.IsPrivateIP(a.IP) {
					// Log the specific IP internally; return a generic message so
					// the blocked address is not disclosed to webhook owners.
					logger.Warn("worker: webhook SSRF block", "host", host, "resolved_ip", a.IP)
					return nil, fmt.Errorf("worker: webhook target resolved to a blocked address")
				}
			}
			return baseDialer.DialContext(ctx, network, net.JoinHostPort(addrs[0].IP.String(), port))
		},
	}
	w := &Worker{
		db:     db,
		svc:    svc,
		logger: logger,
		httpClient: &http.Client{
			Timeout:   10 * time.Second,
			Transport: transport,
		},
	}
	for _, o := range opts {
		o(w)
	}
	return w
}

// Run polls for pending jobs every 5 seconds until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.Poll(ctx)
		}
	}
}

// Poll processes one batch of pending jobs. Exported for testing.
func (w *Worker) Poll(ctx context.Context) {
	rows, err := w.db.QueryContext(ctx, `
		SELECT id, type, payload, attempts, max_attempts
		FROM jobs
		WHERE status = 'pending' AND run_at <= ?
		LIMIT 10`, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		w.logger.Error("worker: poll", "error", err)
		return
	}

	type job struct {
		id, typ, payload    string
		attempts, maxAttempts int
	}
	var jobs []job
	for rows.Next() {
		var j job
		if err := rows.Scan(&j.id, &j.typ, &j.payload, &j.attempts, &j.maxAttempts); err != nil {
			w.logger.Error("worker: scan job", "error", err)
			continue
		}
		jobs = append(jobs, j)
	}
	rows.Close()

	for _, j := range jobs {
		res, err := w.db.ExecContext(ctx,
			`UPDATE jobs SET status = 'running', attempts = attempts + 1 WHERE id = ? AND status = 'pending'`,
			j.id)
		if err != nil {
			w.logger.Error("worker: claim job", "error", err, "job_id", j.id)
			continue
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue // claimed by another worker
		}
		j.attempts++

		if err := w.processJob(ctx, j.typ, j.payload); err != nil {
			w.logger.Error("worker: process job", "error", err, "job_id", j.id, "type", j.typ)
			if j.attempts >= j.maxAttempts {
				w.db.ExecContext(ctx,
					`UPDATE jobs SET status = 'failed', last_error = ? WHERE id = ?`,
					err.Error(), j.id)
			} else {
				runAt := time.Now().UTC().Add(backoff(j.attempts)).Format(time.RFC3339)
				w.db.ExecContext(ctx,
					`UPDATE jobs SET status = 'pending', last_error = ?, run_at = ? WHERE id = ?`,
					err.Error(), runAt, j.id)
			}
		} else {
			w.db.ExecContext(ctx, `UPDATE jobs SET status = 'done' WHERE id = ?`, j.id)
		}
	}
}

func backoff(attempt int) time.Duration {
	if attempt == 1 {
		return 60 * time.Second
	}
	return 5 * time.Minute
}

func (w *Worker) processJob(ctx context.Context, typ, payload string) error {
	switch typ {
	case "webhook.deliver":
		return w.deliverWebhook(ctx, payload)
	default:
		return fmt.Errorf("worker: unknown job type %q", typ)
	}
}

func (w *Worker) deliverWebhook(ctx context.Context, jobPayload string) error {
	var p struct {
		WebhookDeliveryID string `json:"webhook_delivery_id"`
	}
	if err := json.Unmarshal([]byte(jobPayload), &p); err != nil {
		return fmt.Errorf("worker: parse job payload: %w", err)
	}

	var (
		deliveryPayload string
		event           string
		webhookURL      string
		secretEnc       string
	)
	err := w.db.QueryRowContext(ctx, `
		SELECT d.payload, d.event, wh.url, wh.secret_enc
		FROM webhook_deliveries d
		JOIN webhooks wh ON wh.id = d.webhook_id
		WHERE d.id = ?`, p.WebhookDeliveryID).
		Scan(&deliveryPayload, &event, &webhookURL, &secretEnc)
	if err == sql.ErrNoRows {
		return nil // delivery or webhook deleted; skip silently
	}
	if err != nil {
		return fmt.Errorf("worker: fetch delivery: %w", err)
	}

	secret, err := w.svc.DecryptSecret(secretEnc)
	if err != nil {
		return fmt.Errorf("worker: decrypt secret: %w", err)
	}

	payloadBytes := []byte(deliveryPayload)
	mac := hmac.New(sha256.New, secret)
	mac.Write(payloadBytes)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL,
		bytes.NewReader(payloadBytes))
	if err != nil {
		return fmt.Errorf("worker: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Calnode-Signature", sig)
	req.Header.Set("X-Calnode-Event", event)
	req.Header.Set("X-Calnode-Delivery", p.WebhookDeliveryID)

	resp, err := w.httpClient.Do(req)
	now := time.Now().UTC().Format(time.RFC3339)
	if err != nil {
		w.db.ExecContext(ctx, `
			UPDATE webhook_deliveries
			SET status = 'failed', attempt_count = attempt_count + 1, last_attempted_at = ?
			WHERE id = ?`, now, p.WebhookDeliveryID)
		return fmt.Errorf("worker: http post: %w", err)
	}
	defer func() {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20)) //nolint:errcheck
		resp.Body.Close()
	}()

	status := "success"
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		status = "failed"
	}
	w.db.ExecContext(ctx, `
		UPDATE webhook_deliveries
		SET status = ?, response_status = ?, attempt_count = attempt_count + 1, last_attempted_at = ?
		WHERE id = ?`, status, resp.StatusCode, now, p.WebhookDeliveryID)

	if status == "failed" {
		return fmt.Errorf("worker: endpoint returned HTTP %d", resp.StatusCode)
	}
	return nil
}
