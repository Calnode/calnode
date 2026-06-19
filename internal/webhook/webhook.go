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
	"strings"
	"time"

	"github.com/calnode/calnode/internal/uid"
)

var ErrNotFound = errors.New("webhook: not found")

type Webhook struct {
	ID        string
	UserID    string
	URL       string
	Events    []string
	Fields    []string // payload field keys; nil means the default set
	IsActive  bool
	CreatedAt time.Time
}

// Payload field keys (the JSON keys in a delivery's "data" object).
const (
	FieldID              = "id"
	FieldStatus          = "status"
	FieldStartAt         = "start_at"
	FieldEndAt           = "end_at"
	FieldCreatedAt       = "created_at"
	FieldLocation        = "location_value"
	FieldCancelReason    = "cancellation_reason"
	FieldPreviousStartAt = "previous_start_at"
	FieldPreviousEndAt   = "previous_end_at"
	FieldEventTypeSlug   = "event_type_slug"
	FieldEventTypeName   = "event_type_name"
	FieldHostID          = "host_id"
	FieldHostName        = "host_name"
	FieldHostEmail       = "host_email"
	FieldAttendeeName    = "attendee_name"
	FieldAttendeeEmail   = "attendee_email"
	FieldAttendeeTZ      = "attendee_timezone"
	FieldAnswers         = "answers"
)

// AllFields is every selectable field, in payload order. Used to validate config
// and (UI) to render the checkbox list.
var AllFields = []string{
	FieldID, FieldStatus, FieldStartAt, FieldEndAt, FieldCreatedAt,
	FieldLocation, FieldCancelReason, FieldPreviousStartAt, FieldPreviousEndAt,
	FieldEventTypeSlug, FieldEventTypeName,
	FieldHostID, FieldHostName, FieldHostEmail,
	FieldAttendeeName, FieldAttendeeEmail, FieldAttendeeTZ, FieldAnswers,
}

// defaultFields reproduces the original payload exactly (no PII, no answers) so a
// webhook with no field config (fields IS NULL) keeps its historical shape.
var defaultFields = []string{
	FieldID, FieldEventTypeSlug, FieldHostID, FieldStartAt, FieldEndAt,
	FieldStatus, FieldLocation, FieldCancelReason, FieldCreatedAt,
	FieldPreviousStartAt, FieldPreviousEndAt,
}

var validField = func() map[string]bool {
	m := make(map[string]bool, len(AllFields))
	for _, f := range AllFields {
		m[f] = true
	}
	return m
}()

// ValidFields filters fields to known keys, preserving order. Used to sanitise
// caller-supplied config.
func ValidFields(fields []string) []string {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if validField[f] {
			out = append(out, f)
		}
	}
	return out
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
// Create registers a webhook (fields default to the unset/original-payload set;
// callers set field selection via Update).
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

// Update applies partial changes to a webhook owned by userID. Nil pointers are
// left unchanged. Returns ErrNotFound if no such webhook exists for the user.
func (s *Service) Update(ctx context.Context, userID, id string, events, fields *[]string) error {
	var set []string
	var args []any
	if events != nil {
		eb, _ := json.Marshal(*events)
		set = append(set, "events = ?")
		args = append(args, string(eb))
	}
	if fields != nil {
		fb, _ := json.Marshal(ValidFields(*fields))
		set = append(set, "fields = ?")
		args = append(args, string(fb))
	}
	if len(set) == 0 {
		return nil
	}
	args = append(args, id, userID)
	res, err := s.db.ExecContext(ctx,
		`UPDATE webhooks SET `+strings.Join(set, ", ")+` WHERE id = ? AND user_id = ?`, args...)
	if err != nil {
		return fmt.Errorf("webhook: update: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// List returns all webhooks for a user, most recent first.
func (s *Service) List(ctx context.Context, userID string) ([]Webhook, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, url, events, fields, is_active, created_at
		FROM webhooks WHERE user_id = ? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("webhook: list: %w", err)
	}
	defer rows.Close()

	var out []Webhook
	for rows.Next() {
		var wh Webhook
		var eventsJSON string
		var fieldsJSON sql.NullString
		var isActive int
		var createdAt string
		if err := rows.Scan(&wh.ID, &wh.URL, &eventsJSON, &fieldsJSON, &isActive, &createdAt); err != nil {
			return nil, fmt.Errorf("webhook: scan: %w", err)
		}
		wh.UserID = userID
		wh.IsActive = isActive == 1
		_ = json.Unmarshal([]byte(eventsJSON), &wh.Events)
		if fieldsJSON.Valid && fieldsJSON.String != "" {
			_ = json.Unmarshal([]byte(fieldsJSON.String), &wh.Fields)
		} else {
			wh.Fields = defaultFields // surface the effective set for unconfigured webhooks
		}
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

// enrichedBooking is the full set of values available to a webhook payload,
// gathered once per Enqueue and then filtered per-webhook by its field list.
type enrichedBooking struct {
	core                                     BookingPayload
	eventTypeName                            string
	hostName, hostEmail                      string
	attendeeName, attendeeEmail, attendeeTZ  string
	answers                                  []map[string]string
}

// enrich loads the data not carried in BookingPayload (host name/email, event-type
// name, attendee identity, intake answers) from the booking row. Best-effort: any
// query failure simply leaves those fields empty. Queries are drained sequentially,
// safe on the single-connection pool, and run before Enqueue opens its tx.
func (s *Service) enrich(ctx context.Context, p BookingPayload) enrichedBooking {
	bd := enrichedBooking{core: p, answers: []map[string]string{}}
	if p.ID == "" {
		return bd
	}
	_ = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(et.name,''), COALESCE(u.name,''), COALESCE(u.email,'')
		FROM bookings b
		JOIN event_types et ON et.id = b.event_type_id
		JOIN users u ON u.id = b.host_id
		WHERE b.id = ?`, p.ID).Scan(&bd.eventTypeName, &bd.hostName, &bd.hostEmail)

	_ = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(name,''), COALESCE(email,''), COALESCE(iana_timezone,'')
		FROM booking_attendees WHERE booking_id = ? AND is_organizer = 1`, p.ID).
		Scan(&bd.attendeeName, &bd.attendeeEmail, &bd.attendeeTZ)

	rows, err := s.db.QueryContext(ctx, `
		SELECT q.label, ba.value
		FROM booking_answers ba
		JOIN event_type_questions q ON q.id = ba.question_id
		WHERE ba.booking_id = ?
		ORDER BY q.position`, p.ID)
	if err == nil {
		for rows.Next() {
			var label, value string
			if err := rows.Scan(&label, &value); err == nil {
				bd.answers = append(bd.answers, map[string]string{"question": label, "answer": value})
			}
		}
		rows.Close()
	}
	return bd
}

// buildData renders the "data" object containing only the requested field keys.
// The four optional booking fields are omitted when empty (matching the original
// payload's omitempty behaviour); everything else is always included when selected.
func buildData(bd enrichedBooking, fields []string) map[string]any {
	always := map[string]any{
		FieldID:            bd.core.ID,
		FieldStatus:        bd.core.Status,
		FieldStartAt:       bd.core.StartAt,
		FieldEndAt:         bd.core.EndAt,
		FieldCreatedAt:     bd.core.CreatedAt,
		FieldEventTypeSlug: bd.core.EventTypeSlug,
		FieldEventTypeName: bd.eventTypeName,
		FieldHostID:        bd.core.HostID,
		FieldHostName:      bd.hostName,
		FieldHostEmail:     bd.hostEmail,
		FieldAttendeeName:  bd.attendeeName,
		FieldAttendeeEmail: bd.attendeeEmail,
		FieldAttendeeTZ:    bd.attendeeTZ,
		FieldAnswers:       bd.answers,
	}
	omitEmpty := map[string]string{
		FieldLocation:        bd.core.LocationValue,
		FieldCancelReason:    bd.core.CancellationReason,
		FieldPreviousStartAt: bd.core.PreviousStartAt,
		FieldPreviousEndAt:   bd.core.PreviousEndAt,
	}
	out := make(map[string]any, len(fields))
	for _, f := range fields {
		if v, ok := always[f]; ok {
			out[f] = v
		} else if v, ok := omitEmpty[f]; ok && v != "" {
			out[f] = v
		}
	}
	return out
}

// Enqueue finds all active webhooks for p.HostID that subscribe to event,
// and creates a webhook_deliveries + jobs row pair for each. Failures are
// soft-errors (caller logs; a booking is already committed).
func (s *Service) Enqueue(ctx context.Context, event string, p BookingPayload) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, events, fields FROM webhooks
		WHERE user_id = ? AND is_active = 1`, p.HostID)
	if err != nil {
		return fmt.Errorf("webhook: list for enqueue: %w", err)
	}

	type wrow struct {
		id     string
		fields []string // nil = default set
	}
	var matching []wrow
	for rows.Next() {
		var id, eventsJSON string
		var fieldsJSON sql.NullString
		if err := rows.Scan(&id, &eventsJSON, &fieldsJSON); err != nil {
			rows.Close()
			return fmt.Errorf("webhook: scan: %w", err)
		}
		var events []string
		_ = json.Unmarshal([]byte(eventsJSON), &events)
		subscribed := false
		for _, e := range events {
			if e == event {
				subscribed = true
				break
			}
		}
		if !subscribed {
			continue
		}
		var fields []string
		if fieldsJSON.Valid && fieldsJSON.String != "" {
			_ = json.Unmarshal([]byte(fieldsJSON.String), &fields)
		}
		matching = append(matching, wrow{id: id, fields: fields})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(matching) == 0 {
		return nil
	}

	// Gather all available data once; each webhook gets its own field-filtered copy.
	bd := s.enrich(ctx, p)
	createdAt := time.Now().UTC().Format(time.RFC3339)

	// booking_id is a nullable FK; use NULL when empty so callers without a real
	// bookings row don't violate the constraint.
	var bookingIDArg interface{}
	if p.ID != "" {
		bookingIDArg = p.ID
	}

	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("webhook: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	for _, wh := range matching {
		fieldset := wh.fields
		if len(fieldset) == 0 {
			fieldset = defaultFields
		}
		envelope := map[string]any{
			"event":      event,
			"created_at": createdAt,
			"data":       buildData(bd, fieldset),
		}
		payloadBytes, err := json.Marshal(envelope)
		if err != nil {
			return fmt.Errorf("webhook: marshal payload: %w", err)
		}

		deliveryID := uid.New()
		jobID := uid.New()
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
