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
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/i18n"
	"github.com/calnode/calnode/internal/mailer"
	"github.com/disintegration/imaging"
	_ "golang.org/x/image/webp"
)

// logoPath is the served path for the instance logo. The stored value carries a
// cache-busting ?v=<unix> so re-uploads aren't masked by email/browser caching.
const logoServePath = "/branding/logo"

// bannerServePath is the served path for the instance banner image.
const bannerServePath = "/branding/banner"

// brandingSettings is the instance-wide brand identity used in emails and on the
// public booking/manage pages.
type brandingSettings struct {
	BusinessName  string
	LogoURL       string // served path (relative), e.g. "/branding/logo?v=123"; empty = no logo
	LogoHeight    int    // email logo height in px (pages scale up); see pageLogoHeight
	LogoOpacity   int    // 20–100; CSS opacity for a subtle logo. 100 = fully opaque
	BannerURL     string // served path (relative), e.g. "/branding/banner?v=123"; empty = no banner
	BannerOpacity int    // 20–100; CSS opacity for the banner. 100 = fully opaque
	PrivacyURL    string // operator's Privacy Policy URL (absolute http[s]); "" = hidden
	TermsURL      string // operator's Terms URL (absolute http[s]); "" = hidden
	// FallbackLocale is what a visitor sees when their browser doesn't ask for any
	// locale Calnode supports — defaults to "en". See i18n.ResolveWithFallback.
	FallbackLocale string
}

// loadBranding reads the brand identity from the singleton settings row.
func (h *Handler) loadBranding(ctx context.Context) brandingSettings {
	var b brandingSettings
	_ = h.db.QueryRowContext(ctx, `
		SELECT COALESCE(business_name,''), COALESCE(logo_url,''),
		       COALESCE(logo_height,28), COALESCE(logo_opacity,100),
		       COALESCE(banner_url,''), COALESCE(banner_opacity,100),
		       COALESCE(privacy_url,''), COALESCE(terms_url,''),
		       COALESCE(fallback_locale,'en')
		FROM server_settings WHERE id = 1`).Scan(&b.BusinessName, &b.LogoURL, &b.LogoHeight, &b.LogoOpacity,
		&b.BannerURL, &b.BannerOpacity, &b.PrivacyURL, &b.TermsURL, &b.FallbackLocale)
	if b.LogoHeight <= 0 {
		b.LogoHeight = 28
	}
	if b.LogoOpacity <= 0 || b.LogoOpacity > 100 {
		b.LogoOpacity = 100
	}
	if b.BannerOpacity <= 0 || b.BannerOpacity > 100 {
		b.BannerOpacity = 100
	}
	if i18n.Get(b.FallbackLocale) == nil {
		b.FallbackLocale = i18n.DefaultCode
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
	d.BannerOpacity = b.BannerOpacity
	if strings.HasPrefix(b.BannerURL, "/") {
		d.BannerURL = h.publicURL() + b.BannerURL
	} else {
		d.BannerURL = b.BannerURL
	}
}

// GetBranding handles GET /v1/settings/branding (admin).
func (h *Handler) GetBranding(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	b := h.loadBranding(r.Context())
	h.writeJSON(w, http.StatusOK, map[string]any{
		"business_name":     b.BusinessName,
		"logo_url":          b.LogoURL,
		"logo_height":       b.LogoHeight,
		"logo_opacity":      b.LogoOpacity,
		"banner_url":        b.BannerURL,
		"banner_opacity":    b.BannerOpacity,
		"privacy_url":       b.PrivacyURL,
		"terms_url":         b.TermsURL,
		"fallback_locale":   b.FallbackLocale,
		"supported_locales": i18n.SupportedLocales(),
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
		BusinessName   string `json:"business_name"`
		LogoHeight     int    `json:"logo_height"`
		LogoOpacity    int    `json:"logo_opacity"`
		BannerOpacity  int    `json:"banner_opacity"`
		PrivacyURL     string `json:"privacy_url"`
		TermsURL       string `json:"terms_url"`
		FallbackLocale string `json:"fallback_locale"`
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
	req.FallbackLocale = strings.TrimSpace(req.FallbackLocale)
	if req.FallbackLocale == "" {
		req.FallbackLocale = i18n.DefaultCode
	}
	if i18n.Get(req.FallbackLocale) == nil {
		h.writeError(w, http.StatusBadRequest, "fallback_locale is not a supported language code")
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
	// Clamp banner opacity to 20–100; 0/omitted falls back to fully opaque.
	if req.BannerOpacity <= 0 {
		req.BannerOpacity = 100
	}
	if req.BannerOpacity < 20 {
		req.BannerOpacity = 20
	}
	if req.BannerOpacity > 100 {
		req.BannerOpacity = 100
	}
	if _, err := h.db.ExecContext(r.Context(), `
		UPDATE server_settings SET business_name = ?, logo_height = ?, logo_opacity = ?,
		       banner_opacity = ?, privacy_url = ?, terms_url = ?, fallback_locale = ?, updated_at = datetime('now')
		WHERE id = 1`, req.BusinessName, req.LogoHeight, req.LogoOpacity, req.BannerOpacity, privacyURL, termsURL, req.FallbackLocale); err != nil {
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
// maxBrandingPixels bounds the DECODED size of an uploaded branding image.
//
// The 5 MB body limit bounds bytes on the wire, not pixels, and those are very different
// numbers: a highly compressed PNG of 30000x30000 is a few hundred KB on disk and roughly
// 3.6 GB once decoded. Checking dimensions before decoding is the only thing that stops
// that, and it matters more here than the admin-only gate suggests - the process holds the
// single SQLite connection, so an OOM takes the instance down rather than just failing the
// upload. It does not even need an attacker: a genuine 12000x8000 camera PNG is well under
// 5 MB compressed.
//
// 25 megapixels covers any camera or phone photo someone might reasonably use as a banner
// (25 MP is about 6000x4167) and costs at most ~100 MB to decode at 4 bytes per pixel. The
// result is resized to at most 1600x800 regardless, so nothing larger is ever useful.
const maxBrandingPixels = 25_000_000

// decodeBrandingImage validates and decodes an uploaded branding image.
//
// Returns (img, "", nil) on success; (nil, msg, nil) when the upload should be rejected
// with 400 and msg shown to the admin; (nil, "", err) for an internal failure.
func decodeBrandingImage(file io.Reader, label string) (image.Image, string, error) {
	sniff := make([]byte, 512)
	// io.ReadFull rather than a bare Read: Read is permitted to return fewer bytes than
	// requested with no error, which would hand DetectContentType a short prefix and
	// misclassify a perfectly valid image. A file shorter than 512 bytes yields
	// ErrUnexpectedEOF, which is fine - n is still correct.
	n, err := io.ReadFull(file, sniff)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, "", fmt.Errorf("read %s: %w", label, err)
	}
	switch http.DetectContentType(sniff[:n]) {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
	default:
		return nil, label + " must be JPEG, PNG, GIF, or WebP", nil
	}

	var buf bytes.Buffer
	buf.Write(sniff[:n])
	if _, err := buf.ReadFrom(file); err != nil {
		return nil, "", fmt.Errorf("read %s: %w", label, err)
	}
	data := buf.Bytes()

	// Header only - DecodeConfig reads the dimensions without allocating the pixel buffer,
	// which is the whole point: we learn how big the image claims to be before committing
	// the memory to find out.
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "could not decode image", nil
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxBrandingPixels {
		return nil, fmt.Sprintf("image is too large to process (%dx%d); please resize it below %d megapixels",
			cfg.Width, cfg.Height, maxBrandingPixels/1_000_000), nil
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "could not decode image", nil
	}
	return img, "", nil
}

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

	img, userMsg, err := decodeBrandingImage(file, "logo")
	if err != nil {
		h.logger.ErrorContext(r.Context(), "logo: read body", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if userMsg != "" {
		h.writeError(w, http.StatusBadRequest, userMsg)
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

// UploadBrandingBanner handles POST /v1/settings/branding/banner (admin).
// Accepts multipart/form-data with a "banner" file field (JPEG/PNG/GIF/WebP, ≤5 MB).
// Resized to fit 1600×800 preserving aspect ratio, re-encoded as PNG, and stored
// on the data volume. Unlike the logo, the banner is always displayed at 100%
// width, so it's resized larger to stay sharp at full-container width.
func (h *Handler) UploadBrandingBanner(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 5<<20+1024)
	if err := r.ParseMultipartForm(5 << 20); err != nil { // #nosec G120 -- bounded by the MaxBytesReader above; the body can't exceed ~5MB
		h.writeError(w, http.StatusBadRequest, "banner must be ≤5 MB")
		return
	}
	file, _, err := r.FormFile("banner")
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "banner field required")
		return
	}
	defer file.Close()

	img, userMsg, err := decodeBrandingImage(file, "banner")
	if err != nil {
		h.logger.ErrorContext(r.Context(), "banner: read body", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if userMsg != "" {
		h.writeError(w, http.StatusBadRequest, userMsg)
		return
	}

	resized := imaging.Fit(img, 1600, 800, imaging.Lanczos)
	var out bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&out, resized); err != nil {
		h.logger.ErrorContext(r.Context(), "banner: encode png", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	dir := h.brandingDir()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		h.logger.ErrorContext(r.Context(), "banner: mkdir", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	dest := filepath.Join(dir, "banner.png")
	tmp, err := os.CreateTemp(dir, "upload-*.png")
	if err != nil {
		h.logger.ErrorContext(r.Context(), "banner: create temp", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		tmp.Close() // #nosec G104 -- file already written/renamed by this point; nothing actionable
		if !committed {
			if rerr := os.Remove(tmpPath); rerr != nil && !os.IsNotExist(rerr) {
				h.logger.Warn("banner: cleanup temp file", "error", rerr, "path", tmpPath)
			}
		}
	}()
	if _, err := tmp.Write(out.Bytes()); err != nil {
		h.logger.ErrorContext(r.Context(), "banner: write temp", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := tmp.Close(); err != nil {
		h.logger.ErrorContext(r.Context(), "banner: close temp", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		h.logger.ErrorContext(r.Context(), "banner: rename", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	committed = true

	bannerURL := fmt.Sprintf("%s?v=%d", bannerServePath, time.Now().Unix())
	if _, err := h.db.ExecContext(r.Context(),
		`UPDATE server_settings SET banner_url = ?, updated_at = datetime('now') WHERE id = 1`, bannerURL); err != nil {
		h.logger.ErrorContext(r.Context(), "banner: update db", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]string{"banner_url": bannerURL})
}

// DeleteBrandingBanner handles DELETE /v1/settings/branding/banner (admin).
func (h *Handler) DeleteBrandingBanner(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	_ = os.Remove(filepath.Join(h.brandingDir(), "banner.png"))
	if _, err := h.db.ExecContext(r.Context(),
		`UPDATE server_settings SET banner_url = '', updated_at = datetime('now') WHERE id = 1`); err != nil {
		h.logger.ErrorContext(r.Context(), "banner: delete db", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ServeBrandingBanner handles GET /branding/banner. Public — the banner is
// embedded in public pages and emails.
func (h *Handler) ServeBrandingBanner(w http.ResponseWriter, r *http.Request) {
	path := filepath.Join(h.brandingDir(), "banner.png")
	f, err := os.Open(path) // #nosec G304 -- "banner.png" is a literal; h.brandingDir() derives from the server's own dataDir config, never user input
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
