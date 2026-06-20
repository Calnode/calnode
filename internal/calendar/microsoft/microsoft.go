// Package microsoft implements calendar.Provider for Microsoft 365 / Outlook via
// the Microsoft Graph API. It mirrors the structure of internal/gcal.
//
// NOTE: the token store + AES helpers below intentionally mirror internal/gcal.
// A future cleanup can extract a shared calendar token store; kept separate here
// to avoid destabilising the working Google path while adding Graph.
package microsoft

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
	"golang.org/x/oauth2/microsoft"

	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/uid"
)

const providerName = "microsoft"

// Client implements calendar.Provider for Microsoft Graph.
var _ calendar.Provider = (*Client)(nil)

// Client manages Microsoft Graph OAuth tokens and API access.
type Client struct {
	config  *oauth2.Config
	key     [32]byte
	db      *sql.DB
	logger  *slog.Logger
	apiBase string // base URL for Graph API; overridable in tests
}

// New creates a Client. tenant defaults to "common" (any Microsoft account).
// encKeyHex is the 64-char hex AES-256 encryption key.
func New(db *sql.DB, clientID, clientSecret, tenant, redirectURL, encKeyHex string) (*Client, error) {
	b, err := hex.DecodeString(encKeyHex)
	if err != nil || len(b) != 32 {
		return nil, fmt.Errorf("microsoft: invalid encryption key")
	}
	if tenant == "" {
		tenant = "common"
	}
	var key [32]byte
	copy(key[:], b)
	return &Client{
		config: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Endpoint:     microsoft.AzureADEndpoint(tenant),
			RedirectURL:  redirectURL,
			// offline_access → refresh token; Calendars.ReadWrite → read free/busy + write events.
			Scopes: []string{"offline_access", "https://graph.microsoft.com/Calendars.ReadWrite"},
		},
		key:     key,
		db:      db,
		logger:  slog.Default(),
		apiBase: "https://graph.microsoft.com/v1.0",
	}, nil
}

// Name identifies this provider in the calendar_connections table.
func (c *Client) Name() string { return providerName }

// InvitesGuests is true: Graph emails guests its own invite on event create, so
// Calnode must not also attach an .ics (it would duplicate).
func (c *Client) InvitesGuests() bool { return true }

// AuthURL returns the Microsoft consent page URL with the given state value.
// prompt=select_account forces the account chooser so a cached single-sign-on
// session can't silently reconnect the wrong account (e.g. a mailbox-less admin
// account instead of the user's real Exchange Online mailbox).
func (c *Client) AuthURL(state string) string {
	return c.config.AuthCodeURL(state, oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("prompt", "select_account"))
}

// EncryptState encrypts userID into an opaque, URL-safe OAuth state value.
func (c *Client) EncryptState(userID string) (string, error) {
	return c.encryptEncoding([]byte(userID), base64.URLEncoding)
}

// DecryptState reverses EncryptState.
func (c *Client) DecryptState(state string) (string, error) {
	b, err := c.decryptEncoding(state, base64.URLEncoding)
	return string(b), err
}

// Exchange converts an auth code into OAuth tokens and persists them for userID.
// calendarID is unused for Graph (events go to /me/events) but kept for the
// calendar.Provider interface; stored as a placeholder.
func (c *Client) Exchange(ctx context.Context, userID, code, calendarID string) error {
	tok, err := c.config.Exchange(ctx, code)
	if err != nil {
		return fmt.Errorf("microsoft: token exchange: %w", err)
	}
	if calendarID == "" {
		calendarID = "primary"
	}
	return c.saveToken(ctx, userID, calendarID, tok)
}

// Connected reports whether userID has an active Microsoft connection.
func (c *Client) Connected(ctx context.Context, userID string) (bool, error) {
	var n int
	err := c.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM calendar_connections WHERE user_id = ? AND provider = ?`,
		userID, providerName).Scan(&n)
	return n > 0, err
}

// Disconnect removes all Microsoft connections for userID.
func (c *Client) Disconnect(ctx context.Context, userID string) error {
	_, err := c.db.ExecContext(ctx,
		`DELETE FROM calendar_connections WHERE user_id = ? AND provider = ?`,
		userID, providerName)
	return err
}

// HasDestination reports whether userID has a destination calendar connection.
func (c *Client) HasDestination(ctx context.Context, userID string) (bool, error) {
	var x int
	err := c.db.QueryRowContext(ctx,
		`SELECT 1 FROM calendar_connections WHERE user_id = ? AND provider = ? AND is_destination = 1 LIMIT 1`,
		userID, providerName).Scan(&x)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

// httpClient returns an authorized *http.Client for userID, filtered by
// check_conflicts / is_destination (-1 means any). Returns (nil, nil) when no
// matching connection exists.
func (c *Client) httpClient(ctx context.Context, userID string, checkConflicts, isDestination int) (*http.Client, error) {
	q := `SELECT access_token_enc, COALESCE(refresh_token_enc,''), calendar_id, COALESCE(expiry_at,'')
	      FROM calendar_connections WHERE user_id = ? AND provider = ?`
	args := []any{userID, providerName}
	if checkConflicts >= 0 {
		q += " AND check_conflicts = ?"
		args = append(args, checkConflicts)
	}
	if isDestination >= 0 {
		q += " AND is_destination = ?"
		args = append(args, isDestination)
	}
	q += " LIMIT 1"

	var accessEnc, refreshEnc, calID, expiryStr string
	err := c.db.QueryRowContext(ctx, q, args...).Scan(&accessEnc, &refreshEnc, &calID, &expiryStr)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("microsoft: load connection: %w", err)
	}

	access, err := c.decrypt(accessEnc)
	if err != nil {
		return nil, fmt.Errorf("microsoft: decrypt access token: %w", err)
	}
	var refresh string
	if refreshEnc != "" {
		rb, err := c.decrypt(refreshEnc)
		if err != nil {
			return nil, fmt.Errorf("microsoft: decrypt refresh token: %w", err)
		}
		refresh = string(rb)
	}

	var expiry time.Time
	if expiryStr != "" {
		expiry, _ = time.Parse(time.RFC3339, expiryStr)
	}
	if expiry.IsZero() {
		expiry = time.Now().Add(-time.Second) // force refresh of a stale/unknown token
	}

	tok := &oauth2.Token{AccessToken: string(access), RefreshToken: refresh, Expiry: expiry}
	src := c.config.TokenSource(ctx, tok)
	saving := &savingTokenSource{
		inner:  oauth2.ReuseTokenSource(nil, src),
		client: c,
		userID: userID,
		calID:  calID,
	}
	return oauth2.NewClient(ctx, saving), nil
}

// saveToken encrypts and upserts an OAuth token for userID (DELETE+INSERT in a tx).
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
		return fmt.Errorf("microsoft: save token begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM calendar_connections WHERE user_id = ? AND provider = ?`,
		userID, providerName); err != nil {
		return fmt.Errorf("microsoft: save token delete: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO calendar_connections
		    (id, user_id, provider, access_token_enc, refresh_token_enc, calendar_id,
		     check_conflicts, is_destination, expiry_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, 1, 1, ?, ?)`,
		uid.New(), userID, providerName, accessEnc, refreshEnc, calID, expiryStr, now); err != nil {
		return fmt.Errorf("microsoft: save token insert: %w", err)
	}
	return tx.Commit()
}

// savingTokenSource persists refreshed tokens to the DB when the access token changes.
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
			s.client.logger.Error("microsoft: failed to persist refreshed token", "error", err, "user_id", s.userID)
		}
	}
	return tok, nil
}

// ----- AES-GCM helpers (mirror internal/gcal) -----

func (c *Client) encrypt(plaintext []byte) (string, error) {
	return c.encryptEncoding(plaintext, base64.StdEncoding)
}

func (c *Client) decrypt(ciphertext string) ([]byte, error) {
	return c.decryptEncoding(ciphertext, base64.StdEncoding)
}

func (c *Client) encryptEncoding(plaintext []byte, enc *base64.Encoding) (string, error) {
	block, err := aes.NewCipher(c.key[:])
	if err != nil {
		return "", fmt.Errorf("microsoft: cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("microsoft: gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("microsoft: nonce: %w", err)
	}
	return enc.EncodeToString(gcm.Seal(nonce, nonce, plaintext, nil)), nil
}

func (c *Client) decryptEncoding(ciphertext string, enc *base64.Encoding) ([]byte, error) {
	b, err := enc.DecodeString(ciphertext)
	if err != nil {
		return nil, fmt.Errorf("microsoft: base64 decode: %w", err)
	}
	block, err := aes.NewCipher(c.key[:])
	if err != nil {
		return nil, fmt.Errorf("microsoft: cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("microsoft: gcm: %w", err)
	}
	ns := gcm.NonceSize()
	if len(b) < ns {
		return nil, fmt.Errorf("microsoft: ciphertext too short")
	}
	plain, err := gcm.Open(nil, b[:ns], b[ns:], nil)
	if err != nil {
		return nil, fmt.Errorf("microsoft: decrypt: %w", err)
	}
	return plain, nil
}
