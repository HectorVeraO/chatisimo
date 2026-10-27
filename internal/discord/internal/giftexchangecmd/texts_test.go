package giftexchangecmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// loadTextConstants returns the value of every text constant (name starting with "text") of
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
	return map[string]MsgID{
		"textGuildOnly":                textGuildOnly,
		"textNoCandidates":             textNoCandidates,
		"textChooseExchange":           textChooseExchange,
		"textNoMatch":                  textNoMatch,
		"textCreated":                  textCreated,
		"textCardBody":                 textCardBody,
		"textManual":                   textManual,
		"textJoined":                   textJoined,
		"textLeft":                     textLeft,
		"textConfirmLeave":             textConfirmLeave,
		"textConfirmRemove":            textConfirmRemove,
		"textRemoved":                  textRemoved,
		"textOutcomeRemoved":           textOutcomeRemoved,
		"textOutcomePatched":           textOutcomePatched,
		"textOutcomeRedrawn":           textOutcomeRedrawn,
		"textOutcomeReopened":          textOutcomeReopened,
		"textOwnRecipient":             textOwnRecipient,
		"textExcluded":                 textExcluded,
		"textExcludedBoth":             textExcludedBoth,
		"textUnexcluded":               textUnexcluded,
		"textRenamed":                  textRenamed,
		"textConfirmDraw":              textConfirmDraw,
		"textDrawn":                    textDrawn,
		"textDrawnPublic":              textDrawnPublic,
		"textConfirmReveal":            textConfirmReveal,
		"textRevealed":                 textRevealed,
		"textRevealedPublic":           textRevealedPublic,
		"textConfirmCancel":            textConfirmCancel,
		"textCancelled":                textCancelled,
		"textCancelledPublic":          textCancelledPublic,
		"textNothingChanged":           textNothingChanged,
		"textAsked":                    textAsked,
		"textPreferencesSaved":         textPreferencesSaved,
		"textPreferencesCleared":       textPreferencesCleared,
		"textPreferencesNone":          textPreferencesNone,
		"textYourAnswer":               textYourAnswer,
		"textNotSaved":                 textNotSaved,
		"textNotSavedLine":             textNotSavedLine,
		"textInvalidInput":             textInvalidInput,
		"textBadTime":                  textBadTime,
		"textNotFound":                 textNotFound,
		"textForbidden":                textForbidden,
		"textAlreadyParticipant":       textAlreadyParticipant,
		"textNotParticipant":           textNotParticipant,
		"textFull":                     textFull,
		"textTooFew":                   textTooFew,
		"textInfeasible":               textInfeasible,
		"textRuleFixed":                textRuleFixed,
		"textRuleLocked":               textRuleLocked,
		"textUnknownRule":              textUnknownRule,
		"textQuestionsOff":             textQuestionsOff,
		"textRateLimited":              textRateLimited,
		"textRephraseFailed":           textRephraseFailed,
		"textInvalidState":             textInvalidState,
		"textInternal":                 textInternal,
		"textQuestionClosed":           textQuestionClosed,
		"textAlreadyAnswered":          textAlreadyAnswered,
		"textRulesState":               textRulesState,
		"textRoleOrganizer":            textRoleOrganizer,
		"textRoleParticipant":          textRoleParticipant,
		"textRoleBoth":                 textRoleBoth,
		"textRoleNone":                 textRoleNone,
		"textStateOpen":                textStateOpen,
		"textStateDrawn":               textStateDrawn,
		"textStateRevealed":            textStateRevealed,
		"textStateCancelled":           textStateCancelled,
		"textListTitle":                textListTitle,
		"textListEmpty":                textListEmpty,
		"textListLine":                 textListLine,
		"textHelpLead":                 textHelpLead,
		"textHelpCreate":               textHelpCreate,
		"textHelpList":                 textHelpList,
		"textHelpJoin":                 textHelpJoin,
		"textHelpLeave":                textHelpLeave,
		"textHelpRemove":               textHelpRemove,
		"textHelpRules":                textHelpRules,
		"textHelpExclude":              textHelpExclude,
		"textHelpUnexclude":            textHelpUnexclude,
		"textHelpRename":               textHelpRename,
		"textHelpDraw":                 textHelpDraw,
		"textHelpRecipient":            textHelpRecipient,
		"textHelpGiver":                textHelpGiver,
		"textHelpAsk":                  textHelpAsk,
		"textHelpQuestions":            textHelpQuestions,
		"textHelpPreferences":          textHelpPreferences,
		"textHelpReveal":               textHelpReveal,
		"textHelpCancel":               textHelpCancel,
		"textHelpHelp":                 textHelpHelp,
		"textRuleDrawAt":               textRuleDrawAt,
		"textRuleRevealAt":             textRuleRevealAt,
		"textRuleReminderLead":         textRuleReminderLead,
		"textRuleDropoutPolicy":        textRuleDropoutPolicy,
		"textRuleAvoidLastRound":       textRuleAvoidLastRound,
		"textRuleExclusions":           textRuleExclusions,
		"textRuleQuestionsEnabled":     textRuleQuestionsEnabled,
		"textRuleMinParticipants":      textRuleMinParticipants,
		"textRuleMaxParticipants":      textRuleMaxParticipants,
		"textRuleNoSelfAssignment":     textRuleNoSelfAssignment,
		"textRuleRedrawBelow":          textRuleRedrawBelow,
		"textRuleQuestionMaxLength":    textRuleQuestionMaxLength,
		"textRuleQuestionsPerDay":      textRuleQuestionsPerDay,
		"textTimeValue":                textTimeValue,
		"textReminderOff":              textReminderOff,
		"textOn":                       textOn,
		"textOff":                      textOff,
		"textNever":                    textNever,
		"textDropoutPatch":             textDropoutPatch,
		"textDropoutRedraw":            textDropoutRedraw,
		"textRedrawBelowValue":         textRedrawBelowValue,
		"textQuestionLengthValue":      textQuestionLengthValue,
		"textSuffixDefault":            textSuffixDefault,
		"textSuffixFixed":              textSuffixFixed,
		"textSuffixLocked":             textSuffixLocked,
		"textRulesBadTimeReason":       textRulesBadTimeReason,
		"textRulesTooLongReason":       textRulesTooLongReason,
		"textRulesExclusionsHint":      textRulesExclusionsHint,
		"textRulesNotEditable":         textRulesNotEditable,
		"textRulesNotAPersonReason":    textRulesNotAPersonReason,
		"textRulesNotParticipantText":  textRulesNotParticipantText,
		"textButtonJoin":               textButtonJoin,
		"textButtonLeave":              textButtonLeave,
		"textButtonRules":              textButtonRules,
		"textButtonConfirm":            textButtonConfirm,
		"textButtonBack":               textButtonBack,
		"textButtonAnswer":             textButtonAnswer,
		"textFieldDraw":                textFieldDraw,
		"textFieldReveal":              textFieldReveal,
		"textRulesTitle":               textRulesTitle,
		"textParticipantsField":        textParticipantsField,
		"textExclusionsField":          textExclusionsField,
		"textAndMore":                  textAndMore,
		"textQuestionModalTitle":       textQuestionModalTitle,
		"textQuestionModalHint":        textQuestionModalHint,
		"textQuestionListTitle":        textQuestionListTitle,
		"textQuestionsAskedField":      textQuestionsAskedField,
		"textQuestionsReceivedField":   textQuestionsReceivedField,
		"textNoneYet":                  textNoneYet,
		"textClosedMarker":             textClosedMarker,
		"textTitleRecipient":           textTitleRecipient,
		"textTitleNewRecipient":        textTitleNewRecipient,
		"textTitleGiver":               textTitleGiver,
		"textTitlePreferences":         textTitlePreferences,
		"textFieldPreferences":         textFieldPreferences,
		"textFieldLikes":               textFieldLikes,
		"textFieldDislikes":            textFieldDislikes,
		"textFieldDetails":             textFieldDetails,
		"textSelectPlaceholder":        textSelectPlaceholder,
		"textModalLabel":               textModalLabel,
		"textModalTitle":               textModalTitle,
		"textModalPlaceholderTime":     textModalPlaceholderTime,
		"textModalPlaceholderReminder": textModalPlaceholderReminder,
		"textDMDrawn":                  textDMDrawn,
		"textDMRedrawn":                textDMRedrawn,
		"textDMChanged":                textDMChanged,
		"textDMVoided":                 textDMVoided,
		"textDMDrawFailed":             textDMDrawFailed,
		"textDMDrawSoon":               textDMDrawSoon,
		"textDMRevealSoon":             textDMRevealSoon,
		"textDMRevealed":               textDMRevealed,
		"textDMCancelled":              textDMCancelled,
		"textDMQuestion":               textDMQuestion,
		"textDMAnswer":                 textDMAnswer,
		"textDMPreferencesUpdated":     textDMPreferencesUpdated,
		"textDMFooter":                 textDMFooter,
		"textReasonTooFew":             textReasonTooFew,
		"textReasonNoValid":            textReasonNoValid,
		"textUnknownServer":            textUnknownServer,
	}
}

func TestAllTextsAreListed(t *testing.T) {
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

func TestHandlerTextsAreNonEmptyPrintableASCII(t *testing.T) {
	ids := allTextIDs()
	if len(ids) == 0 {
		t.Fatal("no texts to check")
	}

	en := catalogEN()
	for name, id := range ids {
		text, ok := en[id]
		if !ok {
			t.Errorf("%s is missing from catalogEN", name)
			continue
		}
		if strings.TrimSpace(text) == "" {
			t.Errorf("%s is blank in catalogEN", name)
		}
		for _, r := range text {
			isPrintableASCII := r >= 0x20 && r <= 0x7e
			if !isPrintableASCII {
				t.Errorf("%s in catalogEN has the non-ASCII or control rune %U", name, r)
			}
		}
	}
}

var formatVerbPattern = regexp.MustCompile(`%[+-0# ]*[0-9]*(\.[0-9]+)?[a-zA-Z%]`)

func formatVerbs(s string) []string {
	matches := formatVerbPattern.FindAllString(s, -1)
	if matches == nil {
		return []string{}
	}

	return matches
}

func TestCatalogsAreCompleteAndSharePlaceholders(t *testing.T) {
	ids := allTextIDs()
	catalogs := map[Locale]map[MsgID]string{
		LocaleEN:    catalogEN(),
		LocaleES419: catalogES419(),
	}

	en := catalogs[LocaleEN]
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
				if unicode.IsControl(r) {
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
	en := CatalogFor(LocaleEN)
	if en.Locale() != LocaleEN {
		t.Fatalf("CatalogFor(LocaleEN).Locale() = %q, want %q", en.Locale(), LocaleEN)
	}
	es := CatalogFor(LocaleES419)
	if es.Locale() != LocaleES419 {
		t.Fatalf("CatalogFor(LocaleES419).Locale() = %q, want %q", es.Locale(), LocaleES419)
	}

	for _, unsupported := range []Locale{"", "fr", "pt-BR", "es", "de", "en-US"} {
		t.Run(string(unsupported), func(t *testing.T) {
			c := CatalogFor(unsupported)
			if c.Locale() != LocaleEN {
				t.Errorf("CatalogFor(%q).Locale() = %q, want %q", unsupported, c.Locale(), LocaleEN)
			}
			for name, id := range allTextIDs() {
				if got, want := c.Get(id), en.Get(id); got != want {
					t.Errorf("CatalogFor(%q).Get(%s) = %q, want %q", unsupported, name, got, want)
				}
			}
		})
	}

	var zero Catalog
	if zero.Locale() != LocaleEN {
		t.Errorf("zero Catalog.Locale() = %q, want %q", zero.Locale(), LocaleEN)
	}
	if got, want := zero.Get(textGuildOnly), en.Get(textGuildOnly); got != want {
		t.Errorf("zero Catalog.Get(textGuildOnly) = %q, want %q", got, want)
	}
}

func TestES419CatalogTranslatesProseAndAvoidsVoseo(t *testing.T) {
	en := catalogEN()
	es := catalogES419()

	// Format-only templates that contain no natural-language words are identical across locales.
	formatOnly := map[MsgID]bool{
		textNotSavedLine: true,
		textTimeValue:    true,
	}

	voseoPattern := regexp.MustCompile(
		`(?i)\b(vos|podés|tenés|sos|querés|sabés|hacé|poné|vení|decí|salí|uníte|unite|` +
			`elegí|configuralo|usá|creá|sacá|borrá|cancelá|revelá|sorteá|preguntá|respondé|` +
			`mirá|fijate|asegurate|copiá|revisá|acá)\b`,
	)

	for name, id := range allTextIDs() {
		esText := es[id]
		if !formatOnly[id] && esText == en[id] {
			t.Errorf("catalogES419 key %s is identical to English: %q", name, esText)
		}
		if match := voseoPattern.FindString(esText); match != "" {
			t.Errorf("catalogES419 key %s contains voseo or banned regional form %q in %q", name, match, esText)
		}
	}
}

func TestPlaywrightTextsMatchEnglishCatalog(t *testing.T) {
	const path = "../../../../playwright/fixtures/texts.ts"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	// Constants in texts.ts that belong to packages outside giftexchangecmd.
	external := map[string]bool{
		"mustBeInTheFuture": true,
		"composerTooLong":   true,
		"reasonTooLong":     true,
		"reasonBidi":        true,
		"reasonNoVisible":   true,
	}

	declPattern := regexp.MustCompile(`(?m)^export const ([a-zA-Z0-9_]+)\s*=\s*((?:"(?:[^"\\]|\\.)*"\s*\+?\s*)+);`)
	strPattern := regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)

	matches := declPattern.FindAllStringSubmatch(string(raw), -1)
	if len(matches) == 0 {
		t.Fatalf("no export const declarations parsed from %s", path)
	}

	en := CatalogFor(LocaleEN)
	checked := 0
	for _, m := range matches {
		tsName := m[1]
		if external[tsName] {
			continue
		}

		var tsValue strings.Builder
		for _, lit := range strPattern.FindAllString(m[2], -1) {
			unquoted, unquoteErr := strconv.Unquote(lit)
			if unquoteErr != nil {
				t.Fatalf("unquote %s literal %s: %v", tsName, lit, unquoteErr)
			}
			tsValue.WriteString(unquoted)
		}

		if tsName == "rulesPairArrow" {
			if tsValue.String() != rulesPairArrow {
				t.Errorf("texts.ts %s = %q, want rulesPairArrow %q", tsName, tsValue.String(), rulesPairArrow)
			}
			checked++
			continue
		}

		msgID := MsgID("text" + strings.ToUpper(tsName[:1]) + tsName[1:])
		if after, isDM := strings.CutPrefix(tsName, "dm"); isDM {
			msgID = MsgID("textDM" + after)
		}
		want := en.Get(msgID)
		if tsValue.String() != want {
			t.Errorf("texts.ts %s = %q, want %s %q", tsName, tsValue.String(), msgID, want)
		}
		checked++
	}

	if checked == 0 {
		t.Fatal("checked zero giftexchangecmd constants in texts.ts")
	}
}
