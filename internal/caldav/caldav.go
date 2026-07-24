// Package caldav implements calendar.Provider for CalDAV servers (Apple iCloud,
// Fastmail, Nextcloud, and generic RFC 4791 servers).
//
// Unlike Google/Microsoft, CalDAV has no instance-level OAuth app: each host
// connects their OWN server with an app-specific password (username + password
// over HTTPS Basic auth). So the OAuth methods on the Provider interface are
// inert here — connecting is done through a dedicated form handler that calls
// Connect() after discovering the user's calendar collection. Everything else
// (free/busy union, destination write-back, multi-account, the .ics gate) works
// through the same calendar.Service plumbing as the other providers.
package caldav

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/calnode/calnode/internal/calendar"
	"github.com/calnode/calnode/internal/connstore"
	"github.com/calnode/calnode/internal/netutil"
	"github.com/calnode/calnode/internal/secret"
	"github.com/calnode/calnode/internal/uid"
)

// Client implements calendar.Provider for CalDAV servers.
var _ calendar.Provider = (*Client)(nil)

// Client manages CalDAV connections (encrypted app-password credentials) and access.
type Client struct {
	db     *sql.DB
	key    [32]byte
	logger *slog.Logger
	hc     *http.Client
}

// New creates a Client. encKeyHex is the 64-char hex AES-256 encryption key (the same
// instance key used to encrypt the other providers' tokens).
func New(db *sql.DB, encKeyHex string) (*Client, error) {
	b, err := hex.DecodeString(encKeyHex)
	if err != nil || len(b) != 32 {
		return nil, fmt.Errorf("caldav: invalid encryption key")
	}
	var key [32]byte
	copy(key[:], b)
	return &Client{
		db:     db,
		key:    key,
		logger: slog.Default(),
		hc: &http.Client{
			Timeout: 20 * time.Second,
			// CalDAV discovery follows redirects manually (preserving the PROPFIND method),
			// so disable Go's auto-follow which would downgrade 301/302 to GET.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			// server_url is host-supplied — a self-hosted Nextcloud/Radicale/Baïkal
			// instance on the operator's own private network or even localhost is a
			// legitimate, intended configuration (this is a self-hostable product),
			// so use the narrower metadata-only guard rather than blocking private
			// ranges outright. Cloud-metadata addresses are never a real CalDAV
			// server for anyone. Manual redirect-following (webdav.go) always
			// re-enters c.hc.Do, so every hop gets re-checked too.
			Transport: netutil.MetadataSafeTransport(slog.Default(), "caldav: SSRF block"),
		},
	}, nil
}

// Name identifies this provider in the calendar_connections table.
func (c *Client) Name() string { return "caldav" }

// InvitesGuests is false: CalDAV (without RFC 6638 iTIP scheduling, which we don't drive)
// does NOT email guests, so Calnode must attach its own .ics invite when a CalDAV calendar
// is the destination. This is what the email .ics gate keys on.
func (c *Client) InvitesGuests() bool { return false }

// ----- OAuth interface methods (inert for CalDAV) -----
//
// CalDAV is credential-based, not redirect-OAuth, so there is no consent URL or code
// exchange. Connecting happens via Connect() from the dedicated form handler. EncryptState
// / DecryptState still use the real AES-GCM helpers (harmless, and keeps the shared OAuth
// callback safe if CalDAV ever happens to be the primary provider).

// AuthURL returns "" — CalDAV has no OAuth consent page; connect via the form.
func (c *Client) AuthURL(string) string { return "" }

// EncryptState encrypts userID into an opaque, URL-safe string (same scheme as the others).
func (c *Client) EncryptState(userID string) (string, error) {
	return c.encryptEncoding([]byte(userID), base64.URLEncoding)
}

// DecryptState reverses EncryptState.
func (c *Client) DecryptState(state string) (string, error) {
	b, err := c.decryptEncoding(state, base64.URLEncoding)
	return string(b), err
}

// Exchange is not used for CalDAV (no auth code). Connect via the CalDAV form handler.
func (c *Client) Exchange(context.Context, string, string, string) error {
	return errors.New("caldav: connect with username + app password, not OAuth")
}

// Connected reports whether userID has any CalDAV connection.
func (c *Client) Connected(ctx context.Context, userID string) (bool, error) {
	var n int
	err := c.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM calendar_connections WHERE user_id = ? AND provider = 'caldav'`,
		userID).Scan(&n)
	return n > 0, err
}

// Disconnect removes all CalDAV connections for userID.
func (c *Client) Disconnect(ctx context.Context, userID string) error {
	_, err := c.db.ExecContext(ctx,
		`DELETE FROM calendar_connections WHERE user_id = ? AND provider = 'caldav'`, userID)
	return err
}

// HasDestination reports whether userID has a CalDAV destination connection.
func (c *Client) HasDestination(ctx context.Context, userID string) (bool, error) {
	var x int
	err := c.db.QueryRowContext(ctx,
		`SELECT 1 FROM calendar_connections WHERE user_id = ? AND provider = 'caldav' AND is_destination = 1 LIMIT 1`,
		userID).Scan(&x)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

// conn is one CalDAV connection's resolved credentials + calendar collection URL.
type conn struct {
	id       string
	username string
	password string
	calURL   string
}

// loadConn returns ONE matching connection (LIMIT 1), filtered by check_conflicts /
// is_destination (-1 means "any"). Returns ok=false when none matches.
func (c *Client) loadConn(ctx context.Context, userID string, checkConflicts, isDestination int) (conn, bool, error) {
	q := `SELECT id, COALESCE(account_email,''), access_token_enc, calendar_id
	      FROM calendar_connections
	      WHERE user_id = ? AND provider = 'caldav'`
	args := []any{userID}
	frag, fragArgs := connstore.WhereClause(checkConflicts, isDestination)
	q += frag + " LIMIT 1"
	args = append(args, fragArgs...)

	var cn conn
	var pwEnc string
	switch err := c.db.QueryRowContext(ctx, q, args...).Scan(&cn.id, &cn.username, &pwEnc, &cn.calURL); err {
	case nil:
	case sql.ErrNoRows:
		return conn{}, false, nil
	default:
		return conn{}, false, fmt.Errorf("caldav: load connection: %w", err)
	}
	pw, err := c.decrypt(pwEnc)
	if err != nil {
		return conn{}, false, fmt.Errorf("caldav: decrypt password: %w", err)
	}
	cn.password = string(pw)
	return cn, true, nil
}

// conflictConns returns every CalDAV connection for the user with check_conflicts = 1, so a
// user can connect several CalDAV accounts and have them all checked. Decrypt failures on one
// row are logged and skipped (fail-open).
func (c *Client) conflictConns(ctx context.Context, userID string) ([]conn, error) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT id, COALESCE(account_email,''), access_token_enc, calendar_id
		FROM calendar_connections
		WHERE user_id = ? AND provider = 'caldav' AND check_conflicts = 1`, userID)
	if err != nil {
		return nil, fmt.Errorf("caldav: load conflict connections: %w", err)
	}
	type rowData struct{ id, username, pwEnc, calURL string }
	var data []rowData
	for rows.Next() {
		var d rowData
		if err := rows.Scan(&d.id, &d.username, &d.pwEnc, &d.calURL); err != nil {
			rows.Close() // #nosec G104 -- already returning the scan error; nothing more actionable
			return nil, fmt.Errorf("caldav: scan conflict connection: %w", err)
		}
		data = append(data, d)
	}
	rows.Close() // #nosec G104 -- rows already fully consumed above; nothing actionable on close error
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Resolve selection + decrypt after the cursor is closed (single-connection DB pool; the
	// ConflictCalendarIDs query would deadlock against an open cursor). CalDAV binds one
	// calendar per connection, so the sub-calendar picker is simply an on/off toggle: an empty
	// result means the user deselected it.
	var conns []conn
	for _, d := range data {
		calIDs, err := calendar.ConflictCalendarIDs(ctx, c.db, "caldav", userID, d.username, d.calURL)
		if err != nil {
			return nil, fmt.Errorf("caldav: resolve conflict calendars: %w", err)
		}
		if len(calIDs) == 0 {
			continue // deselected
		}
		pw, err := c.decrypt(d.pwEnc)
		if err != nil {
			c.logger.Warn("caldav: skipping connection with bad credentials", "user_id", userID, "error", err)
			continue
		}
		conns = append(conns, conn{id: d.id, username: d.username, password: string(pw), calURL: d.calURL})
	}
	return conns, nil
}

// saveConnection encrypts and upserts ONE CalDAV account (keyed by accountEmail/username), so
// a user can connect several. On a new connection: check_conflicts=1, and it becomes the
// destination only if the user has none yet. On a re-connect (row already exists for this
// account): the existing check_conflicts/is_destination flags are preserved and only the
// password + calendar URL are refreshed. Mirrors gcal.saveToken.
func (c *Client) saveConnection(ctx context.Context, userID, accountEmail, password, calURL string) error {
	pwEnc, err := c.encrypt([]byte(password))
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("caldav: save begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	checkConflicts, isDest, _, err := connstore.ResolveFlags(ctx, tx, userID, "caldav", accountEmail)
	if err != nil {
		return fmt.Errorf("caldav: save: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM calendar_connections WHERE user_id = ? AND provider = 'caldav' AND account_email = ?`,
		userID, accountEmail); err != nil {
		return fmt.Errorf("caldav: save delete: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO calendar_connections
		    (id, user_id, provider, account_email, access_token_enc, calendar_id,
		     check_conflicts, is_destination, created_at)
		VALUES (?, ?, 'caldav', ?, ?, ?, ?, ?, ?)`,
		uid.New(), userID, accountEmail, pwEnc, calURL, checkConflicts, isDest, now); err != nil {
		return fmt.Errorf("caldav: save insert: %w", err)
	}
	return tx.Commit()
}

// ----- AES-GCM helpers -----

// encrypt/decrypt (credential storage, StdEncoding) delegate to the shared
// internal/secret package — same AES-256-GCM/nonce-prepended/base64.StdEncoding
// scheme, so existing stored credentials keep decrypting unchanged.
// encryptEncoding/decryptEncoding below remain for EncryptState/DecryptState
// (OAuth CSRF state, base64.URLEncoding), which secret.Encrypt/Decrypt doesn't support.
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
		return "", fmt.Errorf("caldav: cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("caldav: gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("caldav: nonce: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, plaintext, nil)
	return enc.EncodeToString(sealed), nil
}

func (c *Client) decryptEncoding(ciphertext string, enc *base64.Encoding) ([]byte, error) {
	b, err := enc.DecodeString(ciphertext)
	if err != nil {
		return nil, fmt.Errorf("caldav: base64 decode: %w", err)
	}
	block, err := aes.NewCipher(c.key[:])
	if err != nil {
		return nil, fmt.Errorf("caldav: cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("caldav: gcm: %w", err)
	}
	ns := gcm.NonceSize()
	if len(b) < ns {
		return nil, fmt.Errorf("caldav: ciphertext too short")
	}
	plain, err := gcm.Open(nil, b[:ns], b[ns:], nil)
	if err != nil {
		return nil, fmt.Errorf("caldav: decrypt: %w", err)
	}
	return plain, nil
}
