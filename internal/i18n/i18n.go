// Package i18n resolves a visitor's locale from the Accept-Language header and serves
// translated strings for the public booking surfaces (book.html, manage.html, the embed
// widget, and confirmation emails — see internal-docs/i18n-plan.md).
//
// Translations live in locales/*.json, embedded into the binary. The Spanish strings are
// LLM-drafted, not reviewed by a native speaker — treat them as a starting point, not
// launch-ready copy, until someone fluent has checked them.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"

	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
)

//go:embed locales/*.json
var localeFS embed.FS

// DefaultCode is the fallback locale when nothing else matches.
const DefaultCode = "en"

// Locale holds one language's resolved string table.
type Locale struct {
	Code    string
	strings map[string]string
}

var (
	locales = map[string]*Locale{}
	// supported and supportedCodes are parallel, same-length, same-order slices:
	// supported[i] is the language.Tag used for matching, supportedCodes[i] is the
	// original locales/*.json filename it came from. They're kept separate rather than
	// deriving one from the other via tag.String() because BCP-47 canonicalization can
	// rewrite a tag ("pt-br" -> "pt-BR", "zh-hans" -> "zh-Hans", "iw" -> "he"): a locale
	// file with an uncanonical (if still valid-looking) name would round-trip to a
	// *different* string than its own map key, so locales[supported[index].String()]
	// would silently miss and return nil — a nil-Locale panic on the hottest public page
	// the moment someone adds e.g. locales/pt-br.json. Indexing supportedCodes instead
	// makes the lookup exact regardless of how the tag canonicalizes.
	supported      []language.Tag
	supportedCodes []string
	matcher        language.Matcher
)

func init() {
	entries, err := localeFS.ReadDir("locales")
	if err != nil {
		panic(fmt.Sprintf("i18n: read locales dir: %v", err))
	}
	for _, e := range entries {
		code := e.Name()[:len(e.Name())-len(".json")]
		b, err := localeFS.ReadFile("locales/" + e.Name())
		if err != nil {
			panic(fmt.Sprintf("i18n: read %s: %v", e.Name(), err))
		}
		var m map[string]string
		if err := json.Unmarshal(b, &m); err != nil {
			panic(fmt.Sprintf("i18n: parse %s: %v", e.Name(), err))
		}
		locales[code] = &Locale{Code: code, strings: m}
	}
	if _, ok := locales[DefaultCode]; !ok {
		panic("i18n: no " + DefaultCode + ".json locale found")
	}
	// supported must be built in a stable order — language.NewMatcher treats its first
	// entry as the ultimate low-confidence fallback, and map iteration order is
	// randomized in Go, so building this straight from `locales` would make Resolve's
	// fallback choice non-deterministic across process restarts. English goes first
	// deliberately; the rest are sorted for a reproducible (if arbitrary) tie-break order.
	var otherCodes []string
	for code := range locales {
		if code != DefaultCode {
			otherCodes = append(otherCodes, code)
		}
	}
	sort.Strings(otherCodes)
	supportedCodes = append(supportedCodes, DefaultCode)
	supported = append(supported, language.Make(DefaultCode))
	for _, code := range otherCodes {
		supportedCodes = append(supportedCodes, code)
		supported = append(supported, language.Make(code))
	}
	matcher = language.NewMatcher(supported)
}

// Default returns the fallback (English) locale.
func Default() *Locale { return locales[DefaultCode] }

// Get returns the locale for an exact code (e.g. "es"), or nil if unsupported.
func Get(code string) *Locale { return locales[code] }

// LocaleOption is one entry in a language switcher: a code to pass back (?lang=es) and
// its name in its OWN language (a switcher always shows each option in the language it
// selects — a French speaker should see "Français", not whatever the current UI language
// happens to be), never translated.
type LocaleOption struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// SupportedLocales lists every configured locale for a language-switcher UI, English
// first, then the rest alphabetically by code (mirroring the internal `supported` order).
func SupportedLocales() []LocaleOption {
	out := make([]LocaleOption, 0, len(supported))
	for i, tag := range supported {
		// Code is the original locale-file code (see supportedCodes' doc comment), not
		// tag.String() — a caller round-trips this through ?lang=<code>/Get(code), which
		// is keyed on the original filename, not its BCP-47 canonicalization.
		out = append(out, LocaleOption{Code: supportedCodes[i], Name: display.Self.Name(tag)})
	}
	return out
}

// Resolve picks the best-supported locale for a request, given the raw Accept-Language
// header value and an optional override (from a manual language-switcher cookie/param —
// pass "" when there isn't one). An override wins outright if it's a supported code;
// otherwise falls back to Accept-Language matching, then English. Equivalent to
// ResolveWithFallback with fallbackCode DefaultCode.
func Resolve(acceptLanguage, override string) *Locale {
	return ResolveWithFallback(acceptLanguage, override, DefaultCode)
}

// ResolveWithFallback is Resolve, but the "nothing matched" case returns fallbackCode's
// locale instead of always English — the operator-configured fallback language (see
// server_settings.fallback_locale) for a visitor whose browser doesn't ask for any
// locale Calnode supports. An invalid/unsupported fallbackCode behaves like "" (English).
//
// This does NOT affect a real Accept-Language match, however weak — only the case where
// language.Matcher itself reports zero confidence (nothing in the request matched
// anything). Distinguishing that from a positional fallback matters: matcher.Match never
// fails outright, it just returns supported[0] (English, by construction — see the
// ordering note in init()) at low/no confidence when nothing lines up.
func ResolveWithFallback(acceptLanguage, override, fallbackCode string) *Locale {
	if l := Get(override); l != nil {
		return l
	}
	fallback := Get(fallbackCode)
	if fallback == nil {
		fallback = Default()
	}
	tags, _, err := language.ParseAcceptLanguage(acceptLanguage)
	if err != nil || len(tags) == 0 {
		return fallback
	}
	_, index, confidence := matcher.Match(tags...)
	if confidence == language.No {
		return fallback
	}
	if l := locales[supportedCodes[index]]; l != nil {
		return l
	}
	return fallback // defensive: should be unreachable, but never return a nil Locale
}

// T looks up key in this locale, falling back to English, then to the key itself so a
// missing translation degrades to something visible rather than an empty string.
func (l *Locale) T(key string) string {
	if l != nil {
		if s, ok := l.strings[key]; ok {
			return s
		}
	}
	if s, ok := Default().strings[key]; ok {
		return s
	}
	return key
}

// Tf is T with fmt.Sprintf-style argument substitution, for keys like "Hi %s,".
func (l *Locale) Tf(key string, args ...any) string {
	return fmt.Sprintf(l.T(key), args...)
}

// EnglishName returns this locale's language name in English (e.g. "Spanish" for es) — for
// contexts that are deliberately kept in English (like the booking assistant's system
// prompt; see assistantBaseRules in internal/handler/booking_assistant.go) but still need
// to name a language, e.g. a "reply in %s" directive.
func (l *Locale) EnglishName() string {
	if l == nil {
		return Default().EnglishName()
	}
	return display.English.Languages().Name(language.Make(l.Code))
}

// JSON marshals this locale's full string table for client-side lookup (the embed widget,
// and JS-driven UI copy on the server-rendered pages that can't use {{.T}} directly).
// json.Marshal HTML-escapes <, >, and & by default, so this is safe to embed directly in
// a <script> block.
func (l *Locale) JSON() ([]byte, error) {
	return json.Marshal(l.strings)
}
