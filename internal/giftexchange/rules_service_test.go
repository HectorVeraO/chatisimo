package giftexchange_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

// ruleExpectation is one row of the rule table as the tests expect to read it.
type ruleExpectation struct {
	name         giftexchange.RuleName
	kind         giftexchange.RuleKind
	value        string
	options      []string
	configurable bool
	editableIn   []giftexchange.State
}

func ruleTable() []ruleExpectation {
	open := []giftexchange.State{giftexchange.StateOpen}
	openOrDrawn := []giftexchange.State{giftexchange.StateOpen, giftexchange.StateDrawn}
	onOff := []string{"on", "off"}
	policies := []string{"patch", "redraw"}

	return []ruleExpectation{
		{giftexchange.RuleDrawAt, giftexchange.RuleKindTime, "unset", nil, true, open},
		{giftexchange.RuleRevealAt, giftexchange.RuleKindTime, "unset", nil, true, openOrDrawn},
		{giftexchange.RuleReminderLead, giftexchange.RuleKindDuration, "1d", nil, true, openOrDrawn},
		{giftexchange.RuleDropoutPolicy, giftexchange.RuleKindEnum, "patch", policies, true, openOrDrawn},
		{giftexchange.RuleAvoidLastRound, giftexchange.RuleKindBool, "off", onOff, true, open},
		{giftexchange.RuleExclusions, giftexchange.RuleKindExclusions, "0", nil, true, open},
		{giftexchange.RuleQuestionsEnabled, giftexchange.RuleKindBool, "on", onOff, true, openOrDrawn},
		{giftexchange.RuleMinParticipants, giftexchange.RuleKindInt, "3", nil, false, nil},
		{giftexchange.RuleMaxParticipants, giftexchange.RuleKindInt, "100", nil, false, nil},
		{giftexchange.RuleNoSelfAssignment, giftexchange.RuleKindBool, "on", onOff, false, nil},
		{giftexchange.RuleRedrawBelow, giftexchange.RuleKindInt, "4", nil, false, nil},
		{giftexchange.RuleQuestionMaxLength, giftexchange.RuleKindInt, "1000", nil, false, nil},
		{giftexchange.RuleQuestionsPerDay, giftexchange.RuleKindInt, "20", nil, false, nil},
	}
}

// forceState moves an exchange to state without the draw and reveal operations, which another
// slice owns. It writes the columns the schema requires for that state.
func forceState(t *testing.T, x giftexchange.ExchangeID, state giftexchange.State) {
	t.Helper()

	var query string
	switch state {
	case giftexchange.StateDrawn:
		query = "UPDATE giftexchange.exchanges SET state = 'drawn', round = 1, drawn_at = now(), " +
			"draw_at = NULL WHERE id = $1"
	case giftexchange.StateRevealed:
		query = "UPDATE giftexchange.exchanges SET state = 'revealed', round = 1, drawn_at = now(), " +
			"revealed_at = now(), draw_at = NULL WHERE id = $1"
	default:
		t.Fatalf("forceState: unsupported state %q", state)
	}

	_, err := shared.Pool().Exec(t.Context(), query, x.String())
	if err != nil {
		t.Fatalf("force state %s: %v", state, err)
	}
}

// moveTo brings the scene's exchange to state, through the API where one exists.
func (s scene) moveTo(t *testing.T, state giftexchange.State) {
	t.Helper()

	switch state {
	case giftexchange.StateOpen:
		return
	case giftexchange.StateCancelled:
		err := s.h.svc.Cancel(t.Context(), s.organizer.actor, s.group, s.exchange)
		if err != nil {
			t.Fatalf("Cancel: %v", err)
		}
	default:
		forceState(t, s.exchange, state)
	}
}

// futureText returns an RFC 3339 time d after the harness clock, whole seconds, UTC.
func (h *harness) futureText(d time.Duration) string {
	return h.clock.Now().Add(d).UTC().Truncate(time.Second).Format(time.RFC3339)
}

func ruleNamed(t *testing.T, views []giftexchange.RuleView, name giftexchange.RuleName) giftexchange.RuleView {
	t.Helper()

	for _, view := range views {
		if view.Name == name {
			return view
		}
	}
	t.Fatalf("rule %s missing from %d views", name, len(views))

	return giftexchange.RuleView{}
}

func (s scene) set(t *testing.T, caller person, name giftexchange.RuleName, raw string) (giftexchange.RuleView, error) {
	t.Helper()

	view, err := s.h.svc.SetRule(t.Context(), caller.actor, s.group, s.exchange,
		giftexchange.RuleChange{Name: name, Raw: raw})

	return view, err //nolint:wrapcheck // test helper passes the module error through
}

func TestRulesReturnsTheWholeTableInOrderWithDefaults(t *testing.T) {
	s := newScene(t)

	views, err := s.h.svc.Rules(t.Context(), s.organizer.actor, s.group, s.exchange)
	if err != nil {
		t.Fatalf("Rules: %v", err)
	}

	table := ruleTable()
	if len(views) != len(table) {
		t.Fatalf("got %d rules, want %d", len(views), len(table))
	}
	for i, want := range table {
		got := views[i]
		if got.Name != want.name || got.Kind != want.kind {
			t.Errorf("rule %d = %s/%s, want %s/%s", i, got.Name, got.Kind, want.name, want.kind)
		}
		if got.Value != want.value || got.Default != want.value {
			t.Errorf("rule %s value/default = %q/%q, want %q", want.name, got.Value, got.Default, want.value)
		}
		if !slices.Equal(got.Options, want.options) {
			t.Errorf("rule %s options = %v, want %v", want.name, got.Options, want.options)
		}
		if got.Configurable != want.configurable {
			t.Errorf("rule %s configurable = %v, want %v", want.name, got.Configurable, want.configurable)
		}
		if !slices.Equal(got.EditableIn, want.editableIn) {
			t.Errorf("rule %s editable in %v, want %v", want.name, got.EditableIn, want.editableIn)
		}
	}
}

func TestRulesEditableNowFollowsRoleAndState(t *testing.T) {
	states := []giftexchange.State{
		giftexchange.StateOpen, giftexchange.StateDrawn, giftexchange.StateRevealed, giftexchange.StateCancelled,
	}
	for _, state := range states {
		t.Run(string(state), func(t *testing.T) {
			s := newScene(t)
			participant := s.h.newPerson(t)
			member := s.h.newPerson(t)
			s.h.join(t, participant, s.group, s.exchange)
			s.moveTo(t, state)

			callers := map[string]person{"organizer": s.organizer, "participant": participant, "member": member}
			for who, caller := range callers {
				views, err := s.h.svc.Rules(t.Context(), caller.actor, s.group, s.exchange)
				if err != nil {
					t.Fatalf("%s: Rules: %v", who, err)
				}
				for _, want := range ruleTable() {
					isEditableState := slices.Contains(want.editableIn, state)
					wantNow := who == "organizer" && want.configurable && isEditableState
					got := ruleNamed(t, views, want.name)
					if got.EditableNow != wantNow {
						t.Errorf("%s: rule %s EditableNow = %v, want %v", who, want.name, got.EditableNow, wantNow)
					}
				}
			}
		})
	}
}

func TestSetRuleStoresEveryConfigurableRuleInCanonicalForm(t *testing.T) {
	s := newScene(t)
	draw := s.h.futureText(48 * time.Hour)
	reveal := s.h.futureText(72 * time.Hour)
	tests := []struct {
		rule giftexchange.RuleName
		raw  string
		want string
	}{
		{giftexchange.RuleDrawAt, draw, draw},
		{giftexchange.RuleRevealAt, reveal, reveal},
		{giftexchange.RuleReminderLead, "36h", "36h0m0s"},
		{giftexchange.RuleReminderLead, "3d", "3d"},
		{giftexchange.RuleDropoutPolicy, "redraw", "redraw"},
		{giftexchange.RuleAvoidLastRound, "true", "on"},
		{giftexchange.RuleQuestionsEnabled, "false", "off"},
	}

	for _, tc := range tests {
		view, err := s.set(t, s.organizer, tc.rule, tc.raw)
		if err != nil {
			t.Fatalf("SetRule %s %q: %v", tc.rule, tc.raw, err)
		}
		if view.Name != tc.rule || view.Value != tc.want {
			t.Errorf("SetRule %s %q returned %s=%q, want %q", tc.rule, tc.raw, view.Name, view.Value, tc.want)
		}
		if !view.EditableNow {
			t.Errorf("SetRule %s returned a view that is not editable for the organizer", tc.rule)
		}
	}

	views, err := s.h.svc.Rules(t.Context(), s.organizer.actor, s.group, s.exchange)
	if err != nil {
		t.Fatalf("Rules: %v", err)
	}
	stored := map[giftexchange.RuleName]string{
		giftexchange.RuleDrawAt: draw, giftexchange.RuleRevealAt: reveal,
		giftexchange.RuleReminderLead: "3d", giftexchange.RuleDropoutPolicy: "redraw",
		giftexchange.RuleAvoidLastRound: "on", giftexchange.RuleQuestionsEnabled: "off",
	}
	for name, want := range stored {
		if got := ruleNamed(t, views, name).Value; got != want {
			t.Errorf("Rules %s = %q, want %q", name, got, want)
		}
	}
	exchangeView, err := s.h.svc.Exchange(t.Context(), s.organizer.actor, s.group, s.exchange)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if exchangeView.DrawAt == nil || exchangeView.RevealAt == nil {
		t.Fatalf("summary times = %v/%v, want both set", exchangeView.DrawAt, exchangeView.RevealAt)
	}
	if exchangeView.DrawAt.UTC().Format(time.RFC3339) != draw {
		t.Errorf("summary DrawAt = %v, want %s", exchangeView.DrawAt, draw)
	}
}

func TestSetRuleReturnsCanonicalTextThatSetsTheSameValueAgain(t *testing.T) {
	s := newScene(t)
	draw := s.h.futureText(48 * time.Hour)
	inputs := map[giftexchange.RuleName]string{
		giftexchange.RuleDrawAt:           strings.Replace(draw, "Z", "+00:00", 1),
		giftexchange.RuleReminderLead:     "1d12h",
		giftexchange.RuleAvoidLastRound:   "true",
		giftexchange.RuleQuestionsEnabled: "false",
	}

	for name, raw := range inputs {
		first, err := s.set(t, s.organizer, name, raw)
		if err != nil {
			t.Fatalf("SetRule %s %q: %v", name, raw, err)
		}
		before := takeSnapshot(t, s.h, s.exchange)

		second, err := s.set(t, s.organizer, name, first.Value)
		if err != nil {
			t.Fatalf("SetRule %s %q again: %v", name, first.Value, err)
		}
		if second.Value != first.Value {
			t.Errorf("rule %s changed from %q to %q on a round trip", name, first.Value, second.Value)
		}
		assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))
	}
}

func TestSetRuleWithTheCurrentValueChangesNothingEvenWhenTheTimeHasPassed(t *testing.T) {
	s := newScene(t)
	reveal := s.h.futureText(time.Hour)
	if _, err := s.set(t, s.organizer, giftexchange.RuleRevealAt, reveal); err != nil {
		t.Fatalf("SetRule: %v", err)
	}
	s.h.clock.Advance(2 * time.Hour)
	before := takeSnapshot(t, s.h, s.exchange)

	view, err := s.set(t, s.organizer, giftexchange.RuleRevealAt, reveal)

	if err != nil || view.Value != reveal {
		t.Fatalf("SetRule = %q, %v; want the current value %q and no error", view.Value, err, reveal)
	}
	assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))
}

func TestSetRuleRefusesFixedAndUnknownRules(t *testing.T) {
	s := newScene(t)
	before := takeSnapshot(t, s.h, s.exchange)

	fixed := []giftexchange.RuleName{
		giftexchange.RuleMinParticipants, giftexchange.RuleMaxParticipants, giftexchange.RuleNoSelfAssignment,
		giftexchange.RuleRedrawBelow, giftexchange.RuleQuestionMaxLength, giftexchange.RuleQuestionsPerDay,
	}
	for _, name := range fixed {
		_, err := s.set(t, s.organizer, name, "5")
		if !errors.Is(err, giftexchange.ErrRuleFixed) {
			t.Errorf("rule %s: err = %v, want ErrRuleFixed", name, err)
		}
	}

	_, unknownErr := s.set(t, s.organizer, "nope", "on")
	if !errors.Is(unknownErr, giftexchange.ErrUnknownRule) || !errors.Is(unknownErr, giftexchange.ErrInvalidInput) {
		t.Errorf("unknown rule: err = %v, want ErrUnknownRule and ErrInvalidInput", unknownErr)
	}

	_, exclusionsErr := s.set(t, s.organizer, giftexchange.RuleExclusions, "0")
	if !slices.Equal(inputFields(exclusionsErr), []string{"rule"}) {
		t.Errorf("exclusions rule: err = %v, want *InputError for field rule", exclusionsErr)
	}
	assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))
}

func TestSetRuleReportsLockedRulesWithTheirState(t *testing.T) {
	drawn := giftexchange.StateDrawn
	revealed := giftexchange.StateRevealed
	cancelled := giftexchange.StateCancelled
	tests := []struct {
		name      string
		state     giftexchange.State
		rule      giftexchange.RuleName
		raw       string
		wantLock  bool
		wantValue string
	}{
		{"draw_at is locked once drawn", drawn, giftexchange.RuleDrawAt, "unset", true, ""},
		{"avoid_last_round is locked once drawn", drawn, giftexchange.RuleAvoidLastRound, "on", true, ""},
		{"reminder_lead stays open once drawn", drawn, giftexchange.RuleReminderLead, "2d", false, "2d"},
		{"dropout_policy stays open once drawn", drawn, giftexchange.RuleDropoutPolicy, "redraw", false, "redraw"},
		{"questions_enabled stays open once drawn", drawn, giftexchange.RuleQuestionsEnabled, "off", false, "off"},
		{"reveal_at stays open once drawn", drawn, giftexchange.RuleRevealAt, "2099-01-02T03:04:05Z", false, "2099-01-02T03:04:05Z"},
		{"reminder_lead is locked once revealed", revealed, giftexchange.RuleReminderLead, "2d", true, ""},
		{"reveal_at is locked once revealed", revealed, giftexchange.RuleRevealAt, "unset", true, ""},
		{"dropout_policy is locked once revealed", revealed, giftexchange.RuleDropoutPolicy, "redraw", true, ""},
		{"questions_enabled is locked once cancelled", cancelled, giftexchange.RuleQuestionsEnabled, "off", true, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newScene(t)
			s.moveTo(t, tc.state)
			before := takeSnapshot(t, s.h, s.exchange)

			view, err := s.set(t, s.organizer, tc.rule, tc.raw)

			if !tc.wantLock {
				if err != nil || view.Value != tc.wantValue {
					t.Fatalf("SetRule = %q, %v; want %q", view.Value, err, tc.wantValue)
				}
				return
			}
			var locked *giftexchange.RuleLockedError
			if !errors.As(err, &locked) || !errors.Is(err, giftexchange.ErrRuleLocked) {
				t.Fatalf("err = %v, want *RuleLockedError", err)
			}
			if locked.Rule != tc.rule || locked.State != tc.state {
				t.Errorf("locked error = (%s, %s), want (%s, %s)", locked.Rule, locked.State, tc.rule, tc.state)
			}
			assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))
		})
	}
}

func TestSetRuleTimesMustBeStrictlyInTheFuture(t *testing.T) {
	for _, rule := range []giftexchange.RuleName{giftexchange.RuleDrawAt, giftexchange.RuleRevealAt} {
		t.Run(string(rule), func(t *testing.T) {
			s := newScene(t)
			// A whole-second clock makes "equal to now" expressible in canonical text.
			s.h.clock.Advance(time.Second - time.Duration(s.h.clock.Now().Nanosecond()))
			now := s.h.clock.Now().UTC().Format(time.RFC3339)
			past := s.h.clock.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
			onePast := s.h.clock.Now().Add(time.Second).UTC().Format(time.RFC3339)
			before := takeSnapshot(t, s.h, s.exchange)

			for name, raw := range map[string]string{"past": past, "equal to now": now} {
				_, err := s.set(t, s.organizer, rule, raw)
				var inputErr *giftexchange.InputError
				if !errors.As(err, &inputErr) || inputErr.Field != string(rule) || inputErr.Code != giftexchange.InputCodeMustBeFuture {
					t.Errorf("%s: err = %v, want *InputError for %s with code %q", name, err, rule, giftexchange.InputCodeMustBeFuture)
				}
			}
			assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))

			view, err := s.set(t, s.organizer, rule, onePast)
			if err != nil || view.Value != onePast {
				t.Errorf("one second ahead: %q, %v; want it accepted", view.Value, err)
			}
		})
	}
}

func TestSetRuleKeepsDrawBeforeReveal(t *testing.T) {
	s := newScene(t)
	draw := s.h.futureText(48 * time.Hour)
	if _, err := s.set(t, s.organizer, giftexchange.RuleDrawAt, draw); err != nil {
		t.Fatalf("SetRule draw_at: %v", err)
	}
	before := takeSnapshot(t, s.h, s.exchange)

	_, equalReveal := s.set(t, s.organizer, giftexchange.RuleRevealAt, draw)
	_, earlierReveal := s.set(t, s.organizer, giftexchange.RuleRevealAt, s.h.futureText(24*time.Hour))
	for _, err := range []error{equalReveal, earlierReveal} {
		var inputErr *giftexchange.InputError
		if !errors.As(err, &inputErr) || inputErr.Field != "reveal_at" || inputErr.Code != giftexchange.InputCodeAfterDrawAt {
			t.Errorf("reveal_at err = %v, want *InputError for reveal_at with code %q", err, giftexchange.InputCodeAfterDrawAt)
		}
	}
	assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))

	if _, err := s.set(t, s.organizer, giftexchange.RuleRevealAt, s.h.futureText(72*time.Hour)); err != nil {
		t.Fatalf("SetRule reveal_at after draw_at: %v", err)
	}
	beforeDraw := takeSnapshot(t, s.h, s.exchange)

	_, laterDraw := s.set(t, s.organizer, giftexchange.RuleDrawAt, s.h.futureText(96*time.Hour))
	var drawInputErr *giftexchange.InputError
	if !errors.As(laterDraw, &drawInputErr) || drawInputErr.Field != "draw_at" || drawInputErr.Code != giftexchange.InputCodeBeforeRevealAt {
		t.Errorf("draw_at after reveal_at: err = %v, want *InputError for draw_at with code %q", laterDraw, giftexchange.InputCodeBeforeRevealAt)
	}
	assertUnchanged(t, beforeDraw, takeSnapshot(t, s.h, s.exchange))

	cleared, err := s.set(t, s.organizer, giftexchange.RuleDrawAt, "unset")
	if err != nil || cleared.Value != "unset" {
		t.Errorf("unset draw_at = %q, %v; want unset", cleared.Value, err)
	}
}

func TestSetRuleReminderLeadBoundaries(t *testing.T) {
	s := newScene(t)
	tests := []struct {
		raw     string
		want    string
		wantErr bool
	}{
		{"0", "0s", false},
		{"14d", "14d", false},
		{"14d1s", "", true},
		{"-1s", "", true},
		{"15d", "", true},
	}

	for _, tc := range tests {
		view, err := s.set(t, s.organizer, giftexchange.RuleReminderLead, tc.raw)
		if tc.wantErr {
			if !slices.Equal(inputFields(err), []string{"reminder_lead"}) {
				t.Errorf("%q: err = %v, want *InputError for reminder_lead", tc.raw, err)
			}
			continue
		}
		if err != nil || view.Value != tc.want {
			t.Errorf("%q: %q, %v; want %q", tc.raw, view.Value, err, tc.want)
		}
	}
}

func TestCreateExchangeAppliesRulesInOrder(t *testing.T) {
	h := newService(t)
	group := h.newGroup(t)
	organizer := h.newPerson(t)
	draw := h.futureText(48 * time.Hour)

	view, err := h.svc.CreateExchange(t.Context(), organizer.actor, giftexchange.CreateExchangeRequest{
		Group: group,
		Name:  "Winter exchange",
		Rules: []giftexchange.RuleChange{
			{Name: giftexchange.RuleReminderLead, Raw: "2d"},
			{Name: giftexchange.RuleReminderLead, Raw: "3d"},
			{Name: giftexchange.RuleDrawAt, Raw: draw},
			{Name: giftexchange.RuleRevealAt, Raw: h.futureText(72 * time.Hour)},
			{Name: giftexchange.RuleAvoidLastRound, Raw: "on"},
		},
	})
	if err != nil {
		t.Fatalf("CreateExchange: %v", err)
	}

	if got := ruleNamed(t, view.Rules, giftexchange.RuleReminderLead).Value; got != "3d" {
		t.Errorf("reminder_lead = %q, want the later change 3d", got)
	}
	if got := ruleNamed(t, view.Rules, giftexchange.RuleDrawAt).Value; got != draw {
		t.Errorf("draw_at = %q, want %q", got, draw)
	}
	if got := ruleNamed(t, view.Rules, giftexchange.RuleAvoidLastRound).Value; got != "on" {
		t.Errorf("avoid_last_round = %q, want on", got)
	}
	if view.DrawAt == nil || view.RevealAt == nil {
		t.Errorf("summary times = %v/%v, want both set", view.DrawAt, view.RevealAt)
	}
}

func TestCreateExchangeWithAnInvalidRuleStoresNothingAndReportsEveryError(t *testing.T) {
	h := newService(t)
	group := h.newGroup(t)
	organizer := h.newPerson(t)

	_, err := h.svc.CreateExchange(t.Context(), organizer.actor, giftexchange.CreateExchangeRequest{
		Group: group,
		Name:  "",
		Rules: []giftexchange.RuleChange{
			{Name: giftexchange.RuleReminderLead, Raw: "2d"},
			{Name: giftexchange.RuleDrawAt, Raw: h.futureText(-time.Hour)},
			{Name: giftexchange.RuleMinParticipants, Raw: "5"},
			{Name: "nope", Raw: "x"},
			{Name: giftexchange.RuleExclusions, Raw: "1"},
			{Name: giftexchange.RuleAvoidLastRound, Raw: "maybe"},
		},
	})

	isAllJoined := errors.Is(err, giftexchange.ErrInvalidInput) && errors.Is(err, giftexchange.ErrRuleFixed) &&
		errors.Is(err, giftexchange.ErrUnknownRule)
	if !isAllJoined {
		t.Fatalf("err = %v, want ErrInvalidInput, ErrRuleFixed and ErrUnknownRule joined", err)
	}
	wantFields := []string{"name", "draw_at", "rule", "rule", "avoid_last_round"}
	if !slices.Equal(inputFields(err), wantFields) {
		t.Errorf("fields = %v, want %v", inputFields(err), wantFields)
	}
	listed, listErr := h.svc.ExchangesFor(t.Context(), organizer.actor, &group)
	if listErr != nil || len(listed) != 0 {
		t.Errorf("exchanges after a refused create = %d, %v; want none", len(listed), listErr)
	}
}

func TestCreateExchangeChecksRulesAgainstEachOther(t *testing.T) {
	h := newService(t)
	group := h.newGroup(t)
	organizer := h.newPerson(t)

	_, err := h.svc.CreateExchange(t.Context(), organizer.actor, giftexchange.CreateExchangeRequest{
		Group: group,
		Name:  "Winter exchange",
		Rules: []giftexchange.RuleChange{
			{Name: giftexchange.RuleDrawAt, Raw: h.futureText(72 * time.Hour)},
			{Name: giftexchange.RuleRevealAt, Raw: h.futureText(48 * time.Hour)},
		},
	})

	if !slices.Equal(inputFields(err), []string{"reveal_at"}) {
		t.Errorf("err = %v, want *InputError for reveal_at only", err)
	}
}

func TestRuleErrorsNeverEchoTheRawValue(t *testing.T) {
	s := newScene(t)
	const raw = "zzz-user-supplied-value"

	var texts []string
	for _, name := range allRuleNames {
		_, err := s.set(t, s.organizer, name, raw)
		if err == nil {
			t.Fatalf("rule %s accepted garbage", name)
		}
		texts = append(texts, err.Error())
	}
	_, createErr := s.h.svc.CreateExchange(t.Context(), s.organizer.actor, giftexchange.CreateExchangeRequest{
		Group: s.group,
		Name:  "Winter exchange",
		Rules: []giftexchange.RuleChange{{Name: giftexchange.RuleReminderLead, Raw: raw}},
	})
	if createErr == nil {
		t.Fatal("CreateExchange accepted garbage")
	}
	texts = append(texts, createErr.Error())

	for _, text := range texts {
		if strings.Contains(text, raw) {
			t.Errorf("error %q echoes the raw value", text)
		}
	}
	if strings.Contains(s.h.logs.String(), raw) {
		t.Error("logs echo the raw value")
	}
}
