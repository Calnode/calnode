package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
)

// Google tag ID formats. The ID is interpolated into a <script> URL, so it MUST be validated
// strictly — only the official prefix + alphanumerics/dashes, nothing else.
var (
	gtmIDRe = regexp.MustCompile(`^GTM-[A-Z0-9]+$`)
	ga4IDRe = regexp.MustCompile(`^G-[A-Z0-9]+$`)
)

// googleTagSources are the origins a native GA4/GTM tag needs in the CSP. Added whenever a tag
// is active, regardless of any custom csp_allow, so the tag is never self-blocked.
const googleTagSources = "https://www.googletagmanager.com https://www.google-analytics.com https://*.google-analytics.com https://*.googletagmanager.com"

// strictPublicCSP is the secure default applied to public pages when no code
// injection is configured: inline scripts/styles only, no external origins.
// img-src allows https:/data: so an operator's external brand logo (and any
// remote avatar) loads; images are inert, so this is a safe relaxation even on
// the otherwise-strict default policy.
const strictPublicCSP = "default-src 'self'; script-src 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; connect-src 'self'; frame-ancestors 'none'"

// dataLayerFields is the set of keys an operator may push into window.dataLayer.
// Labels live in the admin UI; the backend only validates keys.
var dataLayerFields = []string{
	"booking_id", "event_type_slug", "event_type_name",
	"start_at", "end_at", "status", "location",
	"host_name", "attendee_name", "attendee_email", "attendee_timezone", "answers",
	// Payment / revenue (for GA4-style conversion tracking on paid bookings).
	"value", "currency", "is_paid", "transaction_id",
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
	GTMContainerID   string
	GA4MeasurementID string
}

// nativeTagActive reports whether a native GA4/GTM tag is configured.
func (t trackingSettings) nativeTagActive() bool {
	return t.GTMContainerID != "" || t.GA4MeasurementID != ""
}

// loadTrackingSettings reads the instance tracking config from the singleton row.
func (h *Handler) loadTrackingSettings(ctx context.Context) trackingSettings {
	var t trackingSettings
	var fieldsJSON string
	var enabled int
	_ = h.db.QueryRowContext(ctx, `
		SELECT COALESCE(head_html,''), COALESCE(tracking_csp_allow,''),
		       COALESCE(datalayer_enabled,0), COALESCE(datalayer_fields,''),
		       COALESCE(gtm_container_id,''), COALESCE(ga4_measurement_id,'')
		FROM server_settings WHERE id = 1`).
		Scan(&t.HeadHTML, &t.CSPAllow, &enabled, &fieldsJSON, &t.GTMContainerID, &t.GA4MeasurementID)
	t.DataLayerEnabled = enabled != 0
	if fieldsJSON != "" {
		_ = json.Unmarshal([]byte(fieldsJSON), &t.DataLayerFields)
	}
	// Defence-in-depth: only surface IDs that still match the format (so junk in the DB can
	// never reach a <script>). Stored values are validated on write.
	if !gtmIDRe.MatchString(t.GTMContainerID) {
		t.GTMContainerID = ""
	}
	if !ga4IDRe.MatchString(t.GA4MeasurementID) {
		t.GA4MeasurementID = ""
	}
	return t
}

// publicCSP returns the Content-Security-Policy for public pages. With no code
// injection it stays strict (dataLayer pushes are inline and need nothing extra).
// With head HTML configured it relaxes to allow external tags: broad https: by
// default, or just the operator's allowlisted origins when provided.
func publicCSP(t trackingSettings) string {
	if strings.TrimSpace(t.HeadHTML) == "" && !t.nativeTagActive() {
		return strictPublicCSP
	}
	sources := "https:"
	if a := strings.Join(strings.Fields(t.CSPAllow), " "); a != "" {
		sources = a
	}
	// A native GA4/GTM tag needs Google's domains even when the operator set a narrow allowlist.
	if t.nativeTagActive() {
		sources += " " + googleTagSources
	}
	var b strings.Builder
	b.WriteString("default-src 'self'; ")
	b.WriteString("script-src 'self' 'unsafe-inline' " + sources + "; ")
	b.WriteString("style-src 'self' 'unsafe-inline' " + sources + "; ")
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
		"head_html":           t.HeadHTML,
		"csp_allow":           t.CSPAllow,
		"datalayer_enabled":   t.DataLayerEnabled,
		"datalayer_fields":    t.DataLayerFields,
		"available_fields":    dataLayerFields,
		"gtm_container_id":    t.GTMContainerID,
		"ga4_measurement_id":  t.GA4MeasurementID,
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
		GTMContainerID   string   `json:"gtm_container_id"`
		GA4MeasurementID string   `json:"ga4_measurement_id"`
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
	// Native tag IDs: empty = off (deactivate); otherwise must match the exact ID format
	// (they're interpolated into a <script>, so reject anything else).
	gtmID := strings.ToUpper(strings.TrimSpace(req.GTMContainerID))
	if gtmID != "" && !gtmIDRe.MatchString(gtmID) {
		h.writeError(w, http.StatusBadRequest, "GTM container ID must look like GTM-XXXXXX")
		return
	}
	ga4ID := strings.ToUpper(strings.TrimSpace(req.GA4MeasurementID))
	if ga4ID != "" && !ga4IDRe.MatchString(ga4ID) {
		h.writeError(w, http.StatusBadRequest, "GA4 measurement ID must look like G-XXXXXXX")
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
		       datalayer_enabled = ?, datalayer_fields = ?,
		       gtm_container_id = ?, ga4_measurement_id = ?, updated_at = datetime('now')
		WHERE id = 1`,
		req.HeadHTML, strings.TrimSpace(req.CSPAllow), enabled, string(fieldsJSON),
		gtmID, ga4ID); err != nil {
		h.logger.ErrorContext(r.Context(), "tracking settings: update", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.GetTrackingSettings(w, r)
}
