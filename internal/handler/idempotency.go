package handler

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strings"
	"time"
)

// idempotencyRecord is a previously-seen Idempotency-Key's stored outcome.
// StatusCode == 0 means the original request is still in flight (reserved but
// not yet finished).
type idempotencyRecord struct {
	RequestHash  string
	StatusCode   int
	ResponseBody string
}

// idemHash fingerprints a request body so a key reused with a different payload
// can be detected and rejected.
func idemHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// claimIdempotencyKey reserves key for a new request. If the key is unseen it
// inserts a pending row and returns (nil, false, nil) — the caller now owns it
// and must later call finishIdempotencyKey (on success) or releaseIdempotencyKey
// (on failure). If the key already exists it returns the stored record with
// replay=true; the caller replays rec or rejects it on a hash mismatch.
func (h *Handler) claimIdempotencyKey(ctx context.Context, key, reqHash string) (rec *idempotencyRecord, replay bool, err error) {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = h.db.ExecContext(ctx, `
		INSERT INTO idempotency_keys (idempotency_key, request_hash, created_at)
		VALUES (?, ?, ?)`, key, reqHash, now)
	if err == nil {
		return nil, false, nil
	}
	if !strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return nil, false, err
	}

	// The key already exists — read back its (possibly still-pending) outcome.
	var r idempotencyRecord
	var status sql.NullInt64
	var body sql.NullString
	if err := h.db.QueryRowContext(ctx, `
		SELECT request_hash, status_code, response_body FROM idempotency_keys
		WHERE idempotency_key = ?`, key).Scan(&r.RequestHash, &status, &body); err != nil {
		return nil, false, err
	}
	r.StatusCode = int(status.Int64)
	r.ResponseBody = body.String
	return &r, true, nil
}

// finishIdempotencyKey records the final response so future retries replay it.
func (h *Handler) finishIdempotencyKey(ctx context.Context, key string, status int, body []byte, bookingID string) error {
	_, err := h.db.ExecContext(ctx, `
		UPDATE idempotency_keys SET status_code = ?, response_body = ?, booking_id = ?
		WHERE idempotency_key = ?`, status, string(body), bookingID, key)
	return err
}

// releaseIdempotencyKey drops a reserved-but-unfinished key so the client may
// retry after a failed attempt. Best-effort: a leftover pending row is purged by
// the worker's TTL sweep regardless.
func (h *Handler) releaseIdempotencyKey(ctx context.Context, key string) {
	if _, err := h.db.ExecContext(ctx,
		`DELETE FROM idempotency_keys WHERE idempotency_key = ?`, key); err != nil {
		h.logger.Error("release idempotency key", "error", err)
	}
}
