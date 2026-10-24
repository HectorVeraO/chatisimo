package giftexchange

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange/internal/store/db"
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
		return instantText(v.instant)
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
	// defaultValue is the canonical text a new exchange starts with; for a fixed rule it is the
	// value.
	defaultValue string
	// editableIn lists the states in which the organizer may change a configurable rule.
	editableIn []State
}

// defaultReminderLead is the reminder_lead of a new exchange.
const defaultReminderLead = 24 * time.Hour

// ruleDescriptors returns the registry in the order of the rule table.
func ruleDescriptors() []ruleDescriptor {
	openOnly := []State{StateOpen}
	openOrDrawn := []State{StateOpen, StateDrawn}

	return []ruleDescriptor{
		{name: RuleDrawAt, kind: RuleKindTime, configurable: true, defaultValue: unsetText, editableIn: openOnly},
		{
			name: RuleRevealAt, kind: RuleKindTime, configurable: true,
			defaultValue: unsetText, editableIn: openOrDrawn,
		},
		{
			name: RuleReminderLead, kind: RuleKindDuration, configurable: true,
			defaultValue: formatLead(defaultReminderLead), editableIn: openOrDrawn,
		},
		{
			name:         RuleDropoutPolicy,
			kind:         RuleKindEnum,
			options:      []string{string(DropoutPatch), string(DropoutRedraw)},
			configurable: true,
			defaultValue: string(DropoutPatch),
			editableIn:   openOrDrawn,
		},
		{
			name: RuleAvoidLastRound, kind: RuleKindBool, configurable: true,
			defaultValue: "off", editableIn: openOnly,
		},
		{
			name: RuleExclusions, kind: RuleKindExclusions, configurable: true,
			defaultValue: "0", editableIn: openOnly,
		},
		{
			name: RuleQuestionsEnabled, kind: RuleKindBool, configurable: true,
			defaultValue: "on", editableIn: openOrDrawn,
		},
		{name: RuleMinParticipants, kind: RuleKindInt, defaultValue: strconv.Itoa(MinParticipants)},
		{name: RuleMaxParticipants, kind: RuleKindInt, defaultValue: strconv.Itoa(MaxParticipants)},
		{name: RuleNoSelfAssignment, kind: RuleKindBool, defaultValue: "on"},
		{name: RuleRedrawBelow, kind: RuleKindInt, defaultValue: strconv.Itoa(RedrawBelow)},
		{name: RuleQuestionMaxLength, kind: RuleKindInt, defaultValue: strconv.Itoa(MaxQuestionRunes)},
		{name: RuleQuestionsPerDay, kind: RuleKindInt, defaultValue: strconv.Itoa(QuestionsPerDay)},
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

// ruleSettings holds the six stored values of an exchange's configurable rules. A zero time is
// "unset"; times are UTC.
type ruleSettings struct {
	drawAt           time.Time
	revealAt         time.Time
	reminderLead     time.Duration
	dropoutPolicy    string
	avoidLastRound   bool
	questionsEnabled bool
}

func defaultSettings() ruleSettings {
	settings := ruleSettings{
		reminderLead:     defaultReminderLead,
		dropoutPolicy:    string(DropoutPatch),
		questionsEnabled: true,
	}
	return settings
}

func settingsOf(exchange db.GiftexchangeExchange) ruleSettings {
	settings := ruleSettings{
		reminderLead:     time.Duration(exchange.ReminderLeadSeconds) * time.Second,
		dropoutPolicy:    exchange.DropoutPolicy,
		avoidLastRound:   exchange.AvoidLastRound,
		questionsEnabled: exchange.QuestionsEnabled,
	}
	if exchange.DrawAt.Valid {
		settings.drawAt = exchange.DrawAt.Time.UTC()
	}
	if exchange.RevealAt.Valid {
		settings.revealAt = exchange.RevealAt.Time.UTC()
	}
	return settings
}

func instantText(instant time.Time) string {
	if instant.IsZero() {
		return unsetText
	}
	return instant.UTC().Format(time.RFC3339)
}

// assign returns the settings with value stored, without checking constraints.
func (c ruleSettings) assign(value RuleValue) ruleSettings {
	switch value.rule {
	case RuleDrawAt:
		c.drawAt = value.instant
	case RuleRevealAt:
		c.revealAt = value.instant
	case RuleReminderLead:
		c.reminderLead = value.duration
	case RuleDropoutPolicy:
		c.dropoutPolicy = value.text
	case RuleAvoidLastRound:
		c.avoidLastRound = value.flag
	case RuleQuestionsEnabled:
		c.questionsEnabled = value.flag
	}
	return c
}

// check reports the constraints that depend on the clock and on the other stored times.
func (c ruleSettings) check(value RuleValue, now time.Time) error {
	switch value.rule {
	case RuleDrawAt:
		return checkTime(value, now, c.revealAt, true)
	case RuleRevealAt:
		return checkTime(value, now, c.drawAt, false)
	default:
		return nil
	}
}

// checkTime requires a set time to be after now and on the right side of other, the stored time
// of the sibling rule (when set): before it for draw_at, after it for reveal_at.
func checkTime(value RuleValue, now, other time.Time, mustPrecede bool) error {
	field := string(value.rule)
	if value.instant.IsZero() {
		return nil
	}
	isFuture := value.instant.After(now)
	if !isFuture {
		return &InputError{Field: field, Reason: "must be in the future"}
	}
	if other.IsZero() {
		return nil
	}
	if mustPrecede && !value.instant.Before(other) {
		return &InputError{Field: field, Reason: "must be before reveal_at"}
	}
	if !mustPrecede && !value.instant.After(other) {
		return &InputError{Field: field, Reason: "must be after draw_at"}
	}
	return nil
}

func optionalTimestamptz(instant time.Time) pgtype.Timestamptz {
	if instant.IsZero() {
		return pgtype.Timestamptz{}
	}
	return timestamptz(instant)
}

// parseChange parses one client edit; a fixed rule is reported as ErrRuleFixed.
func parseChange(change RuleChange) (RuleValue, error) {
	name, nameErr := ParseRuleName(string(change.Name))
	if nameErr != nil {
		return RuleValue{}, nameErr
	}
	return ParseRuleValue(name, change.Raw)
}

// Rules returns every rule of the rule table, fixed ones included, with EditableNow computed
// for the caller. Any caller in the group may read them.
func (s *Service) Rules(ctx context.Context, a Actor, g GroupID, id ExchangeID) ([]RuleView, error) {
	validationErr := validateScope(a, g, id)
	if validationErr != nil {
		return nil, validationErr
	}

	q := db.New(s.pool)
	row, err := readRow(ctx, q, a.person, uuid.UUID(id))
	if err != nil {
		return nil, err
	}

	isOtherGroup := GroupID(row.exchange.GroupID) != g
	if isOtherGroup {
		return nil, ErrNotFound
	}

	return ruleViewsOf(ctx, q, row.exchange, a.person)
}

func ruleViewsOf(
	ctx context.Context,
	q *db.Queries,
	exchange db.GiftexchangeExchange,
	caller PersonID,
) ([]RuleView, error) {
	exclusionCount, err := q.CountExclusions(ctx, exchange.ID)
	if err != nil {
		return nil, fmt.Errorf("count exclusions of %s: %w", exchange.ID, err)
	}

	isOrganizer := PersonID(exchange.OrganizerID) == caller
	return ruleViews(exchange, int(exclusionCount), isOrganizer), nil
}

// SetRule parses ch.Raw with ParseRuleValue, checks the caller, the rule's editable states and
// the time constraints, stores the value and returns the new view of that rule. Setting the
// current value again changes nothing.
func (s *Service) SetRule(
	ctx context.Context,
	a Actor,
	g GroupID,
	id ExchangeID,
	ch RuleChange,
) (RuleView, error) {
	authErr := requireActor(a)
	if authErr != nil {
		return RuleView{}, authErr
	}

	// A fixed rule is public knowledge, but whether the caller may touch the exchange is not,
	// so ErrRuleFixed waits until the caller has been authorized.
	value, parseErr := parseChange(ch)
	isFixed := errors.Is(parseErr, ErrRuleFixed)
	if parseErr != nil && !isFixed {
		return RuleView{}, parseErr
	}

	scopeErr := validateScope(a, g, id)
	if scopeErr != nil {
		return RuleView{}, scopeErr
	}

	var view RuleView
	txErr := s.inTx(ctx, func(q *db.Queries, _ pgx.Tx) error {
		var setErr error
		view, setErr = s.setRuleLocked(ctx, q, a, g, id, ch.Name, value, isFixed)
		return setErr
	})
	if txErr != nil {
		return RuleView{}, txErr
	}

	return view, nil
}

func (s *Service) setRuleLocked(
	ctx context.Context,
	q *db.Queries,
	a Actor,
	g GroupID,
	id ExchangeID,
	name RuleName,
	value RuleValue,
	isFixed bool,
) (RuleView, error) {
	exchange, err := lockExchange(ctx, q, g, id)
	if err != nil {
		return RuleView{}, err
	}

	guardErr := requireOrganizer(exchange, a)
	if guardErr != nil {
		return RuleView{}, guardErr
	}
	if isFixed {
		return RuleView{}, ErrRuleFixed
	}

	descriptor, found := lookupRule(name)
	if !found {
		return RuleView{}, unknownRuleError()
	}
	state := State(exchange.State)
	if !slices.Contains(descriptor.editableIn, state) {
		return RuleView{}, &RuleLockedError{Rule: name, State: state}
	}

	before := settingsOf(exchange)
	after := before.assign(value)
	isSameValue := ruleValueText(before, descriptor, 0) == ruleValueText(after, descriptor, 0)
	if !isSameValue {
		checkErr := before.check(value, s.now())
		if checkErr != nil {
			return RuleView{}, checkErr
		}

		updateErr := q.UpdateExchangeRules(ctx, db.UpdateExchangeRulesParams{
			ID:                  exchange.ID,
			DrawAt:              optionalTimestamptz(after.drawAt),
			RevealAt:            optionalTimestamptz(after.revealAt),
			ReminderLeadSeconds: int32(after.reminderLead.Seconds()),
			DropoutPolicy:       after.dropoutPolicy,
			AvoidLastRound:      after.avoidLastRound,
			QuestionsEnabled:    after.questionsEnabled,
		})
		if updateErr != nil {
			return RuleView{}, fmt.Errorf("update rules of %s: %w", exchange.ID, updateErr)
		}
		s.logger.InfoContext(ctx, "rule changed", "exchange_id", id.String(), "rule", string(name))
	}

	return ruleViewOf(ctx, q, a.person, exchange.ID, name)
}

// ruleViewOf reads the exchange again and returns the view of one rule.
func ruleViewOf(
	ctx context.Context,
	q *db.Queries,
	caller PersonID,
	id uuid.UUID,
	name RuleName,
) (RuleView, error) {
	row, err := readRow(ctx, q, caller, id)
	if err != nil {
		return RuleView{}, err
	}

	views, viewsErr := ruleViewsOf(ctx, q, row.exchange, caller)
	if viewsErr != nil {
		return RuleView{}, viewsErr
	}
	for _, view := range views {
		if view.Name == name {
			return view, nil
		}
	}
	panic(fmt.Sprintf("rule %q has no view: ruleViewsOf must list every validated rule", name))
}
