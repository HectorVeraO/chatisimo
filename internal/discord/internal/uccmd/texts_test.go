package uccmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// loadTextConstants returns the value of every text constant (name starting with "text") in
// texts.go, so a text added later is covered without touching the test.
func loadTextConstants(t *testing.T) map[string]MsgID {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "texts.go", nil, 0)
	if err != nil {
		t.Fatalf("parse texts.go: %v", err)
	}

	texts := make(map[string]MsgID)
	for _, decl := range file.Decls {
		gen, isGen := decl.(*ast.GenDecl)
		if !isGen || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vspec, isVal := spec.(*ast.ValueSpec)
			if !isVal {
				continue
			}
			for i, ident := range vspec.Names {
				if !strings.HasPrefix(ident.Name, "text") || i >= len(vspec.Values) {
					continue
				}
				lit, isLit := vspec.Values[i].(*ast.BasicLit)
				if !isLit || lit.Kind != token.STRING {
					t.Fatalf("const %s is not a string literal", ident.Name)
				}
				unquoted, unquoteErr := strconv.Unquote(lit.Value)
				if unquoteErr != nil {
					t.Fatalf("unquote const %s: %v", ident.Name, unquoteErr)
				}
				texts[ident.Name] = MsgID(unquoted)
			}
		}
	}

	return texts
}

// allTextIDs lists every text key by constant name. TestAllTextsAreListed fails when texts.go
// and this table disagree, so the catalog checks below never skip a key.
func allTextIDs() map[string]MsgID {
	ids := map[string]MsgID{
		"textCmdUCDesc":                textCmdUCDesc,
		"textCmdUCConfigDesc":          textCmdUCConfigDesc,
		"textSubConfigSetName":         textSubConfigSetName,
		"textSubConfigSetDesc":         textSubConfigSetDesc,
		"textSubConfigShowName":        textSubConfigShowName,
		"textSubConfigShowDesc":        textSubConfigShowDesc,
		"textSubConfigClearName":       textSubConfigClearName,
		"textSubConfigClearDesc":       textSubConfigClearDesc,
		"textOptWindowName":            textOptWindowName,
		"textOptWindowDesc":            textOptWindowDesc,
		"textOptDateName":              textOptDateName,
		"textOptDateDesc":              textOptDateDesc,
		"textOptFriendName":            textOptFriendName,
		"textOptFriendDesc":            textOptFriendDesc,
		"textOptChannelName":           textOptChannelName,
		"textOptChannelDesc":           textOptChannelDesc,
		"textOptLocaleName":            textOptLocaleName,
		"textOptLocaleDesc":            textOptLocaleDesc,
		"textOptClearTargetName":       textOptClearTargetName,
		"textOptClearTargetDesc":       textOptClearTargetDesc,
		"textChoiceToday":              textChoiceToday,
		"textChoiceTomorrow":           textChoiceTomorrow,
		"textChoiceThisWeekend":        textChoiceThisWeekend,
		"textChoiceNextWeekend":        textChoiceNextWeekend,
		"textChoiceThisMonth":          textChoiceThisMonth,
		"textChoiceLocaleESCL":         textChoiceLocaleESCL,
		"textChoiceLocaleENUS":         textChoiceLocaleENUS,
		"textChoiceClearAll":           textChoiceClearAll,
		"textChoiceClearFriend":        textChoiceClearFriend,
		"textChoiceClearChannel":       textChoiceClearChannel,
		"textChoiceClearLocale":        textChoiceClearLocale,
		"textWeekdaySun":               textWeekdaySun,
		"textWeekdayMon":               textWeekdayMon,
		"textWeekdayTue":               textWeekdayTue,
		"textWeekdayWed":               textWeekdayWed,
		"textWeekdayThu":               textWeekdayThu,
		"textWeekdayFri":               textWeekdayFri,
		"textWeekdaySat":               textWeekdaySat,
		"textPeriodToday":              textPeriodToday,
		"textPeriodTomorrow":           textPeriodTomorrow,
		"textPeriodThisWeekend":        textPeriodThisWeekend,
		"textPeriodNextWeekend":        textPeriodNextWeekend,
		"textPeriodThisMonth":          textPeriodThisMonth,
		"textPeriodDate":               textPeriodDate,
		"textFriendFallback":           textFriendFallback,
		"textVenueHomeWithName":        textVenueHomeWithName,
		"textVenueHome":                textVenueHome,
		"textVenueAwayWithName":        textVenueAwayWithName,
		"textVenueAway":                textVenueAway,
		"textMatchupHome":              textMatchupHome,
		"textMatchupAway":              textMatchupAway,
		"textBanterBlockedHome":        textBanterBlockedHome,
		"textBanterBlockedAway":        textBanterBlockedAway,
		"textBanterPending":            textBanterPending,
		"textBanterFree":               textBanterFree,
		"textAlertFridayFallback":      textAlertFridayFallback,
		"textConfigUnset":              textConfigUnset,
		"textConfigLocaleDefault":      textConfigLocaleDefault,
		"textConfigShow":               textConfigShow,
		"textConfigSaved":              textConfigSaved,
		"textConfigCleared":            textConfigCleared,
		"textConfigGuildOnly":          textConfigGuildOnly,
		"textConfigForbidden":          textConfigForbidden,
		"textConfigEmptyUpdate":        textConfigEmptyUpdate,
		"textConfigFriendBot":          textConfigFriendBot,
		"textConfigInvalidFriend":      textConfigInvalidFriend,
		"textConfigInvalidChannel":     textConfigInvalidChannel,
		"textConfigInvalidLocale":      textConfigInvalidLocale,
		"textConfigInvalidClearTarget": textConfigInvalidClearTarget,
		"textScheduleUnavailable":      textScheduleUnavailable,
		"textMutuallyExclusiveOptions": textMutuallyExclusiveOptions,
		"textInvalidDate":              textInvalidDate,
		"textInvalidWindow":            textInvalidWindow,
		"textInvalidInput":             textInvalidInput,
		"textInternal":                 textInternal,
	}
	return ids
}

func TestAllTextsAreListed(t *testing.T) {
	t.Parallel()

	declared := loadTextConstants(t)
	listed := allTextIDs()

	for name, id := range declared {
		if string(id) != name {
			t.Errorf("%s has MsgID value %q, want %q", name, id, name)
		}
		_, isListed := listed[name]
		if !isListed {
			t.Errorf("%s is declared in texts.go but missing from allTextIDs", name)
		}
	}
	for name, id := range listed {
		if string(id) != name {
			t.Errorf("%s in allTextIDs has MsgID value %q, want %q", name, id, name)
		}
		_, isDeclared := declared[name]
		if !isDeclared {
			t.Errorf("%s is listed in allTextIDs but not declared in texts.go", name)
		}
	}
}

var formatVerbPattern = regexp.MustCompile(`%(?:\[[0-9]+\])?[+-0# ]*[0-9]*(\.[0-9]+)?[a-zA-Z%]`)

func formatVerbs(s string) []string {
	matches := formatVerbPattern.FindAllString(s, -1)
	if matches == nil {
		return []string{}
	}

	return matches
}

func TestCatalogsAreCompleteAndSharePlaceholders(t *testing.T) {
	t.Parallel()

	ids := allTextIDs()
	if len(ids) == 0 {
		t.Fatal("no texts to check")
	}

	catalogs := map[Locale]map[MsgID]string{
		LocaleENUS: catalogENUS(),
		LocaleESCL: catalogESCL(),
	}

	en := catalogs[LocaleENUS]
	for loc, catalog := range catalogs {
		for name, id := range ids {
			text, ok := catalog[id]
			if !ok {
				t.Errorf("catalog %q is missing key %s", loc, name)
				continue
			}
			if strings.TrimSpace(text) == "" {
				t.Errorf("catalog %q has blank text for %s", loc, name)
			}
			for _, r := range text {
				isDisallowedControl := unicode.IsControl(r) && r != '\n'
				if isDisallowedControl {
					t.Errorf("catalog %q key %s has control rune %U", loc, name, r)
				}
			}

			wantVerbs := formatVerbs(en[id])
			gotVerbs := formatVerbs(text)
			if !reflect.DeepEqual(gotVerbs, wantVerbs) {
				t.Errorf("catalog %q key %s placeholders = %v, want %v (en=%q, %s=%q)",
					loc, name, gotVerbs, wantVerbs, en[id], loc, text)
			}
		}

		for id := range catalog {
			_, isDeclared := ids[string(id)]
			if !isDeclared {
				t.Errorf("catalog %q has undeclared key %q", loc, id)
			}
		}
	}
}

func TestCatalogFallbackToEnglishForUnsupportedLocales(t *testing.T) {
	t.Parallel()

	en := CatalogFor(LocaleENUS)
	if en.Locale() != LocaleENUS {
		t.Fatalf("CatalogFor(LocaleENUS).Locale() = %q, want %q", en.Locale(), LocaleENUS)
	}
	es := CatalogFor(LocaleESCL)
	if es.Locale() != LocaleESCL {
		t.Fatalf("CatalogFor(LocaleESCL).Locale() = %q, want %q", es.Locale(), LocaleESCL)
	}

	for _, spanish := range []string{"es-CL", "es-419", "es-ES", "es"} {
		if got := LocaleFromDiscord(spanish); got != LocaleESCL {
			t.Errorf("LocaleFromDiscord(%q) = %q, want %q", spanish, got, LocaleESCL)
		}
		if got := CatalogFor(Locale(spanish)).Locale(); got != LocaleESCL {
			t.Errorf("CatalogFor(%q).Locale() = %q, want %q", spanish, got, LocaleESCL)
		}
	}

	if got := ResolveLocale("en-US", "es-419"); got != LocaleESCL {
		t.Errorf("ResolveLocale(en-US, es-419) = %q, want %q", got, LocaleESCL)
	}
	if got := ResolveLocale("fr", ""); got != LocaleENUS {
		t.Errorf("ResolveLocale(fr, empty) = %q, want %q", got, LocaleENUS)
	}

	for _, unsupported := range []Locale{"", "fr", "pt-BR", "de", "en-GB"} {
		t.Run(string(unsupported), func(t *testing.T) {
			c := CatalogFor(unsupported)
			if c.Locale() != LocaleENUS {
				t.Errorf("CatalogFor(%q).Locale() = %q, want %q", unsupported, c.Locale(), LocaleENUS)
			}
			for name, id := range allTextIDs() {
				if got, want := c.Get(id), en.Get(id); got != want {
					t.Errorf("CatalogFor(%q).Get(%s) = %q, want %q", unsupported, name, got, want)
				}
			}
		})
	}

	var zero Catalog
	if zero.Locale() != LocaleENUS {
		t.Errorf("zero Catalog.Locale() = %q, want %q", zero.Locale(), LocaleENUS)
	}
	if got, want := zero.Get(textScheduleUnavailable), en.Get(textScheduleUnavailable); got != want {
		t.Errorf("zero Catalog.Get(textScheduleUnavailable) = %q, want %q", got, want)
	}
	if got, want := zero.Format(textInternal, "req-1"), en.Format(textInternal, "req-1"); got != want {
		t.Errorf("zero Catalog.Format(textInternal) = %q, want %q", got, want)
	}
}

func TestESCLCatalogTranslatesProseAndAvoidsVoseo(t *testing.T) {
	t.Parallel()

	en := catalogENUS()
	es := catalogESCL()

	// Format-only templates with no natural-language words are identical across locales.
	formatOnly := map[MsgID]bool{
		textPeriodDate: true,
	}

	voseoPattern := regexp.MustCompile(
		`(?i)\b(vos|pod\x{00e9}s|ten\x{00e9}s|sos|quer\x{00e9}s|sab\x{00e9}s|hac\x{00e9}|pon\x{00e9}|` +
			`ven\x{00ed}|dec\x{00ed}|sal\x{00ed}|un\x{00ed}te|unite|eleg\x{00ed}|configuralo|` +
			`us\x{00e1}|cre\x{00e1}|sac\x{00e1}|borr\x{00e1}|cancel\x{00e1}|revel\x{00e1}|` +
			`sorte\x{00e1}|pregunt\x{00e1}|respond\x{00e9}|mir\x{00e1}|fijate|asegurate|` +
			`copi\x{00e1}|revis\x{00e1}|ac\x{00e1})\b`,
	)

	kingsPattern := regexp.MustCompile(`(?i)\bkings\b`)

	for name, id := range allTextIDs() {
		esText := es[id]
		if !formatOnly[id] && esText == en[id] {
			t.Errorf("catalogESCL key %s is identical to English: %q", name, esText)
		}
		if match := voseoPattern.FindString(esText); match != "" {
			t.Errorf("catalogESCL key %s contains voseo or banned regional form %q in %q", name, match, esText)
		}
		for _, loc := range kingsPattern.FindAllStringIndex(esText, -1) {
			start := loc[0]
			hasMisPrefix := start >= 4 && strings.EqualFold(esText[start-4:start], "mis ")
			if !hasMisPrefix {
				t.Errorf("catalogESCL key %s contains bare \"kings\" without \"mis\" in %q", name, esText)
			}
		}
	}
}
