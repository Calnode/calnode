package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/mailer"
	"github.com/disintegration/imaging"
	_ "golang.org/x/image/webp"
)

// logoPath is the served path for the instance logo. The stored value carries a
// cache-busting ?v=<unix> so re-uploads aren't masked by email/browser caching.
const logoServePath = "/branding/logo"

// brandingSettings is the instance-wide brand identity used in emails and on the
// public booking/manage pages.
type brandingSettings struct {
	BusinessName string
	LogoURL      string // served path (relative), e.g. "/branding/logo?v=123"; empty = no logo
	LogoHeight   int    // email logo height in px (pages scale up); see pageLogoHeight
	LogoOpacity  int    // 20–100; CSS opacity for a subtle logo. 100 = fully opaque
	PrivacyURL   string // operator's Privacy Policy URL (absolute http[s]); "" = hidden
	TermsURL     string // operator's Terms URL (absolute http[s]); "" = hidden
}

// loadBranding reads the brand identity from the singleton settings row.
func (h *Handler) loadBranding(ctx context.Context) brandingSettings {
	var b brandingSettings
	_ = h.db.QueryRowContext(ctx, `
		SELECT COALESCE(business_name,''), COALESCE(logo_url,''),
		       COALESCE(logo_height,28), COALESCE(logo_opacity,100),
		       COALESCE(privacy_url,''), COALESCE(terms_url,'')
		FROM server_settings WHERE id = 1`).Scan(&b.BusinessName, &b.LogoURL, &b.LogoHeight, &b.LogoOpacity, &b.PrivacyURL, &b.TermsURL)
	if b.LogoHeight <= 0 {
		b.LogoHeight = 28
	}
	if b.LogoOpacity <= 0 || b.LogoOpacity > 100 {
		b.LogoOpacity = 100
	}
	return b
}

// pageLogoHeight scales the email logo height up ~1.3× for the roomier public
// booking/manage page headers.
func pageLogoHeight(emailPx int) int {
	if emailPx <= 0 {
		emailPx = 28
	}
	return (emailPx*13 + 5) / 10
}

// validatedLegalURL trims and validates an operator-supplied legal link. Empty is
// allowed (the link is hidden). Non-empty must be an absolute http(s) URL with a host.
func validatedLegalURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", true
	}
	if len(raw) > 500 {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", false
	}
	return raw, true
}

// opacityCSS turns a 20–100 percentage into a CSS opacity value ("1", "0.6", …).
func opacityCSS(pct int) string {
	if pct <= 0 || pct > 100 {
		pct = 100
	}
	return strconv.FormatFloat(float64(pct)/100, 'f', -1, 64)
}

// applyBranding stamps the instance brand identity onto booking email data so
// every outbound email carries the configured wordmark/logo. The logo is stored
// as a relative path; emails need an absolute URL, so it's prefixed with the
// public base URL here. Cheap single-row read; called once per send batch.
func (h *Handler) applyBranding(ctx context.Context, d *mailer.BookingData) {
	b := h.loadBranding(ctx)
	d.BrandName = b.BusinessName
	d.LogoHeight = b.LogoHeight
	d.LogoOpacity = b.LogoOpacity
	if strings.HasPrefix(b.LogoURL, "/") {
		d.LogoURL = h.publicURL() + b.LogoURL
	} else {
		d.LogoURL = b.LogoURL
	}
}

// GetBranding handles GET /v1/settings/branding (admin).
func (h *Handler) GetBranding(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	b := h.loadBranding(r.Context())
	h.writeJSON(w, http.StatusOK, map[string]any{
		"business_name": b.BusinessName,
		"logo_url":      b.LogoURL,
		"logo_height":   b.LogoHeight,
		"logo_opacity":  b.LogoOpacity,
		"privacy_url":   b.PrivacyURL,
		"terms_url":     b.TermsURL,
	})
}

// PatchBranding handles PATCH /v1/settings/branding (admin). Business name only;
// the logo is managed via the upload/delete endpoints.
func (h *Handler) PatchBranding(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var req struct {
		BusinessName string `json:"business_name"`
		LogoHeight   int    `json:"logo_height"`
		LogoOpacity  int    `json:"logo_opacity"`
		PrivacyURL   string `json:"privacy_url"`
		TermsURL     string `json:"terms_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.BusinessName = strings.TrimSpace(req.BusinessName)
	if len(req.BusinessName) > 200 {
		h.writeError(w, http.StatusBadRequest, "business name is too long")
		return
	}
	privacyURL, ok := validatedLegalURL(req.PrivacyURL)
	if !ok {
		h.writeError(w, http.StatusBadRequest, "privacy policy must be a full http(s) URL")
		return
	}
	termsURL, ok := validatedLegalURL(req.TermsURL)
	if !ok {
		h.writeError(w, http.StatusBadRequest, "terms link must be a full http(s) URL")
		return
	}
	// Clamp logo height to a sane range; 0/omitted falls back to the small default.
	if req.LogoHeight <= 0 {
		req.LogoHeight = 28
	}
	if req.LogoHeight < 16 {
		req.LogoHeight = 16
	}
	if req.LogoHeight > 64 {
		req.LogoHeight = 64
	}
	// Clamp opacity to 20–100; 0/omitted falls back to fully opaque.
	if req.LogoOpacity <= 0 {
		req.LogoOpacity = 100
	}
	if req.LogoOpacity < 20 {
		req.LogoOpacity = 20
	}
	if req.LogoOpacity > 100 {
		req.LogoOpacity = 100
	}
	if _, err := h.db.ExecContext(r.Context(), `
		UPDATE server_settings SET business_name = ?, logo_height = ?, logo_opacity = ?,
		       privacy_url = ?, terms_url = ?, updated_at = datetime('now')
		WHERE id = 1`, req.BusinessName, req.LogoHeight, req.LogoOpacity, privacyURL, termsURL); err != nil {
		h.logger.ErrorContext(r.Context(), "branding settings: update", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.GetBranding(w, r)
}

func (h *Handler) brandingDir() string { return filepath.Join(h.dataDir, "branding") }

// UploadBrandingLogo handles POST /v1/settings/branding/logo (admin).
// Accepts multipart/form-data with a "logo" file field (JPEG/PNG/GIF/WebP, ≤5 MB).
// Resized to fit 600×200 preserving aspect ratio, re-encoded as PNG (keeps
// transparency), and stored on the data volume.
func (h *Handler) UploadBrandingLogo(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 5<<20+1024)
	if err := r.ParseMultipartForm(5 << 20); err != nil { // #nosec G120 -- bounded by the MaxBytesReader above; the body can't exceed ~5MB
		h.writeError(w, http.StatusBadRequest, "logo must be ≤5 MB")
		return
	}
	file, _, err := r.FormFile("logo")
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "logo field required")
		return
	}
	defer file.Close()

	sniff := make([]byte, 512)
	n, _ := file.Read(sniff)
	switch http.DetectContentType(sniff[:n]) {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
	default:
		h.writeError(w, http.StatusBadRequest, "logo must be JPEG, PNG, GIF, or WebP")
		return
	}

	var buf bytes.Buffer
	buf.Write(sniff[:n])
	if _, err := buf.ReadFrom(file); err != nil {
		h.logger.ErrorContext(r.Context(), "logo: read body", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	img, _, err := image.Decode(&buf)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "could not decode image")
		return
	}

	// Fit within 600×160, preserving aspect ratio; never upscale. Logos are not
	// square, so we don't crop server-side (the client does any cropping) — the
	// display height-constrains it to ~30px, and ~160px tall covers retina/larger
	// page display while keeping the file small. BestCompression trims size further.
	resized := imaging.Fit(img, 600, 160, imaging.Lanczos)
	var out bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&out, resized); err != nil {
		h.logger.ErrorContext(r.Context(), "logo: encode png", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	dir := h.brandingDir()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		h.logger.ErrorContext(r.Context(), "logo: mkdir", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	dest := filepath.Join(dir, "logo.png")
	tmp, err := os.CreateTemp(dir, "upload-*.png")
	if err != nil {
		h.logger.ErrorContext(r.Context(), "logo: create temp", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		tmp.Close() // #nosec G104 -- file already written/renamed by this point; nothing actionable
		if !committed {
			if rerr := os.Remove(tmpPath); rerr != nil && !os.IsNotExist(rerr) {
				h.logger.Warn("logo: cleanup temp file", "error", rerr, "path", tmpPath)
			}
		}
	}()
	if _, err := tmp.Write(out.Bytes()); err != nil {
		h.logger.ErrorContext(r.Context(), "logo: write temp", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := tmp.Close(); err != nil {
		h.logger.ErrorContext(r.Context(), "logo: close temp", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		h.logger.ErrorContext(r.Context(), "logo: rename", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	committed = true

	logoURL := fmt.Sprintf("%s?v=%d", logoServePath, time.Now().Unix())
	if _, err := h.db.ExecContext(r.Context(),
		`UPDATE server_settings SET logo_url = ?, updated_at = datetime('now') WHERE id = 1`, logoURL); err != nil {
		h.logger.ErrorContext(r.Context(), "logo: update db", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]string{"logo_url": logoURL})
}

// DeleteBrandingLogo handles DELETE /v1/settings/branding/logo (admin).
func (h *Handler) DeleteBrandingLogo(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	_ = os.Remove(filepath.Join(h.brandingDir(), "logo.png"))
	if _, err := h.db.ExecContext(r.Context(),
		`UPDATE server_settings SET logo_url = '', updated_at = datetime('now') WHERE id = 1`); err != nil {
		h.logger.ErrorContext(r.Context(), "logo: delete db", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ServeBrandingLogo handles GET /branding/logo. Public — the logo is embedded in
// public pages and emails.
func (h *Handler) ServeBrandingLogo(w http.ResponseWriter, r *http.Request) {
	path := filepath.Join(h.brandingDir(), "logo.png")
	f, err := os.Open(path) // #nosec G304 -- "logo.png" is a literal; h.brandingDir() derives from the server's own dataDir config, never user input
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, path, fi.ModTime(), f)
}
