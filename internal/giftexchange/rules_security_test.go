package giftexchange_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

func ruleAndExclusionOperations() []denial {
	return []denial{
		{"set rule", giftexchange.ErrForbidden, func(t *testing.T, s scene, c person) error {
			return errOf(s.set(t, c, giftexchange.RuleDropoutPolicy, "redraw"))
		}},
		{"set rule that is fixed", giftexchange.ErrForbidden, func(t *testing.T, s scene, c person) error {
			return errOf(s.set(t, c, giftexchange.RuleMinParticipants, "5"))
		}},
		{"list exclusions", giftexchange.ErrForbidden, func(t *testing.T, s scene, c person) error {
			return errOf(s.h.svc.Exclusions(t.Context(), c.actor, s.group, s.exchange))
		}},
		{"add exclusion", giftexchange.ErrForbidden, func(t *testing.T, s scene, c person) error {
			return s.add(t, c, c, s.organizer, true)
		}},
		{"remove exclusion", giftexchange.ErrForbidden, func(t *testing.T, s scene, c person) error {
			return s.remove(t, c, c, s.organizer)
		}},
	}
}

func TestRuleAndExclusionOperationsDenyEveryoneButTheOrganizer(t *testing.T) {
	for _, op := range ruleAndExclusionOperations() {
		t.Run(op.name, func(t *testing.T) {
			s := newScene(t)
			participant := s.h.newPerson(t)
			stranger := s.h.newPerson(t)
			s.h.join(t, participant, s.group, s.exchange)
			before := takeSnapshot(t, s.h, s.exchange)

			for who, caller := range map[string]person{"participant": participant, "stranger in the group": stranger} {
				err := op.call(t, s, caller)
				if !errors.Is(err, op.want) {
					t.Errorf("%s: err = %v, want %v", who, err, op.want)
				}
			}
			assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))
		})
	}
}

func TestRulesAndExclusionsInAnotherGroupAreNotFound(t *testing.T) {
	s := newScene(t)
	joined := s.h.newPerson(t)
	s.h.join(t, joined, s.group, s.exchange)
	otherGroup := s.h.newGroup(t)
	outsider := s.h.newPerson(t)
	before := takeSnapshot(t, s.h, s.exchange)

	callers := map[string]person{"outsider": outsider, "organizer": s.organizer, "participant": joined}
	for who, caller := range callers {
		calls := map[string]func() error{
			"rules": func() error {
				return errOf(s.h.svc.Rules(t.Context(), caller.actor, otherGroup, s.exchange))
			},
			"set rule": func() error {
				return errOf(s.h.svc.SetRule(t.Context(), caller.actor, otherGroup, s.exchange,
					giftexchange.RuleChange{Name: giftexchange.RuleDropoutPolicy, Raw: "redraw"}))
			},
			"exclusions": func() error {
				return errOf(s.h.svc.Exclusions(t.Context(), caller.actor, otherGroup, s.exchange))
			},
			"add exclusion": func() error {
				return s.h.svc.AddExclusion(t.Context(), caller.actor, otherGroup, s.exchange, joined.id, s.organizer.id, true)
			},
			"remove exclusion": func() error {
				return s.h.svc.RemoveExclusion(t.Context(), caller.actor, otherGroup, s.exchange, joined.id, s.organizer.id)
			},
		}
		for name, call := range calls {
			err := call()
			if !errors.Is(err, giftexchange.ErrNotFound) {
				t.Errorf("%s %s: err = %v, want ErrNotFound", who, name, err)
			}
		}
	}
	assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))
}

func TestRulesAndExclusionsDenyTheZeroActor(t *testing.T) {
	s, p := newTrio(t)
	if err := s.add(t, s.organizer, p[0], p[1], false); err != nil {
		t.Fatalf("AddExclusion: %v", err)
	}
	before := takeSnapshot(t, s.h, s.exchange)
	var zero giftexchange.Actor

	calls := map[string]func() error{
		"rules": func() error { return errOf(s.h.svc.Rules(t.Context(), zero, s.group, s.exchange)) },
		"set rule": func() error {
			return errOf(s.h.svc.SetRule(t.Context(), zero, s.group, s.exchange,
				giftexchange.RuleChange{Name: giftexchange.RuleDropoutPolicy, Raw: "redraw"}))
		},
		"set fixed rule": func() error {
			return errOf(s.h.svc.SetRule(t.Context(), zero, s.group, s.exchange,
				giftexchange.RuleChange{Name: giftexchange.RuleMinParticipants, Raw: "5"}))
		},
		"exclusions": func() error { return errOf(s.h.svc.Exclusions(t.Context(), zero, s.group, s.exchange)) },
		"add exclusion": func() error {
			return s.h.svc.AddExclusion(t.Context(), zero, s.group, s.exchange, p[0].id, p[2].id, false)
		},
		"remove exclusion": func() error {
			return s.h.svc.RemoveExclusion(t.Context(), zero, s.group, s.exchange, p[0].id, p[1].id)
		},
	}
	for name, call := range calls {
		err := call()
		if !errors.Is(err, giftexchange.ErrUnauthenticated) {
			t.Errorf("%s: err = %v, want ErrUnauthenticated", name, err)
		}
	}
	assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))
}

func abusiveRawValues() map[string]string {
	return map[string]string{
		"10000 characters":       strings.Repeat("a", 10_000),
		"bidi override":          "on\u202eno",
		"bidi isolate":           "\u2066on",
		"zero-width only":        "\u200b\u200d",
		"combining marks":        "́́",
		"right-to-left text":     "שלום",
		"emoji":                  "\U0001F381",
		"invalid UTF-8":          "o\xff\xfen",
		"NUL":                    "on\x00",
		"everyone mention":       "@everyone",
		"user mention":           "<@" + fakeUserB + ">",
		"local time":             "2026-12-24 20:00",
		"relative time":          "in 3d",
		"time without offset":    "2026-12-24T20:00:00",
		"line break":             "on\noff",
		"leading space":          " zzq",
		"surrounding whitespace": "\t3d ",
	}
}

func TestSetRuleRejectsAbusiveRawValuesAndStoresNothing(t *testing.T) {
	s := newScene(t)
	before := takeSnapshot(t, s.h, s.exchange)
	abusive := abusiveRawValues()

	rules := []giftexchange.RuleName{
		giftexchange.RuleDrawAt, giftexchange.RuleRevealAt, giftexchange.RuleReminderLead,
		giftexchange.RuleDropoutPolicy, giftexchange.RuleAvoidLastRound, giftexchange.RuleQuestionsEnabled,
	}
	for name, raw := range abusive {
		for _, rule := range rules {
			_, err := s.set(t, s.organizer, rule, raw)
			if !errors.Is(err, giftexchange.ErrInvalidInput) {
				t.Errorf("%s for %s: err = %v, want ErrInvalidInput", name, rule, err)
				continue
			}
			if !slicesEqualOne(inputFields(err), string(rule)) {
				t.Errorf("%s for %s: fields = %v", name, rule, inputFields(err))
			}
			if strings.Contains(err.Error(), raw) {
				t.Errorf("%s for %s: error echoes the raw value", name, rule)
			}
		}
	}
	assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))
}

func TestCreateExchangeRejectsAbusiveRuleValuesAndStoresNothing(t *testing.T) {
	h := newService(t)
	group := h.newGroup(t)
	organizer := h.newPerson(t)

	rules := []giftexchange.RuleName{
		giftexchange.RuleDrawAt, giftexchange.RuleRevealAt, giftexchange.RuleReminderLead,
		giftexchange.RuleDropoutPolicy, giftexchange.RuleAvoidLastRound, giftexchange.RuleQuestionsEnabled,
	}
	for name, raw := range abusiveRawValues() {
		for _, rule := range rules {
			_, err := h.svc.CreateExchange(t.Context(), organizer.actor, giftexchange.CreateExchangeRequest{
				Group: group,
				Name:  "Winter exchange",
				Rules: []giftexchange.RuleChange{{Name: rule, Raw: raw}},
			})
			if !errors.Is(err, giftexchange.ErrInvalidInput) {
				t.Errorf("%s for %s: err = %v, want ErrInvalidInput", name, rule, err)
				continue
			}
			if !slicesEqualOne(inputFields(err), string(rule)) {
				t.Errorf("%s for %s: fields = %v", name, rule, inputFields(err))
			}
			if strings.Contains(err.Error(), raw) {
				t.Errorf("%s for %s: error echoes the raw value", name, rule)
			}
		}
	}
	listed, listErr := h.svc.ExchangesFor(t.Context(), organizer.actor, &group)
	if listErr != nil || len(listed) != 0 {
		t.Errorf("exchanges after refused creates = %d, %v; want none", len(listed), listErr)
	}
}

func slicesEqualOne(got []string, want string) bool {
	return len(got) == 1 && got[0] == want
}

func TestRepeatedAndOutOfOrderRuleCallsKeepTheLastValidState(t *testing.T) {
	s, p := newTrio(t)
	reveal := s.h.futureText(72 * time.Hour)

	if _, err := s.set(t, s.organizer, giftexchange.RuleRevealAt, reveal); err != nil {
		t.Fatalf("SetRule: %v", err)
	}
	if err := s.add(t, s.organizer, p[0], p[1], false); err != nil {
		t.Fatalf("AddExclusion: %v", err)
	}
	s.moveTo(t, giftexchange.StateCancelled)

	_, setErr := s.set(t, s.organizer, giftexchange.RuleRevealAt, "unset")
	removeErr := s.remove(t, s.organizer, p[0], p[1])

	if !errors.Is(setErr, giftexchange.ErrRuleLocked) {
		t.Errorf("set rule after cancel: err = %v, want ErrRuleLocked", setErr)
	}
	if !errors.Is(removeErr, giftexchange.ErrInvalidState) {
		t.Errorf("remove exclusion after cancel: err = %v, want ErrInvalidState", removeErr)
	}
	views, err := s.h.svc.Rules(t.Context(), s.organizer.actor, s.group, s.exchange)
	if err != nil {
		t.Fatalf("Rules: %v", err)
	}
	if got := ruleNamed(t, views, giftexchange.RuleRevealAt).Value; got != reveal {
		t.Errorf("reveal_at = %q, want %q kept", got, reveal)
	}
	if got := ruleNamed(t, views, giftexchange.RuleExclusions).Value; got != "1" {
		t.Errorf("exclusions = %q, want 1 kept", got)
	}
}

func TestRulesAndExclusionsRevealNoPairToNonOrganizers(t *testing.T) {
	s, p := newTrio(t)
	outsider := s.h.newPerson(t)
	if err := s.add(t, s.organizer, p[0], p[1], false); err != nil {
		t.Fatalf("AddExclusion: %v", err)
	}
	if err := s.add(t, s.organizer, p[1], p[2], true); err != nil {
		t.Fatalf("AddExclusion: %v", err)
	}

	secrets := []string{
		s.organizer.id.String(), s.organizer.subject,
		outsider.id.String(), outsider.subject,
	}
	for _, member := range p {
		secrets = append(secrets, member.id.String(), member.subject)
	}

	var errs []error
	for _, caller := range []person{p[0], p[2], outsider} {
		rules, err := s.h.svc.Rules(t.Context(), caller.actor, s.group, s.exchange)
		if err != nil {
			t.Fatalf("Rules: %v", err)
		}
		if got := ruleNamed(t, rules, giftexchange.RuleExclusions).Value; got != "3" {
			t.Errorf("exclusions rule value = %q, want the count 3", got)
		}
		assertNoSecrets(t, "rules", renderRules(rules), secrets)

		_, listErr := s.h.svc.Exclusions(t.Context(), caller.actor, s.group, s.exchange)
		errs = append(errs, listErr,
			s.add(t, caller, p[0], p[2], false),
			s.remove(t, caller, p[0], p[1]),
			errOf(s.set(t, caller, giftexchange.RuleAvoidLastRound, "on")))
	}
	errs = append(errs,
		s.add(t, s.organizer, p[0], outsider, false),
		s.add(t, s.organizer, p[0], p[0], false))

	assertConfidential(t, s.h, errs, secrets)
}

func renderRules(views []giftexchange.RuleView) string {
	var out strings.Builder
	for _, view := range views {
		out.WriteString(view.Value + "|" + view.Default + ";")
	}

	return out.String()
}
