// Package microsoft implements calendar.Provider for Microsoft 365 / Outlook via
// the Microsoft Graph API. It mirrors the structure of internal/gcal — both share
// internal/secret (crypto), internal/oauthstore (refresh-persistence), and
// internal/connstore (the "-1 means any" filter convention + destination-claiming
// flag resolution). account_kind is Microsoft-specific and layered on top of
// connstore.ResolveFlags rather than baked into it.
package microsoft

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/microsoft"

	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/connstore"
	"github.com/calnode/calnode/internal/oauthstore"
	"github.com/calnode/calnode/internal/secret"
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
			// offline_access → refresh token; Calendars.ReadWrite → read free/busy +
			// write events; openid → an id_token whose `tid` claim tells us whether
			// this is a work/school or personal account (Teams links need work).
			Scopes: []string{"openid", "offline_access", "https://graph.microsoft.com/Calendars.ReadWrite"},
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
	kind := accountKindFromIDToken(tok)
	email := accountEmailFromIDToken(tok)
	return c.saveToken(ctx, userID, calendarID, email, kind, tok)
}

// accountEmailFromIDToken reads the connected account's email/UPN from the id_token
// (preferred_username, falling back to email). "" if unavailable — used to identify accounts
// so a user can connect several Microsoft accounts.
func accountEmailFromIDToken(tok *oauth2.Token) string {
	raw, _ := tok.Extra("id_token").(string)
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		PreferredUsername string `json:"preferred_username"`
		Email             string `json:"email"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	if claims.PreferredUsername != "" {
		return claims.PreferredUsername
	}
	return claims.Email
}

// consumersTenantID is the fixed tenant a personal Microsoft account reports in
// the id_token `tid` claim. Anything else is a work/school (Entra) tenant.
const consumersTenantID = "9188040d-6c67-4c5b-b112-36a304b66dad"

// accountKindFromIDToken inspects the OAuth response's id_token and returns
// "personal" for consumer Microsoft accounts, "work" for work/school accounts, or
// "" when it can't tell (treated as capable downstream).
func accountKindFromIDToken(tok *oauth2.Token) string {
	raw, _ := tok.Extra("id_token").(string)
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		TID string `json:"tid"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.TID == "" {
		return ""
	}
	if claims.TID == consumersTenantID {
		return "personal"
	}
	return "work"
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

// buildClient turns one connection row's encrypted tokens into an authorized *http.Client
// whose refreshes persist back to that same account's row (keyed by accountEmail).
func (c *Client) buildClient(ctx context.Context, userID, accessEnc, refreshEnc, calID, expiryStr, accountEmail string) (*http.Client, error) {
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
	saving := &oauthstore.SavingTokenSource{
		Inner: oauth2.ReuseTokenSource(nil, c.config.TokenSource(ctx, tok)),
		// kind="" → preserve the account_kind + flags already stored (refresh has no id_token).
		Save: func(ctx context.Context, t *oauth2.Token) error {
			return c.saveToken(ctx, userID, calID, accountEmail, "", t)
		},
		Logger: c.logger,
		LogMsg: "microsoft: failed to persist refreshed token",
		UserID: userID,
	}
	return oauth2.NewClient(ctx, saving), nil
}

// httpClient returns an authorized *http.Client for one matching connection (LIMIT 1),
// filtered by check_conflicts / is_destination (-1 means any). Returns (nil, nil) when no
// matching connection exists. Used for the single destination write.
func (c *Client) httpClient(ctx context.Context, userID string, checkConflicts, isDestination int) (*http.Client, error) {
	q := `SELECT access_token_enc, COALESCE(refresh_token_enc,''), calendar_id, COALESCE(expiry_at,''), COALESCE(account_email,'')
	      FROM calendar_connections WHERE user_id = ? AND provider = ?`
	args := []any{userID, providerName}
	frag, fragArgs := connstore.WhereClause(checkConflicts, isDestination)
	q += frag + " LIMIT 1"
	args = append(args, fragArgs...)

	var accessEnc, refreshEnc, calID, expiryStr, accountEmail string
	err := c.db.QueryRowContext(ctx, q, args...).Scan(&accessEnc, &refreshEnc, &calID, &expiryStr, &accountEmail)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("microsoft: load connection: %w", err)
	}
	return c.buildClient(ctx, userID, accessEnc, refreshEnc, calID, expiryStr, accountEmail)
}

// freeBusyConnections returns an authorized client for EVERY Microsoft connection the user
// has with check_conflicts = 1 (so several connected Microsoft accounts are all checked).
// Bad-credential rows are logged and skipped (fail-open).
func (c *Client) freeBusyConnections(ctx context.Context, userID string) ([]*http.Client, error) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT access_token_enc, COALESCE(refresh_token_enc,''), calendar_id, COALESCE(expiry_at,''), COALESCE(account_email,'')
		FROM calendar_connections
		WHERE user_id = ? AND provider = ? AND check_conflicts = 1`, userID, providerName)
	if err != nil {
		return nil, fmt.Errorf("microsoft: load freebusy connections: %w", err)
	}
	defer rows.Close()
	type rowData struct{ accessEnc, refreshEnc, calID, expiryStr, accountEmail string }
	var data []rowData
	for rows.Next() {
		var d rowData
		if err := rows.Scan(&d.accessEnc, &d.refreshEnc, &d.calID, &d.expiryStr, &d.accountEmail); err != nil {
			return nil, fmt.Errorf("microsoft: scan freebusy connection: %w", err)
		}
		data = append(data, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var clients []*http.Client
	for _, d := range data {
		hc, err := c.buildClient(ctx, userID, d.accessEnc, d.refreshEnc, d.calID, d.expiryStr, d.accountEmail)
		if err != nil {
			c.logger.Warn("microsoft: skipping connection with bad credentials", "user_id", userID, "error", err)
			continue
		}
		clients = append(clients, hc)
	}
	return clients, nil
}

// saveToken encrypts and upserts an OAuth token for ONE Microsoft account (keyed by
// accountEmail), so a user can connect several. Replaces only that account's row. On a new
// connection: check_conflicts=1, destination only if the user has none yet. On a refresh (row
// exists): preserves check_conflicts/is_destination, and account_kind when kind=="".
func (c *Client) saveToken(ctx context.Context, userID, calID, accountEmail, kind string, tok *oauth2.Token) error {
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

	checkConflicts, isDest, existing, err := connstore.ResolveFlags(ctx, tx, userID, providerName, accountEmail)
	if err != nil {
		return fmt.Errorf("microsoft: save token: %w", err)
	}
	// account_kind isn't part of the shared flag set (Google/CalDAV don't have it) — on a
	// refresh (no id_token, so kind==""), fetch and keep whatever kind is already stored.
	if existing && kind == "" {
		_ = tx.QueryRowContext(ctx,
			`SELECT COALESCE(account_kind,'') FROM calendar_connections
			 WHERE user_id = ? AND provider = ? AND account_email = ?`,
			userID, providerName, accountEmail).Scan(&kind)
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM calendar_connections WHERE user_id = ? AND provider = ? AND account_email = ?`,
		userID, providerName, accountEmail); err != nil {
		return fmt.Errorf("microsoft: save token delete: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO calendar_connections
		    (id, user_id, provider, account_email, access_token_enc, refresh_token_enc, calendar_id,
		     check_conflicts, is_destination, expiry_at, created_at, account_kind)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uid.New(), userID, providerName, accountEmail, accessEnc, refreshEnc, calID, checkConflicts, isDest, expiryStr, now, kind); err != nil {
		return fmt.Errorf("microsoft: save token insert: %w", err)
	}
	return tx.Commit()
}

// ----- AES-GCM helpers -----

// encrypt/decrypt (token storage, StdEncoding) delegate to the shared internal/secret
// package — same AES-256-GCM/nonce-prepended/base64.StdEncoding scheme, so existing
// stored tokens keep decrypting unchanged. encryptEncoding/decryptEncoding below are
// kept only for EncryptState/DecryptState (OAuth CSRF state, base64.URLEncoding),
// which secret.Encrypt/Decrypt doesn't support.
func (c *Client) encrypt(plaintext []byte) (string, error) {
	return secret.Encrypt(c.key, string(plaintext))
}

func (c *Client) decrypt(ciphertext string) ([]byte, error) {
	s, err := secret.Decrypt(c.key, ciphertext)
	return []byte(s), err
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
