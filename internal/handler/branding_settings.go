package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/calnode/calnode/internal/mailer"
)

// brandingSettings is the instance-wide brand identity used in emails and on the
// public booking/manage pages.
type brandingSettings struct {
	BusinessName string
	LogoURL      string
}

// loadBranding reads the brand identity from the singleton settings row.
func (h *Handler) loadBranding(ctx context.Context) brandingSettings {
	var b brandingSettings
	_ = h.db.QueryRowContext(ctx, `
		SELECT COALESCE(business_name,''), COALESCE(logo_url,'')
		FROM server_settings WHERE id = 1`).Scan(&b.BusinessName, &b.LogoURL)
	return b
}

// applyBranding stamps the instance brand identity onto booking email data so
// every outbound email carries the configured wordmark/logo. Cheap single-row
// read; called once per send batch.
func (h *Handler) applyBranding(ctx context.Context, d *mailer.BookingData) {
	b := h.loadBranding(ctx)
	d.BrandName = b.BusinessName
	d.LogoURL = b.LogoURL
}

// GetBranding handles GET /v1/settings/branding (admin).
func (h *Handler) GetBranding(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok || !user.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
		return
	}
	b := h.loadBranding(r.Context())
	h.writeJSON(w, http.StatusOK, map[string]any{
		"business_name": b.BusinessName,
		"logo_url":      b.LogoURL,
	})
}

// PatchBranding handles PATCH /v1/settings/branding (admin).
func (h *Handler) PatchBranding(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok || !user.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var req struct {
		BusinessName string `json:"business_name"`
		LogoURL      string `json:"logo_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.BusinessName = strings.TrimSpace(req.BusinessName)
	req.LogoURL = strings.TrimSpace(req.LogoURL)
	if len(req.BusinessName) > 200 {
		h.writeError(w, http.StatusBadRequest, "business name is too long")
		return
	}
	// Logo is embedded in emails and on public pages, so require an absolute https
	// URL — email clients won't load http/relative/data images reliably, and it
	// keeps an operator from pointing at a non-image local path.
	if req.LogoURL != "" && !strings.HasPrefix(req.LogoURL, "https://") {
		h.writeError(w, http.StatusBadRequest, "logo URL must be an absolute https:// URL")
		return
	}
	if len(req.LogoURL) > 2048 {
		h.writeError(w, http.StatusBadRequest, "logo URL is too long")
		return
	}
	if _, err := h.db.ExecContext(r.Context(), `
		UPDATE server_settings SET business_name = ?, logo_url = ?, updated_at = datetime('now')
		WHERE id = 1`, req.BusinessName, req.LogoURL); err != nil {
		h.logger.ErrorContext(r.Context(), "branding settings: update", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.GetBranding(w, r)
}
