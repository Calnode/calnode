package handler

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/calnode/calnode/internal/calendar/microsoft"
	"github.com/calnode/calnode/internal/secret"
)

// MicrosoftOAuthConfig holds decrypted Microsoft OAuth settings loaded from the DB.
type MicrosoftOAuthConfig struct {
	ClientID             string
	ClientSecret         string
	TenantID             string
	AllowedSignupDomains []string
}

func parseDomainsCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		d := strings.TrimSpace(strings.ToLower(part))
		if d != "" {
			out = append(out, d)
		}
	}
	return out
}

// LoadMicrosoftSettingsFromDB reads Microsoft OAuth credentials from server_settings.
// Returns nil (not an error) when microsoft_client_id is empty.
func LoadMicrosoftSettingsFromDB(db *sql.DB, encKey [32]byte) (*MicrosoftOAuthConfig, error) {
	var clientID, secretEnc, tenantID, domainsCSV string
	err := db.QueryRow(`
		SELECT microsoft_client_id, microsoft_client_secret_enc, microsoft_tenant_id, microsoft_allowed_signup_domains
		FROM server_settings WHERE id = 1`).
		Scan(&clientID, &secretEnc, &tenantID, &domainsCSV)
	if err == sql.ErrNoRows || clientID == "" {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var clientSecret string
	if secretEnc != "" {
		clientSecret, err = secret.Decrypt(encKey, secretEnc)
		if err != nil {
			return nil, fmt.Errorf("decrypt microsoft client secret: %w", err)
		}
	}
	return &MicrosoftOAuthConfig{
		ClientID:             strings.TrimSpace(clientID),
		ClientSecret:         clientSecret,
		TenantID:             strings.TrimSpace(tenantID),
		AllowedSignupDomains: parseDomainsCSV(domainsCSV),
	}, nil
}

// GetMicrosoftSettings handles GET /v1/settings/microsoft (admin only).
func (h *Handler) GetMicrosoftSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	var clientID, secretEnc, tenantID, domainsCSV string
	err := h.db.QueryRowContext(r.Context(), `
		SELECT microsoft_client_id, microsoft_client_secret_enc, microsoft_tenant_id, microsoft_allowed_signup_domains
		FROM server_settings WHERE id = 1`).
		Scan(&clientID, &secretEnc, &tenantID, &domainsCSV)
	if err != nil && err != sql.ErrNoRows {
		h.logger.ErrorContext(r.Context(), "microsoft settings: query", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"client_id":              clientID,
		"client_secret_set":      secretEnc != "",
		"tenant_id":              tenantID,
		"allowed_signup_domains": domainsCSV,
		"configured":             clientID != "" && secretEnc != "",
		"base_url":               h.baseURL,
	})
}

// PutMicrosoftSettings handles PUT and PATCH /v1/settings/microsoft (admin only).
func (h *Handler) PutMicrosoftSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if h.demoMode {
		h.writeError(w, http.StatusServiceUnavailable, "not available in the demo")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var req struct {
		ClientID             string `json:"client_id"`
		ClientSecret         string `json:"client_secret"`
		TenantID             string `json:"tenant_id"`
		AllowedSignupDomains string `json:"allowed_signup_domains"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	req.ClientID = strings.TrimSpace(req.ClientID)
	req.TenantID = strings.TrimSpace(req.TenantID)
	req.AllowedSignupDomains = strings.TrimSpace(req.AllowedSignupDomains)

	if req.ClientID == "" {
		if _, err := h.db.ExecContext(r.Context(), `
			UPDATE server_settings SET
			  microsoft_client_id = '',
			  microsoft_client_secret_enc = '',
			  microsoft_tenant_id = '',
			  microsoft_allowed_signup_domains = ?,
			  updated_at = datetime('now')
			WHERE id = 1`, req.AllowedSignupDomains); err != nil {
			h.logger.ErrorContext(r.Context(), "microsoft settings: clear", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		h.SetAllowedSignupDomains(parseDomainsCSV(req.AllowedSignupDomains))
		h.SetMicrosoftAuth("", "", "common", "", h.secureCookie)
		h.GetMicrosoftSettings(w, r)
		return
	}

	var existingEnc string
	_ = h.db.QueryRowContext(r.Context(), `SELECT microsoft_client_secret_enc FROM server_settings WHERE id = 1`).Scan(&existingEnc)

	secretEnc := existingEnc
	clientSecret := ""
	if req.ClientSecret != "" {
		var err error
		secretEnc, err = secret.Encrypt(h.encKey, req.ClientSecret)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "microsoft settings: encrypt secret", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		clientSecret = req.ClientSecret
	} else if existingEnc != "" {
		var err error
		clientSecret, err = secret.Decrypt(h.encKey, existingEnc)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "microsoft settings: decrypt existing secret", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	if _, err := h.db.ExecContext(r.Context(), `
		UPDATE server_settings SET
		  microsoft_client_id = ?,
		  microsoft_client_secret_enc = ?,
		  microsoft_tenant_id = ?,
		  microsoft_allowed_signup_domains = ?,
		  updated_at = datetime('now')
		WHERE id = 1`,
		req.ClientID, secretEnc, req.TenantID, req.AllowedSignupDomains); err != nil {
		h.logger.ErrorContext(r.Context(), "microsoft settings: save", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	tenant := req.TenantID
	if tenant == "" {
		tenant = "common"
	}
	h.SetAllowedSignupDomains(parseDomainsCSV(req.AllowedSignupDomains))

	if req.ClientID != "" && clientSecret != "" {
		authRedirect := strings.TrimRight(h.baseURL, "/") + "/v1/auth/microsoft/callback"
		calRedirect := strings.TrimRight(h.baseURL, "/") + "/v1/calendar/microsoft/callback"
		h.SetMicrosoftAuth(req.ClientID, clientSecret, tenant, authRedirect, h.secureCookie)
		encKeyHex := hex.EncodeToString(h.encKey[:])
		if mc, err := microsoft.New(h.db, req.ClientID, clientSecret, tenant, calRedirect, encKeyHex); err == nil {
			if h.cal != nil {
				h.cal.Register(mc)
			}
		}
	}

	h.GetMicrosoftSettings(w, r)
}
