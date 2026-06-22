package handler

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"net/http"
	"time"
)

//go:embed embed.js
var embedJS []byte

//go:embed templates/booking.css
var bookingCSS []byte

// embedJSModTime is a fixed build-time-ish stamp for cache validation; the asset
// only changes when the binary is rebuilt, so process start time is fine.
var embedJSModTime = time.Now()

// bookingCSSVersion is a content hash of booking.css used as a cache-busting query
// param (?v=) on the page <link> tags: the URL changes only when the stylesheet content
// changes, so a new deploy with edited CSS is fetched immediately instead of waiting out
// a cached copy. Exposed via BookingCSSVersion for the page handlers.
var bookingCSSVersion = func() string {
	sum := sha256.Sum256(bookingCSS)
	return hex.EncodeToString(sum[:])[:10]
}()

// BookingCSSVersion returns the cache-busting version stamp for /booking.css.
func BookingCSSVersion() string { return bookingCSSVersion }

// BookingCSS serves the shared booking-UI stylesheet at GET /booking.css. Used by
// both the server-rendered booking/manage pages and the embeddable widget (loaded
// into its Shadow DOM), so the two never drift. Public, cacheable, framing-safe.
func (h *Handler) BookingCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	// Versioned (?v=…) requests from the page <link> tags are immutable — the URL changes
	// when the content changes. Unversioned requests (e.g. an old embed) get a short cache.
	if r.URL.Query().Get("v") != "" {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=300")
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	http.ServeContent(w, r, "booking.css", embedJSModTime, bytes.NewReader(bookingCSS))
}

// EmbedJS serves the embeddable booking widget script at GET /embed.js. It's a
// public, framework-free Web Component; cacheable, and safe to load from any site.
func (h *Handler) EmbedJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("Access-Control-Allow-Origin", "*") // the script itself is public
	http.ServeContent(w, r, "embed.js", embedJSModTime, bytes.NewReader(embedJS))
}
