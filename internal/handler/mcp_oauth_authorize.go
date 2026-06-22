package handler

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/uid"
)

// This file implements the interactive half of the MCP OAuth flow: the user-facing
// /oauth/authorize (login + consent) and the /oauth/token exchange (PKCE-verified
// authorization-code and refresh-token grants). See mcp_oauth.go for discovery + DCR.

const oauthReturnCookie = "calnode_oauth_return"

// authRequest is a validated /oauth/authorize request.
type authRequest struct {
	ResponseType  string
	ClientID      string
	RedirectURI   string
	CodeChallenge string
	Method        string
	State         string
	Scope         string
	Resource      string
}

var consentTmpl = template.Must(template.New("consent").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Connect to {{.Business}}</title>
<style>
  :root{color-scheme:light dark}
  body{font:16px/1.5 system-ui,sans-serif;margin:0;min-height:100vh;display:grid;place-items:center;background:#f6f7f9;color:#111}
  @media(prefers-color-scheme:dark){body{background:#0d0f12;color:#e8eaed}}
  .card{max-width:420px;width:calc(100% - 2rem);background:#fff;border-radius:14px;padding:2rem;box-shadow:0 1px 3px rgba(0,0,0,.1),0 8px 24px rgba(0,0,0,.06)}
  @media(prefers-color-scheme:dark){.card{background:#16181d;box-shadow:none;border:1px solid #2a2d34}}
  h1{font-size:1.25rem;margin:0 0 .25rem}
  p{margin:.5rem 0;color:#555}
  @media(prefers-color-scheme:dark){p{color:#aab}}
  .who{font-weight:600;color:#111}
  @media(prefers-color-scheme:dark){.who{color:#e8eaed}}
  .row{display:flex;gap:.75rem;margin-top:1.5rem}
  button{flex:1;padding:.7rem 1rem;border-radius:10px;border:0;font:inherit;font-weight:600;cursor:pointer}
  .allow{background:#2563eb;color:#fff}
  .deny{background:transparent;border:1px solid #cbd0d8;color:inherit}
  .scope{margin-top:1rem;padding:.75rem 1rem;background:#f0f2f5;border-radius:10px;font-size:.9rem}
  @media(prefers-color-scheme:dark){.scope{background:#1f2228}.deny{border-color:#3a3e46}}
</style></head>
<body><div class="card">
  <h1>Connect to {{.Business}}</h1>
  <p><span class="who">{{.ClientName}}</span> wants to access your scheduling workspace as <span class="who">{{.UserEmail}}</span>.</p>
  <div class="scope">It will be able to read your event types and availability, and create, reschedule, and cancel bookings on your behalf.</div>
  <form method="post" action="/oauth/authorize">
    <input type="hidden" name="response_type" value="{{.AR.ResponseType}}">
    <input type="hidden" name="client_id" value="{{.AR.ClientID}}">
    <input type="hidden" name="redirect_uri" value="{{.AR.RedirectURI}}">
    <input type="hidden" name="code_challenge" value="{{.AR.CodeChallenge}}">
    <input type="hidden" name="code_challenge_method" value="{{.AR.Method}}">
    <input type="hidden" name="state" value="{{.AR.State}}">
    <input type="hidden" name="scope" value="{{.AR.Scope}}">
    <input type="hidden" name="resource" value="{{.AR.Resource}}">
    <div class="row">
      <button class="deny" name="decision" value="deny" type="submit">Deny</button>
      <button class="allow" name="decision" value="allow" type="submit">Allow</button>
    </div>
  </form>
</div></body></html>`))

// AuthorizeMCP handles GET /oauth/authorize: validate the request, ensure the user is
// signed in (bouncing through the existing Google/Microsoft login if not), then render
// the consent screen.
func (h *Handler) AuthorizeMCP(w http.ResponseWriter, r *http.Request) {
	ar, redirectOK, err := h.parseAuthRequest(r.Context(), r.URL.Query().Get)
	if err != nil {
		if redirectOK {
			h.redirectAuthError(w, r, ar, "invalid_request", err.Error())
			return
		}
		http.Error(w, "invalid authorization request: "+err.Error(), http.StatusBadRequest)
		return
	}

	_, email, ok := h.sessionUser(r)
	if !ok {
		// Not signed in — bounce through the existing SSO login and return here.
		if h.getGoogleAuth() != nil || h.getMicrosoftAuth() != nil {
			h.setOAuthReturn(w, r.URL.RequestURI())
			dest := "/v1/auth/login"
			if h.getGoogleAuth() == nil {
				dest = "/v1/auth/microsoft/login"
			}
			http.Redirect(w, r, dest, http.StatusFound)
			return
		}
		http.Error(w, "Please sign in to the Calnode admin in this browser, then start the connection again.", http.StatusUnauthorized)
		return
	}

	h.renderConsent(w, r, ar, email)
}

// AuthorizeMCPDecision handles POST /oauth/authorize: the consent decision. On allow it
// mints a single-use authorization code and redirects back to the client.
func (h *Handler) AuthorizeMCPDecision(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	ar, redirectOK, err := h.parseAuthRequest(r.Context(), r.PostForm.Get)
	if err != nil {
		if redirectOK {
			h.redirectAuthError(w, r, ar, "invalid_request", err.Error())
			return
		}
		http.Error(w, "invalid authorization request: "+err.Error(), http.StatusBadRequest)
		return
	}

	userID, _, ok := h.sessionUser(r)
	if !ok {
		http.Error(w, "session expired — please retry the connection", http.StatusUnauthorized)
		return
	}

	if r.PostForm.Get("decision") != "allow" {
		h.redirectAuthError(w, r, ar, "access_denied", "the user denied the request")
		return
	}

	code := "mcac_" + randHex(32)
	expires := time.Now().UTC().Add(mcpAuthCodeTTL).Format(time.RFC3339)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := h.db.ExecContext(r.Context(), `
		INSERT INTO oauth_auth_codes (code_hash, client_id, user_id, redirect_uri, code_challenge, scope, resource, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		hashAPIKey(code), ar.ClientID, userID, ar.RedirectURI, ar.CodeChallenge, ar.Scope, ar.Resource, expires, now); err != nil {
		h.logger.ErrorContext(r.Context(), "oauth: store auth code", "error", err)
		h.redirectAuthError(w, r, ar, "server_error", "could not issue code")
		return
	}

	sep := "?"
	if strings.Contains(ar.RedirectURI, "?") {
		sep = "&"
	}
	// iss (RFC 9207) lets the client confirm which AS issued the code.
	dest := ar.RedirectURI + sep + "code=" + urlQueryEscape(code) + "&iss=" + urlQueryEscape(h.baseURL)
	if ar.State != "" {
		dest += "&state=" + urlQueryEscape(ar.State)
	}
	h.logger.InfoContext(r.Context(), "oauth: issued authorization code",
		"client_id", ar.ClientID, "redirect_uri", ar.RedirectURI, "has_state", ar.State != "")
	http.Redirect(w, r, dest, http.StatusFound)
}

// TokenMCP handles POST /oauth/token for the authorization_code and refresh_token grants.
func (h *Handler) TokenMCP(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "bad form")
		return
	}
	h.logger.InfoContext(r.Context(), "oauth: token request",
		"grant_type", r.PostForm.Get("grant_type"), "client_id", r.PostForm.Get("client_id"))
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		h.tokenAuthCode(w, r)
	case "refresh_token":
		h.tokenRefresh(w, r)
	default:
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "grant_type must be authorization_code or refresh_token")
	}
}

func (h *Handler) tokenAuthCode(w http.ResponseWriter, r *http.Request) {
	code := r.PostForm.Get("code")
	clientID := r.PostForm.Get("client_id")
	redirectURI := r.PostForm.Get("redirect_uri")
	verifier := r.PostForm.Get("code_verifier")
	if code == "" || clientID == "" || verifier == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "code, client_id, and code_verifier are required")
		return
	}

	var userID, storedClient, storedRedirect, challenge, scope, resource, expiresAt string
	err := h.db.QueryRowContext(r.Context(), `
		SELECT user_id, client_id, redirect_uri, code_challenge, scope, resource, expires_at
		FROM oauth_auth_codes WHERE code_hash = ?`, hashAPIKey(code)).
		Scan(&userID, &storedClient, &storedRedirect, &challenge, &scope, &resource, &expiresAt)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "unknown or used authorization code")
		return
	}
	// Single use: consume immediately, whatever happens next.
	_, _ = h.db.ExecContext(r.Context(), `DELETE FROM oauth_auth_codes WHERE code_hash = ?`, hashAPIKey(code))

	if exp, _ := time.Parse(time.RFC3339, expiresAt); time.Now().UTC().After(exp) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "authorization code expired")
		return
	}
	if storedClient != clientID || (redirectURI != "" && storedRedirect != redirectURI) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "client_id or redirect_uri mismatch")
		return
	}
	if !verifyPKCE(verifier, challenge) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
		return
	}

	h.issueAndWriteTokens(w, r, clientID, userID, scope, resource, "")
}

func (h *Handler) tokenRefresh(w http.ResponseWriter, r *http.Request) {
	refresh := r.PostForm.Get("refresh_token")
	clientID := r.PostForm.Get("client_id")
	if refresh == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "refresh_token is required")
		return
	}
	var id, userID, storedClient, scope, resource string
	err := h.db.QueryRowContext(r.Context(), `
		SELECT id, user_id, client_id, scope, resource FROM oauth_access_tokens WHERE refresh_hash = ?`,
		hashAPIKey(refresh)).Scan(&id, &userID, &storedClient, &scope, &resource)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "unknown refresh token")
		return
	}
	if clientID != "" && clientID != storedClient {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "client_id mismatch")
		return
	}
	h.issueAndWriteTokens(w, r, storedClient, userID, scope, resource, id)
}

// issueAndWriteTokens mints a fresh access+refresh token pair and writes the token
// response. When replaceID is non-empty the existing row is rotated in place (refresh);
// otherwise a new row is inserted (authorization_code).
func (h *Handler) issueAndWriteTokens(w http.ResponseWriter, r *http.Request, clientID, userID, scope, resource, replaceID string) {
	access := "mcat_" + randHex(32)
	refresh := "mcrt_" + randHex(32)
	now := time.Now().UTC()
	exp := now.Add(mcpAccessTokenTTL)

	var err error
	if replaceID != "" {
		_, err = h.db.ExecContext(r.Context(), `
			UPDATE oauth_access_tokens SET token_hash = ?, refresh_hash = ?, expires_at = ?, last_used_at = NULL
			WHERE id = ?`, hashAPIKey(access), hashAPIKey(refresh), exp.Format(time.RFC3339), replaceID)
	} else {
		_, err = h.db.ExecContext(r.Context(), `
			INSERT INTO oauth_access_tokens (id, token_hash, refresh_hash, client_id, user_id, scope, resource, expires_at, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			uid.New(), hashAPIKey(access), hashAPIKey(refresh), clientID, userID, scope, resource, exp.Format(time.RFC3339), now.Format(time.RFC3339Nano))
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "oauth: issue token", "error", err)
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not issue token")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	// hand-rolled to keep field order conventional
	resp := `{"access_token":"` + access + `","token_type":"Bearer","expires_in":` +
		strconv.Itoa(int(mcpAccessTokenTTL.Seconds())) + `,"refresh_token":"` + refresh + `","scope":"` + scope + `"}`
	_, _ = w.Write([]byte(resp))
}

// parseAuthRequest validates an /oauth/authorize request from a param getter. The
// second return reports whether errors may be reported back to the client via redirect
// (true once client_id + redirect_uri are validated; false means render an error page).
func (h *Handler) parseAuthRequest(ctx context.Context, get func(string) string) (authRequest, bool, error) {
	ar := authRequest{
		ResponseType:  get("response_type"),
		ClientID:      get("client_id"),
		RedirectURI:   get("redirect_uri"),
		CodeChallenge: get("code_challenge"),
		Method:        get("code_challenge_method"),
		State:         get("state"),
		Scope:         get("scope"),
		Resource:      get("resource"),
	}
	if ar.Scope == "" {
		ar.Scope = "mcp"
	}
	if ar.ClientID == "" {
		return ar, false, errStr("client_id is required")
	}
	registered, ok := h.oauthClientRedirectURIs(ctx, ar.ClientID)
	if !ok {
		return ar, false, errStr("unknown client_id")
	}
	if ar.RedirectURI == "" || !redirectURIAllowed(registered, ar.RedirectURI) {
		return ar, false, errStr("redirect_uri is not registered for this client")
	}
	// client_id + redirect_uri are valid → further errors may go back via redirect.
	if ar.ResponseType != "code" {
		return ar, true, errStr("response_type must be code")
	}
	if ar.CodeChallenge == "" {
		return ar, true, errStr("code_challenge is required (PKCE)")
	}
	if ar.Method != "S256" {
		return ar, true, errStr("code_challenge_method must be S256")
	}
	return ar, true, nil
}

func (h *Handler) renderConsent(w http.ResponseWriter, r *http.Request, ar authRequest, email string) {
	business := "this workspace"
	if b := h.loadBranding(r.Context()); b.BusinessName != "" {
		business = b.BusinessName
	}
	var clientName string
	_ = h.db.QueryRowContext(r.Context(), `SELECT client_name FROM oauth_clients WHERE client_id = ?`, ar.ClientID).Scan(&clientName)
	if strings.TrimSpace(clientName) == "" {
		clientName = "An application"
	}
	// form-action must allow the client's redirect origin: the form POSTs to
	// /oauth/authorize (self), which then 302-redirects to the client — and browsers
	// enforce form-action across that redirect, so 'self' alone blocks the hand-back.
	formAction := "'self'"
	if u, err := url.Parse(ar.RedirectURI); err == nil && u.Scheme != "" && u.Host != "" {
		formAction += " " + u.Scheme + "://" + u.Host
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action "+formAction+"; base-uri 'none'")
	w.Header().Set("X-Frame-Options", "DENY")
	_ = consentTmpl.Execute(w, map[string]any{
		"Business":   business,
		"ClientName": clientName,
		"UserEmail":  email,
		"AR":         ar,
	})
}

func (h *Handler) redirectAuthError(w http.ResponseWriter, r *http.Request, ar authRequest, code, desc string) {
	sep := "?"
	if strings.Contains(ar.RedirectURI, "?") {
		sep = "&"
	}
	dest := ar.RedirectURI + sep + "error=" + urlQueryEscape(code) + "&error_description=" + urlQueryEscape(desc) + "&iss=" + urlQueryEscape(h.baseURL)
	if ar.State != "" {
		dest += "&state=" + urlQueryEscape(ar.State)
	}
	http.Redirect(w, r, dest, http.StatusFound)
}

// sessionUser resolves the current admin session cookie to a user (id, email). It
// mirrors RequireAuth's session branch but is usable outside that middleware.
func (h *Handler) sessionUser(r *http.Request) (userID, email string, ok bool) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return "", "", false
	}
	now := time.Now().UTC().Format(time.RFC3339)
	err = h.db.QueryRowContext(r.Context(), `
		SELECT u.id, u.email FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.id = ? AND s.expires_at > ? AND u.archived_at IS NULL`, c.Value, now).Scan(&userID, &email)
	if err != nil {
		return "", "", false
	}
	return userID, email, true
}

// setOAuthReturn stores a safe local return path (the /oauth/authorize URL) in a
// short-lived cookie so finishOAuthLogin can send the user back after SSO login.
func (h *Handler) setOAuthReturn(w http.ResponseWriter, path string) {
	if !safeLocalPath(path) {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: oauthReturnCookie, Value: path, Path: "/",
		MaxAge: 600, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: h.secureCookie,
	})
}

// consumeOAuthReturn returns the stored return path (if any) and clears the cookie.
func (h *Handler) consumeOAuthReturn(w http.ResponseWriter, r *http.Request) (string, bool) {
	c, err := r.Cookie(oauthReturnCookie)
	if err != nil || c.Value == "" || !safeLocalPath(c.Value) {
		return "", false
	}
	http.SetCookie(w, &http.Cookie{
		Name: oauthReturnCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: h.secureCookie,
	})
	return c.Value, true
}

// safeLocalPath guards against open redirects: only a same-site /oauth/authorize path.
func safeLocalPath(p string) bool {
	return strings.HasPrefix(p, "/oauth/authorize") && !strings.HasPrefix(p, "//")
}

func verifyPKCE(verifier, challenge string) bool {
	sum := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) == 1
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func urlQueryEscape(s string) string { return url.QueryEscape(s) }

type errString string

func (e errString) Error() string { return string(e) }
func errStr(s string) error       { return errString(s) }
