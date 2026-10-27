package giftexchangecmd

import "fmt"

// Locale identifies a language catalog of the Discord adapter.
type Locale string

// Supported locales of the Discord adapter. LocaleEN is the default and fallback.
const (
	LocaleEN    Locale = "en"
	LocaleES419 Locale = "es-419"
)

// MsgID identifies one user-facing message template in a Catalog.
type MsgID string

// Catalog holds the user-facing message templates for one Locale.
type Catalog struct {
	locale   Locale
	messages map[MsgID]string
	fallback map[MsgID]string
}

// CatalogFor returns the message catalog for loc, falling back to LocaleEN when loc is
// unsupported or empty.
func CatalogFor(loc Locale) Catalog {
	en := catalogEN()
	if loc == LocaleES419 {
		return Catalog{
			locale:   LocaleES419,
			messages: catalogES419(),
			fallback: en,
		}
	}

	return Catalog{
		locale:   LocaleEN,
		messages: en,
		fallback: en,
	}
}

// Locale returns the resolved locale of the catalog.
func (c Catalog) Locale() Locale {
	if c.locale == "" {
		return LocaleEN
	}

	return c.locale
}

// Get returns the raw message template for id, falling back to LocaleEN if missing.
func (c Catalog) Get(id MsgID) string {
	if c.messages != nil {
		if text, ok := c.messages[id]; ok {
			return text
		}
	}
	if c.fallback != nil {
		if text, ok := c.fallback[id]; ok {
			return text
		}
	}
	if text, ok := catalogEN()[id]; ok {
		return text
	}

	panic("giftexchangecmd: unknown message ID " + string(id))
}

// Format formats the message template for id with args.
func (c Catalog) Format(id MsgID, args ...any) string {
	text := fmt.Sprintf(c.Get(id), args...)
	return text
}

// Every user-facing text key of the Discord adapter. Placeholders: the exchange name arrives
// already escaped and in bold (render.Bold), numbers as %d, states and roles as %s.
const (
	textGuildOnly          MsgID = "textGuildOnly"
	textNoCandidates       MsgID = "textNoCandidates"
	textChooseExchange     MsgID = "textChooseExchange"
	textNoMatch            MsgID = "textNoMatch"
	textCreated            MsgID = "textCreated"
	textCardBody           MsgID = "textCardBody"
	textManual             MsgID = "textManual"
	textJoined             MsgID = "textJoined"
	textLeft               MsgID = "textLeft"
	textConfirmLeave       MsgID = "textConfirmLeave"
	textConfirmRemove      MsgID = "textConfirmRemove"
	textRemoved            MsgID = "textRemoved"
	textOutcomeRemoved     MsgID = "textOutcomeRemoved"
	textOutcomePatched     MsgID = "textOutcomePatched"
	textOutcomeRedrawn     MsgID = "textOutcomeRedrawn"
	textOutcomeReopened    MsgID = "textOutcomeReopened"
	textOwnRecipient       MsgID = "textOwnRecipient"
	textExcluded           MsgID = "textExcluded"
	textExcludedBoth       MsgID = "textExcludedBoth"
	textUnexcluded         MsgID = "textUnexcluded"
	textRenamed            MsgID = "textRenamed"
	textConfirmDraw        MsgID = "textConfirmDraw"
	textDrawn              MsgID = "textDrawn"
	textDrawnPublic        MsgID = "textDrawnPublic"
	textConfirmReveal      MsgID = "textConfirmReveal"
	textRevealed           MsgID = "textRevealed"
	textRevealedPublic     MsgID = "textRevealedPublic"
	textConfirmCancel      MsgID = "textConfirmCancel"
	textCancelled          MsgID = "textCancelled"
	textCancelledPublic    MsgID = "textCancelledPublic"
	textNothingChanged     MsgID = "textNothingChanged"
	textAsked              MsgID = "textAsked"
	textPreferencesSaved   MsgID = "textPreferencesSaved"
	textPreferencesCleared MsgID = "textPreferencesCleared"
	textPreferencesNone    MsgID = "textPreferencesNone"
	textYourAnswer         MsgID = "textYourAnswer"
	textNotSaved           MsgID = "textNotSaved"
	textNotSavedLine       MsgID = "textNotSavedLine"
	textInvalidInput       MsgID = "textInvalidInput"
	textBadTime            MsgID = "textBadTime"
	textNotFound           MsgID = "textNotFound"
	textForbidden          MsgID = "textForbidden"
	textAlreadyParticipant MsgID = "textAlreadyParticipant"
	textNotParticipant     MsgID = "textNotParticipant"
	textFull               MsgID = "textFull"
	textTooFew             MsgID = "textTooFew"
	textInfeasible         MsgID = "textInfeasible"
	textRuleFixed          MsgID = "textRuleFixed"
	textRuleLocked         MsgID = "textRuleLocked"
	textUnknownRule        MsgID = "textUnknownRule"
	textQuestionsOff       MsgID = "textQuestionsOff"
	textRateLimited        MsgID = "textRateLimited"
	textRephraseFailed     MsgID = "textRephraseFailed"
	textInvalidState       MsgID = "textInvalidState"
	textInternal           MsgID = "textInternal"
	textQuestionClosed     MsgID = "textQuestionClosed"
	textAlreadyAnswered    MsgID = "textAlreadyAnswered"
	textRulesState         MsgID = "textRulesState"
	textRoleOrganizer      MsgID = "textRoleOrganizer"
	textRoleParticipant    MsgID = "textRoleParticipant"
	textRoleBoth           MsgID = "textRoleBoth"
	textRoleNone           MsgID = "textRoleNone"
	textStateOpen          MsgID = "textStateOpen"
	textStateDrawn         MsgID = "textStateDrawn"
	textStateRevealed      MsgID = "textStateRevealed"
	textStateCancelled     MsgID = "textStateCancelled"
	textListTitle          MsgID = "textListTitle"
	textListEmpty          MsgID = "textListEmpty"
	textListLine           MsgID = "textListLine"
)

// The help text keys: a lead line, then one line per verb.
const (
	textHelpLead        MsgID = "textHelpLead"
	textHelpCreate      MsgID = "textHelpCreate"
	textHelpList        MsgID = "textHelpList"
	textHelpJoin        MsgID = "textHelpJoin"
	textHelpLeave       MsgID = "textHelpLeave"
	textHelpRemove      MsgID = "textHelpRemove"
	textHelpRules       MsgID = "textHelpRules"
	textHelpExclude     MsgID = "textHelpExclude"
	textHelpUnexclude   MsgID = "textHelpUnexclude"
	textHelpRename      MsgID = "textHelpRename"
	textHelpDraw        MsgID = "textHelpDraw"
	textHelpRecipient   MsgID = "textHelpRecipient"
	textHelpGiver       MsgID = "textHelpGiver"
	textHelpAsk         MsgID = "textHelpAsk"
	textHelpQuestions   MsgID = "textHelpQuestions"
	textHelpPreferences MsgID = "textHelpPreferences"
	textHelpReveal      MsgID = "textHelpReveal"
	textHelpCancel      MsgID = "textHelpCancel"
	textHelpHelp        MsgID = "textHelpHelp"
)

// Rule labels, values and input error reasons shown by the rules and exclusions commands.
const (
	textRuleDrawAt            MsgID = "textRuleDrawAt"
	textRuleRevealAt          MsgID = "textRuleRevealAt"
	textRuleReminderLead      MsgID = "textRuleReminderLead"
	textRuleDropoutPolicy     MsgID = "textRuleDropoutPolicy"
	textRuleAvoidLastRound    MsgID = "textRuleAvoidLastRound"
	textRuleExclusions        MsgID = "textRuleExclusions"
	textRuleQuestionsEnabled  MsgID = "textRuleQuestionsEnabled"
	textRuleMinParticipants   MsgID = "textRuleMinParticipants"
	textRuleMaxParticipants   MsgID = "textRuleMaxParticipants"
	textRuleNoSelfAssignment  MsgID = "textRuleNoSelfAssignment"
	textRuleRedrawBelow       MsgID = "textRuleRedrawBelow"
	textRuleQuestionMaxLength MsgID = "textRuleQuestionMaxLength"
	textRuleQuestionsPerDay   MsgID = "textRuleQuestionsPerDay"

	textTimeValue           MsgID = "textTimeValue"
	textReminderOff         MsgID = "textReminderOff"
	textOn                  MsgID = "textOn"
	textOff                 MsgID = "textOff"
	textNever               MsgID = "textNever"
	textDropoutPatch        MsgID = "textDropoutPatch"
	textDropoutRedraw       MsgID = "textDropoutRedraw"
	textRedrawBelowValue    MsgID = "textRedrawBelowValue"
	textQuestionLengthValue MsgID = "textQuestionLengthValue"

	textSuffixDefault MsgID = "textSuffixDefault"
	textSuffixFixed   MsgID = "textSuffixFixed"
	textSuffixLocked  MsgID = "textSuffixLocked"

	textRulesBadTimeReason      MsgID = "textRulesBadTimeReason"
	textRulesTooLongReason      MsgID = "textRulesTooLongReason"
	textRulesExclusionsHint     MsgID = "textRulesExclusionsHint"
	textRulesNotEditable        MsgID = "textRulesNotEditable"
	textRulesNotAPersonReason   MsgID = "textRulesNotAPersonReason"
	textRulesNotParticipantText MsgID = "textRulesNotParticipantText"

	textFieldNameName             MsgID = "textFieldNameName"
	textFieldNameText             MsgID = "textFieldNameText"
	textFieldNameLikes            MsgID = "textFieldNameLikes"
	textFieldNameDislikes         MsgID = "textFieldNameDislikes"
	textFieldNameDetails          MsgID = "textFieldNameDetails"
	textFieldNameDrawAt           MsgID = "textFieldNameDrawAt"
	textFieldNameRevealAt         MsgID = "textFieldNameRevealAt"
	textFieldNameReminderLead     MsgID = "textFieldNameReminderLead"
	textFieldNameDropoutPolicy    MsgID = "textFieldNameDropoutPolicy"
	textFieldNameAvoidLastRound   MsgID = "textFieldNameAvoidLastRound"
	textFieldNameQuestionsEnabled MsgID = "textFieldNameQuestionsEnabled"
	textFieldNameRecipient        MsgID = "textFieldNameRecipient"
	textFieldNameGiver            MsgID = "textFieldNameGiver"
	textFieldNameRule             MsgID = "textFieldNameRule"
	textFieldNameExclusion        MsgID = "textFieldNameExclusion"
	textFieldNameExchangeID       MsgID = "textFieldNameExchangeID"
	textFieldNameQuestionID       MsgID = "textFieldNameQuestionID"
	textFieldNamePersonID         MsgID = "textFieldNamePersonID"
	textFieldNameGroupID          MsgID = "textFieldNameGroupID"
	textFieldNameGiverID          MsgID = "textFieldNameGiverID"
	textFieldNameRecipientID      MsgID = "textFieldNameRecipientID"
	textFieldNameIdentityProvider MsgID = "textFieldNameIdentityProvider"
	textFieldNameIdentitySubject  MsgID = "textFieldNameIdentitySubject"
	textFieldNameGroupProvider    MsgID = "textFieldNameGroupProvider"
	textFieldNameGroupSubject     MsgID = "textFieldNameGroupSubject"
	textFieldNameRegistry         MsgID = "textFieldNameRegistry"
	textFieldNameDepsPool         MsgID = "textFieldNameDepsPool"
	textFieldNameDepsScheduler    MsgID = "textFieldNameDepsScheduler"
	textFieldNameDepsClock        MsgID = "textFieldNameDepsClock"
	textFieldNameDepsRandom       MsgID = "textFieldNameDepsRandom"
	textFieldNameDepsNotifier     MsgID = "textFieldNameDepsNotifier"
	textFieldNameDepsRephraser    MsgID = "textFieldNameDepsRephraser"
	textFieldNameDepsLogger       MsgID = "textFieldNameDepsLogger"
	textReasonRequired            MsgID = "textReasonRequired"
	textReasonUnknown             MsgID = "textReasonUnknown"
	textReasonEmpty               MsgID = "textReasonEmpty"
	textReasonTooLong             MsgID = "textReasonTooLong"
	textReasonInvalidUTF8         MsgID = "textReasonInvalidUTF8"
	textReasonInvalidText         MsgID = "textReasonInvalidText"
	textReasonControlChars        MsgID = "textReasonControlChars"
	textReasonLineBreaks          MsgID = "textReasonLineBreaks"
	textReasonBidiChars           MsgID = "textReasonBidiChars"
	textReasonNoVisibleChar       MsgID = "textReasonNoVisibleChar"
	textReasonNotCanonicalUUID    MsgID = "textReasonNotCanonicalUUID"
	textReasonZeroUUID            MsgID = "textReasonZeroUUID"
	textReasonEditedViaExclusions MsgID = "textReasonEditedViaExclusions"
	textReasonInvalidInstant      MsgID = "textReasonInvalidInstant"
	textReasonYearOutOfRange      MsgID = "textReasonYearOutOfRange"
	textReasonInvalidDuration     MsgID = "textReasonInvalidDuration"
	textReasonDurationOutOfRange  MsgID = "textReasonDurationOutOfRange"
	textReasonNotOneOf            MsgID = "textReasonNotOneOf"
	textReasonInvalidBool         MsgID = "textReasonInvalidBool"
	textReasonMustBeFuture        MsgID = "textReasonMustBeFuture"
	textReasonBeforeRevealAt      MsgID = "textReasonBeforeRevealAt"
	textReasonAfterDrawAt         MsgID = "textReasonAfterDrawAt"
	textReasonSameAsGiver         MsgID = "textReasonSameAsGiver"
)

// Buttons, card fields, embed titles and the other labels of the interface.
const (
	textButtonJoin    MsgID = "textButtonJoin"
	textButtonLeave   MsgID = "textButtonLeave"
	textButtonRules   MsgID = "textButtonRules"
	textButtonConfirm MsgID = "textButtonConfirm"
	textButtonBack    MsgID = "textButtonBack"
	textButtonAnswer  MsgID = "textButtonAnswer"

	textFieldDraw   MsgID = "textFieldDraw"
	textFieldReveal MsgID = "textFieldReveal"

	textRulesTitle        MsgID = "textRulesTitle"
	textParticipantsField MsgID = "textParticipantsField"
	textExclusionsField   MsgID = "textExclusionsField"
	textAndMore           MsgID = "textAndMore"

	textQuestionModalTitle     MsgID = "textQuestionModalTitle"
	textQuestionModalHint      MsgID = "textQuestionModalHint"
	textQuestionListTitle      MsgID = "textQuestionListTitle"
	textQuestionsAskedField    MsgID = "textQuestionsAskedField"
	textQuestionsReceivedField MsgID = "textQuestionsReceivedField"
	textNoneYet                MsgID = "textNoneYet"
	textClosedMarker           MsgID = "textClosedMarker"

	textTitleRecipient    MsgID = "textTitleRecipient"
	textTitleNewRecipient MsgID = "textTitleNewRecipient"
	textTitleGiver        MsgID = "textTitleGiver"
	textTitlePreferences  MsgID = "textTitlePreferences"

	textFieldPreferences MsgID = "textFieldPreferences"
	textFieldLikes       MsgID = "textFieldLikes"
	textFieldDislikes    MsgID = "textFieldDislikes"
	textFieldDetails     MsgID = "textFieldDetails"

	textSelectPlaceholder        MsgID = "textSelectPlaceholder"
	textModalLabel               MsgID = "textModalLabel"
	textModalTitle               MsgID = "textModalTitle"
	textModalPlaceholderTime     MsgID = "textModalPlaceholderTime"
	textModalPlaceholderReminder MsgID = "textModalPlaceholderReminder"
)

// The texts of the direct messages. The first two verbs of each are the exchange name and the
// server name.
const (
	textDMDrawn              MsgID = "textDMDrawn"
	textDMRedrawn            MsgID = "textDMRedrawn"
	textDMChanged            MsgID = "textDMChanged"
	textDMVoided             MsgID = "textDMVoided"
	textDMDrawFailed         MsgID = "textDMDrawFailed"
	textDMDrawSoon           MsgID = "textDMDrawSoon"
	textDMRevealSoon         MsgID = "textDMRevealSoon"
	textDMRevealed           MsgID = "textDMRevealed"
	textDMCancelled          MsgID = "textDMCancelled"
	textDMQuestion           MsgID = "textDMQuestion"
	textDMAnswer             MsgID = "textDMAnswer"
	textDMPreferencesUpdated MsgID = "textDMPreferencesUpdated"
	textDMFooter             MsgID = "textDMFooter"
	textReasonTooFew         MsgID = "textReasonTooFew"
	textReasonNoValid        MsgID = "textReasonNoValid"
	textUnknownServer        MsgID = "textUnknownServer"

	// customIDAnswerPrefix starts the custom ID of the Answer button; the question ID follows.
	customIDAnswerPrefix = "/gx/answer/"
)

// catalogEN returns the English message catalog (LocaleEN).
func catalogEN() map[MsgID]string {
	return map[MsgID]string{
		textGuildOnly:      "This command works only in a server.",
		textNoCandidates:   "You have no exchange where you can do this.",
		textChooseExchange: "You are in several exchanges. Choose one with the exchange option:",
		textNoMatch:        "No exchange matches that name. Pick one from the list.",
		textCreated: "You created %s and you organize it. You are not a participant unless you join. " +
			"Configure it with /ge rules.",
		textCardBody:           "Organized by %s. Join with the button or /ge join.",
		textManual:             "not set (organizer runs it)",
		textJoined:             "You joined %s.",
		textLeft:               "You left %s.",
		textConfirmLeave:       "%s is already drawn. Leave anyway? Your giver may get a new recipient.",
		textConfirmRemove:      "%s is already drawn. Remove %s? A giver may get a new recipient.",
		textRemoved:            "Removed %s from %s.",
		textOutcomeRemoved:     "Removed.",
		textOutcomePatched:     "Removed. One giver got a new recipient by DM.",
		textOutcomeRedrawn:     "Removed. The draw was redone and every giver got a DM.",
		textOutcomeReopened:    "Removed. Too few participants or no valid draw remained, so the draw was undone and %s is open again.",
		textOwnRecipient:       "You can't remove your own recipient. They can leave, or you can cancel the exchange.",
		textExcluded:           "Exclusion added: %s will not draw %s.",
		textExcludedBoth:       " The reverse is excluded too.",
		textUnexcluded:         "Exclusion removed: %s may draw %s again.",
		textRenamed:            "Renamed to %s.",
		textConfirmDraw:        "Draw %s now with %d participants? This can't be undone.",
		textDrawn:              "Draw done for %s.",
		textDrawnPublic:        "The draw for %s is done. Each giver got a DM with their recipient, or can use /ge recipient.",
		textConfirmReveal:      "Reveal %s now? Each participant will learn their giver.",
		textRevealed:           "%s is revealed.",
		textRevealedPublic:     "%s is revealed. Each participant got a DM with their giver, or can use /ge giver.",
		textConfirmCancel:      "Cancel %s? This can't be undone.",
		textCancelled:          "%s is cancelled.",
		textCancelledPublic:    "%s was cancelled by the organizer.",
		textNothingChanged:     "Nothing changed.",
		textAsked:              "Your question was sent anonymously to your recipient in %s.",
		textPreferencesSaved:   "Your gift preferences for %s were saved.",
		textPreferencesCleared: "Your gift preferences for %s were cleared.",
		textPreferencesNone:    "You have not set gift preferences for %s yet. Use /ge preferences with likes, dislikes or details.",
		textYourAnswer:         "Your answer:",
		textNotSaved:           "Not saved:",
		textNotSavedLine:       "- %s",
		textInvalidInput:       "invalid input",
		textBadTime:            "Use in 3d, in 2h30m, YYYY-MM-DD HH:MM (Santiago time) or an RFC 3339 time.",
		textNotFound:           "That exchange does not exist here.",
		textForbidden:          "You are not allowed to do that in this exchange.",
		textAlreadyParticipant: "You are already in this exchange.",
		textNotParticipant:     "You are not in this exchange.",
		textFull:               "This exchange is full (%d participants).",
		textTooFew:             "At least %d participants are needed to draw.",
		textInfeasible: "No valid draw exists with the current exclusions: %d givers have no possible " +
			"recipient. Remove some exclusions and try again.",
		textRuleFixed:       "This rule can't be changed.",
		textRuleLocked:      "This rule can't change while the exchange is %s.",
		textUnknownRule:     "Unknown rule.",
		textQuestionsOff:    "The organizer turned off questions for this exchange.",
		textRateLimited:     "You reached the limit of 20 questions per day for this exchange.",
		textRephraseFailed:  "Could not rephrase your question right now. Your question was not sent; please try again.",
		textInvalidState:    "Not possible while the exchange is %s.",
		textInternal:        "Something went wrong. Reference %s.",
		textQuestionClosed:  "This question is closed and can no longer be answered.",
		textAlreadyAnswered: "You already answered this question.",
		textRulesState:      "State: %s. Participants: %d. You are %s.",
		textRoleOrganizer:   "the organizer",
		textRoleParticipant: "a participant",
		textRoleBoth:        "the organizer and a participant",
		textRoleNone:        "not in it",
		textStateOpen:       "open",
		textStateDrawn:      "drawn",
		textStateRevealed:   "revealed",
		textStateCancelled:  "cancelled",
		textListTitle:       "Exchanges in this server",
		textListEmpty:       "No exchanges yet. Create one with /ge create.",
		textListLine:        "- %s - %s - participants: %d - you are %s",

		textHelpLead:        "Every command also works as /chatisimo giftexchange <verb>.",
		textHelpCreate:      "/ge create - create an exchange and become its organizer",
		textHelpList:        "/ge list - list the exchanges of this server",
		textHelpJoin:        "/ge join - join an exchange",
		textHelpLeave:       "/ge leave - leave an exchange",
		textHelpRemove:      "/ge remove - remove a participant (organizer)",
		textHelpRules:       "/ge rules - see the rules, and change them if you organize",
		textHelpExclude:     "/ge exclude - stop one person from drawing another (organizer)",
		textHelpUnexclude:   "/ge unexclude - undo an exclusion (organizer)",
		textHelpRename:      "/ge rename - rename an exchange (organizer)",
		textHelpDraw:        "/ge draw - run the draw now (organizer)",
		textHelpRecipient:   "/ge recipient - see who you give a gift to",
		textHelpGiver:       "/ge giver - see who gave you a gift, after the reveal",
		textHelpAsk:         "/ge ask - ask your recipient an anonymous question",
		textHelpQuestions:   "/ge questions - see the questions you asked and received",
		textHelpPreferences: "/ge preferences - set or view your gift preferences for your giver",
		textHelpReveal:      "/ge reveal - reveal the givers now (organizer)",
		textHelpCancel:      "/ge cancel - cancel an exchange (organizer)",
		textHelpHelp:        "/ge help - show this help",

		textRuleDrawAt:            "Draw time",
		textRuleRevealAt:          "Reveal time",
		textRuleReminderLead:      "Reminder before each",
		textRuleDropoutPolicy:     "Dropout policy",
		textRuleAvoidLastRound:    "Avoid last round's pairs",
		textRuleExclusions:        "Exclusions",
		textRuleQuestionsEnabled:  "Anonymous questions",
		textRuleMinParticipants:   "Minimum participants",
		textRuleMaxParticipants:   "Maximum participants",
		textRuleNoSelfAssignment:  "Self-assignment",
		textRuleRedrawBelow:       "Redraw below",
		textRuleQuestionMaxLength: "Question length",
		textRuleQuestionsPerDay:   "Questions per day",

		textTimeValue:           "%s (%s)",
		textReminderOff:         "off",
		textOn:                  "on",
		textOff:                 "off",
		textNever:               "never",
		textDropoutPatch:        "patch",
		textDropoutRedraw:       "redraw",
		textRedrawBelowValue:    "%d participants",
		textQuestionLengthValue: "%d characters",

		textSuffixDefault: " (default)",
		textSuffixFixed:   " - fixed",
		textSuffixLocked:  " - locked",

		textRulesBadTimeReason:      "is not a time such as in 3d or 2026-12-20 18:00 (Santiago time)",
		textRulesTooLongReason:      "is longer than 64 characters",
		textRulesExclusionsHint:     "is edited with /ge exclude and /ge unexclude",
		textRulesNotEditable:        "cannot be changed here",
		textRulesNotAPersonReason:   "must be a person in this exchange",
		textRulesNotParticipantText: "giver and recipient must both be participants",

		textFieldNameName:             "name",
		textFieldNameText:             "text",
		textFieldNameLikes:            "likes",
		textFieldNameDislikes:         "dislikes",
		textFieldNameDetails:          "details",
		textFieldNameDrawAt:           "draw_at",
		textFieldNameRevealAt:         "reveal_at",
		textFieldNameReminderLead:     "reminder_lead",
		textFieldNameDropoutPolicy:    "dropout_policy",
		textFieldNameAvoidLastRound:   "avoid_last_round",
		textFieldNameQuestionsEnabled: "questions_enabled",
		textFieldNameRecipient:        "recipient",
		textFieldNameGiver:            "giver",
		textFieldNameRule:             "rule",
		textFieldNameExclusion:        "exclusion",
		textFieldNameExchangeID:       "exchange_id",
		textFieldNameQuestionID:       "question_id",
		textFieldNamePersonID:         "person_id",
		textFieldNameGroupID:          "group_id",
		textFieldNameGiverID:          "giver_id",
		textFieldNameRecipientID:      "recipient_id",
		textFieldNameIdentityProvider: "identity.provider",
		textFieldNameIdentitySubject:  "identity.subject",
		textFieldNameGroupProvider:    "group.provider",
		textFieldNameGroupSubject:     "group.subject",
		textFieldNameRegistry:         "registry",
		textFieldNameDepsPool:         "deps.Pool",
		textFieldNameDepsScheduler:    "deps.Scheduler",
		textFieldNameDepsClock:        "deps.Clock",
		textFieldNameDepsRandom:       "deps.Random",
		textFieldNameDepsNotifier:     "deps.Notifier",
		textFieldNameDepsRephraser:    "deps.Rephraser",
		textFieldNameDepsLogger:       "deps.Logger",
		textReasonRequired:            "is required",
		textReasonUnknown:             "is unknown",
		textReasonEmpty:               "is empty",
		textReasonTooLong:             "is too long",
		textReasonInvalidUTF8:         "is not valid UTF-8",
		textReasonInvalidText:         "is not valid text",
		textReasonControlChars:        "contains control characters",
		textReasonLineBreaks:          "contains line breaks",
		textReasonBidiChars:           "contains bidirectional control characters",
		textReasonNoVisibleChar:       "has no visible character",
		textReasonNotCanonicalUUID:    "is not a canonical uuid",
		textReasonZeroUUID:            "must not be the zero uuid",
		textReasonEditedViaExclusions: "is edited through the exclusion methods",
		textReasonInvalidInstant:      "is not unset or an RFC 3339 time with an offset",
		textReasonYearOutOfRange:      "is outside the years %d to %d",
		textReasonInvalidDuration:     "is not a duration such as 0, 36h, 90m, 3d or 1d12h",
		textReasonDurationOutOfRange:  "is outside 0 to 14 days in whole seconds",
		textReasonNotOneOf:            "is not one of %s",
		textReasonInvalidBool:         "is not on, off, true or false",
		textReasonMustBeFuture:        "must be in the future",
		textReasonBeforeRevealAt:      "must be before reveal_at",
		textReasonAfterDrawAt:         "must be after draw_at",
		textReasonSameAsGiver:         "must differ from the giver",

		textButtonJoin:    "Join",
		textButtonLeave:   "Leave",
		textButtonRules:   "Rules",
		textButtonConfirm: "Confirm",
		textButtonBack:    "Back",
		textButtonAnswer:  "Answer",

		textFieldDraw:   "Draw",
		textFieldReveal: "Reveal",

		textRulesTitle:        "Rules - %s",
		textParticipantsField: "Participants (%d)",
		textExclusionsField:   "Exclusions",
		textAndMore:           "and %d more",

		textQuestionModalTitle:     "Answer the question",
		textQuestionModalHint:      "Your answer",
		textQuestionListTitle:      "Your questions",
		textQuestionsAskedField:    "Questions you asked",
		textQuestionsReceivedField: "Questions you received",
		textNoneYet:                "None yet.",
		textClosedMarker:           " (closed)",

		textTitleRecipient:    "Your recipient",
		textTitleNewRecipient: "Your new recipient",
		textTitleGiver:        "Your giver",
		textTitlePreferences:  "Your gift preferences - %s",

		textFieldPreferences: "Gift preferences",
		textFieldLikes:       "Likes and hobbies",
		textFieldDislikes:    "Dislikes / avoid",
		textFieldDetails:     "Helpful details",

		textSelectPlaceholder:        "Change a rule",
		textModalLabel:               "New value",
		textModalTitle:               "Set %s",
		textModalPlaceholderTime:     "in 3d, in 2h30m, 2026-12-20 18:00 (Santiago time), or empty to clear",
		textModalPlaceholderReminder: "24h, 2d, 30m; 0 turns reminders off",

		textDMDrawn:              "The draw for %s (%s) is done.",
		textDMRedrawn:            "A participant left %s (%s) and the draw was redone.",
		textDMChanged:            "A participant left %s (%s). Your recipient changed.",
		textDMVoided:             "The draw for %s (%s) was undone: too few participants or no valid draw. It is open again.",
		textDMDrawFailed:         "The scheduled draw for %s (%s) failed: %s.",
		textDMDrawSoon:           "Reminder: the draw for %s (%s) is %s.",
		textDMRevealSoon:         "Reminder: %s (%s) is revealed %s.",
		textDMRevealed:           "%s (%s) is revealed.",
		textDMCancelled:          "%s (%s) was cancelled by the organizer.",
		textDMQuestion:           "Anonymous question from your giver in %s (%s):",
		textDMAnswer:             "Your recipient answered your question in %s (%s):",
		textDMPreferencesUpdated: "Your recipient updated their gift preferences in %s (%s).",
		textDMFooter:             "Use /ge recipient, /ge giver or /ge questions to see this again.",
		textReasonTooFew:         "too few participants",
		textReasonNoValid:        "no valid draw with the current exclusions",
		textUnknownServer:        "unknown server",
	}
}
