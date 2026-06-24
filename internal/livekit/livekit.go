// Package livekit provides the server-side pieces for Calnode's built-in video meetings,
// backed by a LiveKit server (self-hosted or LiveKit Cloud — same config). It's deliberately
// dependency-free: LiveKit access tokens are plain HS256 JWTs we sign with the API secret, so
// there's no SDK to vendor server-side.
//
// Two token kinds:
//   - AccessToken: the real LiveKit JWT (video grants) the browser SDK uses to join a room.
//     Short-lived, minted on demand by the room page's token-exchange endpoint.
//   - room token: a small opaque HMAC token embedded in a booking's join URL. It names the
//     room and an expiry but carries no LiveKit grant, so the API secret never leaves the
//     server and the join link can't be replayed past the booking window.
package livekit

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/uid"
)

// Client mints tokens for one configured LiveKit server.
type Client struct {
	wsURL     string   // browser-facing signalling URL (wss://… or ws://…)
	apiKey    string   // LiveKit API key (JWT issuer)
	apiSecret string   // LiveKit API secret (JWT HMAC key)
	roomKey   [32]byte // instance key for signing opaque room tokens (not the LiveKit secret)
}

// New builds a Client. serverURL may be given as https:// or wss:// (LiveKit Cloud hands out
// wss://<project>.livekit.cloud); it's normalised to a ws(s):// URL for the browser SDK.
// roomKey is the instance encryption key, used only to sign the opaque join tokens.
func New(serverURL, apiKey, apiSecret string, roomKey [32]byte) *Client {
	return &Client{
		wsURL:     normalizeWS(serverURL),
		apiKey:    apiKey,
		apiSecret: apiSecret,
		roomKey:   roomKey,
	}
}

// ClientURL returns the ws(s):// URL the browser LiveKit SDK should connect to.
func (c *Client) ClientURL() string { return c.wsURL }

// normalizeWS converts an http(s):// URL to ws(s):// and trims a trailing slash; ws(s):// is
// passed through. An empty or unparseable value is returned trimmed.
func normalizeWS(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimRight(raw, "/")
	switch {
	case strings.HasPrefix(raw, "https://"):
		return "wss://" + strings.TrimPrefix(raw, "https://")
	case strings.HasPrefix(raw, "http://"):
		return "ws://" + strings.TrimPrefix(raw, "http://")
	default:
		return raw // already ws:// / wss:// (or bare host — caller validated)
	}
}

// videoGrant is the LiveKit "video" claim controlling what a participant may do.
type videoGrant struct {
	Room           string `json:"room"`
	RoomJoin       bool   `json:"roomJoin"`
	CanPublish     bool   `json:"canPublish"`
	CanSubscribe   bool   `json:"canSubscribe"`
	CanPublishData bool   `json:"canPublishData"`
}

type accessClaims struct {
	Iss   string     `json:"iss"`
	Sub   string     `json:"sub"`
	Name  string     `json:"name,omitempty"`
	Nbf   int64      `json:"nbf"`
	Exp   int64      `json:"exp"`
	Video videoGrant `json:"video"`
}

// AccessToken mints a LiveKit access JWT for one participant joining room. identity is the
// stable LiveKit participant id (made unique here so two attendees with the same display name
// don't evict each other); name is the display name. The token is a full publisher/subscriber.
func (c *Client) AccessToken(room, name string, expiry time.Time) (token, identity string, err error) {
	if c.apiKey == "" || c.apiSecret == "" {
		return "", "", errors.New("livekit: not configured")
	}
	identity = uid.New() // unique per join
	now := time.Now()
	claims := accessClaims{
		Iss:  c.apiKey,
		Sub:  identity,
		Name: name,
		Nbf:  now.Add(-30 * time.Second).Unix(),
		Exp:  expiry.Unix(),
		Video: videoGrant{
			Room: room, RoomJoin: true,
			CanPublish: true, CanSubscribe: true, CanPublishData: true,
		},
	}
	tok, err := signJWT(claims, c.apiSecret)
	return tok, identity, err
}

// signJWT signs claims as a compact HS256 JWT with secret as the HMAC key.
func signJWT(claims any, secret string) (string, error) {
	header := b64(`{"alg":"HS256","typ":"JWT"}`)
	body, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signingInput := header + "." + base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func b64(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

// ----- opaque room tokens (for the booking join URL) -----

type roomTokenPayload struct {
	R string `json:"r"` // room name
	E int64  `json:"e"` // expiry (unix)
}

// SignRoomToken returns an opaque token binding a room name + expiry, signed with the instance
// key. It carries no LiveKit grant; the room page exchanges it for a real AccessToken.
func (c *Client) SignRoomToken(room string, exp time.Time) string {
	p, _ := json.Marshal(roomTokenPayload{R: room, E: exp.Unix()})
	payload := base64.RawURLEncoding.EncodeToString(p)
	return payload + "." + c.roomMAC(payload)
}

// VerifyRoomToken validates an opaque room token and returns its room name and expiry. Errors
// if the signature is bad or the token has expired.
func (c *Client) VerifyRoomToken(tok string) (room string, exp time.Time, err error) {
	dot := strings.IndexByte(tok, '.')
	if dot < 0 {
		return "", time.Time{}, errors.New("livekit: malformed room token")
	}
	payload, sig := tok[:dot], tok[dot+1:]
	if !hmac.Equal([]byte(sig), []byte(c.roomMAC(payload))) {
		return "", time.Time{}, errors.New("livekit: bad room token signature")
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("livekit: decode room token: %w", err)
	}
	var p roomTokenPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", time.Time{}, fmt.Errorf("livekit: parse room token: %w", err)
	}
	exp = time.Unix(p.E, 0)
	if time.Now().After(exp) {
		return "", time.Time{}, errors.New("livekit: this meeting link has expired")
	}
	return p.R, exp, nil
}

func (c *Client) roomMAC(payload string) string {
	mac := hmac.New(sha256.New, c.roomKey[:])
	mac.Write([]byte("lk-room:" + payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// BookingJoinURL builds the public join link Calnode stores as a booking's location_value:
// the room page URL carrying an opaque, expiring room token. baseURL is the instance origin.
func (c *Client) BookingJoinURL(baseURL, room string, exp time.Time) string {
	t := c.SignRoomToken(room, exp)
	return strings.TrimRight(baseURL, "/") + "/room/" + url.PathEscape(room) + "?t=" + url.QueryEscape(t)
}
