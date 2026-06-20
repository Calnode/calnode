package gcal

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/uid"
)

// Client implements calendar.Provider for Google Calendar.
var _ calendar.Provider = (*Client)(nil)

// Name identifies this provider in the calendar_connections table.
func (c *Client) Name() string { return "google" }

// InvitesGuests is true: Google emails guests its own invite (sendUpdates=all),
// so Calnode must not also attach an .ics (it would duplicate).
func (c *Client) InvitesGuests() bool { return true }

// Client manages Google Calendar OAuth tokens and API access.
type Client struct {
	config  *oauth2.Config
	key     [32]byte
	db      *sql.DB
	logger  *slog.Logger
	apiBase string // base URL for Calendar API; overridable in tests
}

// New creates a Client. encKeyHex is the 64-char hex AES-256 encryption key.
func New(db *sql.DB, clientID, clientSecret, redirectURL, encKeyHex string) (*Client, error) {
	b, err := hex.DecodeString(encKeyHex)
	if err != nil || len(b) != 32 {
		return nil, fmt.Errorf("gcal: invalid encryption key")
	}
	var key [32]byte
	copy(key[:], b)
	return &Client{
		config: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Endpoint:     google.Endpoint,
			RedirectURL:  redirectURL,
			Scopes:       []string{"https://www.googleapis.com/auth/calendar"},
		},
		key:     key,
		db:      db,
		logger:  slog.Default(),
		apiBase: "https://www.googleapis.com/calendar/v3",
	}, nil
}

// AuthURL returns the Google OAuth consent page URL with the given state value.
func (c *Client) AuthURL(state string) string {
	return c.config.AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.ApprovalForce, // always return a refresh token
	)
}

// EncryptState encrypts userID into an opaque, URL-safe string for use as the
// OAuth state parameter, preventing CSRF without server-side state storage.
func (c *Client) EncryptState(userID string) (string, error) {
	return c.encryptEncoding([]byte(userID), base64.URLEncoding)
}

// DecryptState reverses EncryptState and returns the original userID.
func (c *Client) DecryptState(state string) (string, error) {
	b, err := c.decryptEncoding(state, base64.URLEncoding)
	return string(b), err
}

// Exchange converts an auth code into OAuth tokens and persists them for userID.
func (c *Client) Exchange(ctx context.Context, userID, code, calendarID string) error {
	tok, err := c.config.Exchange(ctx, code)
	if err != nil {
		return fmt.Errorf("gcal: token exchange: %w", err)
	}
	if calendarID == "" {
		calendarID = "primary"
	}
	return c.saveToken(ctx, userID, calendarID, tok)
}

// Connected reports whether userID has an active Google Calendar connection.
func (c *Client) Connected(ctx context.Context, userID string) (bool, error) {
	var n int
	err := c.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM calendar_connections WHERE user_id = ? AND provider = 'google'`,
		userID).Scan(&n)
	return n > 0, err
}

// Disconnect removes all Google Calendar connections for userID.
func (c *Client) Disconnect(ctx context.Context, userID string) error {
	_, err := c.db.ExecContext(ctx,
		`DELETE FROM calendar_connections WHERE user_id = ? AND provider = 'google'`,
		userID)
	return err
}

// httpClient returns an authorized *http.Client and the calendarID for userID.
// Filters by the given check_conflicts and is_destination values (-1 means any).
// Returns (nil, "", nil) when no matching connection exists.
func (c *Client) httpClient(ctx context.Context, userID string, checkConflicts, isDestination int) (*http.Client, string, error) {
	q := `SELECT id, access_token_enc, COALESCE(refresh_token_enc,''), calendar_id, COALESCE(expiry_at,'')
	      FROM calendar_connections
	      WHERE user_id = ? AND provider = 'google'`
	args := []any{userID}
	if checkConflicts >= 0 {
		q += " AND check_conflicts = ?"
		args = append(args, checkConflicts)
	}
	if isDestination >= 0 {
		q += " AND is_destination = ?"
		args = append(args, isDestination)
	}
	q += " LIMIT 1"

	var connID, accessEnc, refreshEnc, calID, expiryStr string
	err := c.db.QueryRowContext(ctx, q, args...).Scan(&connID, &accessEnc, &refreshEnc, &calID, &expiryStr)
	if err == sql.ErrNoRows {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("gcal: load connection: %w", err)
	}

	access, err := c.decrypt(accessEnc)
	if err != nil {
		return nil, "", fmt.Errorf("gcal: decrypt access token: %w", err)
	}
	var refresh string
	if refreshEnc != "" {
		rb, err := c.decrypt(refreshEnc)
		if err != nil {
			return nil, "", fmt.Errorf("gcal: decrypt refresh token: %w", err)
		}
		refresh = string(rb)
	}

	var expiry time.Time
	if expiryStr != "" {
		expiry, _ = time.Parse(time.RFC3339, expiryStr)
	}
	// If expiry is zero (old row or missing), treat as already expired so oauth2
	// refreshes immediately rather than using a stale access token indefinitely.
	if expiry.IsZero() {
		expiry = time.Now().Add(-time.Second)
	}

	tok := &oauth2.Token{
		AccessToken:  string(access),
		RefreshToken: refresh,
		Expiry:       expiry,
	}
	src := c.config.TokenSource(ctx, tok)
	saving := &savingTokenSource{
		inner:  oauth2.ReuseTokenSource(nil, src),
		client: c,
		userID: userID,
		calID:  calID,
	}
	return oauth2.NewClient(ctx, saving), calID, nil
}

// FreeBusyClient returns an authorized http.Client for free/busy lookups
// (check_conflicts = 1). Returns (nil, "", nil) if no such connection exists.
func (c *Client) FreeBusyClient(ctx context.Context, userID string) (*http.Client, string, error) {
	return c.httpClient(ctx, userID, 1, -1)
}

// DestinationClient returns an authorized http.Client for writing events
// (is_destination = 1). Returns (nil, "", nil) if no such connection exists.
func (c *Client) DestinationClient(ctx context.Context, userID string) (*http.Client, string, error) {
	return c.httpClient(ctx, userID, -1, 1)
}

// HasDestination reports whether userID has a destination calendar connection
// (somewhere to write events). Used by the reconciler to skip hosts who can
// never get a calendar event, avoiding pointless retries.
func (c *Client) HasDestination(ctx context.Context, userID string) (bool, error) {
	var x int
	err := c.db.QueryRowContext(ctx,
		`SELECT 1 FROM calendar_connections WHERE user_id = ? AND provider = 'google' AND is_destination = 1 LIMIT 1`,
		userID).Scan(&x)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

// saveToken encrypts and upserts an OAuth token for userID.
// Uses DELETE + INSERT inside a transaction (no unique index on user_id+provider).
func (c *Client) saveToken(ctx context.Context, userID, calID string, tok *oauth2.Token) error {
	accessEnc, err := c.encrypt([]byte(tok.AccessToken))
	if err != nil {
		return err
	}
	var refreshEnc string
	if tok.RefreshToken != "" {
		refreshEnc, err = c.encrypt([]byte(tok.RefreshToken))
		if err != nil {
			return err
		}
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	var expiryStr string
	if !tok.Expiry.IsZero() {
		expiryStr = tok.Expiry.UTC().Format(time.RFC3339)
	}

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("gcal: save token begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM calendar_connections WHERE user_id = ? AND provider = 'google'`,
		userID); err != nil {
		return fmt.Errorf("gcal: save token delete: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO calendar_connections
		    (id, user_id, provider, access_token_enc, refresh_token_enc, calendar_id,
		     check_conflicts, is_destination, expiry_at, created_at)
		VALUES (?, ?, 'google', ?, ?, ?, 1, 1, ?, ?)`,
		uid.New(), userID, accessEnc, refreshEnc, calID, expiryStr, now); err != nil {
		return fmt.Errorf("gcal: save token insert: %w", err)
	}
	return tx.Commit()
}

// savingTokenSource wraps oauth2.TokenSource and persists new tokens to the DB
// whenever the access token changes (i.e. after a refresh).
type savingTokenSource struct {
	inner  oauth2.TokenSource
	client *Client
	userID string
	calID  string
	last   string
}

func (s *savingTokenSource) Token() (*oauth2.Token, error) {
	tok, err := s.inner.Token()
	if err != nil {
		return nil, err
	}
	if tok.AccessToken != s.last {
		s.last = tok.AccessToken
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.client.saveToken(ctx, s.userID, s.calID, tok); err != nil {
			s.client.logger.Error("gcal: failed to persist refreshed token", "error", err, "user_id", s.userID)
		}
	}
	return tok, nil
}

// ----- AES-GCM helpers -----

func (c *Client) encrypt(plaintext []byte) (string, error) {
	return c.encryptEncoding(plaintext, base64.StdEncoding)
}

func (c *Client) decrypt(ciphertext string) ([]byte, error) {
	return c.decryptEncoding(ciphertext, base64.StdEncoding)
}

func (c *Client) encryptEncoding(plaintext []byte, enc *base64.Encoding) (string, error) {
	block, err := aes.NewCipher(c.key[:])
	if err != nil {
		return "", fmt.Errorf("gcal: cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("gcal: gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("gcal: nonce: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, plaintext, nil)
	return enc.EncodeToString(sealed), nil
}

func (c *Client) decryptEncoding(ciphertext string, enc *base64.Encoding) ([]byte, error) {
	b, err := enc.DecodeString(ciphertext)
	if err != nil {
		return nil, fmt.Errorf("gcal: base64 decode: %w", err)
	}
	block, err := aes.NewCipher(c.key[:])
	if err != nil {
		return nil, fmt.Errorf("gcal: cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcal: gcm: %w", err)
	}
	ns := gcm.NonceSize()
	if len(b) < ns {
		return nil, fmt.Errorf("gcal: ciphertext too short")
	}
	plain, err := gcm.Open(nil, b[:ns], b[ns:], nil)
	if err != nil {
		return nil, fmt.Errorf("gcal: decrypt: %w", err)
	}
	return plain, nil
}
