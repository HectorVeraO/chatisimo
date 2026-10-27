package giftexchangecmd

// Every user-facing text of the Discord adapter. Placeholders: the exchange name arrives
// already escaped and in bold (render.Bold), numbers as %d, states and roles as %s.

const (
	textGuildOnly      = "This command works only in a server."
	textNoCandidates   = "You have no exchange where you can do this."
	textChooseExchange = "You are in several exchanges. Choose one with the exchange option:"
	textNoMatch        = "No exchange matches that name. Pick one from the list."
	textCreated        = "You created %s and you organize it. You are not a participant unless you join. " +
		"Configure it with /ge rules."
	textCardBody           = "Organized by %s. Join with the button or /ge join."
	textManual             = "not set (organizer runs it)"
	textJoined             = "You joined %s."
	textLeft               = "You left %s."
	textConfirmLeave       = "%s is already drawn. Leave anyway? Your giver may get a new recipient."
	textConfirmRemove      = "%s is already drawn. Remove %s? A giver may get a new recipient."
	textRemoved            = "Removed %s from %s."
	textOutcomeRemoved     = "Removed."
	textOutcomePatched     = "Removed. One giver got a new recipient by DM."
	textOutcomeRedrawn     = "Removed. The draw was redone and every giver got a DM."
	textOutcomeReopened    = "Removed. Too few participants or no valid draw remained, so the draw was undone and %s is open again."
	textOwnRecipient       = "You can't remove your own recipient. They can leave, or you can cancel the exchange."
	textExcluded           = "Exclusion added: %s will not draw %s."
	textExcludedBoth       = " The reverse is excluded too."
	textUnexcluded         = "Exclusion removed: %s may draw %s again."
	textRenamed            = "Renamed to %s."
	textConfirmDraw        = "Draw %s now with %d participants? This can't be undone."
	textDrawn              = "Draw done for %s."
	textDrawnPublic        = "The draw for %s is done. Each giver got a DM with their recipient, or can use /ge recipient."
	textConfirmReveal      = "Reveal %s now? Each participant will learn their giver."
	textRevealed           = "%s is revealed."
	textRevealedPublic     = "%s is revealed. Each participant got a DM with their giver, or can use /ge giver."
	textConfirmCancel      = "Cancel %s? This can't be undone."
	textCancelled          = "%s is cancelled."
	textCancelledPublic    = "%s was cancelled by the organizer."
	textNothingChanged     = "Nothing changed."
	textAsked              = "Your question was sent anonymously to your recipient in %s."
	textYourAnswer         = "Your answer:"
	textNotSaved           = "Not saved:"
	textNotSavedLine       = "- %s"
	textBadTime            = "Use in 3d, in 2h30m, YYYY-MM-DD HH:MM (Santiago time) or an RFC 3339 time."
	textNotFound           = "That exchange does not exist here."
	textForbidden          = "You are not allowed to do that in this exchange."
	textAlreadyParticipant = "You are already in this exchange."
	textNotParticipant     = "You are not in this exchange."
	textFull               = "This exchange is full (%d participants)."
	textTooFew             = "At least %d participants are needed to draw."
	textInfeasible         = "No valid draw exists with the current exclusions: %d givers have no possible " +
		"recipient. Remove some exclusions and try again."
	textRuleFixed       = "This rule can't be changed."
	textRuleLocked      = "This rule can't change while the exchange is %s."
	textUnknownRule     = "Unknown rule."
	textQuestionsOff    = "The organizer turned off questions for this exchange."
	textRateLimited     = "You reached the limit of 20 questions per day for this exchange."
	textRephraseFailed  = "Could not rephrase your question right now. Your question was not sent; please try again."
	textInvalidState    = "Not possible while the exchange is %s."
	textInternal        = "Something went wrong. Reference %s."
	textQuestionClosed  = "This question is closed and can no longer be answered."
	textAlreadyAnswered = "You already answered this question."
	textRulesState      = "State: %s. Participants: %d. You are %s."
	textRoleOrganizer   = "the organizer"
	textRoleParticipant = "a participant"
	textRoleBoth        = "the organizer and a participant"
	textRoleNone        = "not in it"
)

// The help text: a lead line, then one line per verb.
const (
	textHelpLead = "Every command also works as /chatisimo giftexchange <verb>."

	textHelpCreate    = "/ge create - create an exchange and become its organizer"
	textHelpList      = "/ge list - list the exchanges of this server"
	textHelpJoin      = "/ge join - join an exchange"
	textHelpLeave     = "/ge leave - leave an exchange"
	textHelpRemove    = "/ge remove - remove a participant (organizer)"
	textHelpRules     = "/ge rules - see the rules, and change them if you organize"
	textHelpExclude   = "/ge exclude - stop one person from drawing another (organizer)"
	textHelpUnexclude = "/ge unexclude - undo an exclusion (organizer)"
	textHelpRename    = "/ge rename - rename an exchange (organizer)"
	textHelpDraw      = "/ge draw - run the draw now (organizer)"
	textHelpRecipient = "/ge recipient - see who you give a gift to"
	textHelpGiver     = "/ge giver - see who gave you a gift, after the reveal"
	textHelpAsk       = "/ge ask - ask your recipient an anonymous question"
	textHelpQuestions = "/ge questions - see the questions you asked and received"
	textHelpReveal    = "/ge reveal - reveal the givers now (organizer)"
	textHelpCancel    = "/ge cancel - cancel an exchange (organizer)"
	textHelpHelp      = "/ge help - show this help"
)

// Rule labels and the values the rules view shows.
const (
	textRuleDrawAt            = "Draw time"
	textRuleRevealAt          = "Reveal time"
	textRuleReminderLead      = "Reminder before each"
	textRuleDropoutPolicy     = "Dropout policy"
	textRuleAvoidLastRound    = "Avoid last round's pairs"
	textRuleExclusions        = "Exclusions"
	textRuleQuestionsEnabled  = "Anonymous questions"
	textRuleMinParticipants   = "Minimum participants"
	textRuleMaxParticipants   = "Maximum participants"
	textRuleNoSelfAssignment  = "Self-assignment"
	textRuleRedrawBelow       = "Redraw below"
	textRuleQuestionMaxLength = "Question length"
	textRuleQuestionsPerDay   = "Questions per day"

	textTimeValue           = "%s (%s)"
	textReminderOff         = "off"
	textOn                  = "on"
	textOff                 = "off"
	textNever               = "never"
	textRedrawBelowValue    = "%d participants"
	textQuestionLengthValue = "%d characters"

	textSuffixDefault = " (default)"
	textSuffixFixed   = " - fixed"
	textSuffixLocked  = " - locked"
)

// Buttons, card fields, embed titles and the other labels of the interface.
const (
	textButtonJoin    = "Join"
	textButtonLeave   = "Leave"
	textButtonRules   = "Rules"
	textButtonConfirm = "Confirm"
	textButtonBack    = "Back"
	textButtonAnswer  = "Answer"

	textFieldDraw   = "Draw"
	textFieldReveal = "Reveal"

	textRulesTitle        = "Rules - %s"
	textParticipantsField = "Participants (%d)"
	textExclusionsField   = "Exclusions"
	textAndMore           = "and %d more"

	textQuestionsAskedField    = "Questions you asked"
	textQuestionsReceivedField = "Questions you received"
	textNoneYet                = "None yet."
	textClosedMarker           = " (closed)"

	textTitleRecipient    = "Your recipient"
	textTitleNewRecipient = "Your new recipient"
	textTitleGiver        = "Your giver"

	textSelectPlaceholder        = "Change a rule"
	textModalLabel               = "New value"
	textModalTitle               = "Set %s"
	textModalPlaceholderTime     = "in 3d, in 2h30m, 2026-12-20 18:00 (Santiago time), or empty to clear"
	textModalPlaceholderReminder = "24h, 2d, 30m; 0 turns reminders off"
)

// The texts of the direct messages. The first two verbs of each are the exchange name and the
// server name.
const (
	textDMDrawn       = "The draw for %s (%s) is done."
	textDMRedrawn     = "A participant left %s (%s) and the draw was redone."
	textDMChanged     = "A participant left %s (%s). Your recipient changed."
	textDMVoided      = "The draw for %s (%s) was undone: too few participants or no valid draw. It is open again."
	textDMDrawFailed  = "The scheduled draw for %s (%s) failed: %s."
	textDMDrawSoon    = "Reminder: the draw for %s (%s) is %s."
	textDMRevealSoon  = "Reminder: %s (%s) is revealed %s."
	textDMRevealed    = "%s (%s) is revealed."
	textDMCancelled   = "%s (%s) was cancelled by the organizer."
	textDMQuestion    = "Anonymous question from your giver in %s (%s):"
	textDMAnswer      = "Your recipient answered your question in %s (%s):"
	textDMFooter      = "Use /ge recipient, /ge giver or /ge questions to see this again."
	textReasonTooFew  = "too few participants"
	textReasonNoValid = "no valid draw with the current exclusions"
	textUnknownServer = "unknown server"

	// customIDAnswerPrefix starts the custom ID of the Answer button; the question ID follows.
	customIDAnswerPrefix = "/gx/answer/"
)
