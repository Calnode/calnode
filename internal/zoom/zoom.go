// Package zoom mints Zoom meeting links for bookings. Zoom is a meeting-link provider,
// not a calendar: each host connects their OWN Zoom account via OAuth (so the meeting is
// hosted by the assigned host and concurrent bookings across a team don't collide), and a
// Zoom-located booking gets a real meeting minted under that host's account. Tokens live in
// calendar_connections with provider='zoom' (check_conflicts=0, is_destination=0 — it is
// not a calendar), encrypted with the server key.
package zoom

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"golang.org/x/oauth2"

	"github.com/calnode/calnode/internal/oauthstore"
	"github.com/calnode/calnode/internal/secret"
)

// Client manages Zoom OAuth tokens and meeting API calls for the configured Zoom app.
type Client struct {
	config  *oauth2.Config
	key     [32]byte
	db      *sql.DB
	logger  *slog.Logger
	apiBase string // https://api.zoom.us/v2; overridable in tests
}

// New builds a Client from the instance's Zoom OAuth app credentials. encKeyHex is the
// 64-char hex AES-256 server key (same one used for calendar tokens).
func New(db *sql.DB, clientID, clientSecret, redirectURL, encKeyHex string) (*Client, error) {
	key, err := secret.ParseKey(encKeyHex)
	if err != nil {
		return nil, fmt.Errorf("zoom: invalid encryption key: %w", err)
	}
	return &Client{
		config: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Endpoint: oauth2.Endpoint{ // #nosec G101 -- Zoom's own public, fixed OAuth endpoint URLs, not credentials
				AuthURL:   "https://zoom.us/oauth/authorize",
				TokenURL:  "https://zoom.us/oauth/token",
				AuthStyle: oauth2.AuthStyleInHeader, // Zoom wants client creds in Basic auth
			},
			RedirectURL: redirectURL,
			// No Scopes: Zoom grants the app's configured scopes when scope is omitted,
			// which avoids "invalid scope" errors when the app uses granular scopes.
		},
		key:     key,
		db:      db,
		logger:  slog.Default(),
		apiBase: "https://api.zoom.us/v2",
	}, nil
}

// AuthURL returns the Zoom consent page URL carrying the opaque state.
func (c *Client) AuthURL(state string) string {
	return c.config.AuthCodeURL(state)
}

// EncryptState / DecryptState protect the per-user OAuth state (CSRF) without server-side
// storage, mirroring the calendar connect flow.
func (c *Client) EncryptState(userID string) (string, error) {
	enc, err := secret.Encrypt(c.key, userID)
	if err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString([]byte(enc)), nil
}

func (c *Client) DecryptState(state string) (string, error) {
	b, err := base64.URLEncoding.DecodeString(state)
	if err != nil {
		return "", err
	}
	return secret.Decrypt(c.key, string(b))
}

// Exchange swaps an auth code for tokens and persists them for userID.
func (c *Client) Exchange(ctx context.Context, userID, code string) error {
	tok, err := c.config.Exchange(ctx, code)
	if err != nil {
		return fmt.Errorf("zoom: token exchange: %w", err)
	}
	return c.saveToken(ctx, userID, tok)
}

// Connected reports whether userID has a Zoom connection.
func (c *Client) Connected(ctx context.Context, userID string) (bool, error) {
	var n int
	err := c.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM zoom_connections WHERE user_id = ?`, userID).Scan(&n)
	return n > 0, err
}

// Disconnect removes userID's Zoom connection.
func (c *Client) Disconnect(ctx context.Context, userID string) error {
	_, err := c.db.ExecContext(ctx, `DELETE FROM zoom_connections WHERE user_id = ?`, userID)
	return err
}

// saveToken encrypts and upserts a Zoom OAuth token for userID (one row per user). Zoom
// rotates refresh tokens, so this also runs after a refresh.
func (c *Client) saveToken(ctx context.Context, userID string, tok *oauth2.Token) error {
	accessEnc, err := secret.Encrypt(c.key, tok.AccessToken)
	if err != nil {
		return err
	}
	var refreshEnc string
	if tok.RefreshToken != "" {
		if refreshEnc, err = secret.Encrypt(c.key, tok.RefreshToken); err != nil {
			return err
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var expiryStr string
	if !tok.Expiry.IsZero() {
		expiryStr = tok.Expiry.UTC().Format(time.RFC3339)
	}
	if _, err := c.db.ExecContext(ctx, `
		INSERT INTO zoom_connections (user_id, access_token_enc, refresh_token_enc, expiry_at, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
		    access_token_enc = excluded.access_token_enc,
		    refresh_token_enc = excluded.refresh_token_enc,
		    expiry_at = excluded.expiry_at`,
		userID, accessEnc, refreshEnc, expiryStr, now); err != nil {
		return fmt.Errorf("zoom: save token: %w", err)
	}
	return nil
}

// authedClient returns an *http.Client that auto-refreshes (persisting rotated tokens),
// or (nil, nil) when userID has no Zoom connection.
func (c *Client) authedClient(ctx context.Context, userID string) (*http.Client, error) {
	var accessEnc, refreshEnc, expiryStr string
	err := c.db.QueryRowContext(ctx, `
		SELECT access_token_enc, COALESCE(refresh_token_enc,''), COALESCE(expiry_at,'')
		FROM zoom_connections WHERE user_id = ? LIMIT 1`,
		userID).Scan(&accessEnc, &refreshEnc, &expiryStr)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("zoom: load connection: %w", err)
	}
	access, err := secret.Decrypt(c.key, accessEnc)
	if err != nil {
		return nil, fmt.Errorf("zoom: decrypt access token: %w", err)
	}
	var refresh string
	if refreshEnc != "" {
		if refresh, err = secret.Decrypt(c.key, refreshEnc); err != nil {
			return nil, fmt.Errorf("zoom: decrypt refresh token: %w", err)
		}
	}
	expiry := time.Now().Add(-time.Second) // zero/missing → refresh immediately
	if expiryStr != "" {
		if t, perr := time.Parse(time.RFC3339, expiryStr); perr == nil {
			expiry = t
		}
	}
	tok := &oauth2.Token{AccessToken: access, RefreshToken: refresh, Expiry: expiry}
	src := oauth2.ReuseTokenSource(nil, c.config.TokenSource(ctx, tok))
	saving := &oauthstore.SavingTokenSource{
		Inner:  src,
		Save:   func(ctx context.Context, t *oauth2.Token) error { return c.saveToken(ctx, userID, t) },
		Logger: c.logger,
		LogMsg: "zoom: persist refreshed token",
		UserID: userID,
	}
	return oauth2.NewClient(ctx, saving), nil
}

// MeetingParams describes a meeting to create.
type MeetingParams struct {
	Topic           string
	Start           time.Time
	DurationMinutes int
	Timezone        string // IANA name, for Zoom's display
}

// CreateMeeting schedules a Zoom meeting under userID's account and returns its join URL
// and meeting id. Returns an error (joinURL empty) when userID has no Zoom connection.
func (c *Client) CreateMeeting(ctx context.Context, userID string, p MeetingParams) (joinURL, meetingID string, err error) {
	cl, err := c.authedClient(ctx, userID)
	if err != nil {
		return "", "", err
	}
	if cl == nil {
		return "", "", fmt.Errorf("zoom: user %s is not connected", userID)
	}
	body, _ := json.Marshal(map[string]any{
		"topic":      p.Topic,
		"type":       2, // scheduled
		"start_time": p.Start.UTC().Format("2006-01-02T15:04:05Z"),
		"duration":   p.DurationMinutes,
		"timezone":   p.Timezone,
		"settings": map[string]any{
			"join_before_host": true,
			"waiting_room":     false,
		},
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.apiBase+"/users/me/meetings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := cl.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("zoom: create meeting: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<10))
		return "", "", fmt.Errorf("zoom: create meeting returned %d: %s", resp.StatusCode, b)
	}
	var out struct {
		ID      json.Number `json:"id"`
		JoinURL string      `json:"join_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", "", fmt.Errorf("zoom: decode meeting: %w", err)
	}
	return out.JoinURL, out.ID.String(), nil
}

// UpdateMeeting changes a meeting's start time and duration (the join URL is unchanged).
// Used on reschedule. A nil/empty meetingID or missing connection is a no-op error.
func (c *Client) UpdateMeeting(ctx context.Context, userID, meetingID string, start time.Time, durationMinutes int, timezone string) error {
	if meetingID == "" {
		return nil
	}
	cl, err := c.authedClient(ctx, userID)
	if err != nil {
		return err
	}
	if cl == nil {
		return fmt.Errorf("zoom: user %s is not connected", userID)
	}
	body, _ := json.Marshal(map[string]any{
		"start_time": start.UTC().Format("2006-01-02T15:04:05Z"),
		"duration":   durationMinutes,
		"timezone":   timezone,
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPatch, c.apiBase+"/meetings/"+meetingID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := cl.Do(req)
	if err != nil {
		return fmt.Errorf("zoom: update meeting: %w", err)
	}
	defer resp.Body.Close()
	// 204 No Content on success.
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<10))
		return fmt.Errorf("zoom: update meeting returned %d: %s", resp.StatusCode, b)
	}
	return nil
}

// DeleteMeeting cancels a Zoom meeting. Used on booking cancel. Treats a 404 (already gone)
// as success so cancel stays idempotent.
func (c *Client) DeleteMeeting(ctx context.Context, userID, meetingID string) error {
	if meetingID == "" {
		return nil
	}
	cl, err := c.authedClient(ctx, userID)
	if err != nil {
		return err
	}
	if cl == nil {
		return fmt.Errorf("zoom: user %s is not connected", userID)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, c.apiBase+"/meetings/"+meetingID, nil)
	resp, err := cl.Do(req)
	if err != nil {
		return fmt.Errorf("zoom: delete meeting: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<10))
		return fmt.Errorf("zoom: delete meeting returned %d: %s", resp.StatusCode, b)
	}
	return nil
}
