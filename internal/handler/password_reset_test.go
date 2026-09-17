package handler_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/calnode/calnode/internal/handler"
	"github.com/calnode/calnode/internal/mailer"
)

// resetBaseURL is the configured BASE_URL in these tests. Every assertion about the link
// is made against it, so a link built from anything else fails.
const resetBaseURL = "https://calnode.example"

// resetMailer records every message sent. It is not a *mailer.Noop, so the handler treats
// email as configured.
type resetMailer struct {
	mu   sync.Mutex
	sent []mailer.Message
}

func (m *resetMailer) Send(_ context.Context, msg mailer.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return nil
}

func (m *resetMailer) messages() []mailer.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]mailer.Message(nil), m.sent...)
}

func newResetHandler(t *testing.T) (*handler.Handler, *sql.DB, *resetMailer) {
	t.Helper()
	h, database := newTestHandlerDB(t)
	m := &resetMailer{}
	h.SetMailer(m, resetBaseURL)
	return h, database, m
}

// seedResetUsers inserts one account of every kind the request endpoint must treat
// identically from the outside. Only active@ is eligible for a reset.
func seedResetUsers(t *testing.T, database *sql.DB) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("oldpassword"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO users (id,email,name,iana_timezone,is_admin,email_login,password_hash) VALUES ('u-active','active@example.com','Active','UTC',0,1,?)`, []any{string(hash)}},
		{`INSERT INTO users (id,email,name,iana_timezone,is_admin,email_login,password_hash,archived_at) VALUES ('u-archived','archived@example.com','Archived','UTC',0,1,?,'2026-01-01T00:00:00Z')`, []any{string(hash)}},
		{`INSERT INTO users (id,email,name,iana_timezone,is_admin,email_login,provider,provider_id) VALUES ('u-sso','sso@example.com','SSO','UTC',0,0,'google','g-1')`, nil},
		{`INSERT INTO users (id,email,name,iana_timezone,is_admin,email_login,password_hash) VALUES ('u-bystander','bystander@example.com','Bystander','UTC',0,1,?)`, []any{string(hash)}},
	} {
		if _, err := database.Exec(q.sql, q.args...); err != nil {
			t.Fatalf("seed user: %v", err)
		}
	}
}

func postReset(fn http.HandlerFunc, path, body string, mutate ...func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for _, m := range mutate {
		m(req)
	}
	rec := httptest.NewRecorder()
	fn(rec, req)
	return rec
}

// requestReset posts to the request endpoint and waits for the background send, so the
// caller can assert on what was (or was not) minted and mailed.
func requestReset(h *handler.Handler, email string, mutate ...func(*http.Request)) *httptest.ResponseRecorder {
	rec := postReset(h.RequestPasswordReset, "/v1/auth/password-reset/request", `{"email":"`+email+`"}`, mutate...)
	h.WaitPasswordResetSends()
	return rec
}

func checkReset(h *handler.Handler, token string) *httptest.ResponseRecorder {
	return postReset(h.CheckPasswordReset, "/v1/auth/password-reset/check", `{"token":"`+token+`"}`)
}

func confirmReset(h *handler.Handler, token, password string) *httptest.ResponseRecorder {
	return postReset(h.ConfirmPasswordReset, "/v1/auth/password-reset/confirm",
		`{"token":"`+token+`","password":"`+password+`"}`)
}

var resetLinkRE = regexp.MustCompile(`(\S+)/admin/reset-password#token=([0-9a-f]+)`)

// lastResetToken returns the base and raw token of the newest reset link mailed to to.
func lastResetToken(t *testing.T, m *resetMailer, to string) (base, token string) {
	t.Helper()
	msgs := m.messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if len(msgs[i].To) == 1 && msgs[i].To[0] == to {
			match := resetLinkRE.FindStringSubmatch(msgs[i].Text)
			if match == nil {
				t.Fatalf("reset email to %s has no reset link in its text body:\n%s", to, msgs[i].Text)
			}
			return match[1], match[2]
		}
	}
	t.Fatalf("no reset email was sent to %s (sent: %d messages)", to, len(msgs))
	return "", ""
}

func canLogIn(h *handler.Handler, email, password string) bool {
	rec := httptest.NewRecorder()
	h.LoginEmail(rec, loginEmailReq(email, password))
	return rec.Code == http.StatusOK
}

func countRows(t *testing.T, database *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := database.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

// TestPasswordReset_requestIsIndistinguishable pins the enumeration guarantee: an existing
// account, an unknown address, an archived account, an SSO-only account and an instance
// with email switched off all get the same status, headers and body. Only the eligible
// account is mailed or gets a token row.
func TestPasswordReset_requestIsIndistinguishable(t *testing.T) {
	type outcome struct {
		code        int
		contentType string
		body        string
	}
	cases := []struct {
		name     string
		email    string
		smtpOff  bool
		wantMail bool
	}{
		{name: "existing password account", email: "active@example.com", wantMail: true},
		{name: "unknown address", email: "nobody@example.com"},
		{name: "archived account", email: "archived@example.com"},
		{name: "SSO-only account", email: "sso@example.com"},
		{name: "email not configured", email: "active@example.com", smtpOff: true},
	}

	var first *outcome
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var h *handler.Handler
			var database *sql.DB
			m := &resetMailer{}
			if c.smtpOff {
				h, database = newTestHandlerDB(t) // Noop mailer: email is not configured
				h.SetBaseURL(resetBaseURL)
			} else {
				h, database, m = newResetHandler(t)
			}
			seedResetUsers(t, database)

			rec := requestReset(h, c.email)
			got := outcome{rec.Code, rec.Header().Get("Content-Type"), rec.Body.String()}
			if got.code != http.StatusOK {
				t.Fatalf("status = %d; want 200: %s", got.code, got.body)
			}
			if first == nil {
				first = &got
			} else if got != *first {
				t.Errorf("response differs from the existing-account case:\n got  %+v\n want %+v", got, *first)
			}

			tokens := countRows(t, database, `SELECT COUNT(*) FROM password_reset_tokens`)
			sent := len(m.messages())
			if c.wantMail {
				if tokens != 1 || sent != 1 {
					t.Fatalf("tokens = %d, emails = %d; want 1 and 1", tokens, sent)
				}
				if to := m.messages()[0].To; len(to) != 1 || to[0] != c.email {
					t.Errorf("email went to %v; want [%s]", to, c.email)
				}
			} else if tokens != 0 || sent != 0 {
				t.Errorf("tokens = %d, emails = %d; want 0 and 0", tokens, sent)
			}
		})
	}
}

// TestPasswordReset_requestNormalisesEmail mirrors LoginEmail, which lower-cases and trims.
func TestPasswordReset_requestNormalisesEmail(t *testing.T) {
	h, database, m := newResetHandler(t)
	seedResetUsers(t, database)
	requestReset(h, "  ACTIVE@Example.com ")
	lastResetToken(t, m, "active@example.com")
}

// TestPasswordReset_storesOnlyTheHash scans every column of every table for the raw
// token after a request. The scanner is proven able to find things by locating the hash
// the same way, so a scanner that silently reads nothing cannot pass this test.
func TestPasswordReset_storesOnlyTheHash(t *testing.T) {
	h, database, m := newResetHandler(t)
	seedResetUsers(t, database)
	requestReset(h, "active@example.com")
	_, raw := lastResetToken(t, m, "active@example.com")

	if len(raw) < 64 {
		t.Errorf("token is %d hex characters; want at least 64 (32 random bytes)", len(raw))
	}
	if hits := columnsContaining(t, database, raw); len(hits) != 0 {
		t.Errorf("raw reset token found in the database at %v; only its hash may be stored", hits)
	}
	hash := sha256HexForTest(raw)
	if hits := columnsContaining(t, database, hash); len(hits) != 1 || hits[0] != "password_reset_tokens.token_hash" {
		t.Errorf("token hash found at %v; want exactly password_reset_tokens.token_hash", hits)
	}
}

// columnsContaining returns table.column for every stored text value containing needle,
// across every table in the database. Each table's cursor is drained and closed before
// the next query, per the single-connection rule.
func columnsContaining(t *testing.T, database *sql.DB, needle string) []string {
	t.Helper()
	rows, err := database.Query(`SELECT name FROM sqlite_master WHERE type = 'table'`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		tables = append(tables, name)
	}
	rows.Close()

	var hits []string
	for _, table := range tables {
		rows, err := database.Query(fmt.Sprintf(`SELECT * FROM %q`, table))
		if err != nil {
			t.Fatalf("read %s: %v", table, err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatalf("scan %s: %v", table, err)
			}
			for i, v := range vals {
				var s string
				switch x := v.(type) {
				case string:
					s = x
				case []byte:
					s = string(x)
				}
				if s != "" && strings.Contains(s, needle) {
					hits = append(hits, table+"."+cols[i])
				}
			}
		}
		rows.Close()
	}
	return hits
}

// TestPasswordReset_linkUsesBaseURLNotHostHeader is the host-header poisoning guard. The
// request arrives with every header an attacker could use to name a host, and the mailed
// link must still point at the configured BASE_URL.
func TestPasswordReset_linkUsesBaseURLNotHostHeader(t *testing.T) {
	h, database, m := newResetHandler(t)
	seedResetUsers(t, database)

	requestReset(h, "active@example.com", func(r *http.Request) {
		r.Host = "evil.example"
		r.Header.Set("X-Forwarded-Host", "evil.example")
		r.Header.Set("X-Forwarded-Proto", "http")
		r.Header.Set("Forwarded", "host=evil.example;proto=http")
		r.Header.Set("Origin", "https://evil.example")
		r.Header.Set("Referer", "https://evil.example/admin/forgot-password")
	})

	base, token := lastResetToken(t, m, "active@example.com")
	if base != resetBaseURL {
		t.Errorf("reset link base = %q; want the configured BASE_URL %q", base, resetBaseURL)
	}
	msg := m.messages()[0]
	for part, body := range map[string]string{"text": msg.Text, "html": msg.HTML} {
		if strings.Contains(body, "evil.example") {
			t.Errorf("%s body names the request's host; the link must come from BASE_URL:\n%s", part, body)
		}
		want := resetBaseURL + "/admin/reset-password#token=" + token
		if !strings.Contains(body, want) {
			t.Errorf("%s body lacks %q", part, want)
		}
	}
	// The token rides in the fragment, never a query string or path the server would log.
	if strings.Contains(msg.Text, "?token=") || strings.Contains(msg.Text, "reset-password/"+token) {
		t.Errorf("token must be carried in the URL fragment:\n%s", msg.Text)
	}
}

// TestPasswordReset_confirmSetsPasswordAndIsSingleUse walks the whole flow and then
// replays the same token.
func TestPasswordReset_confirmSetsPasswordAndIsSingleUse(t *testing.T) {
	h, database, m := newResetHandler(t)
	seedResetUsers(t, database)
	requestReset(h, "active@example.com")
	_, token := lastResetToken(t, m, "active@example.com")

	check := checkReset(h, token)
	if check.Code != http.StatusOK || !strings.Contains(check.Body.String(), `"email":"active@example.com"`) {
		t.Fatalf("check before use: %d %s; want 200 with the account email", check.Code, check.Body.String())
	}

	rec := confirmReset(h, token, "brandnewpass1")
	if rec.Code != http.StatusOK {
		t.Fatalf("first confirm: %d %s; want 200", rec.Code, rec.Body.String())
	}
	if !canLogIn(h, "active@example.com", "brandnewpass1") {
		t.Error("new password does not work after reset")
	}
	if canLogIn(h, "active@example.com", "oldpassword") {
		t.Error("old password still works after reset")
	}

	replay := confirmReset(h, token, "attackerpass1")
	if replay.Code != http.StatusNotFound {
		t.Errorf("second confirm with the same token: %d %s; want 404", replay.Code, replay.Body.String())
	}
	if canLogIn(h, "active@example.com", "attackerpass1") {
		t.Fatal("a replayed token changed the password again")
	}
	if !canLogIn(h, "active@example.com", "brandnewpass1") {
		t.Error("the password set by the first confirm was lost")
	}
	if c := checkReset(h, token); c.Code != http.StatusNotFound {
		t.Errorf("check after use: %d; want 404", c.Code)
	}
}

// TestPasswordReset_concurrentConfirmsSpendTheTokenOnce pins that the conditional UPDATE,
// not the lookup in front of it, is what makes a token single-use. Every goroutine passes
// the lookup before any of them finishes hashing, so only the UPDATE can stop the rest.
func TestPasswordReset_concurrentConfirmsSpendTheTokenOnce(t *testing.T) {
	h, database, m := newResetHandler(t)
	seedResetUsers(t, database)
	requestReset(h, "active@example.com")
	_, token := lastResetToken(t, m, "active@example.com")

	const n = 8
	start := make(chan struct{})
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			<-start
			codes[i] = confirmReset(h, token, fmt.Sprintf("concurrent-%d", i)).Code
		})
	}
	close(start)
	wg.Wait()

	ok := 0
	for _, c := range codes {
		switch c {
		case http.StatusOK:
			ok++
		case http.StatusNotFound:
		default:
			t.Errorf("unexpected status %d", c)
		}
	}
	if ok != 1 {
		t.Errorf("%d of %d concurrent confirms succeeded with one token; want exactly 1 (codes %v)", ok, n, codes)
	}
}

func TestPasswordReset_expiredTokenIsRejected(t *testing.T) {
	h, database, m := newResetHandler(t)
	seedResetUsers(t, database)
	requestReset(h, "active@example.com")
	_, token := lastResetToken(t, m, "active@example.com")

	past := time.Now().UTC().Add(-time.Second).Format(time.RFC3339)
	if _, err := database.Exec(`UPDATE password_reset_tokens SET expires_at = ?`, past); err != nil {
		t.Fatalf("expire token: %v", err)
	}
	if c := checkReset(h, token); c.Code != http.StatusNotFound {
		t.Errorf("check with expired token: %d; want 404", c.Code)
	}
	if c := confirmReset(h, token, "brandnewpass1"); c.Code != http.StatusNotFound {
		t.Errorf("confirm with expired token: %d %s; want 404", c.Code, c.Body.String())
	}
	if !canLogIn(h, "active@example.com", "oldpassword") {
		t.Error("an expired token changed the password")
	}
}

func TestPasswordReset_tokenLivesThirtyMinutes(t *testing.T) {
	h, database, _ := newResetHandler(t)
	seedResetUsers(t, database)
	before := time.Now().UTC()
	requestReset(h, "active@example.com")

	var expiresAt string
	if err := database.QueryRow(`SELECT expires_at FROM password_reset_tokens`).Scan(&expiresAt); err != nil {
		t.Fatalf("read expiry: %v", err)
	}
	exp, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		t.Fatalf("parse expiry %q: %v", expiresAt, err)
	}
	if ttl := exp.Sub(before); ttl < 29*time.Minute || ttl > 31*time.Minute {
		t.Errorf("token lifetime = %s; want 30 minutes", ttl)
	}
}

// TestPasswordReset_newerRequestInvalidatesOlderToken: only the most recent link works.
func TestPasswordReset_newerRequestInvalidatesOlderToken(t *testing.T) {
	h, database, m := newResetHandler(t)
	seedResetUsers(t, database)
	requestReset(h, "active@example.com")
	_, older := lastResetToken(t, m, "active@example.com")

	// Step past the per-account cooldown without sleeping through it.
	aged := time.Now().UTC().Add(-2 * time.Minute).Format(time.RFC3339)
	if _, err := database.Exec(`UPDATE password_reset_tokens SET created_at = ?`, aged); err != nil {
		t.Fatalf("age token: %v", err)
	}
	requestReset(h, "active@example.com")
	_, newer := lastResetToken(t, m, "active@example.com")
	if newer == older {
		t.Fatal("second request mailed the same token")
	}

	if c := confirmReset(h, older, "fromolderlink1"); c.Code != http.StatusNotFound {
		t.Errorf("confirm with superseded token: %d %s; want 404", c.Code, c.Body.String())
	}
	if c := confirmReset(h, newer, "fromnewerlink1"); c.Code != http.StatusOK {
		t.Errorf("confirm with newest token: %d %s; want 200", c.Code, c.Body.String())
	}
	if !canLogIn(h, "active@example.com", "fromnewerlink1") {
		t.Error("newest link did not set the password")
	}
}

// TestPasswordReset_cooldownLimitsSendsPerAccount: a burst of requests for one account,
// from however many addresses, mails one link and leaves that link working.
func TestPasswordReset_cooldownLimitsSendsPerAccount(t *testing.T) {
	h, database, m := newResetHandler(t)
	seedResetUsers(t, database)
	for i := range 5 {
		requestReset(h, "active@example.com", func(r *http.Request) {
			r.RemoteAddr = fmt.Sprintf("198.51.100.%d:4000", i+1)
		})
	}
	if got := len(m.messages()); got != 1 {
		t.Fatalf("emails sent for 5 rapid requests = %d; want 1", got)
	}
	_, token := lastResetToken(t, m, "active@example.com")
	if c := checkReset(h, token); c.Code != http.StatusOK {
		t.Errorf("the one link sent was invalidated by requests inside the cooldown: %d", c.Code)
	}

	// Another account is not held back by this one's cooldown.
	requestReset(h, "bystander@example.com")
	lastResetToken(t, m, "bystander@example.com")
}

// TestPasswordReset_revokesSessions: every session the account held is gone, other
// accounts' sessions are untouched, and the caller is signed in with a fresh one.
func TestPasswordReset_revokesSessions(t *testing.T) {
	h, database, m := newResetHandler(t)
	seedResetUsers(t, database)
	for _, s := range []struct{ id, user string }{
		{"stolen-sess", "u-active"}, {"laptop-sess", "u-active"}, {"other-user-sess", "u-bystander"},
	} {
		if _, err := database.Exec(`INSERT INTO sessions (id,user_id,expires_at) VALUES (?,?,'2099-01-01T00:00:00Z')`, s.id, s.user); err != nil {
			t.Fatalf("seed session: %v", err)
		}
	}
	requestReset(h, "active@example.com")
	_, token := lastResetToken(t, m, "active@example.com")

	rec := confirmReset(h, token, "brandnewpass1")
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm: %d %s", rec.Code, rec.Body.String())
	}
	if n := countRows(t, database, `SELECT COUNT(*) FROM sessions WHERE id IN ('stolen-sess','laptop-sess')`); n != 0 {
		t.Errorf("%d pre-reset sessions survived; want all revoked", n)
	}
	if n := countRows(t, database, `SELECT COUNT(*) FROM sessions WHERE id = 'other-user-sess'`); n != 1 {
		t.Error("another account's session was revoked by this reset")
	}

	var fresh string
	for _, c := range rec.Result().Cookies() {
		if c.Name == "calnode_session" {
			fresh = c.Value
		}
	}
	if fresh == "" {
		t.Fatal("confirm did not sign the user in")
	}
	if n := countRows(t, database, `SELECT COUNT(*) FROM sessions WHERE id = ? AND user_id = 'u-active'`, fresh); n != 1 {
		t.Error("the new session cookie has no matching session row")
	}
}

// TestPasswordReset_accountThatStopsQualifyingCannotComplete covers a link minted while
// the account was eligible and used after it stopped being so: archived by an admin, or
// no longer signing in with a password.
func TestPasswordReset_accountThatStopsQualifyingCannotComplete(t *testing.T) {
	cases := []struct {
		name, change string
	}{
		{"archived", `UPDATE users SET archived_at = '2026-09-01T00:00:00Z' WHERE id = 'u-active'`},
		{"password sign-in turned off", `UPDATE users SET email_login = 0 WHERE id = 'u-active'`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, database, m := newResetHandler(t)
			seedResetUsers(t, database)
			if _, err := database.Exec(`INSERT INTO sessions (id,user_id,expires_at) VALUES ('existing-sess','u-active','2099-01-01T00:00:00Z')`); err != nil {
				t.Fatalf("seed session: %v", err)
			}
			requestReset(h, "active@example.com")
			_, token := lastResetToken(t, m, "active@example.com")

			if _, err := database.Exec(c.change); err != nil {
				t.Fatalf("change account: %v", err)
			}
			if r := checkReset(h, token); r.Code != http.StatusNotFound {
				t.Errorf("check: %d; want 404", r.Code)
			}
			rec := confirmReset(h, token, "brandnewpass1")
			if rec.Code != http.StatusNotFound {
				t.Errorf("confirm: %d %s; want 404", rec.Code, rec.Body.String())
			}
			for _, ck := range rec.Result().Cookies() {
				if ck.Name == "calnode_session" {
					t.Error("a refused reset set a session cookie")
				}
			}
			var hash string
			if err := database.QueryRow(`SELECT password_hash FROM users WHERE id = 'u-active'`).Scan(&hash); err != nil {
				t.Fatalf("read hash: %v", err)
			}
			if bcrypt.CompareHashAndPassword([]byte(hash), []byte("oldpassword")) != nil {
				t.Error("a refused reset changed the stored password")
			}
			if n := countRows(t, database, `SELECT COUNT(*) FROM sessions WHERE id = 'existing-sess'`); n != 1 {
				t.Error("a refused reset revoked the account's sessions")
			}
		})
	}
}

// TestPasswordReset_confirmEnforcesPasswordPolicyWithoutSpendingTheLink: a rejected
// password is reported with the same rule text as everywhere else and the link survives.
func TestPasswordReset_confirmEnforcesPasswordPolicyWithoutSpendingTheLink(t *testing.T) {
	h, database, m := newResetHandler(t)
	seedResetUsers(t, database)
	requestReset(h, "active@example.com")
	_, token := lastResetToken(t, m, "active@example.com")

	for _, bad := range []string{"short", strings.Repeat("x", 73)} {
		rec := confirmReset(h, token, bad)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "password must be") {
			t.Errorf("password of %d chars: %d %s; want 400 with the password rule", len(bad), rec.Code, rec.Body.String())
		}
	}
	if rec := confirmReset(h, token, "brandnewpass1"); rec.Code != http.StatusOK {
		t.Errorf("valid confirm after rejected passwords: %d %s; want 200", rec.Code, rec.Body.String())
	}
}

func TestPasswordReset_unknownTokensAreRejectedAlike(t *testing.T) {
	h, database, _ := newResetHandler(t)
	seedResetUsers(t, database)
	var first string
	for _, tok := range []string{"", "not-a-token", strings.Repeat("ab", 32)} {
		for name, rec := range map[string]*httptest.ResponseRecorder{
			"check":   checkReset(h, tok),
			"confirm": confirmReset(h, tok, "brandnewpass1"),
		} {
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s with token %q: %d; want 404", name, tok, rec.Code)
			}
			if first == "" {
				first = rec.Body.String()
			} else if rec.Body.String() != first {
				t.Errorf("%s with token %q: body %s differs from %s", name, tok, rec.Body.String(), first)
			}
		}
	}
}
