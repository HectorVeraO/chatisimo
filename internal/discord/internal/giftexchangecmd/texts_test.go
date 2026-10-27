package giftexchangecmd

import (
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"
)

// loadTextConstants returns the value of every text constant (name starting with "text") of texts.go, so a text added
// later is covered without touching the test.
func loadTextConstants(t *testing.T) map[string]string {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "texts.go", nil, 0)
	if err != nil {
		t.Fatalf("parse texts.go: %v", err)
	}

	pkg, err := (&types.Config{}).Check("giftexchangecmd", fset, []*ast.File{file}, nil)
	if err != nil {
		t.Fatalf("type-check texts.go: %v", err)
	}

	texts := make(map[string]string)
	for _, name := range pkg.Scope().Names() {
		constObj, isConst := pkg.Scope().Lookup(name).(*types.Const)
		isText := strings.HasPrefix(name, "text")
		if !isConst || !isText || constObj.Val().Kind() != constant.String {
			continue
		}
		texts[name] = constant.StringVal(constObj.Val())
	}

	return texts
}

// allTexts lists every text constant by name. TestAllTextsAreListed fails when texts.go and
// this table disagree, so the checks below never skip a text.
func allTexts() map[string]string {
	return map[string]string{
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
		"textYourAnswer":               textYourAnswer,
		"textNotSaved":                 textNotSaved,
		"textNotSavedLine":             textNotSavedLine,
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
		"textRedrawBelowValue":         textRedrawBelowValue,
		"textQuestionLengthValue":      textQuestionLengthValue,
		"textSuffixDefault":            textSuffixDefault,
		"textSuffixFixed":              textSuffixFixed,
		"textSuffixLocked":             textSuffixLocked,
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
		"textQuestionsAskedField":      textQuestionsAskedField,
		"textQuestionsReceivedField":   textQuestionsReceivedField,
		"textNoneYet":                  textNoneYet,
		"textClosedMarker":             textClosedMarker,
		"textTitleRecipient":           textTitleRecipient,
		"textTitleNewRecipient":        textTitleNewRecipient,
		"textTitleGiver":               textTitleGiver,
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
		"textDMFooter":                 textDMFooter,
		"textReasonTooFew":             textReasonTooFew,
		"textReasonNoValid":            textReasonNoValid,
		"textUnknownServer":            textUnknownServer,
	}
}

func TestAllTextsAreListed(t *testing.T) {
	declared := loadTextConstants(t)
	listed := allTexts()

	for name := range declared {
		_, isListed := listed[name]
		if !isListed {
			t.Errorf("%s is declared in texts.go but missing from allTexts", name)
		}
	}
	for name := range listed {
		_, isDeclared := declared[name]
		if !isDeclared {
			t.Errorf("%s is listed in allTexts but not declared in texts.go", name)
		}
	}
}

func TestHandlerTextsAreNonEmptyPrintableASCII(t *testing.T) {
	texts := allTexts()
	if len(texts) == 0 {
		t.Fatal("no texts to check")
	}

	for name, text := range texts {
		if strings.TrimSpace(text) == "" {
			t.Errorf("%s is blank", name)
		}
		for _, r := range text {
			isPrintableASCII := r >= 0x20 && r <= 0x7e
			if !isPrintableASCII {
				t.Errorf("%s has the non-ASCII or control rune %U", name, r)
			}
		}
	}
}
