package i18n

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/language"
)

// unsupportedTestTags are the language tags the resolution tests use to mean "the visitor
// asked for something we don't ship". They must stay outside locales/ — this used to be
// "fr"/"de", which silently became wrong the moment French and German were added (the
// cases then asserted a fallback that no longer applied). assertUnsupported turns that
// into a loud, self-explaining failure instead.
var unsupportedTestTags = []string{"ja", "ko"}

func assertUnsupported(t *testing.T) {
	t.Helper()
	for _, code := range unsupportedTestTags {
		if Get(code) != nil {
			t.Fatalf("locale %q now ships, so it can no longer stand in for an unsupported "+
				"language here — pick another tag for unsupportedTestTags and update the cases below", code)
		}
	}
}

func TestResolve(t *testing.T) {
	assertUnsupported(t)
	cases := []struct {
		name           string
		acceptLanguage string
		override       string
		wantCode       string
	}{
		{"exact es", "es", "", "es"},
		{"exact en", "en-US,en;q=0.9", "", "en"},
		{"regional subtag falls back to primary", "es-MX,es;q=0.9", "", "es"},
		// fr-CA is shipped as its own file, so a Canadian browser gets Canadian French
		// rather than the France copy — and a French browser is unaffected by its
		// existence, which is the half that would break silently.
		{"Canadian French resolves to its own locale", "fr-CA,fr;q=0.9", "", "fr-CA"},
		{"European French is unaffected by fr-CA", "fr-FR,fr;q=0.9", "", "fr"},
		{"plain fr is unaffected by fr-CA", "fr", "", "fr"},
		{"fr-CA can be selected by the ?lang override", "en", "fr-CA", "fr-CA"},
		{"unsupported language falls back to English", "ja-JP,ja;q=0.9", "", "en"},
		{"empty header falls back to English", "", "", "en"},
		{"garbage header falls back to English", "not a real header ;;;", "", "en"},
		{"override wins over Accept-Language", "en", "es", "es"},
		{"unsupported override is ignored", "es", "ja", "es"},
		// ja isn't supported, but es (an acceptable lower-preference language) is — falling
		// through to it beats giving up to the site default, per Accept-Language semantics.
		{"unsupported top preference falls through to a supported lower one", "ja;q=0.9,es;q=0.5", "", "es"},
		{"unsupported-only list falls back to English", "ja;q=0.9,ko;q=0.5", "", "en"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Resolve(c.acceptLanguage, c.override)
			if got.Code != c.wantCode {
				t.Errorf("Resolve(%q, %q) = %q, want %q", c.acceptLanguage, c.override, got.Code, c.wantCode)
			}
		})
	}
}

func TestT_fallsBackToEnglishThenKey(t *testing.T) {
	es := Get("es")
	if es.T("confirm_booking") != "Confirmar reserva" {
		t.Errorf("expected Spanish translation, got %q", es.T("confirm_booking"))
	}
	if got := es.T("this_key_does_not_exist"); got != "this_key_does_not_exist" {
		t.Errorf("missing key should fall back to the key itself, got %q", got)
	}
}

func TestT_nilLocaleFallsBackToEnglish(t *testing.T) {
	var l *Locale
	if got := l.T("confirm_booking"); got != "Confirm booking" {
		t.Errorf("nil locale should fall back to English, got %q", got)
	}
}

func TestJSON_roundTrips(t *testing.T) {
	b, err := Default().JSON()
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 {
		t.Fatal("expected non-empty JSON")
	}
}

func TestSupportedLocales(t *testing.T) {
	opts := SupportedLocales()
	if len(opts) != len(locales) {
		t.Fatalf("SupportedLocales() returned %d entries, want %d", len(opts), len(locales))
	}
	if opts[0].Code != DefaultCode {
		t.Errorf("SupportedLocales()[0] = %q, want English (%q) first", opts[0].Code, DefaultCode)
	}
	for _, o := range opts {
		if o.Name == "" {
			t.Errorf("locale %q has an empty display name", o.Code)
		}
	}
}

func TestResolveWithFallback(t *testing.T) {
	assertUnsupported(t)
	cases := []struct {
		name           string
		acceptLanguage string
		override       string
		fallback       string
		wantCode       string
	}{
		{"no match falls back to configured fallback, not English", "ja;q=0.9,ko;q=0.5", "", "es", "es"},
		{"empty header falls back to configured fallback", "", "", "es", "es"},
		{"garbage header falls back to configured fallback", "not a real header ;;;", "", "es", "es"},
		{"invalid fallback code falls back to English", "ja;q=0.9,ko;q=0.5", "", "xx", "en"},
		{"empty fallback code falls back to English", "ja;q=0.9,ko;q=0.5", "", "", "en"},
		{"override still wins over the configured fallback", "en", "es", "en", "es"},
		// A real (even weak) Accept-Language match must NOT be overridden by the
		// fallback — the fallback is only for "nothing matched at all".
		{"a real match is unaffected by the fallback setting", "es-MX,es;q=0.9", "", "en", "es"},
		{"exact supported match is unaffected by the fallback setting", "es", "", "en", "es"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ResolveWithFallback(c.acceptLanguage, c.override, c.fallback)
			if got.Code != c.wantCode {
				t.Errorf("ResolveWithFallback(%q, %q, %q) = %q, want %q",
					c.acceptLanguage, c.override, c.fallback, got.Code, c.wantCode)
			}
		})
	}
}

// TestResolve_uncanonicalLocaleFilenameDoesNotPanic guards against a real regression: a
// locale file whose name doesn't round-trip through BCP-47 canonicalization used to make
// Resolve/ResolveWithFallback return a nil *Locale for an exact match on that code —
// callers doing loc.Code on the result would panic. E.g. language.Make("pt-br").String()
// canonicalizes to "pt-BR", "zh-hans" to "zh-Hans", "iw" to "he" — all plausible filenames
// someone adds for a new locale. This rigs the package's real locale tables with an
// uncanonical code and restores them afterward, since they're normally built once by
// init() from the embedded locale files.
func TestResolve_uncanonicalLocaleFilenameDoesNotPanic(t *testing.T) {
	origLocales, origSupported, origCodes, origMatcher := locales, supported, supportedCodes, matcher
	t.Cleanup(func() {
		locales, supported, supportedCodes, matcher = origLocales, origSupported, origCodes, origMatcher
	})

	const uncanonical = "pt-br" // language.Make("pt-br").String() == "pt-BR", not "pt-br"
	locales = map[string]*Locale{
		DefaultCode: origLocales[DefaultCode],
		uncanonical: {Code: uncanonical, strings: map[string]string{"back": "Voltar"}},
	}
	supportedCodes = []string{DefaultCode, uncanonical}
	supported = []language.Tag{language.Make(DefaultCode), language.Make(uncanonical)}
	matcher = language.NewMatcher(supported)

	for _, tag := range supported {
		if tag.String() == uncanonical {
			t.Fatalf("test setup invalid: %q already canonicalizes to itself, doesn't exercise the bug", uncanonical)
		}
	}

	got := ResolveWithFallback("pt-BR,pt;q=0.9", "", DefaultCode)
	if got == nil {
		t.Fatal("ResolveWithFallback returned nil for a locale whose filename doesn't canonicalize to itself")
	}
	if got.Code != uncanonical {
		t.Errorf("Code = %q, want %q (the original locale-file code)", got.Code, uncanonical)
	}

	// Exact override lookup goes through the same Get(), unaffected by this — sanity check.
	if got := Get(uncanonical); got == nil || got.Code != uncanonical {
		t.Errorf("Get(%q) = %v, want the rigged locale", uncanonical, got)
	}

	opts := SupportedLocales()
	found := false
	for _, o := range opts {
		if o.Code == uncanonical {
			found = true
		}
	}
	if !found {
		t.Errorf("SupportedLocales() should return the original code %q, got %+v", uncanonical, opts)
	}
}

func TestEnglishName(t *testing.T) {
	if got := Get("es").EnglishName(); got != "Spanish" {
		t.Errorf("Get(%q).EnglishName() = %q, want %q", "es", got, "Spanish")
	}
	if got := Default().EnglishName(); got != "English" {
		t.Errorf("Default().EnglishName() = %q, want %q", got, "English")
	}
	var nilLocale *Locale
	if got := nilLocale.EnglishName(); got != "English" {
		t.Errorf("nil locale EnglishName() = %q, want %q", got, "English")
	}
}

func TestTf(t *testing.T) {
	if got := Get("es").Tf("calendar_event_summary", "30-Minute Call", "Bob Booker"); got != "30-Minute Call con Bob Booker" {
		t.Errorf("Tf(calendar_event_summary) = %q", got)
	}
	if got := Default().Tf("calendar_event_booking_id", "01J4TEST"); got != "Booking ID: 01J4TEST" {
		t.Errorf("Tf(calendar_event_booking_id) = %q", got)
	}
	// nil Locale (e.g. i18n.Get("") for a booking with no stored locale) must still work —
	// mirrors T's nil-safety, since Tf is used the same way from calendar_reconcile.go and
	// reassign.go on a possibly-nil i18n.Get(orgLocale) result.
	var nilLocale *Locale
	if got := nilLocale.Tf("calendar_event_summary", "X", "Y"); got != "X with Y" {
		t.Errorf("nil Locale Tf() = %q, want English fallback", got)
	}
}

// TestFormatDate_patternIsDataDrivenPerLocale proves date_format actually controls the
// token order — not just that en/es (which happen to agree on ordering) still render
// correctly. A future locale that wants "month day, year" (US English) or a different
// component order entirely needs only a new date_format value, no Go code change.
func TestFormatDate_patternIsDataDrivenPerLocale(t *testing.T) {
	l := &Locale{Code: "us-en-test", strings: map[string]string{
		"date_format":     "%[3]s %[2]d, %[4]d", // "Jun 22, 2026" — no weekday, month first
		"month_short_jun": "Jun",
	}}
	moment := time.Date(2026, time.June, 22, 9, 5, 0, 0, time.UTC)
	if got := l.FormatDate(moment); got != "Jun 22, 2026" {
		t.Errorf("FormatDate with a reordered date_format = %q, want %q", got, "Jun 22, 2026")
	}
}

func TestFormatDateTime(t *testing.T) {
	// Monday 2026-06-22, 09:05 — a fixed reference so weekday/month names are unambiguous.
	moment := time.Date(2026, time.June, 22, 9, 5, 0, 0, time.UTC)

	if got := Default().FormatDateTime(moment); got != "Mon 22 Jun 2026, 9:05 AM" {
		t.Errorf("English FormatDateTime = %q", got)
	}
	if got := Get("es").FormatDateTime(moment); got != "lun 22 jun 2026, 09:05" {
		t.Errorf("Spanish FormatDateTime = %q", got)
	}

	// Hour cycle follows the locale's clock_format, not a hardcoded 12-hour default —
	// this is the actual review-flagged bug: emails must agree with the page, which
	// already renders Spanish times in 24h via Intl.DateTimeFormat.
	afternoon := time.Date(2026, time.June, 22, 15, 30, 0, 0, time.UTC)
	if got := Default().FormatTimeOfDay(afternoon); got != "3:30 PM" {
		t.Errorf("English FormatTimeOfDay = %q, want 12h clock", got)
	}
	if got := Get("es").FormatTimeOfDay(afternoon); got != "15:30" {
		t.Errorf("Spanish FormatTimeOfDay = %q, want 24h clock", got)
	}
}

func TestAllLocalesHaveTheSameKeys(t *testing.T) {
	en := Default()
	for code, l := range locales {
		if code == DefaultCode {
			continue
		}
		for k := range en.strings {
			if _, ok := l.strings[k]; !ok {
				t.Errorf("locale %q is missing key %q (present in English)", code, k)
			}
		}
		for k := range l.strings {
			if _, ok := en.strings[k]; !ok {
				t.Errorf("locale %q has key %q that doesn't exist in English", code, k)
			}
		}
	}
}

// enFormatArgs derives probe arguments for an English format string by walking its
// printf verbs. It only ever parses ENGLISH — the strings we author and control, which
// use plain %s/%d/%q or clean %[n]-indexed forms. Translations are never parsed; they're
// checked by running them through fmt itself (see TestAllLocalesHaveMatchingFormatVerbs),
// because hand-rolling a full printf parser to validate arbitrary translator input is
// exactly the kind of thing that grows silent blind spots — Go accepts an argument index
// after flags, width, OR precision ("%+[2]d", "%.2[1]f"), so a naive parser reports a
// clean bill of health for a format that corrupts at runtime.
//
// Returns nil if the English string has no verbs.
func enFormatArgs(t *testing.T, en string) []any {
	t.Helper()
	var args []any
	for i := 0; i < len(en)-1; i++ {
		if en[i] != '%' {
			continue
		}
		if en[i+1] == '%' { // escaped literal percent
			i++
			continue
		}
		j := i + 1
		if en[j] == '[' { // %[n] — index only tells us ordering, and fmt handles that
			for j < len(en) && en[j] != ']' {
				j++
			}
			j++
		}
		if j >= len(en) {
			break
		}
		switch en[j] {
		case 'd':
			args = append(args, 42)
		case 's', 'q', 'v':
			args = append(args, "probe")
		default:
			t.Fatalf("unhandled verb %%%c in English string %q — extend enFormatArgs", en[j], en)
		}
		i = j
	}
	return args
}

// TestAllLocalesHaveMatchingFormatVerbs is the guard that key-parity alone doesn't give.
// Several keys are consumed via Tf/Sprintf (email subjects, the greeting, the booking
// reference, calendar event titles, duration labels, date_format). If a translation's
// verbs drift from English — wrong count, wrong type, wrong index — Sprintf doesn't
// error, it silently emits "%!d(MISSING)" / "%!s(int=22)" / "%!(EXTRA string=…)" straight
// into a confirmation email subject or a Google Calendar event title. go vet can't catch
// it (Sprintf's format arg isn't constant, and template "{{.Tf …}}" calls are invisible
// to it), and nothing else in the tree checks it.
//
// The check runs each locale's string through fmt with arguments derived from English and
// asserts the result carries no "%!" error marker — i.e. fmt is the oracle for what
// actually corrupts, rather than a parser of ours second-guessing it. A legitimately
// reordered or argument-dropping translation (e.g. a date_format that omits the weekday)
// renders cleanly and passes; only genuine corruption trips it.
func TestAllLocalesHaveMatchingFormatVerbs(t *testing.T) {
	en := Default()
	for code, l := range locales {
		if code == DefaultCode {
			continue
		}
		for k, enVal := range en.strings {
			locVal, ok := l.strings[k]
			if !ok {
				continue // key parity is TestAllLocalesHaveTheSameKeys' job
			}
			// args is empty when English has no verbs, which is still the right probe: a
			// translation that introduces one would consume an argument that never
			// arrives and render "%!s(MISSING)". Passing args... (rather than omitting it)
			// also keeps vet's printf checker happy about the non-constant format.
			args := enFormatArgs(t, enVal)
			if got := fmt.Sprintf(locVal, args...); strings.Contains(got, "%!") {
				t.Errorf("locale %q key %q: format verbs don't match English — Sprintf corrupts\n  en: %q\n  %s: %q\n  renders as: %q",
					code, k, enVal, code, locVal, got)
			}
		}
	}
}

// TestAllLocalesHaveMatchingFormatVerbs_catchesDrift pins that the guard above actually
// fires, so it can't silently rot into a no-op. Each case is a real way a translation
// breaks, including the flags-before-index form ("%+[2]d") that a naive left-to-right
// parser misreads as having no second argument.
func TestAllLocalesHaveMatchingFormatVerbs_catchesDrift(t *testing.T) {
	cases := []struct{ name, en, translated string }{
		{"wrong verb type", "Hi %s,", "Hola %d,"},
		{"dropped verb", "Booking confirmed: %s", "Reserva confirmada"},
		{"extra verb", "Booking confirmed", "Reserva confirmada: %s"},
		{"out-of-range index", "%s with %s", "%[1]s con %[3]s"},
		{"flags before index", "Hi %s,", "Hola %+[2]d %[1]s,"},
		{"precision before index", "Hi %s,", "Hola %.2[2]f %[1]s,"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			args := enFormatArgs(t, c.en)
			got := fmt.Sprintf(c.translated, args...)
			if !strings.Contains(got, "%!") {
				t.Errorf("expected drift to corrupt, but %q rendered cleanly as %q", c.translated, got)
			}
		})
	}
}

// TestAllLocalesHaveMatchingFormatVerbs_allowsLegitimateReordering guards the other
// direction: a translation that reorders arguments, or deliberately omits a trailing one
// via explicit indices (which suppress fmt's EXTRA check), must NOT be flagged.
func TestAllLocalesHaveMatchingFormatVerbs_allowsLegitimateReordering(t *testing.T) {
	cases := []struct{ name, en, translated string }{
		{"reordered", "%s with %s", "%[2]s con %[1]s"},
		{"date_format US style drops the weekday", "%[1]s %[2]d %[3]s %[4]d", "%[3]s %[2]d, %[4]d"},
		{"date_format drops the year too", "%[1]s %[2]d %[3]s %[4]d", "%[2]d %[3]s"},
		{"escaped percent is not a verb", "Hi %s,", "100%% seguro, %s"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			args := enFormatArgs(t, c.en)
			if got := fmt.Sprintf(c.translated, args...); strings.Contains(got, "%!") {
				t.Errorf("legitimate translation %q was flagged: rendered as %q", c.translated, got)
			}
		})
	}
}
