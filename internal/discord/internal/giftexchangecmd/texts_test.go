package giftexchangecmd

import (
	"strings"
	"testing"
)

func TestHandlerTextsAreNonEmptyPrintableASCII(t *testing.T) {
	texts := []string{
		textRuleDrawAt, textRuleRevealAt, textRuleReminderLead, textRuleDropoutPolicy,
		textRuleAvoidLastRound, textRuleExclusions, textRuleQuestionsEnabled,
		textRuleMinParticipants, textRuleMaxParticipants, textRuleNoSelfAssignment,
		textRuleRedrawBelow, textRuleQuestionMaxLength, textRuleQuestionsPerDay, textTimeValue,
		textReminderOff, textOn, textOff, textNever, textRedrawBelowValue, textQuestionLengthValue,
		textSuffixDefault, textSuffixFixed, textSuffixLocked, textRulesTitle, textParticipantsField,
		textExclusionsField, textAndMore, textQuestionsAskedField, textQuestionsReceivedField,
		textNoneYet, textClosedMarker, textSelectPlaceholder, textModalLabel, textModalTitle,
		textModalPlaceholderTime, textModalPlaceholderReminder,
		textHelpLead, textHelpCreate, textHelpList, textHelpJoin, textHelpLeave, textHelpRemove,
		textHelpRules, textHelpExclude, textHelpUnexclude, textHelpRename, textHelpDraw,
		textHelpRecipient, textHelpGiver, textHelpAsk, textHelpQuestions, textHelpReveal,
		textHelpCancel, textHelpHelp,
	}

	for _, text := range texts {
		if strings.TrimSpace(text) == "" {
			t.Errorf("text %q is blank", text)
		}
		for _, r := range text {
			isPrintableASCII := r >= 0x20 && r <= 0x7e
			if !isPrintableASCII {
				t.Errorf("text %q has the non-ASCII or control rune %U", text, r)
			}
		}
	}
}
