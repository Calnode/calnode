package handler

import _ "embed"

// sharedPartialsSrc holds the template partials shared by the book and manage public
// pages (see templates/_shared.html): the consent/tracking/footer chrome
// (trackingHead, consentBanner, legalFooter) plus structural parts (calendarGrid).
// It is parsed into BOTH the book and manage template sets so these pieces have a
// single source and the two surfaces can't drift apart.
//
//go:embed templates/_shared.html
var sharedPartialsSrc string
