package handler

import (
	"net/http"

	"github.com/calnode/calnode/internal/i18n"
)

// langCookie persists a visitor's manual language choice (from the switcher) across
// visits, so they aren't stuck re-picking it on every page load. Public pages only.
const langCookie = "calnode_lang"

// resolveLocale picks the locale for a public-page request: an explicit ?lang= override
// wins (and gets persisted to a cookie so it survives without the query param on later
// visits), then a previously-set langCookie, then Accept-Language matching a supported
// locale, then the operator's configured fallback (server_settings.fallback_locale,
// English by default). See internal-docs/i18n-plan.md.
func (h *Handler) resolveLocale(r *http.Request) *i18n.Locale {
	override := r.URL.Query().Get("lang")
	if override == "" {
		if c, err := r.Cookie(langCookie); err == nil {
			override = c.Value
		}
	}
	fallback := h.loadBranding(r.Context()).FallbackLocale
	return i18n.ResolveWithFallback(r.Header.Get("Accept-Language"), override, fallback)
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
