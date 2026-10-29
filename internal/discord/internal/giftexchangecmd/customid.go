package giftexchangecmd

import (
	"strings"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

// action names what a component or modal does. It is the first segment of a custom ID.
type action string

// The actions of the custom ID table. Every component and modal of the adapter carries one.
const (
	actionJoin             action = "join"
	actionLeave            action = "leave"
	actionRules            action = "rules"
	actionLeaveConfirm     action = "leave-confirm"
	actionDrawConfirm      action = "draw-confirm"
	actionRevealConfirm    action = "reveal-confirm"
	actionCancelConfirm    action = "cancel-confirm"
	actionRemoveConfirm    action = "remove-confirm"
	actionDismiss          action = "dismiss"
	actionRulePick         action = "rule-pick"
	actionRuleOption       action = "rule-option"
	actionRuleModal        action = "rule-modal"
	actionAnswer           action = "answer"
	actionAnswerModal      action = "answer-modal"
	actionPreferencesEdit  action = "preferences-edit"
	actionPreferencesModal action = "preferences-modal"
)

const (
	// customIDPrefix starts every custom ID. disgo's mux drops IDs without the leading slash.
	customIDPrefix = "/gx/"
	// maxCustomIDLength is Discord's limit on a custom ID.
	maxCustomIDLength = 100
	maxRuleNameLength = 32
	customIDSeparator = "/"
)

// idKind tells which ID the second segment of an action carries.
type idKind int

const (
	idExchange idKind = iota
	idQuestion
)

// argKind tells what the optional third segment of an action carries.
type argKind int

const (
	argNone argKind = iota
	argPerson
	argRule
)

// customIDShape is the segment layout of one action.
type customIDShape struct {
	id  idKind
	arg argKind
}

// customID is a parsed custom ID. Only the fields its action uses are set: Exchange for every
// action except answer and answer-modal, which carry Question; Person for remove-confirm; Rule
// for rule-option and rule-modal.
type customID struct {
	Action   action
	Exchange giftexchange.ExchangeID
	Person   giftexchange.PersonID
	Question giftexchange.QuestionID
	Rule     giftexchange.RuleName
}

// shapeOf returns the segment layout of an action, and false for a name outside the table.
func shapeOf(a action) (customIDShape, bool) {
	switch a {
	case actionJoin, actionLeave, actionRules,
		actionLeaveConfirm, actionDrawConfirm, actionRevealConfirm, actionCancelConfirm,
		actionDismiss, actionRulePick, actionPreferencesEdit, actionPreferencesModal:
		return customIDShape{id: idExchange, arg: argNone}, true
	case actionRemoveConfirm:
		return customIDShape{id: idExchange, arg: argPerson}, true
	case actionRuleOption, actionRuleModal:
		return customIDShape{id: idExchange, arg: argRule}, true
	case actionAnswer, actionAnswerModal:
		return customIDShape{id: idQuestion, arg: argNone}, true
	default:
		return customIDShape{}, false
	}
}

// customIDPattern returns the mux pattern that matches every custom ID of the action. Handlers
// ignore the mux variables and read the IDs through parseCustomID.
func customIDPattern(a action) string {
	shape, known := shapeOf(a)
	if !known {
		panic("giftexchangecmd: unknown action " + string(a))
	}

	pattern := customIDPrefix + string(a) + "/{x}"
	if shape.arg != argNone {
		pattern += "/{y}"
	}

	return pattern
}

// formatCustomID builds the custom ID of c. It panics on an action outside the table, which is
// a programming error.
func formatCustomID(c customID) string {
	shape, known := shapeOf(c.Action)
	if !known {
		panic("giftexchangecmd: unknown action " + string(c.Action))
	}

	var b strings.Builder
	b.WriteString(customIDPrefix)
	b.WriteString(string(c.Action))
	b.WriteString(customIDSeparator)

	if shape.id == idQuestion {
		b.WriteString(c.Question.String())
	} else {
		b.WriteString(c.Exchange.String())
	}

	switch shape.arg {
	case argPerson:
		b.WriteString(customIDSeparator + c.Person.String())
	case argRule:
		b.WriteString(customIDSeparator + string(c.Rule))
	case argNone:
	}

	formatted := b.String()
	return formatted
}

// parseCustomID reads a custom ID. It refuses anything formatCustomID could not have built:
// a wrong prefix, an unknown action, a wrong number of segments, an ID that is not a lower
// case canonical uuid, a malformed rule name, and anything over 100 characters.
func parseCustomID(raw string) (customID, error) {
	var zero customID

	isTooLong := len(raw) > maxCustomIDLength
	if isTooLong || !strings.HasPrefix(raw, customIDPrefix) {
		return zero, errBadCustomID
	}

	segments := strings.Split(strings.TrimPrefix(raw, customIDPrefix), customIDSeparator)
	a := action(segments[0])
	shape, known := shapeOf(a)
	if !known {
		return zero, errBadCustomID
	}

	wantSegments := 2
	if shape.arg != argNone {
		wantSegments = 3
	}
	if len(segments) != wantSegments {
		return zero, errBadCustomID
	}

	parsed := customID{Action: a}
	err := parseCustomIDParts(&parsed, shape, segments[1:])
	if err != nil {
		return zero, err
	}

	return parsed, nil
}

func parseCustomIDParts(c *customID, shape customIDShape, parts []string) error {
	if shape.id == idQuestion {
		question, err := giftexchange.ParseQuestionID(parts[0])
		isCanonical := err == nil && question.String() == parts[0]
		if !isCanonical {
			return errBadCustomID
		}
		c.Question = question
	} else {
		exchange, err := giftexchange.ParseExchangeID(parts[0])
		isCanonical := err == nil && exchange.String() == parts[0]
		if !isCanonical {
			return errBadCustomID
		}
		c.Exchange = exchange
	}

	switch shape.arg {
	case argPerson:
		person, err := giftexchange.ParsePersonID(parts[1])
		isCanonical := err == nil && person.String() == parts[1]
		if !isCanonical {
			return errBadCustomID
		}
		c.Person = person
	case argRule:
		if !isRuleToken(parts[1]) {
			return errBadCustomID
		}
		c.Rule = giftexchange.RuleName(parts[1])
	case argNone:
	}

	return nil
}

// isRuleToken reports whether s matches ^[a-z_]{1,32}$.
func isRuleToken(s string) bool {
	if len(s) == 0 || len(s) > maxRuleNameLength {
		return false
	}

	for i := 0; i < len(s); i++ {
		c := s[i]
		isAllowed := (c >= 'a' && c <= 'z') || c == '_'
		if !isAllowed {
			return false
		}
	}

	return true
}
