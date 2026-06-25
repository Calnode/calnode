package livekit

import (
	"bytes"
	"context"
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

// DeleteRoom ends the room for everyone — all participants are disconnected.
func (c *Client) DeleteRoom(ctx context.Context, room string) error {
	_, err := c.twirp(ctx, "RoomService", "DeleteRoom",
		videoGrant{RoomAdmin: true, Room: room}, map[string]string{"room": room})
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
