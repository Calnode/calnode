package i18n

import "testing"

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
