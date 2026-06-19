package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// strictPublicCSP is the secure default applied to public pages when no code
// injection is configured: inline scripts/styles only, no external origins.
// img-src allows https:/data: so an operator's external brand logo (and any
// remote avatar) loads; images are inert, so this is a safe relaxation even on
// the otherwise-strict default policy.
const strictPublicCSP = "default-src 'self'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src 'self' data: https:; connect-src 'self'; frame-ancestors 'none'"

// dataLayerFields is the set of keys an operator may push into window.dataLayer.
// Labels live in the admin UI; the backend only validates keys.
var dataLayerFields = []string{
	"booking_id", "event_type_slug", "event_type_name",
	"start_at", "end_at", "status", "location",
	"host_name", "attendee_name", "attendee_email", "attendee_timezone", "answers",
}

var validDataLayerField = func() map[string]bool {
	m := make(map[string]bool, len(dataLayerFields))
	for _, f := range dataLayerFields {
		m[f] = true
	}
	return m
}()

type trackingSettings struct {
	HeadHTML         string
	CSPAllow         string
	DataLayerEnabled bool
	DataLayerFields  []string
}

// loadTrackingSettings reads the instance tracking config from the singleton row.
func (h *Handler) loadTrackingSettings(ctx context.Context) trackingSettings {
	var t trackingSettings
	var fieldsJSON string
	var enabled int
	_ = h.db.QueryRowContext(ctx, `
		SELECT COALESCE(head_html,''), COALESCE(tracking_csp_allow,''),
		       COALESCE(datalayer_enabled,0), COALESCE(datalayer_fields,'')
		FROM server_settings WHERE id = 1`).Scan(&t.HeadHTML, &t.CSPAllow, &enabled, &fieldsJSON)
	t.DataLayerEnabled = enabled != 0
	if fieldsJSON != "" {
		_ = json.Unmarshal([]byte(fieldsJSON), &t.DataLayerFields)
	}
	return t
}

// publicCSP returns the Content-Security-Policy for public pages. With no code
// injection it stays strict (dataLayer pushes are inline and need nothing extra).
// With head HTML configured it relaxes to allow external tags: broad https: by
// default, or just the operator's allowlisted origins when provided.
func publicCSP(t trackingSettings) string {
	if strings.TrimSpace(t.HeadHTML) == "" {
		return strictPublicCSP
	}
	sources := "https:"
	if a := strings.Join(strings.Fields(t.CSPAllow), " "); a != "" {
		sources = a
	}
	var b strings.Builder
	b.WriteString("default-src 'self'; ")
	b.WriteString("script-src 'self' 'unsafe-inline' " + sources + "; ")
	b.WriteString("style-src 'unsafe-inline' " + sources + "; ")
	b.WriteString("connect-src 'self' " + sources + "; ")
	b.WriteString("img-src 'self' data: " + sources + "; ")
	b.WriteString("frame-src " + sources + "; ")
	b.WriteString("frame-ancestors 'none'")
	return b.String()
}

// GetTrackingSettings handles GET /v1/settings/tracking (admin).
func (h *Handler) GetTrackingSettings(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok || !user.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
		return
	}
	t := h.loadTrackingSettings(r.Context())
	if t.DataLayerFields == nil {
		t.DataLayerFields = []string{}
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"head_html":          t.HeadHTML,
		"csp_allow":          t.CSPAllow,
		"datalayer_enabled":  t.DataLayerEnabled,
		"datalayer_fields":   t.DataLayerFields,
		"available_fields":   dataLayerFields,
	})
}

// PatchTrackingSettings handles PATCH /v1/settings/tracking (admin).
func (h *Handler) PatchTrackingSettings(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok || !user.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req struct {
		HeadHTML         string   `json:"head_html"`
		CSPAllow         string   `json:"csp_allow"`
		DataLayerEnabled bool     `json:"datalayer_enabled"`
		DataLayerFields  []string `json:"datalayer_fields"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if len(req.HeadHTML) > 32<<10 {
		h.writeError(w, http.StatusBadRequest, "head_html exceeds 32 KB")
		return
	}
	if len(req.CSPAllow) > 2048 {
		h.writeError(w, http.StatusBadRequest, "csp_allow is too long")
		return
	}
	// Keep only known field keys, preserving order.
	fields := make([]string, 0, len(req.DataLayerFields))
	for _, f := range req.DataLayerFields {
		if validDataLayerField[f] {
			fields = append(fields, f)
		}
	}
	fieldsJSON, _ := json.Marshal(fields)

	enabled := 0
	if req.DataLayerEnabled {
		enabled = 1
	}
	if _, err := h.db.ExecContext(r.Context(), `
		UPDATE server_settings SET head_html = ?, tracking_csp_allow = ?,
		       datalayer_enabled = ?, datalayer_fields = ?, updated_at = datetime('now')
		WHERE id = 1`,
		req.HeadHTML, strings.TrimSpace(req.CSPAllow), enabled, string(fieldsJSON)); err != nil {
		h.logger.ErrorContext(r.Context(), "tracking settings: update", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.GetTrackingSettings(w, r)
}
