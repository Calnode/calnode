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
	"os"
	"path/filepath"
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
}

// loadBranding reads the brand identity from the singleton settings row.
func (h *Handler) loadBranding(ctx context.Context) brandingSettings {
	var b brandingSettings
	_ = h.db.QueryRowContext(ctx, `
		SELECT COALESCE(business_name,''), COALESCE(logo_url,''), COALESCE(logo_height,28)
		FROM server_settings WHERE id = 1`).Scan(&b.BusinessName, &b.LogoURL, &b.LogoHeight)
	if b.LogoHeight <= 0 {
		b.LogoHeight = 28
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

// applyBranding stamps the instance brand identity onto booking email data so
// every outbound email carries the configured wordmark/logo. The logo is stored
// as a relative path; emails need an absolute URL, so it's prefixed with the
// public base URL here. Cheap single-row read; called once per send batch.
func (h *Handler) applyBranding(ctx context.Context, d *mailer.BookingData) {
	b := h.loadBranding(ctx)
	d.BrandName = b.BusinessName
	d.LogoHeight = b.LogoHeight
	if strings.HasPrefix(b.LogoURL, "/") {
		d.LogoURL = h.publicURL() + b.LogoURL
	} else {
		d.LogoURL = b.LogoURL
	}
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
		"logo_height":   b.LogoHeight,
	})
}

// PatchBranding handles PATCH /v1/settings/branding (admin). Business name only;
// the logo is managed via the upload/delete endpoints.
func (h *Handler) PatchBranding(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok || !user.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var req struct {
		BusinessName string `json:"business_name"`
		LogoHeight   int    `json:"logo_height"`
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
	if _, err := h.db.ExecContext(r.Context(), `
		UPDATE server_settings SET business_name = ?, logo_height = ?, updated_at = datetime('now')
		WHERE id = 1`, req.BusinessName, req.LogoHeight); err != nil {
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
	user, ok := userFromContext(r.Context())
	if !ok || !user.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 5<<20+1024)
	if err := r.ParseMultipartForm(5 << 20); err != nil {
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
	if err := os.MkdirAll(dir, 0o755); err != nil {
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
		tmp.Close()
		if !committed {
			os.Remove(tmpPath)
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
	user, ok := userFromContext(r.Context())
	if !ok || !user.IsAdmin {
		h.writeError(w, http.StatusForbidden, "admin access required")
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
	f, err := os.Open(path)
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
