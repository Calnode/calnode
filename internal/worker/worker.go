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

	"github.com/calnode/calnode/internal/mailer"
	"github.com/calnode/calnode/internal/netutil"
	"github.com/calnode/calnode/internal/webhook"
)

// Worker polls the jobs table and processes pending jobs (webhooks, reminders).
type Worker struct {
	db         *sql.DB
	svc        *webhook.Service
	mailer     mailer.Mailer
	logger     *slog.Logger
	httpClient *http.Client
	done       chan struct{}
}

// WithHTTPClient overrides the default SSRF-safe HTTP client. Intended for testing only.
func WithHTTPClient(c *http.Client) func(*Worker) {
	return func(w *Worker) { w.httpClient = c }
}

// WithMailer configures the mailer used to send reminder emails.
func WithMailer(m mailer.Mailer) func(*Worker) {
	return func(w *Worker) { w.mailer = m }
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
		mailer: &mailer.Noop{},
		logger: logger,
		httpClient: &http.Client{
			Timeout:   10 * time.Second,
			Transport: transport,
		},
		done: make(chan struct{}),
	}
	for _, o := range opts {
		o(w)
	}
	return w
}

// Run polls for pending jobs every 5 seconds until ctx is cancelled.
// When ctx is cancelled the current Poll cycle (if any) runs to completion
// before Run returns, so in-progress jobs are not abandoned mid-delivery.
// Call Wait to block until Run has exited.
func (w *Worker) Run(ctx context.Context) {
	defer close(w.done)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Poll uses a background context so that a shutdown signal does not
			// cancel an in-progress webhook delivery or reminder email mid-flight.
			w.Poll(context.Background())
		}
	}
}

// Wait blocks until Run has returned. It returns immediately if Run was never
// started or has already exited.
func (w *Worker) Wait() {
	<-w.done
}

// Poll processes one batch of pending jobs. Exported for testing.
func (w *Worker) Poll(ctx context.Context) {
	now := time.Now().UTC().Format(time.RFC3339)

	// Purge expired manage tokens and sessions to keep tables small.
	if _, err := w.db.ExecContext(ctx,
		`DELETE FROM booking_manage_tokens WHERE expires_at < ?`, now); err != nil {
		w.logger.Error("worker: purge expired tokens", "error", err)
	}
	if _, err := w.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at < ?`, now); err != nil {
		w.logger.Error("worker: purge expired sessions", "error", err)
	}
	// Magic-link tokens are single-use + short-lived; sweep expired/consumed ones.
	if _, err := w.db.ExecContext(ctx,
		`DELETE FROM magic_link_tokens WHERE expires_at < ? OR used_at IS NOT NULL`, now); err != nil {
		w.logger.Error("worker: purge magic link tokens", "error", err)
	}
	// Idempotency keys are only useful for the retry window of the original
	// request; purge them 24h after creation so the table stays small.
	idemCutoff := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	if _, err := w.db.ExecContext(ctx,
		`DELETE FROM idempotency_keys WHERE created_at < ?`, idemCutoff); err != nil {
		w.logger.Error("worker: purge idempotency keys", "error", err)
	}
	// Expired MCP OAuth authorization codes are single-use and short-lived; sweep the
	// abandoned ones so the table doesn't accumulate dead rows.
	if _, err := w.db.ExecContext(ctx,
		`DELETE FROM oauth_auth_codes WHERE expires_at < ?`, now); err != nil {
		w.logger.Error("worker: purge oauth auth codes", "error", err)
	}
	// Backstop for the Stripe checkout.session.expired webhook: release any payment hold
	// still pending well past the 31-min checkout window, freeing the slot. The webhook
	// normally does this promptly; this catches missed/late deliveries.
	holdCutoff := time.Now().UTC().Add(-45 * time.Minute).Format(time.RFC3339)
	if _, err := w.db.ExecContext(ctx,
		`UPDATE bookings SET status = 'cancelled', cancellation_reason = 'payment not completed'
		 WHERE status = 'confirmed' AND payment_status = 'pending' AND created_at < ?`, holdCutoff); err != nil {
		w.logger.Error("worker: release expired payment holds", "error", err)
	}

	// Reaper: handle running jobs whose lock has expired (process crashed mid-job).
	// Jobs with retries remaining are reset to pending with a 1-minute delay so
	// they do not immediately re-enter this Poll cycle. Jobs that have already
	// exhausted max_attempts are marked failed directly.
	reaperRunAt := time.Now().UTC().Add(time.Minute).Format(time.RFC3339)
	if _, err := w.db.ExecContext(ctx, `
		UPDATE jobs SET status = 'pending', run_at = ?, last_error = 'recovered after crash'
		WHERE status = 'running' AND locked_until < ? AND attempts < max_attempts`,
		reaperRunAt, now); err != nil {
		w.logger.Error("worker: reaper: reset", "error", err)
	}
	if _, err := w.db.ExecContext(ctx, `
		UPDATE jobs SET status = 'failed', last_error = 'max attempts exceeded after crash'
		WHERE status = 'running' AND locked_until < ? AND attempts >= max_attempts`, now); err != nil {
		w.logger.Error("worker: reaper: fail exhausted", "error", err)
	}

	rows, err := w.db.QueryContext(ctx, `
		SELECT id, type, payload, attempts, max_attempts
		FROM jobs
		WHERE status = 'pending' AND run_at <= ?
		LIMIT 10`, now)
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
		lockedUntil := time.Now().UTC().Add(30 * time.Second).Format(time.RFC3339)
		res, err := w.db.ExecContext(ctx,
			`UPDATE jobs SET status = 'running', attempts = attempts + 1, locked_until = ?
			 WHERE id = ? AND status = 'pending'`,
			lockedUntil, j.id)
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
	case "reminder.send":
		return w.sendReminder(ctx, payload)
	default:
		return fmt.Errorf("worker: unknown job type %q", typ)
	}
}

func (w *Worker) sendReminder(ctx context.Context, payload string) error {
	var p struct {
		BookingID string `json:"booking_id"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return fmt.Errorf("worker: reminder: parse payload: %w", err)
	}

	// One query: join bookings → event_types → users (host).
	// Also load notify_reminder pref and msg_reminder custom note.
	// Skip if booking is deleted or no longer confirmed.
	var d mailer.BookingData
	d.BookingID = p.BookingID
	var startAt, endAt, status string
	var locVal, msgReminder, subjReminder sql.NullString
	var notifyReminder int
	err := w.db.QueryRowContext(ctx, `
		SELECT b.status, b.start_at, b.end_at, b.location_value,
		       et.name, et.slug, et.msg_reminder, et.subj_reminder,
		       u.name, u.email, COALESCE(u.notify_reminder, 1)
		FROM bookings b
		JOIN event_types et ON et.id = b.event_type_id
		JOIN users u ON u.id = et.user_id
		WHERE b.id = ?`, p.BookingID).
		Scan(&status, &startAt, &endAt, &locVal,
			&d.EventTypeName, &d.EventTypeSlug, &msgReminder, &subjReminder,
			&d.HostName, &d.HostEmail, &notifyReminder)
	if err == sql.ErrNoRows {
		return nil // booking deleted; skip silently
	}
	if err != nil {
		return fmt.Errorf("worker: reminder: load booking: %w", err)
	}
	if status != "confirmed" {
		return nil // cancelled or otherwise; skip silently
	}
	if notifyReminder == 0 {
		return nil // host has disabled reminder emails
	}

	var parseErr error
	if d.StartAt, parseErr = time.Parse(time.RFC3339, startAt); parseErr != nil {
		return fmt.Errorf("worker: reminder: parse start_at %q: %w", startAt, parseErr)
	}
	if d.EndAt, parseErr = time.Parse(time.RFC3339, endAt); parseErr != nil {
		return fmt.Errorf("worker: reminder: parse end_at %q: %w", endAt, parseErr)
	}
	if locVal.Valid {
		d.LocationValue = locVal.String
	}
	if msgReminder.Valid {
		d.CustomNote = msgReminder.String
	}
	if subjReminder.Valid {
		d.SubjectOverride = subjReminder.String
	}

	// Load organizer attendee.
	orgErr := w.db.QueryRowContext(ctx, `
		SELECT name, email, iana_timezone
		FROM booking_attendees WHERE booking_id = ? AND is_organizer = 1`, p.BookingID).
		Scan(&d.OrganizerName, &d.OrganizerEmail, &d.OrganizerTimezone)
	if orgErr == sql.ErrNoRows {
		return nil // no organizer attendee (data integrity gap); skip silently
	}
	if orgErr != nil {
		return fmt.Errorf("worker: reminder: load organizer: %w", orgErr)
	}

	// Brand the reminder email with the instance wordmark/logo.
	_ = w.db.QueryRowContext(ctx, `
		SELECT COALESCE(business_name,''), COALESCE(logo_url,'')
		FROM server_settings WHERE id = 1`).Scan(&d.BrandName, &d.LogoURL)

	if err := mailer.SendReminder(ctx, w.mailer, d); err != nil {
		return fmt.Errorf("worker: reminder: send: %w", err)
	}
	return nil
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
