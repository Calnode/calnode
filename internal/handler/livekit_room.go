package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"html/template"
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

func etagOf(b []byte) string {
	sum := sha256.Sum256(b)
	return `"` + hex.EncodeToString(sum[:])[:16] + `"`
}

// The room page injects these content-hash versions into its <script src="…?v=…"> tags so a
// changed asset gets a brand-new URL — unservable from any stale browser/service-worker cache.
var (
	liveKitRoomTmpl = template.Must(template.New("lkroom").Parse(string(liveKitRoomHTML)))
	liveKitSDKVer   = strings.Trim(liveKitSDKETag, `"`)
	liveKitRoomJSVer = strings.Trim(liveKitRoomJSETag, `"`)
)

// LiveKitRoom serves the public video-room page at GET /room/{room}. The page itself is
// static; the opaque room token travels in the query string and the room JS exchanges it for
// a real LiveKit token via LiveKitToken. 404s when LiveKit isn't configured.
func (h *Handler) LiveKitRoom(w http.ResponseWriter, r *http.Request) {
	if h.getLiveKit() == nil {
		h.writeError(w, http.StatusNotFound, "video meetings are not configured on this instance")
		return
	}
	brand := h.loadBranding(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Referrer-Policy", "no-referrer")
	// The HTML carries content-versioned asset URLs, so it must never be cached itself.
	w.Header().Set("Cache-Control", "no-store")
	if err := liveKitRoomTmpl.Execute(w, map[string]string{
		"SDKVer":       liveKitSDKVer,
		"RoomVer":      liveKitRoomJSVer,
		"LogoURL":      brand.LogoURL, // relative, same-origin; empty = no logo
		"BusinessName": brand.BusinessName,
	}); err != nil {
		h.logger.ErrorContext(r.Context(), "livekit room: render", "error", err)
	}
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
	room, role, exp, err := lk.VerifyRoomToken(req.Token)
	if err != nil {
		h.writeError(w, http.StatusForbidden, err.Error())
		return
	}
	// Auto-promote: a signed-in Calnode user who hosts this booking gets host controls no matter
	// which link they opened — so the host never needs the special host link to drive the meeting.
	if role != "host" {
		if uid, _, ok := h.sessionUser(r); ok && h.isBookingHost(r.Context(), room, uid) {
			role = "host"
		}
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "Guest"
	}
	if len(name) > 60 {
		name = name[:60]
	}
	token, identity, err := lk.AccessToken(room, name, role, exp)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "livekit: mint access token", "error", err)
		h.writeError(w, http.StatusInternalServerError, "could not create a meeting token")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"url":        lk.ClientURL(),
		"token":      token,
		"room":       room,
		"identity":   identity,
		"role":       role,                                                  // "host" unlocks host controls
		"can_record": role == "host" && h.recordingAvailable(r.Context()), // host + recording configured
	})
}

// isBookingHost reports whether userID is a host of the booking the room belongs to
// (room = "booking-<id>"). Used to auto-grant the host role to a signed-in owner.
func (h *Handler) isBookingHost(ctx context.Context, room, userID string) bool {
	if userID == "" || !strings.HasPrefix(room, "booking-") {
		return false
	}
	bookingID := strings.TrimPrefix(room, "booking-")
	var x int
	err := h.db.QueryRowContext(ctx,
		`SELECT 1 FROM booking_hosts WHERE booking_id = ? AND user_id = ? LIMIT 1`, bookingID, userID).Scan(&x)
	return err == nil
}

// requireHostRoom validates the opaque room token in the request body and confirms it carries
// the host role. Returns the room name, or "" after writing an error response.
func (h *Handler) requireHostRoom(w http.ResponseWriter, r *http.Request, token string) (string, bool) {
	lk := h.getLiveKit()
	if lk == nil {
		h.writeError(w, http.StatusNotFound, "video meetings are not configured")
		return "", false
	}
	room, role, _, err := lk.VerifyRoomToken(token)
	if err != nil {
		h.writeError(w, http.StatusForbidden, err.Error())
		return "", false
	}
	// Host if the token says so, OR the requester is a signed-in host of this booking (so an
	// owner who opened the attendee link can still drive the meeting).
	if role != "host" {
		uid, _, ok := h.sessionUser(r)
		if !ok || !h.isBookingHost(r.Context(), room, uid) {
			h.writeError(w, http.StatusForbidden, "only the meeting host can do that")
			return "", false
		}
	}
	return room, true
}

// EndRoom handles POST /v1/livekit/room/end (host token in body) — ends the meeting for everyone.
func (h *Handler) EndRoom(w http.ResponseWriter, r *http.Request) {
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
	if err := h.getLiveKit().DeleteRoom(r.Context(), room); err != nil {
		h.logger.ErrorContext(r.Context(), "livekit: end room", "error", err, "room", room)
		h.writeError(w, http.StatusBadGateway, "could not end the meeting")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ReassignHost handles POST /v1/livekit/room/reassign-host — promotes another participant to
// host (sets their metadata to "host"), so the meeting can continue after the host leaves.
func (h *Handler) ReassignHost(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token    string `json:"t"`
		Identity string `json:"identity"` // the participant to promote
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	room, ok := h.requireHostRoom(w, r, req.Token)
	if !ok {
		return
	}
	if strings.TrimSpace(req.Identity) == "" {
		h.writeError(w, http.StatusBadRequest, "identity is required")
		return
	}
	if err := h.getLiveKit().SetParticipantRole(r.Context(), room, req.Identity, "host"); err != nil {
		h.logger.ErrorContext(r.Context(), "livekit: reassign host", "error", err, "room", room)
		h.writeError(w, http.StatusBadGateway, "could not reassign the host")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
