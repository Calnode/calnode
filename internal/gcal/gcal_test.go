package gcal

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/calnode/calnode/internal/db"
)

// testKeyHex is a valid 64-char hex key (32 bytes) used across tests.
const testKeyHex = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open("sqlite://:memory:")
	if err != nil {
		t.Fatalf("newTestDB: open: %v", err)
	}
	if err := db.Migrate(database); err != nil {
		t.Fatalf("newTestDB: migrate: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func newTestClient(t *testing.T) *Client {
	t.Helper()
	c, err := New(newTestDB(t), "client-id", "client-secret", "http://localhost/callback", testKeyHex)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// seedUser inserts a minimal user row so that calendar_connections FK is satisfied.
func seedUser(t *testing.T, db *sql.DB, userID string) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO users (id, email, name, iana_timezone, is_admin, created_at)
		VALUES (?, ?, 'Test User', 'UTC', 0, '2026-01-01T00:00:00Z')`,
		userID, userID+"@test.example")
	if err != nil {
		t.Fatalf("seedUser(%q): %v", userID, err)
	}
}

// ---------------------------------------------------------------------------
// New — constructor validation
// ---------------------------------------------------------------------------

func TestNew_badHexKey(t *testing.T) {
	_, err := New(newTestDB(t), "id", "secret", "http://localhost/cb", "not-valid-hex!!")
	if err == nil {
		t.Error("expected error for non-hex key")
	}
}

func TestNew_shortKey(t *testing.T) {
	// 32 hex chars = 16 bytes; need 64 hex chars (32 bytes).
	_, err := New(newTestDB(t), "id", "secret", "http://localhost/cb", strings.Repeat("de", 16))
	if err == nil {
		t.Error("expected error for 16-byte key (want 32)")
	}
}

func TestNew_validKey(t *testing.T) {
	if _, err := New(newTestDB(t), "id", "secret", "http://localhost/cb", testKeyHex); err != nil {
		t.Errorf("unexpected error with valid key: %v", err)
	}
}

// ---------------------------------------------------------------------------
// AES-GCM encrypt / decrypt
// ---------------------------------------------------------------------------

func TestEncryptDecrypt_roundtrip(t *testing.T) {
	c := newTestClient(t)
	cases := []string{"", "hello", "access_token_value", strings.Repeat("x", 4096)}
	for _, msg := range cases {
		enc, err := c.encrypt([]byte(msg))
		if err != nil {
			t.Fatalf("encrypt(%q): %v", msg, err)
		}
		got, err := c.decrypt(enc)
		if err != nil {
			t.Fatalf("decrypt: %v", err)
		}
		if string(got) != msg {
			t.Errorf("roundtrip: got %q; want %q", got, msg)
		}
	}
}

func TestDecrypt_tamperedCiphertextRejected(t *testing.T) {
	c := newTestClient(t)
	enc, _ := c.encrypt([]byte("secret-token"))
	// Corrupt the last 4 characters of the base64 string (modifies the GCM tag).
	corrupted := enc[:len(enc)-4] + "ZZZZ"
	if _, err := c.decrypt(corrupted); err == nil {
		t.Error("expected error for tampered ciphertext; got nil")
	}
}

func TestDecrypt_emptyStringRejected(t *testing.T) {
	c := newTestClient(t)
	if _, err := c.decrypt(""); err == nil {
		t.Error("expected error for empty ciphertext")
	}
}

// ---------------------------------------------------------------------------
// EncryptState / DecryptState — CSRF-prevention state parameter
// ---------------------------------------------------------------------------

func TestEncryptState_decryptRoundTrip(t *testing.T) {
	c := newTestClient(t)
	userID := "01J4TESTUSERID"

	state, err := c.EncryptState(userID)
	if err != nil {
		t.Fatalf("EncryptState: %v", err)
	}
	// State must be URL-safe (no + or /).
	if strings.ContainsAny(state, "+/") {
		t.Error("state contains non-URL-safe characters (+ or /)")
	}

	got, err := c.DecryptState(state)
	if err != nil {
		t.Fatalf("DecryptState: %v", err)
	}
	if got != userID {
		t.Errorf("got userID %q; want %q", got, userID)
	}
}

func TestDecryptState_tamperedRejected(t *testing.T) {
	c := newTestClient(t)
	state, _ := c.EncryptState("some-user-id")
	corrupted := state[:len(state)-4] + "ZZZZ"
	if _, err := c.DecryptState(corrupted); err == nil {
		t.Error("expected error for tampered state")
	}
}

func TestDecryptState_emptyRejected(t *testing.T) {
	c := newTestClient(t)
	if _, err := c.DecryptState(""); err == nil {
		t.Error("expected error for empty state")
	}
}

// State encrypted by one client must not decrypt with a different key.
func TestDecryptState_wrongKeyRejected(t *testing.T) {
	c1 := newTestClient(t)
	otherKey := strings.Repeat("cd", 32) // 64 hex chars = valid 32-byte key
	c2, _ := New(newTestDB(t), "id", "secret", "http://localhost/cb", otherKey)

	state, _ := c1.EncryptState("user-1")
	if _, err := c2.DecryptState(state); err == nil {
		t.Error("expected error when decrypting with wrong key")
	}
}

// ---------------------------------------------------------------------------
// saveToken / Connected / Disconnect
// ---------------------------------------------------------------------------

func TestConnected_falseWhenNoConnection(t *testing.T) {
	c := newTestClient(t)
	ok, err := c.Connected(context.Background(), "user-1")
	if err != nil {
		t.Fatalf("Connected: %v", err)
	}
	if ok {
		t.Error("Connected = true; want false for user with no connection")
	}
}

func TestSaveToken_connectedReturnsTrue(t *testing.T) {
	c := newTestClient(t)
	seedUser(t, c.db, "user-1")
	tok := &oauth2.Token{AccessToken: "access-abc", RefreshToken: "refresh-xyz"}
	if err := c.saveToken(context.Background(), "user-1", "primary", "", tok); err != nil {
		t.Fatalf("saveToken: %v", err)
	}
	ok, err := c.Connected(context.Background(), "user-1")
	if err != nil {
		t.Fatalf("Connected: %v", err)
	}
	if !ok {
		t.Error("Connected = false; want true after saveToken")
	}
}

func TestSaveToken_upsertReplacesExisting(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	seedUser(t, c.db, "user-1")
	c.saveToken(ctx, "user-1", "primary", "", &oauth2.Token{AccessToken: "old"})                    //nolint:errcheck
	c.saveToken(ctx, "user-1", "primary", "", &oauth2.Token{AccessToken: "new", RefreshToken: "r"}) //nolint:errcheck

	var n int
	c.db.QueryRowContext(ctx, //nolint:errcheck
		`SELECT COUNT(*) FROM calendar_connections WHERE user_id = ?`, "user-1").Scan(&n)
	if n != 1 {
		t.Errorf("got %d rows after upsert; want 1", n)
	}
}

func TestSaveToken_multiAccountDestinationInvariants(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	seedUser(t, c.db, "user-1")

	// First account connected → becomes the destination.
	c.saveToken(ctx, "user-1", "primary", "work@x.test", &oauth2.Token{AccessToken: "a1", RefreshToken: "r1"}) //nolint:errcheck
	// Second account → checked for conflicts but NOT the destination.
	c.saveToken(ctx, "user-1", "primary", "personal@x.test", &oauth2.Token{AccessToken: "a2", RefreshToken: "r2"}) //nolint:errcheck

	rows := map[string]int{}
	r, _ := c.db.QueryContext(ctx, `SELECT account_email, is_destination FROM calendar_connections WHERE user_id='user-1'`)
	defer r.Close()
	n := 0
	for r.Next() {
		var email string
		var dest int
		_ = r.Scan(&email, &dest)
		rows[email] = dest
		n++
	}
	if n != 2 {
		t.Fatalf("got %d connections; want 2 (one per account)", n)
	}
	if rows["work@x.test"] != 1 || rows["personal@x.test"] != 0 {
		t.Errorf("destination invariant wrong: %v (want work=1, personal=0)", rows)
	}

	// A token refresh of the SECOND account must NOT steal the destination or wipe the first.
	c.saveToken(ctx, "user-1", "primary", "personal@x.test", &oauth2.Token{AccessToken: "a2b", RefreshToken: "r2"}) //nolint:errcheck
	var workDest, personalDest, total int
	c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM calendar_connections WHERE user_id='user-1'`).Scan(&total)                             //nolint:errcheck
	c.db.QueryRowContext(ctx, `SELECT is_destination FROM calendar_connections WHERE account_email='work@x.test'`).Scan(&workDest)         //nolint:errcheck
	c.db.QueryRowContext(ctx, `SELECT is_destination FROM calendar_connections WHERE account_email='personal@x.test'`).Scan(&personalDest) //nolint:errcheck
	if total != 2 || workDest != 1 || personalDest != 0 {
		t.Errorf("after refresh: total=%d work.dest=%d personal.dest=%d; want 2,1,0", total, workDest, personalDest)
	}
}

func TestDisconnect_removesConnection(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	seedUser(t, c.db, "user-1")
	c.saveToken(ctx, "user-1", "primary", "", &oauth2.Token{AccessToken: "tok"}) //nolint:errcheck

	if err := c.Disconnect(ctx, "user-1"); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}
	ok, _ := c.Connected(ctx, "user-1")
	if ok {
		t.Error("Connected = true; want false after Disconnect")
	}
}

func TestDisconnect_noOpWhenNotConnected(t *testing.T) {
	c := newTestClient(t)
	if err := c.Disconnect(context.Background(), "never-connected"); err != nil {
		t.Errorf("Disconnect on absent user returned error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// HTTPClient helpers
// ---------------------------------------------------------------------------

func TestFreeBusyConnections_emptyWhenNotConnected(t *testing.T) {
	c := newTestClient(t)
	conns, err := c.freeBusyConnections(context.Background(), "user-1")
	if err != nil {
		t.Fatalf("freeBusyConnections: %v", err)
	}
	if len(conns) != 0 {
		t.Errorf("got %d connections; want 0", len(conns))
	}
}

func TestFreeBusyConnections_returnsConnectedCalendars(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	seedUser(t, c.db, "user-1")
	c.saveToken(ctx, "user-1", "user@example.com", "user@example.com", &oauth2.Token{ //nolint:errcheck
		AccessToken: "tok",
		Expiry:      time.Now().Add(time.Hour),
	})

	conns, err := c.freeBusyConnections(ctx, "user-1")
	if err != nil {
		t.Fatalf("freeBusyConnections: %v", err)
	}
	if len(conns) != 1 {
		t.Fatalf("got %d connections; want 1", len(conns))
	}
	if conns[0].hc == nil {
		t.Error("expected non-nil http.Client")
	}
	if len(conns[0].calIDs) != 1 || conns[0].calIDs[0] != "user@example.com" {
		t.Errorf("calIDs = %v; want [user@example.com]", conns[0].calIDs)
	}
}

// insertConnCal adds a per-account sub-calendar selection row for the google provider.
func insertConnCal(t *testing.T, db *sql.DB, userID, account, calID string, check bool) {
	t.Helper()
	cc := 0
	if check {
		cc = 1
	}
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO connection_calendars (id, user_id, provider, account_email, calendar_id, name, check_conflicts, is_destination)
		VALUES (lower(hex(randomblob(16))), ?, 'google', ?, ?, '', ?, 0)`,
		userID, account, calID, cc)
	if err != nil {
		t.Fatalf("insertConnCal(%q): %v", calID, err)
	}
}

// When an account has sub-calendar selections, only the checked calendars are returned — not
// the account's single bound calendar.
func TestFreeBusyConnections_honorsSubCalendarSelection(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	seedUser(t, c.db, "user-1")
	c.saveToken(ctx, "user-1", "primary", "user@example.com", &oauth2.Token{ //nolint:errcheck
		AccessToken: "tok", Expiry: time.Now().Add(time.Hour),
	})
	insertConnCal(t, c.db, "user-1", "user@example.com", "primary", true)
	insertConnCal(t, c.db, "user-1", "user@example.com", "team@group.calendar.google.com", true)
	insertConnCal(t, c.db, "user-1", "user@example.com", "muted@group.calendar.google.com", false)

	conns, err := c.freeBusyConnections(ctx, "user-1")
	if err != nil {
		t.Fatalf("freeBusyConnections: %v", err)
	}
	if len(conns) != 1 {
		t.Fatalf("got %d connections; want 1", len(conns))
	}
	got := map[string]bool{}
	for _, id := range conns[0].calIDs {
		got[id] = true
	}
	if len(got) != 2 || !got["primary"] || !got["team@group.calendar.google.com"] {
		t.Errorf("calIDs = %v; want {primary, team@group.calendar.google.com}", conns[0].calIDs)
	}
	if got["muted@group.calendar.google.com"] {
		t.Error("deselected calendar leaked into conflict check")
	}
}

// When every calendar of an account is deselected, that account is dropped entirely.
func TestFreeBusyConnections_skipsFullyDeselectedAccount(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	seedUser(t, c.db, "user-1")
	c.saveToken(ctx, "user-1", "primary", "user@example.com", &oauth2.Token{ //nolint:errcheck
		AccessToken: "tok", Expiry: time.Now().Add(time.Hour),
	})
	insertConnCal(t, c.db, "user-1", "user@example.com", "primary", false)

	conns, err := c.freeBusyConnections(ctx, "user-1")
	if err != nil {
		t.Fatalf("freeBusyConnections: %v", err)
	}
	if len(conns) != 0 {
		t.Fatalf("got %d connections; want 0 (account fully deselected)", len(conns))
	}
}

// ---------------------------------------------------------------------------
// AuthURL
// ---------------------------------------------------------------------------

func TestAuthURL_containsExpectedParams(t *testing.T) {
	c := newTestClient(t)
	u := c.AuthURL("my-state")
	if !strings.Contains(u, "client-id") {
		t.Errorf("AuthURL %q missing client_id", u)
	}
	if !strings.Contains(u, "my-state") {
		t.Errorf("AuthURL %q missing state", u)
	}
	if !strings.Contains(u, "offline") {
		t.Errorf("AuthURL %q missing access_type=offline", u)
	}
}
