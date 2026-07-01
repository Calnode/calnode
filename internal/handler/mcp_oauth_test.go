package handler_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// drives the full OAuth "Connect" flow against the handler: dynamic client
// registration → consent (with a session) → code → token → the token authenticates as
// the workspace user. Also checks PKCE enforcement and refresh-token rotation.
func TestMCP_OAuthFlow(t *testing.T) {
	h, database, _, userID := setupWorkspaceWithDB(t)

	// A logged-in admin session (the consent step requires one).
	const sessID = "oauth-test-session"
	if _, err := database.Exec(`INSERT INTO sessions (id, user_id, expires_at) VALUES (?, ?, ?)`,
		sessID, userID, time.Now().Add(time.Hour).UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	session := &http.Cookie{Name: "calnode_session", Value: sessID}

	const redirectURI = "https://claude.ai/api/mcp/auth_callback"

	// 1. Dynamic client registration.
	regBody := `{"client_name":"Claude","redirect_uris":["` + redirectURI + `"]}`
	rec := httptest.NewRecorder()
	h.RegisterOAuthClient(rec, httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(regBody)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("register: %d — %s", rec.Code, rec.Body.String())
	}
	var reg struct {
		ClientID string `json:"client_id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &reg)
	if reg.ClientID == "" {
		t.Fatal("register: no client_id")
	}

	// PKCE pair.
	verifier := "verifier-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	authForm := func() url.Values {
		return url.Values{
			"response_type":         {"code"},
			"client_id":             {reg.ClientID},
			"redirect_uri":          {redirectURI},
			"code_challenge":        {challenge},
			"code_challenge_method": {"S256"},
			"state":                 {"xyz-state"},
			"scope":                 {"mcp"},
		}
	}

	// 2a. The consent page renders for a signed-in user.
	greq := httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+authForm().Encode(), nil)
	greq.AddCookie(session)
	grec := httptest.NewRecorder()
	h.AuthorizeMCP(grec, greq)
	if grec.Code != http.StatusOK || !strings.Contains(grec.Body.String(), "Allow") || !strings.Contains(grec.Body.String(), "Claude") {
		t.Fatalf("consent render: %d — %s", grec.Code, grec.Body.String())
	}
	// form-action CSP must allow the client's redirect origin, or the browser blocks the
	// post-consent redirect back to the client (enforced across redirects).
	if csp := grec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "https://claude.ai") {
		t.Errorf("consent CSP form-action missing client origin: %q", csp)
	}
	var csrfCookie *http.Cookie
	for _, c := range grec.Result().Cookies() {
		if c.Name == "calnode_oauth_csrf" {
			csrfCookie = c
		}
	}
	if csrfCookie == nil || csrfCookie.Value == "" {
		t.Fatalf("consent render: no csrf cookie set")
	}

	// 2b. Consent (decision=allow) → 302 back to the client with a code.
	getCode := func(t *testing.T) string {
		t.Helper()
		form := authForm()
		form.Set("decision", "allow")
		form.Set("csrf", csrfCookie.Value)
		req := httptest.NewRequest(http.MethodPost, "/oauth/authorize", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(session)
		req.AddCookie(csrfCookie)
		rec := httptest.NewRecorder()
		h.AuthorizeMCPDecision(rec, req)
		if rec.Code != http.StatusFound {
			t.Fatalf("authorize: %d — %s", rec.Code, rec.Body.String())
		}
		loc, _ := url.Parse(rec.Header().Get("Location"))
		if loc.Query().Get("state") != "xyz-state" {
			t.Errorf("authorize: state not echoed: %s", rec.Header().Get("Location"))
		}
		code := loc.Query().Get("code")
		if code == "" {
			t.Fatalf("authorize: no code in %s", rec.Header().Get("Location"))
		}
		return code
	}

	token := func(t *testing.T, form url.Values) (*httptest.ResponseRecorder, map[string]any) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		h.TokenMCP(rec, req)
		var body map[string]any
		json.Unmarshal(rec.Body.Bytes(), &body)
		return rec, body
	}

	// 3. Exchange the code for tokens.
	code := getCode(t)
	rec, body := token(t, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {reg.ClientID},
		"redirect_uri":  {redirectURI},
		"code_verifier": {verifier},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("token: %d — %s", rec.Code, rec.Body.String())
	}
	access, _ := body["access_token"].(string)
	refresh, _ := body["refresh_token"].(string)
	if access == "" || refresh == "" {
		t.Fatalf("token: missing tokens: %v", body)
	}

	// 4. The access token authenticates as the workspace user on /mcp.
	info, err := h.VerifyMCPBearer(context.Background(), access, nil)
	if err != nil || info == nil || info.UserID != userID {
		t.Fatalf("VerifyMCPBearer(access) = %+v, %v; want UserID=%s", info, err, userID)
	}

	// 5. Refresh rotates to a new working access token.
	rec, body = token(t, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refresh},
		"client_id":     {reg.ClientID},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh: %d — %s", rec.Code, rec.Body.String())
	}
	newAccess, _ := body["access_token"].(string)
	if newAccess == "" || newAccess == access {
		t.Fatalf("refresh: expected a new access token, got %q", newAccess)
	}
	if info, err := h.VerifyMCPBearer(context.Background(), newAccess, nil); err != nil || info.UserID != userID {
		t.Fatalf("VerifyMCPBearer(refreshed) failed: %+v, %v", info, err)
	}

	// 6. PKCE is enforced: a fresh code with the wrong verifier is rejected.
	badCode := getCode(t)
	rec, _ = token(t, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {badCode},
		"client_id":     {reg.ClientID},
		"redirect_uri":  {redirectURI},
		"code_verifier": {"the-wrong-verifier-zzzzzzzzzzzzzzzzzzzzzzzzzzzz"},
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("token with bad PKCE verifier = %d; want 400", rec.Code)
	}
}

func TestMCP_OAuthConnections(t *testing.T) {
	h, database, apiKey, userID := setupWorkspaceWithDB(t)

	// Seed a connected client + an access token for this user.
	now := time.Now().UTC()
	database.Exec(`INSERT INTO oauth_clients (client_id, client_name, redirect_uris, created_at) VALUES (?, ?, ?, ?)`,
		"client-1", "Claude", `["https://claude.ai/cb"]`, now.Format(time.RFC3339Nano))
	if _, err := database.Exec(`
		INSERT INTO oauth_access_tokens (id, token_hash, refresh_hash, client_id, user_id, scope, resource, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"tok-1", "hash-1", "rhash-1", "client-1", userID, "mcp", "", now.Add(time.Hour).Format(time.RFC3339), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("seed token: %v", err)
	}

	// List shows the connected app.
	rec := httptest.NewRecorder()
	h.RequireAuth(h.ListOAuthConnections)(rec, authReq(http.MethodGet, "/v1/oauth/connections", "", apiKey))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Claude") || !strings.Contains(rec.Body.String(), "tok-1") {
		t.Fatalf("list connections: %d — %s", rec.Code, rec.Body.String())
	}

	// Revoke it.
	dreq := authReq(http.MethodDelete, "/v1/oauth/connections/tok-1", "", apiKey)
	dreq.SetPathValue("id", "tok-1")
	drec := httptest.NewRecorder()
	h.RequireAuth(h.RevokeOAuthConnection)(drec, dreq)
	if drec.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d — %s", drec.Code, drec.Body.String())
	}

	// Now gone, and the token no longer authenticates.
	rec = httptest.NewRecorder()
	h.RequireAuth(h.ListOAuthConnections)(rec, authReq(http.MethodGet, "/v1/oauth/connections", "", apiKey))
	if strings.Contains(rec.Body.String(), "tok-1") {
		t.Errorf("connection still listed after revoke: %s", rec.Body.String())
	}
}

// TestMCP_OAuthConsentCSRF verifies the consent decision POST is rejected when the
// csrf form field doesn't match the cookie set when the consent screen was rendered —
// closing the "cross-site page auto-submits decision=allow" consent-forgery gap.
func TestMCP_OAuthConsentCSRF(t *testing.T) {
	h, database, _, userID := setupWorkspaceWithDB(t)
	const sessID = "oauth-csrf-session"
	database.Exec(`INSERT INTO sessions (id, user_id, expires_at) VALUES (?, ?, ?)`,
		sessID, userID, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	session := &http.Cookie{Name: "calnode_session", Value: sessID}

	const redirectURI = "https://claude.ai/cb"
	rec := httptest.NewRecorder()
	h.RegisterOAuthClient(rec, httptest.NewRequest(http.MethodPost, "/oauth/register",
		strings.NewReader(`{"client_name":"X","redirect_uris":["`+redirectURI+`"]}`)))
	var reg struct {
		ClientID string `json:"client_id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &reg)

	baseForm := func() url.Values {
		return url.Values{
			"response_type": {"code"}, "client_id": {reg.ClientID}, "redirect_uri": {redirectURI},
			"code_challenge": {"abc"}, "code_challenge_method": {"S256"}, "state": {"s"}, "decision": {"allow"},
		}
	}

	// No csrf cookie at all (never rendered the consent screen in this "browser").
	form := baseForm()
	form.Set("csrf", "guessed")
	req := httptest.NewRequest(http.MethodPost, "/oauth/authorize", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(session)
	rec = httptest.NewRecorder()
	h.AuthorizeMCPDecision(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("no csrf cookie: code=%d; want 400", rec.Code)
	}

	// csrf cookie present but the form field doesn't match it.
	form = baseForm()
	form.Set("csrf", "wrong-value")
	req = httptest.NewRequest(http.MethodPost, "/oauth/authorize", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(session)
	req.AddCookie(&http.Cookie{Name: "calnode_oauth_csrf", Value: "actual-value"})
	rec = httptest.NewRecorder()
	h.AuthorizeMCPDecision(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("mismatched csrf: code=%d; want 400", rec.Code)
	}
}

func TestMCP_OAuthDeny(t *testing.T) {
	h, database, _, userID := setupWorkspaceWithDB(t)
	const sessID = "oauth-deny-session"
	database.Exec(`INSERT INTO sessions (id, user_id, expires_at) VALUES (?, ?, ?)`,
		sessID, userID, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))

	const redirectURI = "https://claude.ai/cb"
	rec := httptest.NewRecorder()
	h.RegisterOAuthClient(rec, httptest.NewRequest(http.MethodPost, "/oauth/register",
		strings.NewReader(`{"client_name":"X","redirect_uris":["`+redirectURI+`"]}`)))
	var reg struct {
		ClientID string `json:"client_id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &reg)

	form := url.Values{
		"response_type": {"code"}, "client_id": {reg.ClientID}, "redirect_uri": {redirectURI},
		"code_challenge": {"abc"}, "code_challenge_method": {"S256"}, "state": {"s"}, "decision": {"deny"},
		"csrf": {"nonce"},
	}
	req := httptest.NewRequest(http.MethodPost, "/oauth/authorize", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "calnode_session", Value: sessID})
	req.AddCookie(&http.Cookie{Name: "calnode_oauth_csrf", Value: "nonce"})
	rec = httptest.NewRecorder()
	h.AuthorizeMCPDecision(rec, req)
	loc := rec.Header().Get("Location")
	if rec.Code != http.StatusFound || !strings.Contains(loc, "error=access_denied") {
		t.Errorf("deny: code=%d location=%q; want 302 with error=access_denied", rec.Code, loc)
	}
}
