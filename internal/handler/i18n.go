package handler

import (
	"net/http"

	"github.com/calnode/calnode/internal/i18n"
)

// langCookie persists a visitor's manual language choice (from the switcher) across
// visits, so they aren't stuck re-picking it on every page load. Public pages only.
const langCookie = "calnode_lang"

// localeOverride extracts a request's manual language switch: an explicit ?lang= query
// param, else a previously-set langCookie. No DB access.
func (h *Handler) localeOverride(r *http.Request) string {
	if override := r.URL.Query().Get("lang"); override != "" {
		return override
	}
	if c, err := r.Cookie(langCookie); err == nil {
		return c.Value
	}
	return ""
}

// resolveLocale picks the locale for a public-page request: an explicit ?lang= override
// wins (and gets persisted to a cookie so it survives without the query param on later
// visits), then a previously-set langCookie, then Accept-Language matching a supported
// locale, then the operator's configured fallback (server_settings.fallback_locale,
// English by default). See internal-docs/i18n-plan.md.
//
// This loads branding settings itself (for the fallback code) — a convenience for
// callers that don't already have it. If the caller already loaded branding (or, on the
// manage page, already resolved the locale earlier in the same request), prefer
// resolveLocaleWithFallback / passing the locale through explicitly, so a single request
// doesn't hit server_settings more than once for the same singleton row.
func (h *Handler) resolveLocale(r *http.Request) *i18n.Locale {
	return h.resolveLocaleWithFallback(r, h.loadBranding(r.Context()).FallbackLocale)
}

// resolveLocaleWithFallback is resolveLocale given an already-known fallback code (e.g.
// from a brandingSettings the caller loaded moments ago) — no DB access.
func (h *Handler) resolveLocaleWithFallback(r *http.Request, fallbackCode string) *i18n.Locale {
	return i18n.ResolveWithFallback(r.Header.Get("Accept-Language"), h.localeOverride(r), fallbackCode)
}

// persistLangOverride sets langCookie when the request carries a valid ?lang= switch, so
// the choice sticks on subsequent page loads without the query param. No-op otherwise —
// including an invalid/unsupported code, which just falls through to Accept-Language
// again next time rather than getting pinned to a broken value.
func (h *Handler) persistLangOverride(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("lang")
	if code == "" || i18n.Get(code) == nil {
		return
	}
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- HttpOnly/SameSite/Secure are all set; Secure is h.secureCookie (dynamic on BASE_URL scheme), which gosec's static check can't verify
		Name: langCookie, Value: code, Path: "/",
		MaxAge: 365 * 24 * 60 * 60, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: h.secureCookie,
	})
}
