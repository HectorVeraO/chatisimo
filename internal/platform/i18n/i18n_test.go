package i18n_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/platform/i18n"
)

func TestSupportedLocalesReturnsENUSAndESCL(t *testing.T) {
	got := i18n.SupportedLocales()
	want := []i18n.Locale{i18n.LocaleENUS, i18n.LocaleESCL}
	if !slices.Equal(got, want) {
		t.Fatalf("SupportedLocales() = %v, want %v", got, want)
	}
}

func TestParseLocaleAndMatchLocale(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		want   i18n.Locale
		wantOK bool
	}{
		{name: "canonical en-US", raw: "en-US", want: i18n.LocaleENUS, wantOK: true},
		{name: "lowercase en-us", raw: "en-us", want: i18n.LocaleENUS, wantOK: true},
		{name: "bare en", raw: "en", want: i18n.LocaleENUS, wantOK: true},
		{name: "regional en-GB", raw: "en-GB", want: i18n.LocaleENUS, wantOK: true},
		{name: "canonical es-CL", raw: "es-CL", want: i18n.LocaleESCL, wantOK: true},
		{name: "lowercase es-cl", raw: "es-cl", want: i18n.LocaleESCL, wantOK: true},
		{name: "Discord Latin America es-419", raw: "es-419", want: i18n.LocaleESCL, wantOK: true},
		{name: "Discord Spain es-ES", raw: "es-ES", want: i18n.LocaleESCL, wantOK: true},
		{name: "bare es", raw: "es", want: i18n.LocaleESCL, wantOK: true},
		{name: "whitespace around tag", raw: "  es-CL  ", want: i18n.LocaleESCL, wantOK: true},
		{name: "empty string", raw: "", want: i18n.LocaleENUS, wantOK: false},
		{name: "whitespace only", raw: "   ", want: i18n.LocaleENUS, wantOK: false},
		{name: "unsupported French", raw: "fr", want: i18n.LocaleENUS, wantOK: false},
		{name: "unsupported German", raw: "de-DE", want: i18n.LocaleENUS, wantOK: false},
		{name: "unsupported Portuguese", raw: "pt-BR", want: i18n.LocaleENUS, wantOK: false},
		{name: "malformed tag", raw: "not_a_bcp47_tag!!!", want: i18n.LocaleENUS, wantOK: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := i18n.ParseLocale(tc.raw)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("ParseLocale(%q) = (%q, %v), want (%q, %v)", tc.raw, got, ok, tc.want, tc.wantOK)
			}

			matched := i18n.MatchLocale(tc.raw)
			if matched != tc.want {
				t.Errorf("MatchLocale(%q) = %q, want %q", tc.raw, matched, tc.want)
			}
		})
	}
}

func TestResolveLocaleSelectsNonDefaultCandidateOrFallsBackToENUS(t *testing.T) {
	cases := []struct {
		name       string
		candidates []string
		want       i18n.Locale
	}{
		{name: "no candidates", candidates: nil, want: i18n.LocaleENUS},
		{name: "all empty", candidates: []string{"", " "}, want: i18n.LocaleENUS},
		{name: "en-US user and es-419 guild selects es-CL", candidates: []string{"en-US", "es-419"}, want: i18n.LocaleESCL},
		{name: "es-419 user and en-US guild selects es-CL", candidates: []string{"es-419", "en-US"}, want: i18n.LocaleESCL},
		{name: "es-419 user and unsupported fr guild selects es-CL", candidates: []string{"es-419", "fr"}, want: i18n.LocaleESCL},
		{name: "en-US user and es-ES guild selects es-CL", candidates: []string{"en-US", "es-ES"}, want: i18n.LocaleESCL},
		{name: "en-US user and fr guild selects en-US", candidates: []string{"en-US", "fr"}, want: i18n.LocaleENUS},
		{name: "de user and fr guild falls back to en-US", candidates: []string{"de", "fr"}, want: i18n.LocaleENUS},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := i18n.ResolveLocale(tc.candidates...)
			if got != tc.want {
				t.Errorf("ResolveLocale(%v) = %q, want %q", tc.candidates, got, tc.want)
			}
		})
	}
}

func TestParseJSONCatalogValidatesInput(t *testing.T) {
	entries, err := i18n.ParseJSONCatalog([]byte(`{"hello": "Hello %s", "bye": "Goodbye"}`))
	if err != nil {
		t.Fatalf("ParseJSONCatalog valid: %v", err)
	}
	if entries["hello"] != "Hello %s" || entries["bye"] != "Goodbye" {
		t.Errorf("entries = %+v", entries)
	}

	invalidCases := []struct {
		name string
		raw  []byte
	}{
		{name: "empty bytes", raw: nil},
		{name: "malformed JSON", raw: []byte(`{"hello":`)},
		{name: "empty JSON object", raw: []byte(`{}`)},
	}
	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			_, parseErr := i18n.ParseJSONCatalog(tc.raw)
			if parseErr == nil {
				t.Errorf("ParseJSONCatalog(%q) succeeded, want error", tc.raw)
			}
		})
	}
}

func TestBundleAndCatalogServeMessagesAndNormalizeLocales(t *testing.T) {
	bundle, err := i18n.NewBundle(i18n.LocaleENUS, map[i18n.Locale]map[string]string{
		i18n.LocaleENUS: {
			"greet":   "Hello %s!",
			"ordered": "%[1]s gave a gift to %[2]s.",
		},
		i18n.LocaleESCL: {
			"greet":   "Hola %s!",
			"ordered": "A %[2]s le regalo %[1]s.",
		},
	})
	if err != nil {
		t.Fatalf("NewBundle: %v", err)
	}
	if bundle.TextCatalog() == nil {
		t.Fatal("TextCatalog() is nil")
	}

	en := bundle.Catalog("en")
	if en.Locale() != i18n.LocaleENUS {
		t.Errorf("Catalog(en).Locale() = %q, want %q", en.Locale(), i18n.LocaleENUS)
	}
	if got := en.Format("greet", "Olga"); got != "Hello Olga!" {
		t.Errorf("en.Format(greet) = %q, want Hello Olga!", got)
	}
	if got := en.Format("ordered", "Alice", "Bruno"); got != "Alice gave a gift to Bruno." {
		t.Errorf("en.Format(ordered) = %q", got)
	}

	for _, alias := range []i18n.Locale{i18n.LocaleESCL, "es-419", "es-ES", "es"} {
		es := bundle.Catalog(alias)
		if es.Locale() != i18n.LocaleESCL {
			t.Errorf("Catalog(%q).Locale() = %q, want %q", alias, es.Locale(), i18n.LocaleESCL)
		}
		if got := es.Format("greet", "Olga"); got != "Hola Olga!" {
			t.Errorf("Catalog(%q).Format(greet) = %q, want Hola Olga!", alias, got)
		}
		if got := es.Format("ordered", "Alice", "Bruno"); got != "A Bruno le regalo Alice." {
			t.Errorf("Catalog(%q).Format(ordered) = %q", alias, got)
		}
	}

	fallback := bundle.Catalog("fr")
	if fallback.Locale() != i18n.LocaleENUS {
		t.Errorf("Catalog(fr).Locale() = %q, want %q", fallback.Locale(), i18n.LocaleENUS)
	}
	if got := fallback.Get("greet"); got != "Hello %s!" {
		t.Errorf("Catalog(fr).Get(greet) = %q, want Hello %%s!", got)
	}

	var zero i18n.Catalog
	if zero.Locale() != i18n.LocaleENUS {
		t.Errorf("zero.Locale() = %q, want %q", zero.Locale(), i18n.LocaleENUS)
	}
}

func TestNewBundleAggregatesValidationErrors(t *testing.T) {
	_, err := i18n.NewBundle("fr", nil)
	if err == nil {
		t.Fatal("NewBundle(fr, nil) succeeded, want error")
	}

	_, err = i18n.NewBundle(i18n.LocaleENUS, map[i18n.Locale]map[string]string{
		i18n.LocaleENUS: {
			"":      "non-empty",
			"blank": "   ",
			"ok":    "value",
		},
		i18n.LocaleESCL: {
			"blank": "   ",
			"extra": "sobra",
		},
		"es-419": {
			"ok": "valor",
		},
	})
	if err == nil {
		t.Fatal("NewBundle with invalid entries succeeded, want aggregated error")
	}
	msg := err.Error()
	for _, want := range []string{"empty key", "blank", "missing key \"ok\"", "undeclared key \"extra\"", "unsupported catalog locale \"es-419\""} {
		if !strings.Contains(msg, want) {
			t.Errorf("NewBundle error %q lacks %q", msg, want)
		}
	}
}

func TestCatalogGetPanicsOnUnknownKey(t *testing.T) {
	bundle, err := i18n.NewBundle(i18n.LocaleENUS, map[i18n.Locale]map[string]string{
		i18n.LocaleENUS: {"known": "value"},
	})
	if err != nil {
		t.Fatalf("NewBundle: %v", err)
	}

	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("Catalog.Get(unknown) did not panic")
		}
	}()

	_ = bundle.Catalog(i18n.LocaleENUS).Get("unknown")
}

func FuzzParseLocale(f *testing.F) {
	seeds := []string{
		"",
		"en",
		"en-US",
		"en-GB",
		"es",
		"es-CL",
		"es-419",
		"es-ES",
		"fr-FR",
		"pt-BR",
		"zh-Hant-HK",
		"invalid--tag",
		"\x00\xff\xfe",
		"\u202eRTL\u202c",
		strings.Repeat("a", 512),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		loc, ok := i18n.ParseLocale(raw)
		matched := i18n.MatchLocale(raw)
		if loc != matched {
			t.Fatalf("ParseLocale(%q) locale = %q != MatchLocale = %q", raw, loc, matched)
		}
		if !ok && loc != i18n.LocaleENUS {
			t.Fatalf("ParseLocale(%q) returned ok=false with non-default locale %q", raw, loc)
		}
		if loc != i18n.LocaleENUS && loc != i18n.LocaleESCL {
			t.Fatalf("ParseLocale(%q) returned unsupported locale %q", raw, loc)
		}
	})
}
