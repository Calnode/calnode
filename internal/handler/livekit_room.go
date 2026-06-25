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
	// Single host: a new host joining (e.g. the owner rejoining) takes over — demote any prior
	// host so there's never two. The joiner connects fresh as the sole host below.
	if role == "host" {
		h.demoteOtherHosts(r.Context(), room)
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "Guest"
	}
	if len(name) > 60 {
		name = name[:60]
	}
	allowShare := h.attendeeShareAllowed(r.Context(), room)
	canShare := role == "host" || allowShare // host can always share
	token, identity, err := lk.AccessToken(room, name, role, canShare, exp)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "livekit: mint access token", "error", err)
		h.writeError(w, http.StatusInternalServerError, "could not create a meeting token")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"url":             lk.ClientURL(),
		"token":           token,
		"room":            room,
		"identity":        identity,
		"role":            role,                                                  // "host" unlocks host controls
		"can_record":      role == "host" && h.recordingAvailable(r.Context()), // host + recording configured
		"can_screenshare": canShare,                                            // may this participant share?
		"allow_share":     allowShare,                                          // current attendee-share setting (for the host toggle)
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

// demoteOtherHosts clears the host role from anyone currently marked host in the room (sets
// their metadata to "attendee"), so a newly-joining host becomes the single host. Best-effort:
// the room may not exist yet for the first joiner.
func (h *Handler) demoteOtherHosts(ctx context.Context, room string) {
	lk := h.getLiveKit()
	if lk == nil {
		return
	}
	parts, err := lk.ListParticipants(ctx, room)
	if err != nil {
		return // room not created yet (first participant) — nothing to demote
	}
	for _, p := range parts {
		if p.Metadata == "host" {
			if err := lk.SetParticipantRole(ctx, room, p.Identity, "attendee"); err != nil {
				h.logger.WarnContext(ctx, "livekit: demote prior host", "error", err, "identity", p.Identity)
			}
		}
	}
}

// mergeRoomMeta reads the room's JSON metadata, sets one key, and writes it back — so the
// recording and screen-share flags don't clobber each other.
func (h *Handler) mergeRoomMeta(ctx context.Context, room, key string, val any) {
	lk := h.getLiveKit()
	if lk == nil {
		return
	}
	cur, _ := lk.RoomMetadata(ctx, room)
	m := map[string]any{}
	if cur != "" {
		_ = json.Unmarshal([]byte(cur), &m)
	}
	m[key] = val
	b, _ := json.Marshal(m)
	if err := lk.UpdateRoomMetadata(ctx, room, string(b)); err != nil {
		h.logger.WarnContext(ctx, "livekit: update room metadata", "error", err, "room", room)
	}
}

// attendeeShareAllowed reports whether attendees may share their screen (room metadata
// allowShare; defaults to FALSE when unset — the host opts attendees in explicitly).
func (h *Handler) attendeeShareAllowed(ctx context.Context, room string) bool {
	lk := h.getLiveKit()
	if lk == nil {
		return false
	}
	cur, err := lk.RoomMetadata(ctx, room)
	if err != nil || cur == "" {
		return false
	}
	var m struct {
		AllowShare *bool `json:"allowShare"`
	}
	if json.Unmarshal([]byte(cur), &m) == nil && m.AllowShare != nil {
		return *m.AllowShare
	}
	return false
}

// ScreenShareToggle handles POST /v1/livekit/room/screenshare (host token) — turns attendee
// screen sharing on/off, updating the room metadata and every connected non-host's grant.
func (h *Handler) ScreenShareToggle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"t"`
		Allow bool   `json:"allow"`
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
	h.mergeRoomMeta(r.Context(), room, "allowShare", req.Allow)
	parts, _ := lk.ListParticipants(r.Context(), room)
	for _, p := range parts {
		if p.Metadata == "host" {
			continue // hosts can always share
		}
		var sources []string
		if !req.Allow {
			sources = []string{"camera", "microphone"}
		}
		_ = lk.SetParticipantSources(r.Context(), room, p.Identity, sources)
	}
	w.WriteHeader(http.StatusNoContent)
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
