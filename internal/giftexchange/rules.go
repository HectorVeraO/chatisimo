package giftexchange

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
)

// RuleName identifies one rule. Values are stable (clients put them in custom IDs).
type RuleName string

// The rule names, in the order Rules returns them.
const (
	RuleDrawAt            RuleName = "draw_at"
	RuleRevealAt          RuleName = "reveal_at"
	RuleReminderLead      RuleName = "reminder_lead"
	RuleDropoutPolicy     RuleName = "dropout_policy"
	RuleAvoidLastRound    RuleName = "avoid_last_round"
	RuleExclusions        RuleName = "exclusions"
	RuleQuestionsEnabled  RuleName = "questions_enabled"
	RuleMinParticipants   RuleName = "min_participants"
	RuleMaxParticipants   RuleName = "max_participants"
	RuleNoSelfAssignment  RuleName = "no_self_assignment"
	RuleRedrawBelow       RuleName = "redraw_below"
	RuleQuestionMaxLength RuleName = "question_max_length"
	RuleQuestionsPerDay   RuleName = "questions_per_day"
)

// RuleKind tells a client how to render and edit a rule's value.
type RuleKind string

// The rule kinds.
const (
	RuleKindTime       RuleKind = "time"
	RuleKindDuration   RuleKind = "duration"
	RuleKindEnum       RuleKind = "enum"
	RuleKindBool       RuleKind = "bool"
	RuleKindInt        RuleKind = "int"
	RuleKindExclusions RuleKind = "exclusions"
)

// RuleView is one rule as the caller sees it now. Rules returns them in the order of the rule
// table. Value and Default are canonical text.
type RuleView struct {
	Name    RuleName
	Kind    RuleKind
	Value   string
	Default string
	// Options lists the accepted values of an enum or bool rule, in canonical form.
	Options      []string
	Configurable bool
	// EditableIn lists the states in which the organizer may change the rule; empty when fixed.
	EditableIn []State
	// EditableNow is true when the rule is configurable, the exchange is in one of EditableIn and
	// the caller is the organizer. For RuleExclusions it means AddExclusion and RemoveExclusion
	// are allowed now; SetRule never accepts that rule.
	EditableNow bool
}

// RuleChange is a client edit of one rule. Raw is canonical text for ParseRuleValue.
type RuleChange struct {
	Name RuleName
	Raw  string
}

// RuleValue is a parsed rule value. Its zero value is invalid.
type RuleValue struct {
	rule     RuleName
	kind     RuleKind
	instant  time.Time
	duration time.Duration
	text     string
	flag     bool
}

// Rule returns the rule the value belongs to.
func (v RuleValue) Rule() RuleName {
	return v.rule
}

// String returns the canonical text. ParseRuleValue(v.Rule(), v.String()) returns an equal value.
func (v RuleValue) String() string {
	switch v.kind {
	case RuleKindTime:
		if v.instant.IsZero() {
			return unsetText
		}
		return v.instant.Format(time.RFC3339)
	case RuleKindDuration:
		return formatLead(v.duration)
	case RuleKindEnum:
		return v.text
	case RuleKindBool:
		if v.flag {
			return "on"
		}
		return "off"
	default:
		return ""
	}
}

const (
	unsetText = "unset"
	minYear   = 2000
	maxYear   = 9999
)

// ruleDescriptor holds what the parsers read about one rule.
type ruleDescriptor struct {
	name         RuleName
	kind         RuleKind
	options      []string
	configurable bool
}

// ruleDescriptors returns the registry in the order of the rule table.
func ruleDescriptors() []ruleDescriptor {
	return []ruleDescriptor{
		{name: RuleDrawAt, kind: RuleKindTime, configurable: true},
		{name: RuleRevealAt, kind: RuleKindTime, configurable: true},
		{name: RuleReminderLead, kind: RuleKindDuration, configurable: true},
		{
			name:         RuleDropoutPolicy,
			kind:         RuleKindEnum,
			options:      []string{string(DropoutPatch), string(DropoutRedraw)},
			configurable: true,
		},
		{name: RuleAvoidLastRound, kind: RuleKindBool, configurable: true},
		{name: RuleExclusions, kind: RuleKindExclusions, configurable: true},
		{name: RuleQuestionsEnabled, kind: RuleKindBool, configurable: true},
		{name: RuleMinParticipants, kind: RuleKindInt},
		{name: RuleMaxParticipants, kind: RuleKindInt},
		{name: RuleNoSelfAssignment, kind: RuleKindBool},
		{name: RuleRedrawBelow, kind: RuleKindInt},
		{name: RuleQuestionMaxLength, kind: RuleKindInt},
		{name: RuleQuestionsPerDay, kind: RuleKindInt},
	}
}

func lookupRule(name RuleName) (ruleDescriptor, bool) {
	for _, descriptor := range ruleDescriptors() {
		if descriptor.name == name {
			return descriptor, true
		}
	}
	return ruleDescriptor{}, false
}

func unknownRuleError() error {
	return errors.Join(&InputError{Field: "rule", Reason: "is unknown"}, ErrUnknownRule) //nolint:wrapcheck // joins this function's own errors
}

// ParseRuleName parses a rule name, exact match. Unknown names return an error wrapping both
// ErrInvalidInput and ErrUnknownRule.
func ParseRuleName(raw string) (RuleName, error) {
	name := RuleName(raw)
	_, found := lookupRule(name)
	if !found {
		return "", unknownRuleError()
	}
	return name, nil
}

// ParseRuleValue parses the canonical text of a value for rule name. It checks the form and
// static bounds only; SetRule checks the state- and time-dependent constraints. An unknown name
// matches ErrInvalidInput and ErrUnknownRule; a fixed rule matches ErrRuleFixed only; the
// exclusions rule, edited through the exclusion methods, is an *InputError for field "rule".
func ParseRuleValue(name RuleName, raw string) (RuleValue, error) {
	descriptor, found := lookupRule(name)
	if !found {
		return RuleValue{}, unknownRuleError()
	}
	if !descriptor.configurable {
		return RuleValue{}, ErrRuleFixed
	}
	field := string(name)
	value := RuleValue{rule: name, kind: descriptor.kind}
	var err error
	switch descriptor.kind {
	case RuleKindTime:
		value.instant, err = parseInstant(field, raw)
	case RuleKindDuration:
		value.duration, err = parseLead(field, raw)
	case RuleKindEnum:
		value.text, err = parseOption(field, raw, descriptor.options)
	case RuleKindBool:
		value.flag, err = parseFlag(field, raw)
	default:
		err = &InputError{Field: "rule", Reason: "is edited through the exclusion methods"}
	}
	if err != nil {
		return RuleValue{}, err
	}
	return value, nil
}

// parseInstant returns the zero time for "unset".
func parseInstant(field, raw string) (time.Time, error) {
	if raw == unsetText {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, &InputError{Field: field, Reason: "is not unset or an RFC 3339 time with an offset"}
	}
	instant := parsed.UTC().Truncate(time.Second)
	isInRange := instant.Year() >= minYear && instant.Year() <= maxYear
	if !isInRange {
		return time.Time{}, &InputError{Field: field, Reason: "is outside the years 2000 to 9999"}
	}
	return instant, nil
}

func parseLead(field, raw string) (time.Duration, error) {
	badForm := &InputError{Field: field, Reason: "is not a duration such as 0, 36h, 90m, 3d or 1d12h"}
	outOfRange := &InputError{Field: field, Reason: "is outside 0 to 14 days in whole seconds"}

	if raw == "0" {
		return 0, nil
	}
	var total time.Duration
	rest := raw
	digits := leadingDigits(raw)
	hasDays := digits > 0 && digits < len(raw) && raw[digits] == 'd'
	if hasDays {
		if digits > 3 {
			return 0, outOfRange
		}
		days, err := strconv.Atoi(raw[:digits])
		if err != nil {
			return 0, badForm
		}
		total = time.Duration(days) * 24 * time.Hour
		rest = raw[digits+1:]
	}
	if rest != "" || !hasDays {
		startsWithDigit := rest != "" && rest[0] >= '0' && rest[0] <= '9'
		if !startsWithDigit {
			return 0, badForm
		}
		parsed, err := time.ParseDuration(rest)
		if err != nil {
			return 0, badForm
		}
		total += parsed
	}
	isValid := total >= 0 && total <= MaxReminderLead && total%time.Second == 0
	if !isValid {
		return 0, outOfRange
	}
	return total, nil
}

func leadingDigits(s string) int {
	n := 0
	for n < len(s) && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	return n
}

func formatLead(d time.Duration) string {
	if d == 0 {
		return "0s"
	}
	const day = 24 * time.Hour
	if d%day == 0 {
		return strconv.Itoa(int(d/day)) + "d"
	}
	return d.String()
}

func parseOption(field, raw string, options []string) (string, error) {
	if !slices.Contains(options, raw) {
		return "", &InputError{Field: field, Reason: "is not one of " + strings.Join(options, ", ")}
	}
	return raw, nil
}

func parseFlag(field, raw string) (bool, error) {
	switch raw {
	case "on", "true":
		return true, nil
	case "off", "false":
		return false, nil
	default:
		return false, &InputError{Field: field, Reason: "is not on, off, true or false"}
	}
}
