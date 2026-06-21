package handler

import (
	"bytes"
	_ "embed"
	"net/http"
	"time"
)

//go:embed embed.js
var embedJS []byte

// embedJSModTime is a fixed build-time-ish stamp for cache validation; the asset
// only changes when the binary is rebuilt, so process start time is fine.
var embedJSModTime = time.Now()

// EmbedJS serves the embeddable booking widget script at GET /embed.js. It's a
// public, framework-free Web Component; cacheable, and safe to load from any site.
func (h *Handler) EmbedJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("Access-Control-Allow-Origin", "*") // the script itself is public
	http.ServeContent(w, r, "embed.js", embedJSModTime, bytes.NewReader(embedJS))
}
