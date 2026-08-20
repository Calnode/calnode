package handler_test

import (
	"testing"

	"github.com/calnode/calnode/internal/i18n"
)

// unsupportedLocaleCodes are language tags these tests use to mean "the visitor asked for
// something Calnode doesn't ship". They must stay outside internal/i18n/locales.
//
// These used to be "fr" and "de", which quietly became wrong the day French and German
// were added: the tests then asserted a fallback that no longer applied, and failed with
// misleading messages about the fallback logic rather than about the fixture. Adding a
// locale should not require hunting through unrelated test failures, so requireUnsupported
// turns that into one loud, self-explaining error instead.
var unsupportedLocaleCodes = []string{"ja", "ko"}

func requireUnsupported(t *testing.T) {
	t.Helper()
	for _, code := range unsupportedLocaleCodes {
		if i18n.Get(code) != nil {
			t.Fatalf("locale %q now ships, so it can no longer stand in for an unsupported "+
				"language here — pick another tag for unsupportedLocaleCodes and update the "+
				"tests that use it", code)
		}
	}
}
