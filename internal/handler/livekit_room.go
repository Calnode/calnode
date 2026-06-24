package handler

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

//go:embed templates/livekit-room.html
var liveKitRoomHTML []byte

//go:embed assets/livekit-client.umd.min.js
var liveKitSDK []byte

//go:embed assets/livekit-room.js
var liveKitRoomJS []byte

var liveKitSDKETag = etagOf(liveKitSDK)
var liveKitRoomJSETag = etagOf(liveKitRoomJS)
var liveKitRoomHTMLETag = etagOf(liveKitRoomHTML)

func etagOf(b []byte) string {
	sum := sha256.Sum256(b)
	return `"` + hex.EncodeToString(sum[:])[:16] + `"`
}

// LiveKitRoom serves the public video-room page at GET /room/{room}. The page itself is
// static; the opaque room token travels in the query string and the room JS exchanges it for
// a real LiveKit token via LiveKitToken. 404s when LiveKit isn't configured.
func (h *Handler) LiveKitRoom(w http.ResponseWriter, r *http.Request) {
	if h.getLiveKit() == nil {
		h.writeError(w, http.StatusNotFound, "video meetings are not configured on this instance")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("ETag", liveKitRoomHTMLETag)
	// no-cache = always revalidate before use; a redeploy of the room UI is picked up on the
	// next load (cheap 304 when unchanged) instead of being pinned by a long max-age.
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "livekit-room.html", time.Time{}, bytes.NewReader(liveKitRoomHTML))
}

// LiveKitSDKAsset serves the vendored LiveKit browser SDK at GET /assets/livekit-client.js.
func (h *Handler) LiveKitSDKAsset(w http.ResponseWriter, r *http.Request) {
	serveJSAsset(w, r, liveKitSDK, liveKitSDKETag)
}

// LiveKitRoomJSAsset serves the room UI script at GET /assets/livekit-room.js.
func (h *Handler) LiveKitRoomJSAsset(w http.ResponseWriter, r *http.Request) {
	serveJSAsset(w, r, liveKitRoomJS, liveKitRoomJSETag)
}

func serveJSAsset(w http.ResponseWriter, r *http.Request, body []byte, etag string) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("ETag", etag)
	// no-cache = revalidate every load; the content-hash ETag makes that a tiny 304 unless the
	// asset actually changed. Avoids the room UI being pinned to a stale copy after a deploy.
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "asset.js", time.Time{}, bytes.NewReader(body))
}

// LiveKitToken handles POST /v1/livekit/token (public). It exchanges the opaque, signed room
// token (from the booking's join URL) plus a display name for a short-lived LiveKit access
// token scoped to that room and bounded by the room token's expiry. No auth: the signed room
// token IS the capability.
func (h *Handler) LiveKitToken(w http.ResponseWriter, r *http.Request) {
	lk := h.getLiveKit()
	if lk == nil {
		h.writeError(w, http.StatusNotFound, "video meetings are not configured")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var req struct {
		Token string `json:"t"`
		Name  string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	room, exp, err := lk.VerifyRoomToken(req.Token)
	if err != nil {
		h.writeError(w, http.StatusForbidden, err.Error())
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "Guest"
	}
	if len(name) > 60 {
		name = name[:60]
	}
	token, identity, err := lk.AccessToken(room, name, exp)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "livekit: mint access token", "error", err)
		h.writeError(w, http.StatusInternalServerError, "could not create a meeting token")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"url":      lk.ClientURL(),
		"token":    token,
		"room":     room,
		"identity": identity,
	})
}
