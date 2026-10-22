// Package branding holds the instance-wide brand identity (wordmark, logo,
// banner, legal links) and the one place that stamps it onto outbound email.
//
// It lives outside internal/handler because two different senders need it: the
// HTTP handlers (confirmations, cancellations, reschedules) and the background
// worker (reminders). Duplicating the loading logic is exactly what let reminder
// emails ship with a relative logo path the recipient's mail client could not
// resolve (#167) — so both paths call Load and ApplyTo here instead.
package branding

import (
	"context"
	"database/sql"
	"strings"

	"github.com/calnode/calnode/internal/i18n"
	"github.com/calnode/calnode/internal/mailer"
)

// Settings is the instance-wide brand identity used in emails and on the public
// booking/manage/directory pages.
type Settings struct {
	BusinessName  string
	LogoURL       string // served path (relative), e.g. "/branding/logo?v=123"; empty = no logo
	LogoHeight    int    // email logo height in px (pages scale up); see handler.pageLogoHeight
	LogoOpacity   int    // 20–100; CSS opacity for a subtle logo. 100 = fully opaque
	BannerURL     string // served path (relative), e.g. "/branding/banner?v=123"; empty = no banner
	BannerOpacity int    // 20–100; CSS opacity for the banner. 100 = fully opaque
	PrivacyURL    string // operator's Privacy Policy URL (absolute http[s]); "" = hidden
	TermsURL      string // operator's Terms URL (absolute http[s]); "" = hidden
	// FallbackLocale is what a visitor sees when their browser doesn't ask for any
	// locale Calnode supports — defaults to "en". See i18n.ResolveWithFallback.
	FallbackLocale string
}

// Load reads the brand identity from the singleton settings row.
func Load(ctx context.Context, db *sql.DB) Settings {
	var s Settings
	_ = db.QueryRowContext(ctx, `
		SELECT COALESCE(business_name,''), COALESCE(logo_url,''),
		       COALESCE(logo_height,28), COALESCE(logo_opacity,100),
		       COALESCE(banner_url,''), COALESCE(banner_opacity,100),
		       COALESCE(privacy_url,''), COALESCE(terms_url,''),
		       COALESCE(fallback_locale,'en')
		FROM server_settings WHERE id = 1`).Scan(&s.BusinessName, &s.LogoURL, &s.LogoHeight, &s.LogoOpacity,
		&s.BannerURL, &s.BannerOpacity, &s.PrivacyURL, &s.TermsURL, &s.FallbackLocale)
	if s.LogoHeight <= 0 {
		s.LogoHeight = 28
	}
	if s.LogoOpacity <= 0 || s.LogoOpacity > 100 {
		s.LogoOpacity = 100
	}
	if s.BannerOpacity <= 0 || s.BannerOpacity > 100 {
		s.BannerOpacity = 100
	}
	if i18n.Get(s.FallbackLocale) == nil {
		s.FallbackLocale = i18n.DefaultCode
	}
	return s
}

// ApplyTo stamps the brand identity onto booking email data so every outbound
// email carries the configured wordmark/logo/banner.
//
// The logo and banner are stored as relative served paths; email clients cannot
// resolve those, so a leading "/" is prefixed with the caller's public base URL.
// baseURL is supplied by the caller (handler or worker) rather than read here,
// because the two resolve it from different config sources.
func (s Settings) ApplyTo(d *mailer.BookingData, baseURL string) {
	d.BrandName = s.BusinessName
	d.LogoHeight = s.LogoHeight
	d.LogoOpacity = s.LogoOpacity
	d.LogoURL = absoluteAssetURL(s.LogoURL, baseURL)
	d.BannerOpacity = s.BannerOpacity
	d.BannerURL = absoluteAssetURL(s.BannerURL, baseURL)
}

// absoluteAssetURL joins a relative served path onto the public base URL,
// leaving absolute or empty values untouched.
func absoluteAssetURL(path, baseURL string) string {
	if !strings.HasPrefix(path, "/") {
		return path
	}
	return strings.TrimRight(baseURL, "/") + path
}
