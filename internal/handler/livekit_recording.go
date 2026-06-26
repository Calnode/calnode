package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/livekit"
	"github.com/calnode/calnode/internal/uid"
)

// recordingStorage derives the recordings S3 destination from the Litestream backup env, so
// recordings reuse the same bucket (under a recordings/ prefix). ok=false when not configured.
func recordingStorage() (livekit.S3Config, bool) {
	replica := os.Getenv("LITESTREAM_REPLICA_URL")
	key := os.Getenv("LITESTREAM_ACCESS_KEY_ID")
	secret := os.Getenv("LITESTREAM_SECRET_ACCESS_KEY")
	if replica == "" || key == "" || secret == "" {
		return livekit.S3Config{}, false
	}
	bucket := strings.TrimPrefix(replica, "s3://") // s3://bucket/path → bucket
	if i := strings.IndexByte(bucket, '/'); i >= 0 {
		bucket = bucket[:i]
	}
	if bucket == "" {
		return livekit.S3Config{}, false
	}
	return livekit.S3Config{
		AccessKey: key,
		Secret:    secret,
		Region:    os.Getenv("LITESTREAM_REGION"),
		Endpoint:  os.Getenv("LITESTREAM_ENDPOINT"),
		Bucket:    bucket,
	}, true
}

// recordingsEnabled reports whether the admin has turned meeting recording on.
func (h *Handler) recordingsEnabled(ctx context.Context) bool {
	var n int
	_ = h.db.QueryRowContext(ctx, `SELECT COALESCE(recordings_enabled,0) FROM server_settings WHERE id = 1`).Scan(&n)
	return n != 0
}

// RecordingAvailable reports whether recording can be started (enabled + storage configured).
// Surfaced to the room so the Record button only shows when it'll actually work.
func (h *Handler) recordingAvailable(ctx context.Context) bool {
	if !h.recordingsEnabled(ctx) {
		return false
	}
	_, ok := recordingStorage()
	return ok
}

// RecordStart handles POST /v1/livekit/record/start (host token). Starts a room-composite egress
// to the backups bucket and flips the room's recording metadata on. Idempotent per room.
func (h *Handler) RecordStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"t"`
		At    string `json:"at"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	room, ok := h.authorizeHost(w, r, req.Token, req.At)
	if !ok {
		return
	}
	if !h.recordingsEnabled(r.Context()) {
		h.writeError(w, http.StatusForbidden, "recording is turned off — enable it in Settings → Video")
		return
	}
	s3, ok := recordingStorage()
	if !ok {
		h.writeError(w, http.StatusFailedDependency, "no storage is configured for recordings")
		return
	}
	lk := h.getLiveKit()

	// Idempotent: if this room is already recording, succeed without starting a second egress.
	var existing string
	_ = h.db.QueryRowContext(r.Context(),
		`SELECT egress_id FROM recordings WHERE room = ? AND status = 'active' LIMIT 1`, room).Scan(&existing)
	if existing != "" {
		h.mergeRoomMeta(r.Context(), room, "recording", true) // already recording — re-assert the banner
		h.writeJSON(w, http.StatusOK, map[string]any{"recording": true})
		return
	}

	filepath := "recordings/" + room + "/" + time.Now().UTC().Format("20060102T150405Z") + ".mp4"
	egressID, err := lk.StartRoomCompositeEgress(r.Context(), room, filepath, s3)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "livekit: start egress", "error", err, "room", room)
		h.writeError(w, http.StatusBadGateway, "could not start recording")
		return
	}
	h.logger.InfoContext(r.Context(), "livekit: egress started", "room", room, "egress_id", egressID, "filepath", filepath)
	bookingID := strings.TrimPrefix(room, "booking-")
	if _, err := h.db.ExecContext(r.Context(), `
		INSERT INTO recordings (id, booking_id, room, egress_id, status, object_key, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'active', ?, datetime('now'), datetime('now'))`,
		uid.New(), bookingID, room, egressID, filepath); err != nil {
		h.logger.ErrorContext(r.Context(), "livekit: save recording", "error", err)
	}
	h.mergeRoomMeta(r.Context(), room, "recording", true) // drives the consent banner
	h.writeJSON(w, http.StatusOK, map[string]any{"recording": true})
}

// RecordStop handles POST /v1/livekit/record/stop (host token). Stops the active egress and
// clears the recording metadata; the egress webhook finalizes the row when the file is ready.
func (h *Handler) RecordStop(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"t"`
		At    string `json:"at"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	room, ok := h.authorizeHost(w, r, req.Token, req.At)
	if !ok {
		return
	}
	h.finalizeActiveRecording(r.Context(), room)
	h.mergeRoomMeta(r.Context(), room, "recording", false)
	h.writeJSON(w, http.StatusOK, map[string]any{"recording": false})
}

// finalizeActiveRecording stops the room's active egress (if any) and marks its recordings row
// complete RIGHT NOW — rather than waiting on the egress webhook, which may not be registered in
// LiveKit. The object_key was set at start, so the recording stays listed and downloadable; the
// webhook, if it later arrives, just refines the duration. Without this a stopped recording's row
// stays 'active' forever and blocks the next record/start in the same room (idempotent no-op).
func (h *Handler) finalizeActiveRecording(ctx context.Context, room string) {
	lk := h.getLiveKit()
	if lk == nil {
		return
	}
	var egressID string
	_ = h.db.QueryRowContext(ctx,
		`SELECT egress_id FROM recordings WHERE room = ? AND status = 'active' LIMIT 1`, room).Scan(&egressID)
	if egressID == "" {
		return
	}
	if err := lk.StopEgress(ctx, egressID); err != nil {
		h.logger.ErrorContext(ctx, "livekit: stop egress", "error", err, "egress", egressID)
	}
	if _, err := h.db.ExecContext(ctx,
		`UPDATE recordings SET status = 'complete', updated_at = datetime('now')
		 WHERE room = ? AND status = 'active'`, room); err != nil {
		h.logger.ErrorContext(ctx, "livekit: close recording row", "error", err, "room", room)
	}
}

// RecordConsent handles POST /v1/livekit/consent — a participant's response to the recording
// notice (Zoom-style notice + consent-or-leave). It's an AUDIT LOG only: it records who
// acknowledged, but never gates recording. The caller's identity is proven by their LiveKit
// access token (`at`); the room comes from that token, not client-asserted.
func (h *Handler) RecordConsent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token    string `json:"t"`
		At       string `json:"at"`
		Decision string `json:"decision"` // continue | leave
		Name     string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	lk := h.getLiveKit()
	if lk == nil {
		h.writeError(w, http.StatusNotFound, "video meetings are not configured")
		return
	}
	room, identity, err := lk.VerifyAccessToken(req.At)
	if err != nil || room == "" || identity == "" {
		h.writeError(w, http.StatusForbidden, "invalid meeting token")
		return
	}
	decision := "continue"
	if req.Decision == "leave" {
		decision = "leave"
	}
	name := strings.TrimSpace(req.Name)
	if len(name) > 120 {
		name = name[:120]
	}
	if _, err := h.db.ExecContext(r.Context(), `
		INSERT INTO meeting_consents (room, participant_identity, name, decision)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(room, participant_identity) DO UPDATE SET
			name = excluded.name, decision = excluded.decision,
			decided_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')`,
		room, identity, name, decision); err != nil {
		h.logger.ErrorContext(r.Context(), "livekit: record consent", "error", err, "room", room)
		h.writeError(w, http.StatusInternalServerError, "could not record consent")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListRecordings handles GET /v1/recordings (admin) — newest first, for the Recordings page.
func (h *Handler) ListRecordings(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok || !user.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
		return
	}
	rows, err := h.db.QueryContext(r.Context(), `
		SELECT id, COALESCE(booking_id,''), room, status, duration_s, COALESCE(object_key,''), created_at
		FROM recordings ORDER BY created_at DESC LIMIT 200`)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "recordings: list", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()
	type rec struct {
		ID        string `json:"id"`
		BookingID string `json:"booking_id"`
		Room      string `json:"room"`
		Status    string `json:"status"`
		DurationS int    `json:"duration_s"`
		HasFile   bool   `json:"has_file"`
		CreatedAt string `json:"created_at"`
	}
	out := []rec{}
	for rows.Next() {
		var x rec
		var key string
		if err := rows.Scan(&x.ID, &x.BookingID, &x.Room, &x.Status, &x.DurationS, &key, &x.CreatedAt); err != nil {
			continue
		}
		x.HasFile = key != ""
		out = append(out, x)
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"recordings": out})
}

// DownloadRecording handles GET /v1/recordings/{id}/download (admin) — redirects to a short-lived
// presigned URL for the object in the bucket.
func (h *Handler) DownloadRecording(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok || !user.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
		return
	}
	var key string
	switch err := h.db.QueryRowContext(r.Context(),
		`SELECT COALESCE(object_key,'') FROM recordings WHERE id = ?`, r.PathValue("id")).Scan(&key); err {
	case nil:
	case sql.ErrNoRows:
		h.writeError(w, http.StatusNotFound, "recording not found")
		return
	default:
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if key == "" {
		h.writeError(w, http.StatusConflict, "this recording isn't ready yet")
		return
	}
	s3, okS3 := recordingStorage()
	if !okS3 {
		h.writeError(w, http.StatusFailedDependency, "recording storage is not configured")
		return
	}
	http.Redirect(w, r, presignS3Get(s3, key, 15*time.Minute, timeNow()), http.StatusFound)
}

// timeNow is a tiny seam so the presign is testable with a fixed clock.
var timeNow = func() time.Time { return time.Now() }

// LiveKitWebhook is the single sink for ALL LiveKit project webhook events (LiveKit only allows
// one URL per project), at POST /v1/livekit/webhook. Public, but every event is signature-verified
// with the API key/secret. Today it acts only on the recording-relevant events — egress_started/
// ended/failed (banner flag + finalize the recordings row) and room_finished (stop a straggling
// egress) — and 200-ACKs everything else (room_started, participant_joined/left, track_*, …)
// without acting on them. Lifecycle events (attendance, duration, etc.) are not yet wired up.
func (h *Handler) LiveKitWebhook(w http.ResponseWriter, r *http.Request) {
	lk := h.getLiveKit()
	if lk == nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err := lk.VerifyWebhook(r.Header.Get("Authorization"), body); err != nil {
		h.writeError(w, http.StatusForbidden, "invalid webhook signature")
		return
	}
	var ev struct {
		Event      string `json:"event"`
		Room       struct {
			Name string `json:"name"`
		} `json:"room"`
		EgressInfo struct {
			EgressID    string `json:"egress_id"`
			RoomName    string `json:"room_name"`
			Status      string `json:"status"`
			FileResults []struct {
				Filename string `json:"filename"`
				Duration int64  `json:"duration"` // nanoseconds
			} `json:"file_results"`
		} `json:"egress_info"`
	}
	_ = json.Unmarshal(body, &ev)
	// Room closed (host ended it, or everyone left) — stop + finalize any recording still running,
	// so it never outlives the meeting. (Requires the webhook to be registered in LiveKit.)
	if ev.Event == "room_finished" && ev.Room.Name != "" {
		h.finalizeActiveRecording(r.Context(), ev.Room.Name)
		w.WriteHeader(http.StatusOK)
		return
	}
	// The egress lifecycle is the source of truth for the recording banner: drive the room's
	// recording flag off the actual egress, so the indicator self-heals regardless of which code
	// path started/stopped it (no reliance on every caller remembering to clear the flag).
	if ev.Event == "egress_started" && ev.EgressInfo.RoomName != "" {
		h.mergeRoomMeta(r.Context(), ev.EgressInfo.RoomName, "recording", true)
		w.WriteHeader(http.StatusOK)
		return
	}
	if ev.Event == "egress_ended" || ev.Event == "egress_failed" {
		info := ev.EgressInfo
		status := "complete"
		if ev.Event == "egress_failed" || strings.Contains(strings.ToUpper(info.Status), "FAIL") {
			status = "failed"
		}
		var key string
		var durSec int64
		if len(info.FileResults) > 0 {
			key = info.FileResults[0].Filename
			durSec = info.FileResults[0].Duration / 1_000_000_000
		}
		if _, err := h.db.ExecContext(r.Context(), `
			UPDATE recordings SET status = ?, object_key = COALESCE(NULLIF(?,''), object_key),
			       duration_s = ?, updated_at = datetime('now') WHERE egress_id = ?`,
			status, key, durSec, info.EgressID); err != nil {
			h.logger.ErrorContext(r.Context(), "livekit: finalize recording", "error", err)
		}
		if info.RoomName != "" {
			h.mergeRoomMeta(r.Context(), info.RoomName, "recording", false) // clear the banner (no-op if the room is gone)
		}
		// Notetaker: the file is ready in S3 now — transcribe + summarise it (no-op unless enabled).
		if status == "complete" {
			var recID string
			_ = h.db.QueryRowContext(r.Context(), `SELECT id FROM recordings WHERE egress_id = ?`, info.EgressID).Scan(&recID)
			h.maybeStartNotetaker(r.Context(), recID)
		}
	}
	w.WriteHeader(http.StatusOK)
}
