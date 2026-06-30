package handler

import _ "embed"

// chromePartialsSrc holds the shared public-page chrome partials — trackingHead,
// consentBanner, and legalFooter (see templates/_chrome.html). It is parsed into
// BOTH the book and manage template sets so the consent/tracking/footer logic has
// a single source and the two surfaces can't drift apart.
//
//go:embed templates/_chrome.html
var chromePartialsSrc string
