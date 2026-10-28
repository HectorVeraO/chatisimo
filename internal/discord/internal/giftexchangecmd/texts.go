package giftexchangecmd

import (
	"github.com/HectorVeraO/chatisimo/internal/platform/i18n"
)

// Locale identifies a BCP 47 language catalog of the Discord adapter.
type Locale = i18n.Locale

// Supported locales of the Discord adapter. LocaleENUS is the default and fallback.
const (
	LocaleENUS Locale = i18n.LocaleENUS
	LocaleESCL Locale = i18n.LocaleESCL
)

// LocaleFromDiscord maps a Discord locale tag (e.g. "es-419", "es-CL", "es-ES", "en-US")
// to a supported Locale, falling back to LocaleENUS.
func LocaleFromDiscord(raw string) Locale {
	return i18n.MatchLocale(raw)
}

// ResolveLocale picks the best supported Locale from candidates in priority order, preferring
// a non-default supported locale when an earlier candidate is the default fallback.
func ResolveLocale(candidates ...string) Locale {
	return i18n.ResolveLocale(candidates...)
}

// MsgID identifies one user-facing message template in a Catalog.
type MsgID string

// Catalog holds the user-facing message templates for one Locale.
type Catalog struct {
	inner     i18n.Catalog
	hasBundle bool
}

// CatalogFor returns the message catalog for loc, falling back to LocaleENUS when loc is
// unsupported or empty.
func CatalogFor(loc Locale) Catalog {
	return Catalog{inner: defaultBundle().Catalog(loc), hasBundle: true}
}

// Locale returns the resolved canonical locale of the catalog.
func (c Catalog) Locale() Locale {
	if !c.hasBundle {
		return LocaleENUS
	}

	return c.inner.Locale()
}

// Get returns the raw message template for id, falling back to LocaleENUS if missing.
func (c Catalog) Get(id MsgID) string {
	if !c.hasBundle {
		return defaultBundle().Catalog(LocaleENUS).Get(string(id))
	}

	return c.inner.Get(string(id))
}

// Format formats the message template for id with args.
func (c Catalog) Format(id MsgID, args ...any) string {
	if !c.hasBundle {
		return defaultBundle().Catalog(LocaleENUS).Format(string(id), args...)
	}

	return c.inner.Format(string(id), args...)
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

// Slash command and option description keys used to populate Discord DescriptionLocalizations.
const (
	textCmdChatisimoDesc           MsgID = "textCmdChatisimoDesc"
	textCmdGiftexchangeDesc        MsgID = "textCmdGiftexchangeDesc"
	textCmdCreateDesc              MsgID = "textCmdCreateDesc"
	textCmdListDesc                MsgID = "textCmdListDesc"
	textCmdJoinDesc                MsgID = "textCmdJoinDesc"
	textCmdLeaveDesc               MsgID = "textCmdLeaveDesc"
	textCmdRemoveDesc              MsgID = "textCmdRemoveDesc"
	textCmdRulesDesc               MsgID = "textCmdRulesDesc"
	textCmdExcludeDesc             MsgID = "textCmdExcludeDesc"
	textCmdUnexcludeDesc           MsgID = "textCmdUnexcludeDesc"
	textCmdRenameDesc              MsgID = "textCmdRenameDesc"
	textCmdDrawDesc                MsgID = "textCmdDrawDesc"
	textCmdRecipientDesc           MsgID = "textCmdRecipientDesc"
	textCmdGiverDesc               MsgID = "textCmdGiverDesc"
	textCmdAskDesc                 MsgID = "textCmdAskDesc"
	textCmdQuestionsDesc           MsgID = "textCmdQuestionsDesc"
	textCmdPreferencesDesc         MsgID = "textCmdPreferencesDesc"
	textCmdRevealDesc              MsgID = "textCmdRevealDesc"
	textCmdCancelDesc              MsgID = "textCmdCancelDesc"
	textCmdHelpDesc                MsgID = "textCmdHelpDesc"
	textOptExchangeDesc            MsgID = "textOptExchangeDesc"
	textOptCreateNameDesc          MsgID = "textOptCreateNameDesc"
	textOptCreateDrawAtDesc        MsgID = "textOptCreateDrawAtDesc"
	textOptCreateRevealAtDesc      MsgID = "textOptCreateRevealAtDesc"
	textOptRemoveParticipantDesc   MsgID = "textOptRemoveParticipantDesc"
	textOptExcludeGiverDesc        MsgID = "textOptExcludeGiverDesc"
	textOptExcludeRecipientDesc    MsgID = "textOptExcludeRecipientDesc"
	textOptExcludeBothDesc         MsgID = "textOptExcludeBothDesc"
	textOptUnexcludeGiverDesc      MsgID = "textOptUnexcludeGiverDesc"
	textOptUnexcludeRecipientDesc  MsgID = "textOptUnexcludeRecipientDesc"
	textOptRenameNameDesc          MsgID = "textOptRenameNameDesc"
	textOptAskQuestionDesc         MsgID = "textOptAskQuestionDesc"
	textOptPreferencesLikesDesc    MsgID = "textOptPreferencesLikesDesc"
	textOptPreferencesDislikesDesc MsgID = "textOptPreferencesDislikesDesc"
	textOptPreferencesDetailsDesc  MsgID = "textOptPreferencesDetailsDesc"
	textOptPreferencesClearDesc    MsgID = "textOptPreferencesClearDesc"
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
