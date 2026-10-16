package handler

import _ "embed"

// sharedPartialsSrc holds the template partials shared by the book, manage and directory
// public pages (see templates/_shared.html): the business header and consent/tracking/footer chrome
// (trackingHead, consentBanner, legalFooter) plus structural parts (calendarGrid).
// It is parsed into all three template sets so these pieces have a single source.
//
//go:embed templates/_shared.html
var sharedPartialsSrc string
