package webhook

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/calnode/calnode/internal/uid"
)

var ErrNotFound = errors.New("webhook: not found")

type Webhook struct {
	ID        string
	UserID    string
	URL       string
	Events    []string
	IsActive  bool
	CreatedAt time.Time
}

type Delivery struct {
	ID              string
	WebhookID       string
	BookingID       string
	Event           string
	Status          string
	ResponseStatus  *int
	AttemptCount    int
	LastAttemptedAt *string
}

// BookingPayload is the "data" object inside every webhook envelope.
type BookingPayload struct {
	ID                 string `json:"id"`
	EventTypeSlug      string `json:"event_type_slug"`
	HostID             string `json:"host_id"`
	StartAt            string `json:"start_at"`
	EndAt              string `json:"end_at"`
	Status             string `json:"status"`
	CancellationReason string `json:"cancellation_reason,omitempty"`
	LocationValue      string `json:"location_value,omitempty"`
	CreatedAt          string `json:"created_at"`
	PreviousStartAt    string `json:"previous_start_at,omitempty"`
	PreviousEndAt      string `json:"previous_end_at,omitempty"`
}

type Service struct {
	db  *sql.DB
	key [32]byte
}

// New creates a Service. If encKeyHex is empty an ephemeral key is generated
// (secrets won't survive restarts but the server still works in dev/test).
func New(db *sql.DB, encKeyHex string) (*Service, error) {
	s := &Service{db: db}
	if encKeyHex != "" {
		b, err := hex.DecodeString(encKeyHex)
		if err != nil || len(b) != 32 {
			return nil, fmt.Errorf("webhook: encryption key must be 64 hex chars")
		}
		copy(s.key[:], b)
	} else {
		if _, err := io.ReadFull(rand.Reader, s.key[:]); err != nil {
			return nil, fmt.Errorf("webhook: generate ephemeral key: %w", err)
		}
	}
	return s, nil
}

// Create registers a new webhook and returns the Webhook plus the plain-text
// signing secret (shown only once; stored encrypted).
func (s *Service) Create(ctx context.Context, userID, url string, events []string) (*Webhook, string, error) {
	rawSecret := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, rawSecret); err != nil {
		return nil, "", fmt.Errorf("webhook: generate secret: %w", err)
	}
	plainSecret := hex.EncodeToString(rawSecret)

	encSecret, err := s.encrypt(rawSecret)
	if err != nil {
		return nil, "", fmt.Errorf("webhook: encrypt secret: %w", err)
	}

	eventsJSON, err := json.Marshal(events)
	if err != nil {
		return nil, "", fmt.Errorf("webhook: marshal events: %w", err)
	}

	id := uid.New()
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO webhooks (id, user_id, url, events, secret_enc)
		VALUES (?, ?, ?, ?, ?)`,
		id, userID, url, string(eventsJSON), encSecret)
	if err != nil {
		return nil, "", fmt.Errorf("webhook: insert: %w", err)
	}

	wh := &Webhook{
		ID:        id,
		UserID:    userID,
		URL:       url,
		Events:    events,
		IsActive:  true,
		CreatedAt: time.Now().UTC(),
	}
	return wh, plainSecret, nil
}

// List returns all webhooks for a user, most recent first.
func (s *Service) List(ctx context.Context, userID string) ([]Webhook, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, url, events, is_active, created_at
		FROM webhooks WHERE user_id = ? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("webhook: list: %w", err)
	}
	defer rows.Close()

	var out []Webhook
	for rows.Next() {
		var wh Webhook
		var eventsJSON string
		var isActive int
		var createdAt string
		if err := rows.Scan(&wh.ID, &wh.URL, &eventsJSON, &isActive, &createdAt); err != nil {
			return nil, fmt.Errorf("webhook: scan: %w", err)
		}
		wh.UserID = userID
		wh.IsActive = isActive == 1
		_ = json.Unmarshal([]byte(eventsJSON), &wh.Events)
		if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
			wh.CreatedAt = t
		}
		out = append(out, wh)
	}
	return out, rows.Err()
}

// Delete removes a webhook owned by userID. Returns ErrNotFound if it doesn't exist.
func (s *Service) Delete(ctx context.Context, userID, id string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM webhooks WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return fmt.Errorf("webhook: delete: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Enqueue finds all active webhooks for p.HostID that subscribe to event,
// and creates a webhook_deliveries + jobs row pair for each. Failures are
// soft-errors (caller logs; a booking is already committed).
func (s *Service) Enqueue(ctx context.Context, event string, p BookingPayload) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, events FROM webhooks
		WHERE user_id = ? AND is_active = 1`, p.HostID)
	if err != nil {
		return fmt.Errorf("webhook: list for enqueue: %w", err)
	}

	type wrow struct {
		id     string
		events []string
	}
	var matching []wrow
	for rows.Next() {
		var r wrow
		var eventsJSON string
		if err := rows.Scan(&r.id, &eventsJSON); err != nil {
			rows.Close()
			return fmt.Errorf("webhook: scan: %w", err)
		}
		_ = json.Unmarshal([]byte(eventsJSON), &r.events)
		for _, e := range r.events {
			if e == event {
				matching = append(matching, r)
				break
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(matching) == 0 {
		return nil
	}

	envelope := map[string]any{
		"event":      event,
		"created_at": time.Now().UTC().Format(time.RFC3339),
		"data":       p,
	}
	payloadBytes, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("webhook: marshal payload: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("webhook: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	for _, wh := range matching {
		deliveryID := uid.New()
		jobID := uid.New()

		// booking_id is a nullable FK reference; use nil (NULL) when empty
		// so callers without a real bookings row don't violate the constraint.
		var bookingIDArg interface{}
		if p.ID != "" {
			bookingIDArg = p.ID
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO webhook_deliveries (id, webhook_id, booking_id, event, payload, status)
			VALUES (?, ?, ?, ?, ?, 'pending')`,
			deliveryID, wh.id, bookingIDArg, event, string(payloadBytes)); err != nil {
			return fmt.Errorf("webhook: insert delivery: %w", err)
		}

		jobPayload, _ := json.Marshal(map[string]string{"webhook_delivery_id": deliveryID})
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO jobs (id, type, payload, run_at)
			VALUES (?, 'webhook.deliver', ?, ?)`,
			jobID, string(jobPayload), now); err != nil {
			return fmt.Errorf("webhook: insert job: %w", err)
		}
	}
	return tx.Commit()
}

// ListDeliveries returns the 50 most recent deliveries for a webhook.
func (s *Service) ListDeliveries(ctx context.Context, userID, webhookID string) ([]Delivery, error) {
	var ownerID string
	if err := s.db.QueryRowContext(ctx,
		`SELECT user_id FROM webhooks WHERE id = ?`, webhookID).Scan(&ownerID); err != nil {
		return nil, ErrNotFound
	}
	if ownerID != userID {
		return nil, ErrNotFound
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, COALESCE(booking_id,''), event, status,
		       response_status, attempt_count, last_attempted_at
		FROM webhook_deliveries WHERE webhook_id = ?
		ORDER BY rowid DESC LIMIT 50`, webhookID)
	if err != nil {
		return nil, fmt.Errorf("webhook: list deliveries: %w", err)
	}
	defer rows.Close()

	var out []Delivery
	for rows.Next() {
		var d Delivery
		var respStatus sql.NullInt64
		var lastAt sql.NullString
		if err := rows.Scan(&d.ID, &d.BookingID, &d.Event, &d.Status,
			&respStatus, &d.AttemptCount, &lastAt); err != nil {
			return nil, fmt.Errorf("webhook: scan delivery: %w", err)
		}
		d.WebhookID = webhookID
		if respStatus.Valid {
			v := int(respStatus.Int64)
			d.ResponseStatus = &v
		}
		if lastAt.Valid && lastAt.String != "" {
			s := lastAt.String
			d.LastAttemptedAt = &s
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DecryptSecret decrypts the stored secret_enc for use by the worker when signing deliveries.
func (s *Service) DecryptSecret(encSecret string) ([]byte, error) {
	return s.decrypt(encSecret)
}

// Sign returns the HMAC-SHA256 signature header value for a payload.
func Sign(secret, payload []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func (s *Service) encrypt(plaintext []byte) (string, error) {
	block, err := aes.NewCipher(s.key[:])
	if err != nil {
		return "", fmt.Errorf("webhook: cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("webhook: gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("webhook: nonce: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func (s *Service) decrypt(encoded string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("webhook: base64: %w", err)
	}
	block, err := aes.NewCipher(s.key[:])
	if err != nil {
		return nil, fmt.Errorf("webhook: cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("webhook: gcm: %w", err)
	}
	ns := gcm.NonceSize()
	if len(b) < ns {
		return nil, fmt.Errorf("webhook: ciphertext too short")
	}
	plain, err := gcm.Open(nil, b[:ns], b[ns:], nil)
	if err != nil {
		return nil, fmt.Errorf("webhook: decrypt: %w", err)
	}
	return plain, nil
}
