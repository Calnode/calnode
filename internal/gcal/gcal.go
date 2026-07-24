package gcal

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
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/connstore"
	"github.com/calnode/calnode/internal/oauthstore"
	"github.com/calnode/calnode/internal/secret"
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

// Exchange converts an auth code into OAuth tokens and persists them for userID. Captures the
// connected account's email so a user can connect several Google accounts (each a distinct
// row); re-connecting the same account updates its row rather than duplicating it.
func (c *Client) Exchange(ctx context.Context, userID, code, calendarID string) error {
	tok, err := c.config.Exchange(ctx, code)
	if err != nil {
		return fmt.Errorf("gcal: token exchange: %w", err)
	}
	if calendarID == "" {
		calendarID = "primary"
	}
	email := c.fetchAccountEmail(ctx, tok) // best-effort; "" on failure
	return c.saveToken(ctx, userID, calendarID, email, tok)
}

// fetchAccountEmail returns the connected account's email — the primary calendar's id is the
// account email. Best-effort: returns "" on any error (the connection still works; only
// dedup/display degrade). Avoids needing extra OAuth scopes.
func (c *Client) fetchAccountEmail(ctx context.Context, tok *oauth2.Token) string {
	hc := oauth2.NewClient(ctx, c.config.TokenSource(ctx, tok))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBase+"/calendars/primary", nil)
	if err != nil {
		return ""
	}
	resp, err := hc.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var out struct {
		ID string `json:"id"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil {
		return ""
	}
	return out.ID
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

// buildClient turns one connection row's encrypted tokens into an authorized *http.Client
// whose refreshes persist back to that same account's row (keyed by accountEmail).
func (c *Client) buildClient(ctx context.Context, userID, accessEnc, refreshEnc, calID, expiryStr, accountEmail string) (*http.Client, error) {
	access, err := c.decrypt(accessEnc)
	if err != nil {
		return nil, fmt.Errorf("gcal: decrypt access token: %w", err)
	}
	var refresh string
	if refreshEnc != "" {
		rb, err := c.decrypt(refreshEnc)
		if err != nil {
			return nil, fmt.Errorf("gcal: decrypt refresh token: %w", err)
		}
		refresh = string(rb)
	}
	var expiry time.Time
	if expiryStr != "" {
		expiry, _ = time.Parse(time.RFC3339, expiryStr)
	}
	// Zero expiry (legacy/missing) → treat as expired so oauth2 refreshes immediately.
	if expiry.IsZero() {
		expiry = time.Now().Add(-time.Second)
	}
	tok := &oauth2.Token{AccessToken: string(access), RefreshToken: refresh, Expiry: expiry}
	saving := &oauthstore.SavingTokenSource{
		Inner: oauth2.ReuseTokenSource(nil, c.config.TokenSource(ctx, tok)),
		Save: func(ctx context.Context, t *oauth2.Token) error {
			return c.saveToken(ctx, userID, calID, accountEmail, t)
		},
		Logger: c.logger,
		LogMsg: "gcal: failed to persist refreshed token",
		UserID: userID,
	}
	return oauth2.NewClient(ctx, saving), nil
}

// httpClient returns an authorized *http.Client and the calendarID for one matching
// connection (LIMIT 1). Filters by check_conflicts/is_destination (-1 means any). Returns
// (nil, "", nil) when no matching connection exists. Used for the single destination write.
func (c *Client) httpClient(ctx context.Context, userID string, checkConflicts, isDestination int) (*http.Client, string, error) {
	q := `SELECT access_token_enc, COALESCE(refresh_token_enc,''), calendar_id, COALESCE(expiry_at,''), COALESCE(account_email,'')
	      FROM calendar_connections
	      WHERE user_id = ? AND provider = 'google'`
	args := []any{userID}
	frag, fragArgs := connstore.WhereClause(checkConflicts, isDestination)
	q += frag + " LIMIT 1"
	args = append(args, fragArgs...)

	var accessEnc, refreshEnc, calID, expiryStr, accountEmail string
	err := c.db.QueryRowContext(ctx, q, args...).Scan(&accessEnc, &refreshEnc, &calID, &expiryStr, &accountEmail)
	if err == sql.ErrNoRows {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("gcal: load connection: %w", err)
	}
	hc, err := c.buildClient(ctx, userID, accessEnc, refreshEnc, calID, expiryStr, accountEmail)
	if err != nil {
		return nil, "", err
	}
	return hc, calID, nil
}

// fbConn is one conflict-check connection's authorized client + the calendar ids to check
// (the account's selected sub-calendars, or its single bound calendar when unselected).
type fbConn struct {
	hc     *http.Client
	calIDs []string
}

// freeBusyConnections returns an authorized client for EVERY Google connection the user has
// with check_conflicts = 1 (so a user can connect several Google accounts and have them all
// checked for conflicts), each paired with the set of that account's calendars to check for
// conflicts (per-account sub-calendar selection). Decrypt failures on one row are logged and
// skipped (fail-open); an account whose calendars are all deselected is dropped.
func (c *Client) freeBusyConnections(ctx context.Context, userID string) ([]fbConn, error) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT access_token_enc, COALESCE(refresh_token_enc,''), calendar_id, COALESCE(expiry_at,''), COALESCE(account_email,'')
		FROM calendar_connections
		WHERE user_id = ? AND provider = 'google' AND check_conflicts = 1`, userID)
	if err != nil {
		return nil, fmt.Errorf("gcal: load freebusy connections: %w", err)
	}
	type rowData struct{ accessEnc, refreshEnc, calID, expiryStr, accountEmail string }
	var data []rowData
	for rows.Next() {
		var d rowData
		if err := rows.Scan(&d.accessEnc, &d.refreshEnc, &d.calID, &d.expiryStr, &d.accountEmail); err != nil {
			rows.Close() // #nosec G104 -- already returning the scan error; nothing more actionable
			return nil, fmt.Errorf("gcal: scan freebusy connection: %w", err)
		}
		data = append(data, d)
	}
	rows.Close() // #nosec G104 -- rows already fully consumed above; nothing actionable on close error
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Resolve selections + build clients after the cursor is closed: ConflictCalendarIDs runs
	// its own query, which would deadlock the single-connection pool against an open cursor.
	var conns []fbConn
	for _, d := range data {
		calIDs, err := calendar.ConflictCalendarIDs(ctx, c.db, "google", userID, d.accountEmail, d.calID)
		if err != nil {
			return nil, fmt.Errorf("gcal: resolve conflict calendars: %w", err)
		}
		if len(calIDs) == 0 {
			continue // account fully deselected
		}
		hc, err := c.buildClient(ctx, userID, d.accessEnc, d.refreshEnc, d.calID, d.expiryStr, d.accountEmail)
		if err != nil {
			c.logger.Warn("gcal: skipping connection with bad credentials", "user_id", userID, "error", err)
			continue
		}
		conns = append(conns, fbConn{hc: hc, calIDs: calIDs})
	}
	return conns, nil
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

// saveToken encrypts and upserts an OAuth token for ONE Google account (keyed by
// accountEmail), so a user can connect several. Replaces only that account's row (a refresh of
// one account never touches another). On a new connection: check_conflicts=1, and it becomes
// the destination only if the user has none yet. On a refresh (row already exists): the
// existing check_conflicts/is_destination flags are preserved.
func (c *Client) saveToken(ctx context.Context, userID, calID, accountEmail string, tok *oauth2.Token) error {
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

	checkConflicts, isDest, _, err := connstore.ResolveFlags(ctx, tx, userID, "google", accountEmail)
	if err != nil {
		return fmt.Errorf("gcal: save token: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM calendar_connections WHERE user_id = ? AND provider = 'google' AND account_email = ?`,
		userID, accountEmail); err != nil {
		return fmt.Errorf("gcal: save token delete: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO calendar_connections
		    (id, user_id, provider, account_email, access_token_enc, refresh_token_enc, calendar_id,
		     check_conflicts, is_destination, expiry_at, created_at)
		VALUES (?, ?, 'google', ?, ?, ?, ?, ?, ?, ?, ?)`,
		uid.New(), userID, accountEmail, accessEnc, refreshEnc, calID, checkConflicts, isDest, expiryStr, now); err != nil {
		return fmt.Errorf("gcal: save token insert: %w", err)
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
