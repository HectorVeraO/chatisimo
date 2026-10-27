package giftexchangecmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/HectorVeraO/chatisimo/internal/discord/internal/render"
	"github.com/HectorVeraO/chatisimo/internal/discord/internal/router"
	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

// Errors the handlers return for what the caller got wrong, before or instead of calling
// the module. MapError turns each into a reply.
var (
	errOwnRecipient = errors.New("giftexchangecmd: an organizer cannot remove their own recipient")
	errBadTime      = errors.New("giftexchangecmd: unreadable time")
	errNoMatch      = errors.New("giftexchangecmd: no exchange matches")
	errBadCustomID  = errors.New("giftexchangecmd: unreadable custom ID")
	errGuildOnly    = errors.New("giftexchangecmd: command works only in a server")
	errNoCandidates = errors.New("giftexchangecmd: no exchange to choose from")
)

// chooseExchangeError reports that the caller is in several exchanges and must pick one.
// Names holds the names of the candidates.
type chooseExchangeError struct {
	Names []string
}

func (e *chooseExchangeError) Error() string {
	text := fmt.Sprintf("giftexchangecmd: %d exchanges to choose from", len(e.Names))
	return text
}

const (
	// maxListedLines bounds the lines of a reply that lists names or problems.
	maxListedLines = 20
	// maxLeafDepth bounds the walk over nested joined errors.
	maxLeafDepth = 8
	// maxReplyRunes is below Discord's 2000 character limit on message content.
	maxReplyRunes = 1900

	outcomeRejectedPrefix = "rejected:"
	outcomeError          = "error"
)

func rejected(reason, text string) router.Rejection {
	return router.Rejection{Text: text, Outcome: outcomeRejectedPrefix + reason}
}

// MapError turns an error a handler returned into the English reply and the log outcome.
// The first match wins. requestID is the reference shown for unexpected failures.
func MapError(requestID string, err error) router.Rejection {
	return MapErrorIn(LocaleEN, requestID, err)
}

// MapErrorIn turns an error a handler returned into the reply in loc (falling back to
// LocaleEN) and the log outcome.
func MapErrorIn(loc Locale, requestID string, err error) router.Rejection {
	c := CatalogFor(loc)
	var (
		infeasible *giftexchange.InfeasibleError
		locked     *giftexchange.RuleLockedError
		state      *giftexchange.StateError
		choose     *chooseExchangeError
	)

	switch {
	case errors.Is(err, giftexchange.ErrNotFound):
		return rejected("not_found", c.Get(textNotFound))
	case errors.Is(err, errOwnRecipient):
		return rejected("forbidden", c.Get(textOwnRecipient))
	case errors.Is(err, giftexchange.ErrForbidden):
		return rejected("forbidden", c.Get(textForbidden))
	// ParseRuleName joins ErrUnknownRule with ErrInvalidInput, so this case must come first.
	case errors.Is(err, giftexchange.ErrUnknownRule):
		return rejected("rule", c.Get(textUnknownRule))
	case errors.Is(err, giftexchange.ErrInvalidInput):
		return rejected("invalid_input", invalidInputText(c, err))
	case errors.Is(err, giftexchange.ErrAlreadyParticipant):
		return rejected("already_participant", c.Get(textAlreadyParticipant))
	case errors.Is(err, giftexchange.ErrNotParticipant):
		return rejected("not_participant", c.Get(textNotParticipant))
	case errors.Is(err, giftexchange.ErrExchangeFull):
		return rejected("full", c.Format(textFull, giftexchange.MaxParticipants))
	case errors.Is(err, giftexchange.ErrTooFewParticipants):
		return rejected("too_few", c.Format(textTooFew, giftexchange.MinParticipants))
	case errors.As(err, &infeasible):
		return rejected("infeasible", c.Format(textInfeasible, infeasible.Stranded))
	case errors.Is(err, giftexchange.ErrRuleFixed):
		return rejected("rule", c.Get(textRuleFixed))
	case errors.As(err, &locked):
		return rejected("rule", c.Format(textRuleLocked, stateText(c, locked.State)))
	case errors.Is(err, giftexchange.ErrQuestionsDisabled):
		return rejected("questions_off", c.Get(textQuestionsOff))
	case errors.Is(err, giftexchange.ErrRateLimited):
		return rejected("rate_limited", c.Get(textRateLimited))
	case errors.Is(err, giftexchange.ErrRephraseUnavailable):
		return rejected("rephrase_unavailable", c.Get(textRephraseFailed))
	case errors.Is(err, giftexchange.ErrQuestionClosed):
		return rejected("question", c.Get(textQuestionClosed))
	case errors.Is(err, giftexchange.ErrAlreadyAnswered):
		return rejected("question", c.Get(textAlreadyAnswered))
	case errors.As(err, &state):
		return rejected("state", c.Format(textInvalidState, stateText(c, state.State)))
	case errors.Is(err, errBadTime):
		return rejected("bad_input", c.Get(textBadTime))
	case errors.Is(err, errNoMatch):
		return rejected("bad_input", c.Get(textNoMatch))
	case errors.Is(err, errBadCustomID):
		return rejected("bad_input", c.Get(textNotFound))
	case errors.Is(err, errGuildOnly):
		return rejected("bad_input", c.Get(textGuildOnly))
	case errors.Is(err, errNoCandidates):
		return rejected("bad_input", c.Get(textNoCandidates))
	case errors.As(err, &choose):
		return rejected("bad_input", chooseExchangeText(c, choose.Names))
	}

	internal := router.Rejection{
		Text:     c.Format(textInternal, requestID),
		Outcome:  outcomeError,
		Internal: true,
	}
	return internal
}

// invalidInputText lists every invalid field under the Not saved line. Fields and reasons are
// fixed phrases of the module, never user input.
func invalidInputText(c Catalog, err error) string {
	var lines []string
	collectLeaves(c, err, &lines, 0)

	text := listText(c, c.Get(textNotSaved), lines)
	return text
}

func collectLeaves(c Catalog, err error, lines *[]string, depth int) {
	if depth > maxLeafDepth {
		return
	}

	joined, isJoined := err.(interface{ Unwrap() []error })
	if isJoined {
		for _, child := range joined.Unwrap() {
			collectLeaves(c, child, lines, depth+1)
		}
		return
	}

	var input *giftexchange.InputError
	if errors.As(err, &input) {
		*lines = append(*lines, render.Text(formatInputProblem(c, input)))
		return
	}

	*lines = append(*lines, c.Get(textInvalidInput))
}

func formatInputProblem(c Catalog, input *giftexchange.InputError) string {
	field := inputFieldLabel(c, input.Field)
	reason := inputReasonText(c, input)
	return field + " " + reason
}

func inputFieldLabel(c Catalog, field string) string {
	if id, ok := inputFieldMsgID(field); ok {
		return c.Get(id)
	}
	return field
}

func inputFieldMsgID(field string) (MsgID, bool) {
	switch field {
	case "name":
		return textFieldNameName, true
	case "text":
		return textFieldNameText, true
	case "likes":
		return textFieldNameLikes, true
	case "dislikes":
		return textFieldNameDislikes, true
	case "details":
		return textFieldNameDetails, true
	case "draw_at":
		return textFieldNameDrawAt, true
	case "reveal_at":
		return textFieldNameRevealAt, true
	case "reminder_lead":
		return textFieldNameReminderLead, true
	case "dropout_policy":
		return textFieldNameDropoutPolicy, true
	case "avoid_last_round":
		return textFieldNameAvoidLastRound, true
	case "questions_enabled":
		return textFieldNameQuestionsEnabled, true
	case "recipient":
		return textFieldNameRecipient, true
	case "giver":
		return textFieldNameGiver, true
	case "rule":
		return textFieldNameRule, true
	case "exclusion":
		return textFieldNameExclusion, true
	case "exchange_id":
		return textFieldNameExchangeID, true
	case "question_id":
		return textFieldNameQuestionID, true
	case "person_id":
		return textFieldNamePersonID, true
	case "group_id":
		return textFieldNameGroupID, true
	case "giver_id":
		return textFieldNameGiverID, true
	case "recipient_id":
		return textFieldNameRecipientID, true
	case "identity.provider":
		return textFieldNameIdentityProvider, true
	case "identity.subject":
		return textFieldNameIdentitySubject, true
	case "group.provider":
		return textFieldNameGroupProvider, true
	case "group.subject":
		return textFieldNameGroupSubject, true
	case "registry":
		return textFieldNameRegistry, true
	case "deps.Pool":
		return textFieldNameDepsPool, true
	case "deps.Scheduler":
		return textFieldNameDepsScheduler, true
	case "deps.Clock":
		return textFieldNameDepsClock, true
	case "deps.Random":
		return textFieldNameDepsRandom, true
	case "deps.Notifier":
		return textFieldNameDepsNotifier, true
	case "deps.Rephraser":
		return textFieldNameDepsRephraser, true
	case "deps.Logger":
		return textFieldNameDepsLogger, true
	default:
		return "", false
	}
}

func inputReasonText(c Catalog, input *giftexchange.InputError) string {
	switch input.Code {
	case giftexchange.InputCodeRequired:
		return c.Get(textReasonRequired)
	case giftexchange.InputCodeUnknown:
		return c.Get(textReasonUnknown)
	case giftexchange.InputCodeEmpty:
		return c.Get(textReasonEmpty)
	case giftexchange.InputCodeTooLong:
		return c.Get(textReasonTooLong)
	case giftexchange.InputCodeInvalidUTF8:
		return c.Get(textReasonInvalidUTF8)
	case giftexchange.InputCodeInvalidText:
		return c.Get(textReasonInvalidText)
	case giftexchange.InputCodeControlChars:
		return c.Get(textReasonControlChars)
	case giftexchange.InputCodeLineBreaks:
		return c.Get(textReasonLineBreaks)
	case giftexchange.InputCodeBidiChars:
		return c.Get(textReasonBidiChars)
	case giftexchange.InputCodeNoVisibleChar:
		return c.Get(textReasonNoVisibleChar)
	case giftexchange.InputCodeNotCanonicalUUID:
		return c.Get(textReasonNotCanonicalUUID)
	case giftexchange.InputCodeZeroUUID:
		return c.Get(textReasonZeroUUID)
	case giftexchange.InputCodeEditedViaExclusions:
		return c.Get(textReasonEditedViaExclusions)
	case giftexchange.InputCodeInvalidInstant:
		return c.Get(textReasonInvalidInstant)
	case giftexchange.InputCodeYearOutOfRange:
		return c.Format(textReasonYearOutOfRange, input.Min, input.Max)
	case giftexchange.InputCodeInvalidDuration:
		return c.Get(textReasonInvalidDuration)
	case giftexchange.InputCodeDurationOutOfRange:
		return c.Get(textReasonDurationOutOfRange)
	case giftexchange.InputCodeNotOneOf:
		return c.Format(textReasonNotOneOf, strings.Join(input.Allowed, ", "))
	case giftexchange.InputCodeInvalidBool:
		return c.Get(textReasonInvalidBool)
	case giftexchange.InputCodeMustBeFuture:
		return c.Get(textReasonMustBeFuture)
	case giftexchange.InputCodeBeforeRevealAt:
		return c.Get(textReasonBeforeRevealAt)
	case giftexchange.InputCodeAfterDrawAt:
		return c.Get(textReasonAfterDrawAt)
	case giftexchange.InputCodeSameAsGiver:
		return c.Get(textReasonSameAsGiver)
	default:
		if input.Reason != "" {
			return input.Reason
		}
		return c.Get(textInvalidInput)
	}
}

func chooseExchangeText(c Catalog, names []string) string {
	lines := make([]string, 0, len(names))
	for _, name := range names {
		lines = append(lines, render.Text(name))
	}

	text := listText(c, c.Get(textChooseExchange), lines)
	return text
}

// listText joins a lead line and bullet lines, shows at most maxListedLines of them and
// keeps the whole under Discord's limit.
func listText(c Catalog, lead string, lines []string) string {
	var b strings.Builder
	b.WriteString(lead)

	for i, line := range lines {
		if i == maxListedLines {
			b.WriteString("\n" + c.Format(textAndMore, len(lines)-maxListedLines))
			break
		}
		b.WriteString("\n" + c.Format(textNotSavedLine, line))
	}

	text := render.Truncate(b.String(), maxReplyRunes, "...")
	return text
}

// GuildNameOrUnknown returns the guild name, or the English text shown when the Gateway has
// not reported the guild.
func GuildNameOrUnknown(name string) string {
	return GuildNameOrUnknownIn(LocaleEN, name)
}

// GuildNameOrUnknownIn returns the guild name, or the text in loc shown when the Gateway has
// not reported the guild.
func GuildNameOrUnknownIn(loc Locale, name string) string {
	if name == "" {
		return CatalogFor(loc).Get(textUnknownServer)
	}

	return name
}
