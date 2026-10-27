package giftexchangecmd_test

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/discord/internal/giftexchangecmd"
	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

const testReference = "01a16eec-90c4-701c-a17c-6d3c5a3b3c1d"

func TestMapErrorShowsTheTextAndOutcomeOfEveryModuleError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		err          error
		wantOutcome  string
		wantContains string
	}{
		{"not found", giftexchange.ErrNotFound, "rejected:not_found", "That exchange does not exist here."},
		{"forbidden", giftexchange.ErrForbidden, "rejected:forbidden", "You are not allowed to do that in this exchange."},
		{"already participant", giftexchange.ErrAlreadyParticipant, "rejected:already_participant", "You are already in this exchange."},
		{"not participant", giftexchange.ErrNotParticipant, "rejected:not_participant", "You are not in this exchange."},
		{"full", giftexchange.ErrExchangeFull, "rejected:full", strconv.Itoa(giftexchange.MaxParticipants)},
		{"too few", giftexchange.ErrTooFewParticipants, "rejected:too_few", strconv.Itoa(giftexchange.MinParticipants)},
		{"infeasible", &giftexchange.InfeasibleError{Stranded: 2}, "rejected:infeasible", "2"},
		{"unknown rule", giftexchange.ErrUnknownRule, "rejected:rule", "Unknown rule."},
		{"unknown rule from the parser", unknownRuleFromParser(), "rejected:rule", "Unknown rule."},
		{"fixed rule", giftexchange.ErrRuleFixed, "rejected:rule", "This rule can't be changed."},
		{
			"locked rule",
			&giftexchange.RuleLockedError{Rule: giftexchange.RuleDrawAt, State: giftexchange.StateDrawn},
			"rejected:rule", "drawn",
		},
		{"questions off", giftexchange.ErrQuestionsDisabled, "rejected:questions_off", "The organizer turned off questions for this exchange."},
		{"rate limited", giftexchange.ErrRateLimited, "rejected:rate_limited", "You reached the limit of 20 questions per day for this exchange."},
		{"rephrase unavailable", giftexchange.ErrRephraseUnavailable, "rejected:rephrase_unavailable", "Could not rephrase your question right now. Your question was not sent; please try again."},
		{"question closed", giftexchange.ErrQuestionClosed, "rejected:question", "This question is closed and can no longer be answered."},
		{"already answered", giftexchange.ErrAlreadyAnswered, "rejected:question", "You already answered this question."},
		{
			"invalid state",
			&giftexchange.StateError{Op: "draw", State: giftexchange.StateRevealed},
			"rejected:state", "revealed",
		},
		{"unauthenticated", giftexchange.ErrUnauthenticated, "error", testReference},
		{"anything else", errors.New("disk on fire"), "error", testReference},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := giftexchangecmd.MapError(testReference, fmt.Errorf("wrapped: %w", tt.err))

			if got.Outcome != tt.wantOutcome {
				t.Errorf("outcome = %q, want %q", got.Outcome, tt.wantOutcome)
			}
			if tt.wantContains == "" || !strings.Contains(got.Text, tt.wantContains) {
				t.Errorf("text %q does not contain %q", got.Text, tt.wantContains)
			}
			if got.Internal != (tt.wantOutcome == "error") {
				t.Errorf("Internal = %v for outcome %s", got.Internal, got.Outcome)
			}
		})
	}
}

func TestMapErrorListsEveryInvalidFieldAndEscapesThem(t *testing.T) {
	t.Parallel()

	joined := errors.Join(
		&giftexchange.InputError{Field: "draw_at", Reason: "is in the past"},
		fmt.Errorf("outer: %w", errors.Join(&giftexchange.InputError{Field: "name", Reason: "is required"})),
	)

	got := giftexchangecmd.MapError(testReference, joined)

	if got.Outcome != "rejected:invalid_input" {
		t.Errorf("outcome = %s", got.Outcome)
	}
	for _, want := range []string{`draw\_at is in the past`, "name is required"} {
		if !strings.Contains(got.Text, want) {
			t.Errorf("text %q lacks %q", got.Text, want)
		}
	}
}

func TestMapErrorCapsALongListOfInvalidFields(t *testing.T) {
	t.Parallel()

	var problems []error
	for range 60 {
		problems = append(problems, &giftexchange.InputError{Field: "rule", Reason: "is invalid"})
	}

	got := giftexchangecmd.MapError(testReference, errors.Join(problems...))

	const maxLines = 25
	if lines := strings.Count(got.Text, "\n"); lines > maxLines {
		t.Errorf("%d lines in the reply for 60 problems, want a bounded list", lines)
	}
	if !strings.Contains(got.Text, "40") {
		t.Errorf("text does not say how many are left: %q", got.Text)
	}
}

func TestMapErrorTextNeverHoldsTheErrorString(t *testing.T) {
	t.Parallel()

	secret := "snowflake 100000000000000001"
	got := giftexchangecmd.MapError(testReference, fmt.Errorf("%s: %w", secret, giftexchange.ErrForbidden))

	if strings.Contains(got.Text, secret) {
		t.Errorf("the reply holds the error string: %q", got.Text)
	}
}

func TestGuildNameOrUnknown(t *testing.T) {
	t.Parallel()

	if got := giftexchangecmd.GuildNameOrUnknown(""); got == "" {
		t.Error("an empty guild name gives an empty text")
	}
	if got := giftexchangecmd.GuildNameOrUnknown("Test server"); got != "Test server" {
		t.Errorf("name = %q", got)
	}
	if got := giftexchangecmd.GuildNameOrUnknownIn(giftexchangecmd.LocaleES419, ""); got != "servidor desconocido" {
		t.Errorf("GuildNameOrUnknownIn(es-419, empty) = %q, want servidor desconocido", got)
	}
	if got := giftexchangecmd.GuildNameOrUnknownIn("fr", ""); got != "unknown server" {
		t.Errorf("GuildNameOrUnknownIn(fr, empty) = %q, want fallback unknown server", got)
	}
}

func TestMapErrorInLocalizesAndFallsBackToEnglish(t *testing.T) {
	t.Parallel()

	es := giftexchangecmd.MapErrorIn(giftexchangecmd.LocaleES419, testReference, giftexchange.ErrNotFound)
	if es.Outcome != "rejected:not_found" || es.Text != "Ese intercambio no existe aquí." {
		t.Errorf("MapErrorIn(es-419) = %+v", es)
	}

	fallback := giftexchangecmd.MapErrorIn("pt-BR", testReference, giftexchange.ErrNotFound)
	if fallback.Outcome != "rejected:not_found" || fallback.Text != "That exchange does not exist here." {
		t.Errorf("MapErrorIn(pt-BR) = %+v", fallback)
	}
}

func TestNewReportsEveryMissingDependency(t *testing.T) {
	t.Parallel()

	_, err := giftexchangecmd.New(nil, nil, nil, nil, nil)
	if err == nil {
		t.Fatal("New accepted nil dependencies")
	}
	for _, want := range []string{"service", "guild name", "clock", "location", "logger"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}

func unknownRuleFromParser() error {
	_, err := giftexchange.ParseRuleName("no-such-rule")
	return fmt.Errorf("parse rule: %w", err)
}
