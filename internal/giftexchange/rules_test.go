package giftexchange_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

var allRuleNames = []giftexchange.RuleName{
	giftexchange.RuleDrawAt,
	giftexchange.RuleRevealAt,
	giftexchange.RuleReminderLead,
	giftexchange.RuleDropoutPolicy,
	giftexchange.RuleAvoidLastRound,
	giftexchange.RuleExclusions,
	giftexchange.RuleQuestionsEnabled,
	giftexchange.RuleLanguage,
	giftexchange.RuleMinParticipants,
	giftexchange.RuleMaxParticipants,
	giftexchange.RuleNoSelfAssignment,
	giftexchange.RuleRedrawBelow,
	giftexchange.RuleQuestionMaxLength,
	giftexchange.RuleQuestionsPerDay,
}

func TestParseRuleNameAcceptsEveryRule(t *testing.T) {
	for _, name := range allRuleNames {
		t.Run(string(name), func(t *testing.T) {
			got, err := giftexchange.ParseRuleName(string(name))
			if err != nil || got != name {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
}

func TestParseRuleNameRejectsUnknown(t *testing.T) {
	for _, raw := range []string{"", "Draw_At", " draw_at", "draw_at ", "nope", "draw_at\x00"} {
		t.Run("rejects "+raw, func(t *testing.T) {
			_, err := giftexchange.ParseRuleName(raw)
			if !errors.Is(err, giftexchange.ErrInvalidInput) || !errors.Is(err, giftexchange.ErrUnknownRule) {
				t.Fatalf("err = %v, want ErrInvalidInput and ErrUnknownRule", err)
			}
			var inputErr *giftexchange.InputError
			if !errors.As(err, &inputErr) || inputErr.Code != giftexchange.InputCodeUnknown {
				t.Fatalf("err = %v, want *InputError with code %q", err, giftexchange.InputCodeUnknown)
			}
		})
	}
}

func TestParseRuleValueCanonicalForms(t *testing.T) {
	tests := []struct {
		name string
		rule giftexchange.RuleName
		in   string
		want string
	}{
		{"time unset", giftexchange.RuleDrawAt, "unset", "unset"},
		{"time with negative offset becomes UTC", giftexchange.RuleDrawAt, "2026-12-24T23:00:00-03:00", "2026-12-25T02:00:00Z"},
		{"time already UTC", giftexchange.RuleRevealAt, "2026-12-25T02:00:00Z", "2026-12-25T02:00:00Z"},
		{"time fractional seconds truncated", giftexchange.RuleDrawAt, "2026-12-25T02:00:00.999Z", "2026-12-25T02:00:00Z"},
		{"time year 2000", giftexchange.RuleDrawAt, "2000-01-01T00:00:00Z", "2000-01-01T00:00:00Z"},
		{"time year 9999", giftexchange.RuleDrawAt, "9999-12-31T23:59:59Z", "9999-12-31T23:59:59Z"},
		{"time 1999 offset that lands in 2000", giftexchange.RuleDrawAt, "1999-12-31T23:00:00-03:00", "2000-01-01T02:00:00Z"},
		{"duration zero", giftexchange.RuleReminderLead, "0", "0s"},
		{"duration 0s", giftexchange.RuleReminderLead, "0s", "0s"},
		{"duration days", giftexchange.RuleReminderLead, "3d", "3d"},
		{"duration days and hours", giftexchange.RuleReminderLead, "1d12h", "36h0m0s"},
		{"duration hours", giftexchange.RuleReminderLead, "36h", "36h0m0s"},
		{"duration 48h is whole days", giftexchange.RuleReminderLead, "48h", "2d"},
		{"duration minutes", giftexchange.RuleReminderLead, "90m", "1h30m0s"},
		{"duration maximum", giftexchange.RuleReminderLead, "14d", "14d"},
		{"duration maximum in hours", giftexchange.RuleReminderLead, "336h", "14d"},
		{"enum patch", giftexchange.RuleDropoutPolicy, "patch", "patch"},
		{"enum redraw", giftexchange.RuleDropoutPolicy, "redraw", "redraw"},
		{"enum language en", giftexchange.RuleLanguage, "en", "en"},
		{"enum language es-419", giftexchange.RuleLanguage, "es-419", "es-419"},
		{"bool on", giftexchange.RuleAvoidLastRound, "on", "on"},
		{"bool true", giftexchange.RuleAvoidLastRound, "true", "on"},
		{"bool off", giftexchange.RuleQuestionsEnabled, "off", "off"},
		{"bool false", giftexchange.RuleQuestionsEnabled, "false", "off"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := giftexchange.ParseRuleValue(tc.rule, tc.in)
			if err != nil {
				t.Fatalf("unexpected error %v", err)
			}
			if got.Rule() != tc.rule || got.String() != tc.want {
				t.Fatalf("got %q for %q, want %q", got.String(), got.Rule(), tc.want)
			}
			again, err := giftexchange.ParseRuleValue(got.Rule(), got.String())
			if err != nil || again != got {
				t.Fatalf("round trip gave %v, %v; want %v", again, err, got)
			}
		})
	}
}

func TestParseRuleValueRejectsBadForms(t *testing.T) {
	dropoutAllowed := []string{string(giftexchange.DropoutPatch), string(giftexchange.DropoutRedraw)}
	languageAllowed := []string{string(giftexchange.LanguageEN), string(giftexchange.LanguageES419)}
	tests := []struct {
		name        string
		rule        giftexchange.RuleName
		in          string
		wantCode    giftexchange.InputErrorCode
		wantMin     int
		wantMax     int
		wantAllowed []string
	}{
		{"time empty", giftexchange.RuleDrawAt, "", giftexchange.InputCodeInvalidInstant, 0, 0, nil},
		{"time without offset", giftexchange.RuleDrawAt, "2026-12-24T23:00:00", giftexchange.InputCodeInvalidInstant, 0, 0, nil},
		{"time date only", giftexchange.RuleDrawAt, "2026-12-24", giftexchange.InputCodeInvalidInstant, 0, 0, nil},
		{"time relative", giftexchange.RuleDrawAt, "tomorrow", giftexchange.InputCodeInvalidInstant, 0, 0, nil},
		{"time upper case unset", giftexchange.RuleDrawAt, "UNSET", giftexchange.InputCodeInvalidInstant, 0, 0, nil},
		{"time year 1999", giftexchange.RuleDrawAt, "1999-12-31T23:59:59Z", giftexchange.InputCodeYearOutOfRange, 2000, 9999, nil},
		{"time year 10000", giftexchange.RuleDrawAt, "10000-01-01T00:00:00Z", giftexchange.InputCodeInvalidInstant, 0, 0, nil},
		{"time lands in 10000 by offset", giftexchange.RuleDrawAt, "9999-12-31T23:00:00-03:00", giftexchange.InputCodeYearOutOfRange, 2000, 9999, nil},
		{"duration empty", giftexchange.RuleReminderLead, "", giftexchange.InputCodeInvalidDuration, 0, 0, nil},
		{"duration above maximum by a second", giftexchange.RuleReminderLead, "14d1s", giftexchange.InputCodeDurationOutOfRange, 0, 0, nil},
		{"duration 15 days", giftexchange.RuleReminderLead, "15d", giftexchange.InputCodeDurationOutOfRange, 0, 0, nil},
		{"duration huge days", giftexchange.RuleReminderLead, "99999999999d", giftexchange.InputCodeDurationOutOfRange, 0, 0, nil},
		{"duration negative", giftexchange.RuleReminderLead, "-1h", giftexchange.InputCodeInvalidDuration, 0, 0, nil},
		{"duration negative after days", giftexchange.RuleReminderLead, "1d-1h", giftexchange.InputCodeInvalidDuration, 0, 0, nil},
		{"duration sub-second", giftexchange.RuleReminderLead, "1.5s", giftexchange.InputCodeDurationOutOfRange, 0, 0, nil},
		{"duration milliseconds", giftexchange.RuleReminderLead, "500ms", giftexchange.InputCodeDurationOutOfRange, 0, 0, nil},
		{"duration bare number", giftexchange.RuleReminderLead, "5", giftexchange.InputCodeInvalidDuration, 0, 0, nil},
		{"duration only d", giftexchange.RuleReminderLead, "d", giftexchange.InputCodeInvalidDuration, 0, 0, nil},
		{"duration upper case d", giftexchange.RuleReminderLead, "3D", giftexchange.InputCodeInvalidDuration, 0, 0, nil},
		{"duration trailing garbage", giftexchange.RuleReminderLead, "3dx", giftexchange.InputCodeInvalidDuration, 0, 0, nil},
		{"duration overflow", giftexchange.RuleReminderLead, "9999999999999999h", giftexchange.InputCodeInvalidDuration, 0, 0, nil},
		{"enum unknown", giftexchange.RuleDropoutPolicy, "random", giftexchange.InputCodeNotOneOf, 0, 0, dropoutAllowed},
		{"enum upper case", giftexchange.RuleDropoutPolicy, "Patch", giftexchange.InputCodeNotOneOf, 0, 0, dropoutAllowed},
		{"enum empty", giftexchange.RuleDropoutPolicy, "", giftexchange.InputCodeNotOneOf, 0, 0, dropoutAllowed},
		{"language unknown", giftexchange.RuleLanguage, "fr", giftexchange.InputCodeNotOneOf, 0, 0, languageAllowed},
		{"language es-ES", giftexchange.RuleLanguage, "es-ES", giftexchange.InputCodeNotOneOf, 0, 0, languageAllowed},
		{"language empty", giftexchange.RuleLanguage, "", giftexchange.InputCodeNotOneOf, 0, 0, languageAllowed},
		{"bool upper case", giftexchange.RuleAvoidLastRound, "ON", giftexchange.InputCodeInvalidBool, 0, 0, nil},
		{"bool yes", giftexchange.RuleAvoidLastRound, "yes", giftexchange.InputCodeInvalidBool, 0, 0, nil},
		{"bool empty", giftexchange.RuleQuestionsEnabled, "", giftexchange.InputCodeInvalidBool, 0, 0, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := giftexchange.ParseRuleValue(tc.rule, tc.in)
			var inputErr *giftexchange.InputError
			if !errors.Is(err, giftexchange.ErrInvalidInput) || !errors.As(err, &inputErr) {
				t.Fatalf("err = %v, want an *InputError", err)
			}
			if inputErr.Field != string(tc.rule) {
				t.Fatalf("Field = %q, want %q", inputErr.Field, tc.rule)
			}
			if inputErr.Code != tc.wantCode {
				t.Errorf("Code = %q, want %q", inputErr.Code, tc.wantCode)
			}
			if inputErr.Min != tc.wantMin || inputErr.Max != tc.wantMax {
				t.Errorf("Min, Max = %d, %d; want %d, %d", inputErr.Min, inputErr.Max, tc.wantMin, tc.wantMax)
			}
			if !slices.Equal(inputErr.Allowed, tc.wantAllowed) {
				t.Errorf("Allowed = %v, want %v", inputErr.Allowed, tc.wantAllowed)
			}
		})
	}
}

func TestParseRuleValueFixedRules(t *testing.T) {
	fixed := []giftexchange.RuleName{
		giftexchange.RuleMinParticipants,
		giftexchange.RuleMaxParticipants,
		giftexchange.RuleNoSelfAssignment,
		giftexchange.RuleRedrawBelow,
		giftexchange.RuleQuestionMaxLength,
		giftexchange.RuleQuestionsPerDay,
	}
	for _, name := range fixed {
		t.Run(string(name), func(t *testing.T) {
			_, err := giftexchange.ParseRuleValue(name, "3")
			if !errors.Is(err, giftexchange.ErrRuleFixed) {
				t.Fatalf("err = %v, want ErrRuleFixed", err)
			}
			if errors.Is(err, giftexchange.ErrInvalidInput) {
				t.Fatalf("err = %v must not match ErrInvalidInput", err)
			}
		})
	}
}

func TestParseRuleValueExclusionsIsEditedElsewhere(t *testing.T) {
	_, err := giftexchange.ParseRuleValue(giftexchange.RuleExclusions, "0")
	var inputErr *giftexchange.InputError
	if !errors.As(err, &inputErr) || inputErr.Field != "rule" || inputErr.Code != giftexchange.InputCodeEditedViaExclusions {
		t.Fatalf("err = %v, want *InputError{Field: rule, Code: %q}", err, giftexchange.InputCodeEditedViaExclusions)
	}
}

func TestParseRuleValueUnknownRule(t *testing.T) {
	_, err := giftexchange.ParseRuleValue("nope", "on")
	if !errors.Is(err, giftexchange.ErrInvalidInput) || !errors.Is(err, giftexchange.ErrUnknownRule) {
		t.Fatalf("err = %v, want ErrInvalidInput and ErrUnknownRule", err)
	}
}

func TestRuleValueZeroIsInvalid(t *testing.T) {
	var zero giftexchange.RuleValue
	if zero.Rule() != "" || zero.String() != "" {
		t.Fatalf("zero value = %q %q, want empty", zero.Rule(), zero.String())
	}
}

func TestParseRuleValueErrorsDoNotEchoInput(t *testing.T) {
	const secret = "zzz-secret-input"
	for _, name := range allRuleNames {
		_, err := giftexchange.ParseRuleValue(name, secret)
		if err == nil {
			t.Fatalf("rule %q accepted garbage", name)
		}
		if contains(err.Error(), secret) {
			t.Fatalf("rule %q error %q echoes the input", name, err.Error())
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
