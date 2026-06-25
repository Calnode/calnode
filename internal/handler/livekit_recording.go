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
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	room, ok := h.requireHostRoom(w, r, req.Token)
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
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	room, ok := h.requireHostRoom(w, r, req.Token)
	if !ok {
		return
	}
	lk := h.getLiveKit()
	var egressID string
	_ = h.db.QueryRowContext(r.Context(),
		`SELECT egress_id FROM recordings WHERE room = ? AND status = 'active' LIMIT 1`, room).Scan(&egressID)
	if egressID != "" {
		if err := lk.StopEgress(r.Context(), egressID); err != nil {
			h.logger.ErrorContext(r.Context(), "livekit: stop egress", "error", err, "egress", egressID)
		}
	}
	h.mergeRoomMeta(r.Context(), room, "recording", false)
	h.writeJSON(w, http.StatusOK, map[string]any{"recording": false})
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

// EgressWebhook handles POST /v1/livekit/egress-webhook (public; verified by the LiveKit
// signature). On egress completion it finalizes the recordings row with the object key + duration.
func (h *Handler) EgressWebhook(w http.ResponseWriter, r *http.Request) {
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
		EgressInfo struct {
			EgressID    string `json:"egress_id"`
			Status      string `json:"status"`
			FileResults []struct {
				Filename string `json:"filename"`
				Duration int64  `json:"duration"` // nanoseconds
			} `json:"file_results"`
		} `json:"egress_info"`
	}
	_ = json.Unmarshal(body, &ev)
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
	}
	w.WriteHeader(http.StatusOK)
}
