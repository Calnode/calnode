package livekit

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/uid"
)

// LiveKit's server APIs are Twirp (JSON over HTTP) on the same host as the signalling URL, just
// https:// instead of wss://. We call them directly with a short-lived admin JWT — no SDK, in
// keeping with the hand-rolled token approach.

// apiBase returns the HTTPS base for the Twirp server APIs, derived from the ws(s) URL.
func (c *Client) apiBase() string {
	switch {
	case strings.HasPrefix(c.wsURL, "wss://"):
		return "https://" + strings.TrimPrefix(c.wsURL, "wss://")
	case strings.HasPrefix(c.wsURL, "ws://"):
		return "http://" + strings.TrimPrefix(c.wsURL, "ws://")
	default:
		return c.wsURL
	}
}

// adminToken mints a short-lived admin JWT carrying the given grant for one server API call.
func (c *Client) adminToken(grant videoGrant) (string, error) {
	if c.apiKey == "" || c.apiSecret == "" {
		return "", errors.New("livekit: not configured")
	}
	now := time.Now()
	return signJWT(accessClaims{
		Iss:   c.apiKey,
		Sub:   "calnode-" + uid.New(),
		Nbf:   now.Add(-30 * time.Second).Unix(),
		Exp:   now.Add(5 * time.Minute).Unix(),
		Video: grant,
	}, c.apiSecret)
}

// twirp POSTs a JSON body to livekit.<service>/<method> with an admin bearer token and returns
// the raw response body. A non-200 is an error carrying the server's message.
func (c *Client) twirp(ctx context.Context, service, method string, grant videoGrant, body any) ([]byte, error) {
	tok, err := c.adminToken(grant)
	if err != nil {
		return nil, err
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.apiBase()+"/twirp/livekit."+service+"/"+method, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("livekit %s.%s: status %d: %s", service, method, resp.StatusCode, strings.TrimSpace(string(rb)))
	}
	return rb, nil
}

// DeleteRoom ends the room for everyone — all participants are disconnected. Requires the
// server-level roomCreate grant (not room-scoped roomAdmin).
func (c *Client) DeleteRoom(ctx context.Context, room string) error {
	_, err := c.twirp(ctx, "RoomService", "DeleteRoom",
		videoGrant{RoomCreate: true}, map[string]string{"room": room})
	return err
}

// SetParticipantRole updates a connected participant's metadata to grant ("host") or revoke ("")
// the host role at runtime — used when the host reassigns and leaves.
func (c *Client) SetParticipantRole(ctx context.Context, room, identity, role string) error {
	_, err := c.twirp(ctx, "RoomService", "UpdateParticipant",
		videoGrant{RoomAdmin: true, Room: room},
		map[string]any{"room": room, "identity": identity, "metadata": role})
	return err
}

// UpdateRoomMetadata sets the room's metadata (read by clients to show e.g. a recording banner).
func (c *Client) UpdateRoomMetadata(ctx context.Context, room, metadata string) error {
	_, err := c.twirp(ctx, "RoomService", "UpdateRoomMetadata",
		videoGrant{RoomAdmin: true, Room: room},
		map[string]string{"room": room, "metadata": metadata})
	return err
}

// S3Config is the destination bucket for recordings (reused from the Litestream backup config).
type S3Config struct {
	AccessKey, Secret, Region, Endpoint, Bucket string
}

// StartRoomCompositeEgress records the whole room (composited video + mixed audio) to an MP4 in
// S3 and returns the egress id. filepath is the object key within the bucket.
func (c *Client) StartRoomCompositeEgress(ctx context.Context, room, filepath string, s3 S3Config) (string, error) {
	s3body := map[string]any{"access_key": s3.AccessKey, "secret": s3.Secret, "bucket": s3.Bucket}
	if s3.Region != "" {
		s3body["region"] = s3.Region
	}
	if s3.Endpoint != "" { // non-AWS (R2/B2/MinIO/…): explicit endpoint + path-style addressing
		s3body["endpoint"] = s3.Endpoint
		s3body["force_path_style"] = true
	}
	body := map[string]any{
		"room_name": room,
		"layout":    "grid",
		"file_outputs": []any{map[string]any{
			"file_type": "MP4",
			"filepath":  filepath,
			"s3":        s3body,
		}},
	}
	rb, err := c.twirp(ctx, "Egress", "StartRoomCompositeEgress", videoGrant{RoomRecord: true}, body)
	if err != nil {
		return "", err
	}
	var out struct {
		EgressID string `json:"egress_id"`
	}
	_ = json.Unmarshal(rb, &out)
	return out.EgressID, nil
}

// StopEgress stops a running egress (recording).
func (c *Client) StopEgress(ctx context.Context, egressID string) error {
	_, err := c.twirp(ctx, "Egress", "StopEgress", videoGrant{RoomRecord: true}, map[string]string{"egress_id": egressID})
	return err
}

// VerifyWebhook validates a LiveKit webhook: the Authorization header is a JWT (HS256, signed
// with the API secret) whose sha256 claim is the base64 SHA-256 of the request body.
func (c *Client) VerifyWebhook(authToken string, body []byte) error {
	parts := strings.Split(authToken, ".")
	if len(parts) != 3 {
		return errors.New("livekit: malformed webhook token")
	}
	mac := hmac.New(sha256.New, []byte(c.apiSecret))
	mac.Write([]byte(parts[0] + "." + parts[1]))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(parts[2]), []byte(want)) {
		return errors.New("livekit: bad webhook signature")
	}
	var claims struct {
		Sha256 string `json:"sha256"`
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || json.Unmarshal(raw, &claims) != nil {
		return errors.New("livekit: bad webhook claims")
	}
	sum := sha256.Sum256(body)
	if claims.Sha256 != base64.StdEncoding.EncodeToString(sum[:]) {
		return errors.New("livekit: webhook body hash mismatch")
	}
	return nil
}
