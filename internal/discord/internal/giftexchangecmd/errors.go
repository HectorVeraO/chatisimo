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

// MapError turns an error a handler returned into the reply and the log outcome. The first
// match wins. requestID is the reference shown for unexpected failures.
func MapError(requestID string, err error) router.Rejection {
	var (
		infeasible *giftexchange.InfeasibleError
		locked     *giftexchange.RuleLockedError
		state      *giftexchange.StateError
		choose     *chooseExchangeError
	)

	switch {
	case errors.Is(err, giftexchange.ErrNotFound):
		return rejected("not_found", textNotFound)
	case errors.Is(err, errOwnRecipient):
		return rejected("forbidden", textOwnRecipient)
	case errors.Is(err, giftexchange.ErrForbidden):
		return rejected("forbidden", textForbidden)
	// ParseRuleName joins ErrUnknownRule with ErrInvalidInput, so this case must come first.
	case errors.Is(err, giftexchange.ErrUnknownRule):
		return rejected("rule", textUnknownRule)
	case errors.Is(err, giftexchange.ErrInvalidInput):
		return rejected("invalid_input", invalidInputText(err))
	case errors.Is(err, giftexchange.ErrAlreadyParticipant):
		return rejected("already_participant", textAlreadyParticipant)
	case errors.Is(err, giftexchange.ErrNotParticipant):
		return rejected("not_participant", textNotParticipant)
	case errors.Is(err, giftexchange.ErrExchangeFull):
		return rejected("full", fmt.Sprintf(textFull, giftexchange.MaxParticipants))
	case errors.Is(err, giftexchange.ErrTooFewParticipants):
		return rejected("too_few", fmt.Sprintf(textTooFew, giftexchange.MinParticipants))
	case errors.As(err, &infeasible):
		return rejected("infeasible", fmt.Sprintf(textInfeasible, infeasible.Stranded))
	case errors.Is(err, giftexchange.ErrRuleFixed):
		return rejected("rule", textRuleFixed)
	case errors.As(err, &locked):
		return rejected("rule", fmt.Sprintf(textRuleLocked, locked.State))
	case errors.Is(err, giftexchange.ErrQuestionsDisabled):
		return rejected("questions_off", textQuestionsOff)
	case errors.Is(err, giftexchange.ErrRateLimited):
		return rejected("rate_limited", textRateLimited)
	case errors.Is(err, giftexchange.ErrRephraseUnavailable):
		return rejected("rephrase_unavailable", textRephraseFailed)
	case errors.Is(err, giftexchange.ErrQuestionClosed):
		return rejected("question", textQuestionClosed)
	case errors.Is(err, giftexchange.ErrAlreadyAnswered):
		return rejected("question", textAlreadyAnswered)
	case errors.As(err, &state):
		return rejected("state", fmt.Sprintf(textInvalidState, state.State))
	case errors.Is(err, errBadTime):
		return rejected("bad_input", textBadTime)
	case errors.Is(err, errNoMatch):
		return rejected("bad_input", textNoMatch)
	case errors.Is(err, errBadCustomID):
		return rejected("bad_input", textNotFound)
	case errors.Is(err, errGuildOnly):
		return rejected("bad_input", textGuildOnly)
	case errors.Is(err, errNoCandidates):
		return rejected("bad_input", textNoCandidates)
	case errors.As(err, &choose):
		return rejected("bad_input", chooseExchangeText(choose.Names))
	}

	internal := router.Rejection{
		Text:     fmt.Sprintf(textInternal, requestID),
		Outcome:  outcomeError,
		Internal: true,
	}
	return internal
}

// invalidInputText lists every invalid field under the Not saved line. Fields and reasons are
// fixed phrases of the module, never user input.
func invalidInputText(err error) string {
	var lines []string
	collectLeaves(err, &lines, 0)

	text := listText(textNotSaved, lines)
	return text
}

func collectLeaves(err error, lines *[]string, depth int) {
	if depth > maxLeafDepth {
		return
	}

	joined, isJoined := err.(interface{ Unwrap() []error })
	if isJoined {
		for _, child := range joined.Unwrap() {
			collectLeaves(child, lines, depth+1)
		}
		return
	}

	var input *giftexchange.InputError
	if errors.As(err, &input) {
		*lines = append(*lines, render.Text(input.Field+" "+input.Reason))
		return
	}

	*lines = append(*lines, "invalid input")
}

func chooseExchangeText(names []string) string {
	lines := make([]string, 0, len(names))
	for _, name := range names {
		lines = append(lines, render.Text(name))
	}

	text := listText(textChooseExchange, lines)
	return text
}

// listText joins a lead line and bullet lines, shows at most maxListedLines of them and
// keeps the whole under Discord's limit.
func listText(lead string, lines []string) string {
	var b strings.Builder
	b.WriteString(lead)

	for i, line := range lines {
		if i == maxListedLines {
			b.WriteString("\n" + fmt.Sprintf(textAndMore, len(lines)-maxListedLines))
			break
		}
		b.WriteString("\n" + fmt.Sprintf(textNotSavedLine, line))
	}

	text := render.Truncate(b.String(), maxReplyRunes, "...")
	return text
}

// GuildNameOrUnknown returns the guild name, or the text shown when the Gateway has not
// reported the guild.
func GuildNameOrUnknown(name string) string {
	if name == "" {
		return textUnknownServer
	}

	return name
}
