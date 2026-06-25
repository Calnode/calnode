package livekit

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"
)

func testClient() *Client {
	var key [32]byte
	copy(key[:], []byte("0123456789abcdef0123456789abcdef"))
	return New("https://demo.livekit.cloud", "APIabc", "topsecret", key)
}

func TestNormalizeWS(t *testing.T) {
	cases := map[string]string{
		"https://x.livekit.cloud":  "wss://x.livekit.cloud",
		"http://localhost:7880":    "ws://localhost:7880",
		"wss://x.livekit.cloud/":   "wss://x.livekit.cloud",
		"wss://x.livekit.cloud":    "wss://x.livekit.cloud",
	}
	for in, want := range cases {
		if got := normalizeWS(in); got != want {
			t.Errorf("normalizeWS(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAccessToken_claimsAndSignature(t *testing.T) {
	c := testClient()
	exp := time.Now().Add(2 * time.Hour)
	tok, identity, err := c.AccessToken("booking-123", "Wynne", "host", true, exp)
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if identity == "" {
		t.Error("identity should be non-empty")
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT should have 3 parts, got %d", len(parts))
	}
	// Verify HS256 signature with the API secret.
	mac := hmac.New(sha256.New, []byte("topsecret"))
	mac.Write([]byte(parts[0] + "." + parts[1]))
	wantSig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if parts[2] != wantSig {
		t.Error("JWT signature does not verify with the API secret")
	}
	// Decode claims and assert the LiveKit grant.
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims accessClaims
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	if claims.Iss != "APIabc" {
		t.Errorf("iss = %q, want APIabc", claims.Iss)
	}
	if claims.Sub != identity {
		t.Errorf("sub = %q, want identity %q", claims.Sub, identity)
	}
	if claims.Video.Room != "booking-123" || !claims.Video.RoomJoin || !claims.Video.CanPublish {
		t.Errorf("video grant wrong: %+v", claims.Video)
	}
	if claims.Exp != exp.Unix() {
		t.Errorf("exp = %d, want %d", claims.Exp, exp.Unix())
	}
}

func TestVerifyAccessToken_roundTripAndTamper(t *testing.T) {
	c := testClient()
	tok, identity, err := c.AccessToken("booking-77", "Alex", "host", true, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	room, gotID, err := c.VerifyAccessToken(tok)
	if err != nil {
		t.Fatalf("VerifyAccessToken: %v", err)
	}
	if room != "booking-77" {
		t.Errorf("room = %q, want booking-77", room)
	}
	if gotID != identity {
		t.Errorf("identity = %q, want %q", gotID, identity)
	}
	// Tampered signature is rejected.
	if _, _, err := c.VerifyAccessToken(tok + "x"); err == nil {
		t.Error("tampered access token should fail")
	}
	// A token signed with a different API secret can't validate.
	var k [32]byte
	other := New("https://x.livekit.cloud", "APIabc", "different-secret", k)
	if _, _, err := other.VerifyAccessToken(tok); err == nil {
		t.Error("access token from a different secret should fail")
	}
}

func TestRoomToken_roundTripAndTamper(t *testing.T) {
	c := testClient()
	exp := time.Now().Add(time.Hour)
	tok := c.SignRoomToken("booking-xyz", "host", exp)

	room, role, gotExp, err := c.VerifyRoomToken(tok)
	if err != nil {
		t.Fatalf("VerifyRoomToken: %v", err)
	}
	if room != "booking-xyz" {
		t.Errorf("room = %q, want booking-xyz", room)
	}
	if role != "host" {
		t.Errorf("role = %q, want host", role)
	}
	if gotExp.Unix() != exp.Unix() {
		t.Errorf("exp = %v, want %v", gotExp, exp)
	}

	// Tampered signature is rejected.
	if _, _, _, err := c.VerifyRoomToken(tok + "x"); err == nil {
		t.Error("tampered token should fail verification")
	}
	// A different instance key can't validate it.
	var otherKey [32]byte
	copy(otherKey[:], []byte("ffffffffffffffffffffffffffffffff"))
	other := New("https://x", "APIabc", "topsecret", otherKey)
	if _, _, _, err := other.VerifyRoomToken(tok); err == nil {
		t.Error("token signed by a different key should fail")
	}
}

func TestRoomToken_expired(t *testing.T) {
	c := testClient()
	tok := c.SignRoomToken("booking-old", "", time.Now().Add(-time.Minute))
	if _, _, _, err := c.VerifyRoomToken(tok); err == nil {
		t.Error("expired token should be rejected")
	}
}

func TestBookingJoinURL(t *testing.T) {
	c := testClient()
	raw := c.BookingJoinURL("https://book.example.com/", "booking-9", "", time.Now().Add(time.Hour))
	if !strings.HasPrefix(raw, "https://book.example.com/room/booking-9?t=") {
		t.Fatalf("join URL = %q", raw)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse join URL: %v", err)
	}
	room, _, _, err := c.VerifyRoomToken(u.Query().Get("t"))
	if err != nil {
		t.Fatalf("embedded room token must verify: %v", err)
	}
	if room != "booking-9" {
		t.Errorf("token room = %q, want booking-9", room)
	}
}

func TestAPIBaseAndAdminToken(t *testing.T) {
	c := testClient() // wsURL normalised from https://demo.livekit.cloud
	if got := c.apiBase(); got != "https://demo.livekit.cloud" {
		t.Errorf("apiBase = %q, want https://demo.livekit.cloud", got)
	}
	tok, err := c.adminToken(videoGrant{RoomAdmin: true, Room: "booking-1"})
	if err != nil {
		t.Fatalf("adminToken: %v", err)
	}
	if len(strings.Split(tok, ".")) != 3 {
		t.Errorf("admin token should be a JWT")
	}
}
