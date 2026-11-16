package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/microsoft"
)

// SetMicrosoftAuth configures Microsoft (Entra) OAuth sign-in. Identity only —
// openid/email/profile/User.Read; calendar access is a separate connection with its
// own scopes. Called from server.New when Microsoft credentials are configured.
// secure should be true when BASE_URL starts with https://.
func (h *Handler) SetMicrosoftAuth(clientID, clientSecret, tenant, redirectURL string, secure bool) {
	clientID = strings.TrimSpace(clientID)
	clientSecret = strings.TrimSpace(clientSecret)
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		tenant = "common"
	}
	var cfg *oauth2.Config
	if clientID != "" && clientSecret != "" {
		cfg = &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Endpoint:     microsoft.AzureADEndpoint(tenant),
			RedirectURL:  redirectURL,
			Scopes:       []string{"openid", "email", "profile", "User.Read"},
		}
	}
	h.authMu.Lock()
	h.microsoftAuth = cfg
	h.secureCookie = secure
	h.authMu.Unlock()
}

// LoginMicrosoft redirects to Microsoft's OAuth consent screen.
// GET /v1/auth/microsoft/login
func (h *Handler) LoginMicrosoft(w http.ResponseWriter, r *http.Request) {
	ma := h.getMicrosoftAuth()
	if ma == nil {
		http.Error(w, "Microsoft OAuth not configured", http.StatusServiceUnavailable)
		return
	}
	state, err := h.newOAuthState(w)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "auth: generate state", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// prompt=select_account so a cached SSO session can't silently pick the wrong one.
	http.Redirect(w, r, ma.AuthCodeURL(state, oauth2.AccessTypeOnline,
		oauth2.SetAuthURLParam("prompt", "select_account")), http.StatusFound)
}

// CallbackMicrosoft handles the OAuth redirect from Microsoft.
// GET /v1/auth/microsoft/callback
func (h *Handler) CallbackMicrosoft(w http.ResponseWriter, r *http.Request) {
	ma := h.getMicrosoftAuth()
	if ma == nil {
		http.Error(w, "Microsoft OAuth not configured", http.StatusServiceUnavailable)
		return
	}
	if !h.verifyOAuthState(w, r) {
		http.Redirect(w, r, "/admin/login?error=state", http.StatusFound)
		return
	}
	if r.URL.Query().Get("error") != "" {
		http.Redirect(w, r, "/admin/login?error=denied", http.StatusFound)
		return
	}

	tok, err := ma.Exchange(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		h.logger.ErrorContext(r.Context(), "auth: microsoft token exchange", "error", err)
		http.Redirect(w, r, "/admin/login?error=oauth", http.StatusFound)
		return
	}

	email, err := fetchMicrosoftEmail(r.Context(), ma, tok)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "auth: microsoft user info", "error", err)
		http.Redirect(w, r, "/admin/login?error=userinfo", http.StatusFound)
		return
	}
	h.finishOAuthLogin(w, r, email)
}

type microsoftUserInfo struct {
	Mail              string `json:"mail"`
	UserPrincipalName string `json:"userPrincipalName"`
}

// fetchMicrosoftEmail reads the signed-in account's email from Graph /me, preferring
// the routable `mail` (so existing accounts keyed by mailbox address match even when
// Entra UPNs use *.onmicrosoft.com) and falling back to userPrincipalName. B2B guest
// UPNs ("...#EXT#@...") are always rejected. Note: when JIT provisioning is enabled
// via ALLOWED_SIGNUP_DOMAINS, pair it with a specific Microsoft Tenant ID rather than
// "common" so foreign Entra tenants cannot assert a matching `mail` attribute.
func fetchMicrosoftEmail(ctx context.Context, cfg *oauth2.Config, tok *oauth2.Token) (string, error) {
	resp, err := cfg.Client(ctx, tok).Get("https://graph.microsoft.com/v1.0/me?$select=mail,userPrincipalName")
	if err != nil {
		return "", fmt.Errorf("auth: graph /me request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("auth: graph /me status %d", resp.StatusCode)
	}
	var info microsoftUserInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", fmt.Errorf("auth: decode graph /me: %w", err)
	}
	upn := strings.ToLower(strings.TrimSpace(info.UserPrincipalName))
	if strings.Contains(upn, "#ext#") {
		return "", fmt.Errorf("auth: Microsoft account is a B2B guest identity, not a verifiable email")
	}
	email := strings.ToLower(strings.TrimSpace(info.Mail))
	if email == "" {
		email = upn
	}
	if email == "" {
		return "", fmt.Errorf("auth: empty email from Microsoft")
	}
	return email, nil
}
