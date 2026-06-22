package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/calnode/calnode/internal/uid"
)

// This file implements the OAuth 2.1 authorization-server + resource-server pieces that
// let an MCP client (Claude, ChatGPT, …) connect to /mcp with a "click Connect → sign
// in → approve" flow instead of a pre-shared API key. Calnode is its own AS:
//
//   /.well-known/oauth-protected-resource   — RS metadata (points at the AS)
//   /.well-known/oauth-authorization-server — AS metadata (RFC 8414)
//   POST /oauth/register                    — dynamic client registration (RFC 7591)
//   GET/POST /oauth/authorize               — login + consent (see mcp_oauth_authorize.go)
//   POST /oauth/token                       — code/refresh exchange (see mcp_oauth_authorize.go)
//
// /mcp itself is guarded by auth.RequireBearerToken(verifyMCPBearer), which also still
// accepts cno_ API keys so scripted/programmatic callers keep working.

const (
	mcpAccessTokenTTL = 30 * 24 * time.Hour // access-token lifetime
	mcpAuthCodeTTL    = 2 * time.Minute     // authorization-code lifetime (single use)
)

// VerifyMCPBearer authenticates a /mcp request. It accepts either an OAuth access token
// issued by this server or a cno_ API key, returning the resolved workspace user. The
// MCP tools are workspace-scoped, so the identity is used only for audit (last_used_at),
// not to narrow what the tools can do. Exported for the server package to wire as the
// auth.RequireBearerToken verifier.
func (h *Handler) VerifyMCPBearer(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
	hash := hashAPIKey(token)

	// OAuth access token?
	var userID, expiresAt string
	err := h.db.QueryRowContext(ctx,
		`SELECT user_id, expires_at FROM oauth_access_tokens WHERE token_hash = ?`, hash).
		Scan(&userID, &expiresAt)
	if err == nil {
		exp, _ := time.Parse(time.RFC3339, expiresAt)
		if time.Now().UTC().After(exp) {
			return nil, auth.ErrInvalidToken // expired — client should refresh
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		_, _ = h.db.ExecContext(ctx, `UPDATE oauth_access_tokens SET last_used_at = ? WHERE token_hash = ?`, now, hash)
		return &auth.TokenInfo{UserID: userID, Expiration: exp}, nil
	}

	// API key fallback (programmatic callers).
	var keyUser string
	if err := h.db.QueryRowContext(ctx, `
		SELECT u.id FROM api_keys ak JOIN users u ON u.id = ak.user_id
		WHERE ak.key_hash = ? AND u.archived_at IS NULL`, hash).Scan(&keyUser); err == nil {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		_, _ = h.db.ExecContext(ctx, `UPDATE api_keys SET last_used_at = ? WHERE key_hash = ?`, now, hash)
		// API keys don't expire; report a far-future expiry so the SDK doesn't reject it.
		return &auth.TokenInfo{UserID: keyUser, Expiration: time.Now().Add(mcpAccessTokenTTL)}, nil
	}

	return nil, auth.ErrInvalidToken
}

// OAuthProtectedResourceMetadata serves RFC 9728 protected-resource metadata: it tells
// the client which authorization server guards this resource.
func (h *Handler) OAuthProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	writeJSONCached(w, map[string]any{
		"resource":                 h.baseURL + "/mcp",
		"authorization_servers":    []string{h.baseURL},
		"bearer_methods_supported": []string{"header"},
		"scopes_supported":         []string{"mcp"},
	})
}

// OAuthAuthServerMetadata serves RFC 8414 authorization-server metadata.
func (h *Handler) OAuthAuthServerMetadata(w http.ResponseWriter, r *http.Request) {
	writeJSONCached(w, map[string]any{
		"issuer":                                h.baseURL,
		"authorization_endpoint":                h.baseURL + "/oauth/authorize",
		"token_endpoint":                        h.baseURL + "/oauth/token",
		"registration_endpoint":                 h.baseURL + "/oauth/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"scopes_supported":                      []string{"mcp"},
	})
}

// RegisterOAuthClient implements RFC 7591 dynamic client registration. MCP clients are
// public (PKCE, no client secret); the real authorization gate is the user login +
// consent at /oauth/authorize, so open registration is acceptable for a single-workspace
// instance.
func (h *Handler) RegisterOAuthClient(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var req struct {
		RedirectURIs []string `json:"redirect_uris"`
		ClientName   string   `json:"client_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "invalid JSON")
		return
	}
	if len(req.RedirectURIs) == 0 {
		writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uris is required")
		return
	}
	for _, ru := range req.RedirectURIs {
		if !validRedirectURI(ru) {
			writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "invalid redirect_uri: "+ru)
			return
		}
	}
	if len(req.ClientName) > 255 {
		req.ClientName = req.ClientName[:255]
	}

	clientID := uid.New()
	urisJSON, _ := json.Marshal(req.RedirectURIs)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := h.db.ExecContext(r.Context(), `
		INSERT INTO oauth_clients (client_id, client_name, redirect_uris, created_at)
		VALUES (?, ?, ?, ?)`, clientID, req.ClientName, string(urisJSON), now); err != nil {
		h.logger.ErrorContext(r.Context(), "oauth: register client", "error", err)
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not register client")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"client_id":                  clientID,
		"client_name":                req.ClientName,
		"redirect_uris":              req.RedirectURIs,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
}

// validRedirectURI accepts https URLs, and http only for loopback (the MCP local-client
// convention, e.g. http://127.0.0.1:port/callback). Custom app schemes (e.g. for native
// desktop clients) are also allowed.
func validRedirectURI(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" && u.Scheme == "" {
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		host := u.Hostname()
		return host == "localhost" || host == "127.0.0.1" || host == "::1"
	case "":
		return false
	default:
		// A non-http(s) custom scheme (native app redirect) — require a scheme + opaque/path.
		return u.Scheme != "" && raw != ""
	}
}

// writeJSONCached writes a JSON body with a short cache header — used for the static
// discovery documents.
func writeJSONCached(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_ = json.NewEncoder(w).Encode(v)
}

// writeOAuthError writes an RFC 6749 / 7591 error object.
func writeOAuthError(w http.ResponseWriter, status int, code, desc string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":             code,
		"error_description": desc,
	})
}

// oauthClientRedirectURIs returns the registered redirect URIs for a client.
func (h *Handler) oauthClientRedirectURIs(ctx context.Context, clientID string) ([]string, bool) {
	var urisJSON string
	if err := h.db.QueryRowContext(ctx,
		`SELECT redirect_uris FROM oauth_clients WHERE client_id = ?`, clientID).Scan(&urisJSON); err != nil {
		return nil, false
	}
	var uris []string
	if err := json.Unmarshal([]byte(urisJSON), &uris); err != nil {
		return nil, false
	}
	return uris, true
}

// redirectURIAllowed reports whether candidate exactly matches one of the client's
// registered redirect URIs.
func redirectURIAllowed(registered []string, candidate string) bool {
	for _, ru := range registered {
		if strings.EqualFold(ru, candidate) {
			return true
		}
	}
	return false
}
