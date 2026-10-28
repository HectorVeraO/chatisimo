// Package i18n resolves BCP 47 locales and serves localized message catalogs.
package i18n

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"golang.org/x/text/language"
	"golang.org/x/text/message/catalog"
)

// Locale is a canonical BCP 47 language tag supported by the platform.
type Locale string

// The supported locales. LocaleENUS is the default and fallback locale.
const (
	LocaleENUS Locale = "en-US"
	LocaleESCL Locale = "es-CL"
)

// SupportedLocales returns the canonical locales the platform supports, with the default first.
func SupportedLocales() []Locale {
	locales := []Locale{LocaleENUS, LocaleESCL}
	return locales
}

// ParseLocale parses raw as a BCP 47 tag and matches it against the supported locales using CLDR
// language distance. It returns the matched canonical Locale and true when raw belongs to a
// supported language family (English or Spanish), or (LocaleENUS, false) when raw is empty,
// malformed, or an unsupported language.
func ParseLocale(raw string) (Locale, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return LocaleENUS, false
	}

	tag, err := language.Parse(trimmed)
	if err != nil {
		return LocaleENUS, false
	}

	matcher := language.NewMatcher([]language.Tag{
		language.AmericanEnglish,
		language.MustParse(string(LocaleESCL)),
	})
	_, index, confidence := matcher.Match(tag)
	if confidence == language.No {
		return LocaleENUS, false
	}

	supported := SupportedLocales()
	matched := supported[index]
	return matched, true
}

// MatchLocale returns the canonical supported Locale for raw, falling back to LocaleENUS when raw
// is empty, malformed, or unsupported.
func MatchLocale(raw string) Locale {
	loc, _ := ParseLocale(raw)
	return loc
}

// ResolveLocale selects the effective Locale from ordered BCP 47 candidates (typically a user
// locale followed by a guild locale). A non-default supported locale (such as es-CL matched from
// es-419 or es-ES) takes precedence over the platform default en-US so that a Spanish guild or a
// Spanish user in a default-locale guild renders in Spanish. It returns LocaleENUS when no
// candidate matches a non-default locale.
func ResolveLocale(candidates ...string) Locale {
	for _, raw := range candidates {
		loc, ok := ParseLocale(raw)
		if ok && loc != LocaleENUS {
			return loc
		}
	}

	for _, raw := range candidates {
		loc, ok := ParseLocale(raw)
		if ok {
			return loc
		}
	}

	return LocaleENUS
}

// ParseJSONCatalog decodes a flat JSON object of message keys to templates. It validates that the
// JSON is well-formed and non-empty.
func ParseJSONCatalog(raw []byte) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, errors.New("i18n: catalog JSON is empty")
	}

	var entries map[string]string
	err := json.Unmarshal(raw, &entries)
	if err != nil {
		return nil, fmt.Errorf("i18n: decode catalog JSON: %w", err)
	}
	if len(entries) == 0 {
		return nil, errors.New("i18n: catalog JSON has no entries")
	}

	return entries, nil
}

// Bundle holds validated message catalogs for the supported locales, backed by
// golang.org/x/text/message/catalog. It is safe for concurrent use after construction.
type Bundle struct {
	defaultLocale Locale
	messages      map[Locale]map[string]string
	textCatalog   catalog.Catalog
}

// NewBundle validates catalogs, reporting every problem together, and builds an immutable Bundle.
// Every locale in catalogs must be a valid supported Locale and contain the exact key set of
// defaultLocale with non-blank templates.
func NewBundle(defaultLocale Locale, catalogs map[Locale]map[string]string) (*Bundle, error) {
	var problems []error

	canonDefault, ok := ParseLocale(string(defaultLocale))
	if !ok || canonDefault != defaultLocale {
		problems = append(problems, fmt.Errorf("i18n: invalid default locale %q", defaultLocale))
	}
	if len(catalogs) == 0 {
		problems = append(problems, errors.New("i18n: catalogs map is empty"))
	}

	defaultEntries, hasDefault := catalogs[defaultLocale]
	if !hasDefault || len(defaultEntries) == 0 {
		problems = append(problems, fmt.Errorf("i18n: default locale %q has no entries", defaultLocale))
	}

	if len(problems) > 0 {
		return nil, errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
	}

	validationErrs := validateCatalogEntries(defaultLocale, defaultEntries, catalogs)
	if len(validationErrs) > 0 {
		return nil, errors.Join(validationErrs...) //nolint:wrapcheck // joins this function's own errors
	}

	defaultTag := language.MustParse(string(defaultLocale))
	builder := catalog.NewBuilder(catalog.Fallback(defaultTag))
	copied := make(map[Locale]map[string]string, len(catalogs))

	for _, loc := range sortedLocales(catalogs) {
		tag := language.MustParse(string(loc))
		entries := catalogs[loc]
		cloned := make(map[string]string, len(entries))
		for _, key := range slices.Sorted(maps.Keys(entries)) {
			val := entries[key]
			cloned[key] = val
			setErr := builder.SetString(tag, key, val)
			if setErr != nil {
				problems = append(problems, fmt.Errorf("i18n: register %s/%s: %w", loc, key, setErr))
			}
		}
		copied[loc] = cloned
	}

	if len(problems) > 0 {
		return nil, errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
	}

	bundle := &Bundle{
		defaultLocale: defaultLocale,
		messages:      copied,
		textCatalog:   builder,
	}
	return bundle, nil
}

func validateCatalogEntries(
	defaultLocale Locale,
	defaultEntries map[string]string,
	catalogs map[Locale]map[string]string,
) []error {
	var problems []error

	defaultKeys := slices.Sorted(maps.Keys(defaultEntries))
	for _, key := range defaultKeys {
		if strings.TrimSpace(key) == "" {
			problems = append(problems, fmt.Errorf("i18n: locale %q has an empty key", defaultLocale))
		}
		if strings.TrimSpace(defaultEntries[key]) == "" {
			problems = append(problems, fmt.Errorf("i18n: locale %q key %q is blank", defaultLocale, key))
		}
	}

	for _, loc := range sortedLocales(catalogs) {
		canon, ok := ParseLocale(string(loc))
		if !ok || canon != loc {
			problems = append(problems, fmt.Errorf("i18n: unsupported catalog locale %q", loc))
			continue
		}
		if loc == defaultLocale {
			continue
		}

		entries := catalogs[loc]
		for _, key := range defaultKeys {
			val, exists := entries[key]
			if !exists {
				problems = append(problems, fmt.Errorf("i18n: locale %q is missing key %q", loc, key))
				continue
			}
			if strings.TrimSpace(val) == "" {
				problems = append(problems, fmt.Errorf("i18n: locale %q key %q is blank", loc, key))
			}
		}

		for _, key := range slices.Sorted(maps.Keys(entries)) {
			_, inDefault := defaultEntries[key]
			if !inDefault {
				problems = append(problems, fmt.Errorf("i18n: locale %q has undeclared key %q", loc, key))
			}
		}
	}

	return problems
}

func sortedLocales(catalogs map[Locale]map[string]string) []Locale {
	locales := make([]Locale, 0, len(catalogs))
	for loc := range catalogs {
		locales = append(locales, loc)
	}
	slices.Sort(locales)
	return locales
}

// TextCatalog returns the underlying golang.org/x/text/message/catalog.Catalog.
func (b *Bundle) TextCatalog() catalog.Catalog {
	return b.textCatalog
}

// Catalog returns the Catalog for loc, normalizing BCP 47 aliases (such as "en", "es-419",
// "es-ES") and falling back to the bundle's default locale when loc is unsupported.
func (b *Bundle) Catalog(loc Locale) Catalog {
	matched := MatchLocale(string(loc))
	entries, exists := b.messages[matched]
	if !exists {
		matched = b.defaultLocale
		entries = b.messages[b.defaultLocale]
	}

	cat := Catalog{
		locale:  matched,
		entries: entries,
	}
	return cat
}

// Catalog serves localized messages for one resolved Locale.
type Catalog struct {
	locale  Locale
	entries map[string]string
}

// Locale returns the catalog's canonical Locale. A zero Catalog returns LocaleENUS.
func (c Catalog) Locale() Locale {
	if c.locale == "" {
		return LocaleENUS
	}
	return c.locale
}

// Get returns the template for key. An unknown key or a zero Catalog is a programmer error and
// panics with the key name.
func (c Catalog) Get(key string) string {
	text, ok := c.entries[key]
	if !ok {
		panic("i18n: unknown message key " + key)
	}
	return text
}

// Format formats the template for key with args using fmt.Sprintf (supporting both positional and
// indexed %[1]s verbs).
func (c Catalog) Format(key string, args ...any) string {
	formatted := fmt.Sprintf(c.Get(key), args...)
	return formatted
}
