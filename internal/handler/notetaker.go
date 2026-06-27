package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/llm"
	"github.com/calnode/calnode/internal/secret"
	"github.com/calnode/calnode/internal/stt"
	"github.com/calnode/calnode/internal/uid"
)

// notetakerSummaryPrompt is the code-owned system prompt for turning a transcript into notes.
const notetakerSummaryPrompt = `You are a meeting note-taker. You are given the transcript of a meeting; speakers are labelled "Speaker 0", "Speaker 1", etc. Produce concise, well-structured notes in Markdown using only the sections that apply:

## Summary
2-4 sentences on what the meeting covered and the outcome.

## Key points
- The main topics and important details discussed.

## Decisions
- Decisions that were made.

## Action items
- [ ] Task — owner (if identifiable) — due date (if mentioned).

Be faithful to the transcript and do not invent details. If it's too short or unclear to summarise, say so briefly.`

// notetakerEnabled reports whether AI notes from recordings are turned on.
func (h *Handler) notetakerEnabled(ctx context.Context) bool {
	var n int
	_ = h.db.QueryRowContext(ctx, `SELECT COALESCE(notetaker_enabled,0) FROM server_settings WHERE id = 1`).Scan(&n)
	return n != 0
}

// deepgramKey returns the decrypted Deepgram API key, or "" if unset/unconfigured.
func (h *Handler) deepgramKey(ctx context.Context) string {
	var enc string
	_ = h.db.QueryRowContext(ctx, `SELECT COALESCE(stt_api_key_enc,'') FROM server_settings WHERE id = 1`).Scan(&enc)
	if enc == "" {
		return ""
	}
	key, err := secret.Decrypt(h.encKey, enc)
	if err != nil {
		h.logger.ErrorContext(ctx, "notetaker: decrypt stt key", "error", err)
		return ""
	}
	return key
}

// enqueueJob inserts a background job for the worker. The jobs table has a UNIQUE(type, payload)
// index for dedup, so re-queuing the same (type, payload) — e.g. re-summarising a booking on a new
// recording or a manual regenerate — would otherwise fail the constraint. Instead, re-queue it:
// reset an existing matching job back to pending so the worker runs it again. (Notetaker-only;
// reminders/webhooks enqueue via their own paths.)
func (h *Handler) enqueueJob(ctx context.Context, typ string, payload any) error {
	b, _ := json.Marshal(payload)
	_, err := h.db.ExecContext(ctx, `
		INSERT INTO jobs (id, type, payload, run_at, status, attempts, max_attempts)
		VALUES (?, ?, ?, datetime('now'), 'pending', 0, 3)
		ON CONFLICT(type, payload) DO UPDATE SET
			status = 'pending', run_at = datetime('now'), attempts = 0,
			last_error = NULL, locked_until = NULL`,
		uid.New(), typ, string(b))
	return err
}

// maybeStartNotetaker is called when a recording finalizes (egress_ended): if the notetaker is on
// and both STT + an LLM are configured, enqueue transcription for that recording.
func (h *Handler) maybeStartNotetaker(ctx context.Context, recordingID string) {
	if recordingID == "" {
		return
	}
	if !h.notetakerEnabled(ctx) {
		h.logger.InfoContext(ctx, "notetaker: skip — disabled", "recording_id", recordingID)
		return
	}
	if h.deepgramKey(ctx) == "" {
		h.logger.WarnContext(ctx, "notetaker: skip — no Deepgram key set", "recording_id", recordingID)
		return
	}
	if h.getLLM() == nil {
		h.logger.WarnContext(ctx, "notetaker: skip — no LLM configured", "recording_id", recordingID)
		return
	}
	if err := h.enqueueJob(ctx, "notetaker.transcribe", map[string]string{"recording_id": recordingID}); err != nil {
		h.logger.ErrorContext(ctx, "notetaker: enqueue transcribe", "error", err, "recording_id", recordingID)
		return
	}
	h.logger.InfoContext(ctx, "notetaker: queued transcription", "recording_id", recordingID)
}

// JobNotetakerTranscribe (worker job) transcribes a finished recording via Deepgram, stores the
// transcript, and enqueues summarisation. Returns an error to retry on transient failures.
func (h *Handler) JobNotetakerTranscribe(ctx context.Context, payload string) error {
	var p struct {
		RecordingID string `json:"recording_id"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return err
	}
	var bookingID sql.NullString
	var room, objectKey string
	err := h.db.QueryRowContext(ctx,
		`SELECT booking_id, room, COALESCE(object_key,'') FROM recordings WHERE id = ?`, p.RecordingID).
		Scan(&bookingID, &room, &objectKey)
	if err == sql.ErrNoRows || objectKey == "" {
		return nil // nothing to transcribe
	}
	if err != nil {
		return err
	}
	key := h.deepgramKey(ctx)
	if key == "" {
		return nil
	}
	s3, ok := recordingStorage()
	if !ok {
		return nil
	}
	url := presignS3Get(s3, objectKey, time.Hour, timeNow())
	res, err := stt.NewDeepgram(key).TranscribeURL(ctx, url)
	if err != nil {
		return err // transient — retry
	}
	segs, _ := json.Marshal(res.Segments)
	if _, err := h.db.ExecContext(ctx, `
		INSERT INTO transcripts (id, booking_id, recording_id, room, text, segments, status)
		VALUES (?, ?, ?, ?, ?, ?, 'complete')`,
		uid.New(), bookingID, p.RecordingID, room, res.Text, string(segs)); err != nil {
		return err
	}
	if bookingID.Valid && bookingID.String != "" {
		if err := h.enqueueJob(ctx, "notetaker.summarize", map[string]string{"booking_id": bookingID.String}); err != nil {
			h.logger.ErrorContext(ctx, "notetaker: enqueue summarize", "error", err, "booking_id", bookingID.String)
		}
	}
	h.logger.InfoContext(ctx, "notetaker: transcribed", "recording_id", p.RecordingID, "chars", len(res.Text))
	return nil
}

// JobNotetakerSummarize (worker job) summarises a booking's transcript(s) into Markdown notes via
// the configured BYO-LLM. One notes doc per booking, regenerable.
func (h *Handler) JobNotetakerSummarize(ctx context.Context, payload string) error {
	var p struct {
		BookingID string `json:"booking_id"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return err
	}
	client := h.getLLM()
	if client == nil {
		return nil
	}
	// Concatenate the booking's transcripts (usually one). Materialize before any further query —
	// the DB pool is MaxOpenConns(1), so don't hold a cursor open.
	rows, err := h.db.QueryContext(ctx,
		`SELECT text FROM transcripts WHERE booking_id = ? AND status = 'complete' ORDER BY created_at`, p.BookingID)
	if err != nil {
		return err
	}
	var sb strings.Builder
	for rows.Next() {
		var t string
		if rows.Scan(&t) == nil && t != "" {
			sb.WriteString(t)
			sb.WriteString("\n\n")
		}
	}
	rows.Close()
	transcript := strings.TrimSpace(sb.String())
	if transcript == "" {
		return nil
	}
	res, err := client.Chat(ctx, llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "system", Content: notetakerSummaryPrompt},
			{Role: "user", Content: transcript},
		},
		// Generous budget: reasoning models (e.g. minimax) spend most of the completion inside
		// <think>; too small a cap means the output is all reasoning and stripReasoning empties it.
		MaxTokens: 8000,
	})
	if err != nil {
		return err // transient — retry
	}
	content := strings.TrimSpace(stripReasoning(res.Message.Content))
	if content == "" {
		// Don't fail silently: the transcript was fine but the model returned nothing usable
		// (commonly: all reasoning, no final answer / truncated). Surfaced via the warn log and the
		// notes status below so the UI can offer a regenerate.
		h.logger.WarnContext(ctx, "notetaker: summary empty after stripping reasoning",
			"booking_id", p.BookingID, "raw_chars", len(res.Message.Content))
		_, _ = h.db.ExecContext(ctx, `
			INSERT INTO notes (id, booking_id, content, status)
			VALUES (?, ?, '', 'empty')
			ON CONFLICT(booking_id) DO UPDATE SET status = 'empty', updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')`,
			uid.New(), p.BookingID)
		return nil
	}
	if _, err := h.db.ExecContext(ctx, `
		INSERT INTO notes (id, booking_id, content, status)
		VALUES (?, ?, ?, 'complete')
		ON CONFLICT(booking_id) DO UPDATE SET
			content = excluded.content, status = 'complete', updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')`,
		uid.New(), p.BookingID, content); err != nil {
		return err
	}
	h.logger.InfoContext(ctx, "notetaker: notes generated", "booking_id", p.BookingID, "chars", len(content))
	return nil
}
