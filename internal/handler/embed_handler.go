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

// embedJSVersion is a content hash of embed.js, used as a strong ETag so third-party
// host pages (which embed an unversioned <script src=".../embed.js">) can revalidate
// cheaply: a redeploy that changes the script ships fresh bytes, an unchanged one gets
// a 304. Unlike a process-start Last-Modified, it doesn't churn caches on every restart.
var embedJSVersion = func() string {
	sum := sha256.Sum256(embedJS)
	return `"` + hex.EncodeToString(sum[:])[:16] + `"`
}()

// BookingCSS serves the shared booking-UI stylesheet at GET /booking.css. Used by
// both the server-rendered booking/manage pages and the embeddable widget (loaded
// into its Shadow DOM), so the two never drift. Public, cacheable, framing-safe.
func (h *Handler) BookingCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	// Versioned (?v=…) requests from the page <link> tags are immutable — the URL changes
	// when the content changes. Unversioned requests (e.g. the embed widget, which links
	// /booking.css without a version) get a short cache plus a content-hash ETag so they
	// revalidate cheaply (304 when unchanged) and pick up edits within minutes.
	w.Header().Set("ETag", `"`+bookingCSSVersion+`"`)
	if r.URL.Query().Get("v") != "" {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=300, must-revalidate")
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	http.ServeContent(w, r, "booking.css", time.Time{}, bytes.NewReader(bookingCSS))
}

// EmbedJS serves the embeddable booking widget script at GET /embed.js. It's a
// public, framework-free Web Component; cacheable, and safe to load from any site.
func (h *Handler) EmbedJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	// Embedded on third-party sites via an unversioned <script src>: cache briefly but
	// always allow a cheap conditional revalidation through the content-hash ETag, so a
	// redeploy propagates within minutes (304 when unchanged) instead of being pinned for
	// an hour. ServeContent answers If-None-Match against the ETag we set here.
	w.Header().Set("ETag", embedJSVersion)
	w.Header().Set("Cache-Control", "public, max-age=300, must-revalidate")
	w.Header().Set("Access-Control-Allow-Origin", "*") // the script itself is public
	http.ServeContent(w, r, "embed.js", time.Time{}, bytes.NewReader(embedJS))
}
