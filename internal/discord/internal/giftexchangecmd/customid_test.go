package giftexchangecmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

const (
	testExchange = "0198a3b0-0000-7000-8000-000000000001"
	testPerson   = "0198a3b0-0000-7000-8000-000000000002"
	testQuestion = "0198a3b0-0000-7000-8000-000000000003"
)

func mustExchangeID(tb testing.TB, raw string) giftexchange.ExchangeID {
	tb.Helper()

	id, err := giftexchange.ParseExchangeID(raw)
	if err != nil {
		tb.Fatalf("ParseExchangeID(%q): %v", raw, err)
	}

	return id
}

func allActions() []action {
	return []action{
		actionJoin, actionLeave, actionRules, actionLeaveConfirm, actionDrawConfirm,
		actionRevealConfirm, actionCancelConfirm, actionRemoveConfirm, actionDismiss,
		actionRulePick, actionRuleOption, actionRuleModal, actionAnswer, actionAnswerModal,
	}
}

func sampleCustomID(tb testing.TB, a action) customID {
	tb.Helper()

	c := customID{Action: a}
	switch a {
	case actionAnswer, actionAnswerModal:
		question, err := giftexchange.ParseQuestionID(testQuestion)
		if err != nil {
			tb.Fatalf("ParseQuestionID: %v", err)
		}
		c.Question = question
		return c
	case actionRemoveConfirm:
		person, err := giftexchange.ParsePersonID(testPerson)
		if err != nil {
			tb.Fatalf("ParsePersonID: %v", err)
		}
		c.Person = person
	case actionRuleOption, actionRuleModal:
		c.Rule = giftexchange.RuleName("draw_at")
	default:
	}
	c.Exchange = mustExchangeID(tb, testExchange)

	return c
}

func TestCustomIDRoundTripsForEveryAction(t *testing.T) {
	for _, a := range allActions() {
		t.Run(string(a), func(t *testing.T) {
			want := sampleCustomID(t, a)

			formatted := formatCustomID(want)
			got, err := parseCustomID(formatted)
			if err != nil {
				t.Fatalf("parseCustomID(%q): %v", formatted, err)
			}
			if got != want {
				t.Errorf("parseCustomID(%q) = %+v, want %+v", formatted, got, want)
			}
			if len(formatted) > maxCustomIDLength {
				t.Errorf("custom ID has %d characters, want at most %d", len(formatted), maxCustomIDLength)
			}
			if !strings.HasPrefix(formatted, "/gx/"+string(a)+"/") {
				t.Errorf("custom ID %q does not start with the action", formatted)
			}
		})
	}
}

func TestLongestCustomIDFitsTheLimit(t *testing.T) {
	c := sampleCustomID(t, actionRemoveConfirm)

	if got := len(formatCustomID(c)); got != 92 {
		t.Errorf("remove-confirm custom ID has %d characters, want 92", got)
	}
}

func TestAnswerCustomIDEqualsWhatTheNotifierBuilds(t *testing.T) {
	c := sampleCustomID(t, actionAnswer)

	want := customIDAnswerPrefix + c.Question.String()
	if got := formatCustomID(c); got != want {
		t.Errorf("formatCustomID = %q, want %q", got, want)
	}
}

func TestParseCustomIDRefusesMalformedInput(t *testing.T) {
	cases := map[string]string{
		"empty":                       "",
		"no leading slash":            "gx/join/" + testExchange,
		"other prefix":                "/zz/join/" + testExchange,
		"prefix only":                 "/gx/",
		"unknown action":              "/gx/explode/" + testExchange,
		"action with different case":  "/gx/JOIN/" + testExchange,
		"missing id":                  "/gx/join",
		"empty id":                    "/gx/join/",
		"extra segment":               "/gx/join/" + testExchange + "/extra",
		"trailing slash":              "/gx/join/" + testExchange + "/",
		"missing person":              "/gx/remove-confirm/" + testExchange,
		"missing rule":                "/gx/rule-option/" + testExchange,
		"extra on answer":             "/gx/answer/" + testQuestion + "/x",
		"bad uuid":                    "/gx/join/not-a-uuid",
		"zero uuid":                   "/gx/join/00000000-0000-0000-0000-000000000000",
		"upper case uuid":             "/gx/join/" + strings.ToUpper(testExchange),
		"uuid without dashes":         "/gx/join/" + strings.ReplaceAll(testExchange, "-", ""),
		"upper case question uuid":    "/gx/answer/" + strings.ToUpper(testQuestion),
		"upper case person uuid":      "/gx/remove-confirm/" + testExchange + "/" + strings.ToUpper(testPerson),
		"rule above z":                "/gx/rule-modal/" + testExchange + "/draw{",
		"rule below a":                "/gx/rule-modal/" + testExchange + "/draw@",
		"bad person uuid":             "/gx/remove-confirm/" + testExchange + "/zzz",
		"snowflake as id":             "/gx/join/100000000000000001",
		"rule with digits":            "/gx/rule-option/" + testExchange + "/draw1",
		"rule with upper case":        "/gx/rule-modal/" + testExchange + "/Draw_at",
		"rule with dash":              "/gx/rule-modal/" + testExchange + "/draw-at",
		"empty rule":                  "/gx/rule-modal/" + testExchange + "/",
		"rule over 32 characters":     "/gx/rule-modal/" + testExchange + "/" + strings.Repeat("a", 33),
		"over 100 characters":         "/gx/rule-option/" + testExchange + "/" + strings.Repeat("a", 60),
		"non ascii":                   "/gx/join/" + testExchange + "\u00e9",
		"null byte":                   "/gx/join/" + testExchange + "\x00",
		"path traversal":              "/gx/../gx/join/" + testExchange,
		"answer with a bad id":        "/gx/answer/not-a-uuid",
		"whitespace before":           " /gx/join/" + testExchange,
		"newline after":               "/gx/join/" + testExchange + "\n",
		"double slash before the id":  "/gx/join//" + testExchange,
		"double slash after the head": "/gx//join/" + testExchange,
	}

	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := parseCustomID(raw)
			if !errors.Is(err, errBadCustomID) {
				t.Errorf("parseCustomID(%q) = %+v, %v; want errBadCustomID", raw, got, err)
			}
		})
	}
}

func TestParseCustomIDAcceptsARuleOfThirtyTwoCharacters(t *testing.T) {
	rule := strings.Repeat("a", 32)

	got, err := parseCustomID("/gx/rule-option/" + testExchange + "/" + rule)
	if err != nil {
		t.Fatalf("parseCustomID: %v", err)
	}
	if got.Rule != giftexchange.RuleName(rule) {
		t.Errorf("rule = %q, want %q", got.Rule, rule)
	}
}

func FuzzParseCustomID(f *testing.F) {
	seeds := []string{
		"", "/gx/", "/gx/join/" + testExchange, "/gx/answer/" + testQuestion,
		"/gx/remove-confirm/" + testExchange + "/" + testPerson,
		"/gx/rule-option/" + testExchange + "/draw_at", "gx/join/x",
		"/gx/join/" + strings.ToUpper(testExchange),
		"/gx/" + strings.Repeat("a", 200), "/gx/join/\u202e" + testExchange,
		"/gx/join/" + testExchange + "\x00", "/gx/join/%s", "/gx/join/\U0001F600",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		parsed, err := parseCustomID(raw)
		if err != nil {
			if !errors.Is(err, errBadCustomID) {
				t.Fatalf("parseCustomID(%q) error = %v, want errBadCustomID", raw, err)
			}
			return
		}

		formatted := formatCustomID(parsed)
		if formatted != raw {
			t.Errorf("formatCustomID(parseCustomID(%q)) = %q", raw, formatted)
		}
		if len(formatted) > maxCustomIDLength {
			t.Errorf("custom ID %q has %d characters", formatted, len(formatted))
		}
	})
}
