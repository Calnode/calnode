package i18n

import (
	"testing"
	"time"

	"golang.org/x/text/language"
)

func TestResolve(t *testing.T) {
	cases := []struct {
		name           string
		acceptLanguage string
		override       string
		wantCode       string
	}{
		{"exact es", "es", "", "es"},
		{"exact en", "en-US,en;q=0.9", "", "en"},
		{"regional subtag falls back to primary", "es-MX,es;q=0.9", "", "es"},
		{"unsupported language falls back to English", "fr-FR,fr;q=0.9", "", "en"},
		{"empty header falls back to English", "", "", "en"},
		{"garbage header falls back to English", "not a real header ;;;", "", "en"},
		{"override wins over Accept-Language", "en", "es", "es"},
		{"unsupported override is ignored", "es", "de", "es"},
		// fr isn't supported, but es (an acceptable lower-preference language) is — falling
		// through to it beats giving up to the site default, per Accept-Language semantics.
		{"unsupported top preference falls through to a supported lower one", "fr;q=0.9,es;q=0.5", "", "es"},
		{"unsupported-only list falls back to English", "fr;q=0.9,de;q=0.5", "", "en"},
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
	cases := []struct {
		name           string
		acceptLanguage string
		override       string
		fallback       string
		wantCode       string
	}{
		{"no match falls back to configured fallback, not English", "fr;q=0.9,de;q=0.5", "", "es", "es"},
		{"empty header falls back to configured fallback", "", "", "es", "es"},
		{"garbage header falls back to configured fallback", "not a real header ;;;", "", "es", "es"},
		{"invalid fallback code falls back to English", "fr;q=0.9,de;q=0.5", "", "xx", "en"},
		{"empty fallback code falls back to English", "fr;q=0.9,de;q=0.5", "", "", "en"},
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

// formatVerbs extracts a string's printf verbs in order, normalising indexed verbs
// ("%[2]d") to (index, verb) pairs so a reordered translation still compares equal to the
// English original by argument identity rather than by position. Literal "%%" is skipped.
// Returns a map of argument index -> verb letter; index 0 means "positional" (unindexed).
func formatVerbs(s string) map[int]byte {
	out := map[int]byte{}
	nextPositional := 1
	for i := 0; i < len(s); i++ {
		if s[i] != '%' || i+1 >= len(s) {
			continue
		}
		j := i + 1
		if s[j] == '%' { // escaped literal percent, not a verb
			i = j
			continue
		}
		idx := 0
		if s[j] == '[' { // explicit argument index, e.g. %[2]d
			k := j + 1
			for k < len(s) && s[k] >= '0' && s[k] <= '9' {
				idx = idx*10 + int(s[k]-'0')
				k++
			}
			if k >= len(s) || s[k] != ']' {
				continue // malformed; leave it to Sprintf to complain
			}
			j = k + 1
		}
		// Skip flags/width/precision to reach the verb letter.
		for j < len(s) && (s[j] == '+' || s[j] == '-' || s[j] == '#' || s[j] == ' ' ||
			s[j] == '0' || s[j] == '.' || (s[j] >= '1' && s[j] <= '9')) {
			j++
		}
		if j >= len(s) {
			continue
		}
		if idx == 0 {
			idx = nextPositional
			nextPositional++
		} else {
			nextPositional = idx + 1
		}
		out[idx] = s[j]
		i = j
	}
	return out
}

// TestAllLocalesHaveMatchingFormatVerbs is the guard that key-parity alone doesn't give.
// Several keys are consumed via Tf/Sprintf (email subjects, the greeting, the booking
// reference, calendar event titles, duration labels, date_format). If a translation's
// verbs drift from English — wrong count, wrong type, wrong index — Sprintf doesn't error,
// it silently emits "%!d(MISSING)" / "%!s(int=22)" / "%!(EXTRA string=…)" straight into a
// confirmation email subject or a Google Calendar event title. go vet can't catch it
// (Sprintf's format arg isn't constant, and template "{{.Tf …}}" calls are invisible to
// it), and nothing else in the tree checks it.
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
			enVerbs, locVerbs := formatVerbs(enVal), formatVerbs(locVal)
			if len(enVerbs) != len(locVerbs) {
				t.Errorf("locale %q key %q: %d format verb(s) vs English's %d\n  en: %q\n  %s: %q",
					code, k, len(locVerbs), len(enVerbs), enVal, code, locVal)
				continue
			}
			for idx, enVerb := range enVerbs {
				locVerb, ok := locVerbs[idx]
				if !ok {
					t.Errorf("locale %q key %q: missing argument %d (English uses %%%c for it)\n  en: %q\n  %s: %q",
						code, k, idx, enVerb, enVal, code, locVal)
					continue
				}
				if locVerb != enVerb {
					t.Errorf("locale %q key %q: argument %d is %%%c but English uses %%%c (type mismatch → Sprintf corruption)\n  en: %q\n  %s: %q",
						code, k, idx, locVerb, enVerb, enVal, code, locVal)
				}
			}
		}
	}
}

func TestFormatVerbs(t *testing.T) {
	cases := []struct {
		in   string
		want map[int]byte
	}{
		{"no verbs here", map[int]byte{}},
		{"Hi %s,", map[int]byte{1: 's'}},
		{"%d hr %d min", map[int]byte{1: 'd', 2: 'd'}},
		{"%[1]s %[2]d %[3]s %[4]d", map[int]byte{1: 's', 2: 'd', 3: 's', 4: 'd'}},
		{"%[3]s %[2]d, %[4]d", map[int]byte{2: 'd', 3: 's', 4: 'd'}},
		{"invalid option for %[1]q: %[2]q is not allowed", map[int]byte{1: 'q', 2: 'q'}},
		{"100%% sure, %s", map[int]byte{1: 's'}}, // escaped percent isn't a verb
	}
	for _, c := range cases {
		got := formatVerbs(c.in)
		if len(got) != len(c.want) {
			t.Errorf("formatVerbs(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for k, v := range c.want {
			if got[k] != v {
				t.Errorf("formatVerbs(%q)[%d] = %q, want %q", c.in, k, got[k], v)
			}
		}
	}
}
