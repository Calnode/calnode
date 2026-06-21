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
	if tenant == "" {
		tenant = "common"
	}
	cfg := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint:     microsoft.AzureADEndpoint(tenant),
		RedirectURL:  redirectURL,
		Scopes:       []string{"openid", "email", "profile", "User.Read"},
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
// the routable `mail` and falling back to the userPrincipalName. Lowercased to match
// stored account emails.
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
	email := strings.ToLower(strings.TrimSpace(info.Mail))
	if email == "" {
		email = strings.ToLower(strings.TrimSpace(info.UserPrincipalName))
	}
	if email == "" {
		return "", fmt.Errorf("auth: empty email from Microsoft")
	}
	return email, nil
}
