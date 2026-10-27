package giftexchange_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

func TestTypedErrorsMatchTheirSentinels(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		sentinel error
	}{
		{"input", &giftexchange.InputError{Field: "name", Reason: "is empty"}, giftexchange.ErrInvalidInput},
		{"state", &giftexchange.StateError{Op: "draw", State: giftexchange.StateDrawn}, giftexchange.ErrInvalidState},
		{"rule locked", &giftexchange.RuleLockedError{Rule: giftexchange.RuleDrawAt, State: giftexchange.StateDrawn}, giftexchange.ErrRuleLocked},
		{"infeasible", &giftexchange.InfeasibleError{Stranded: 2}, giftexchange.ErrNoValidAssignment},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wrapped := fmt.Errorf("operation: %w", tc.err)
			if !errors.Is(wrapped, tc.sentinel) {
				t.Fatalf("%v does not match %v", wrapped, tc.sentinel)
			}
			if errors.Is(wrapped, giftexchange.ErrNotFound) {
				t.Fatalf("%v must not match ErrNotFound", wrapped)
			}
		})
	}
}

func TestTypedErrorsAreReachableWithAs(t *testing.T) {
	var state *giftexchange.StateError
	wrapped := fmt.Errorf("op: %w", &giftexchange.StateError{Op: "join", State: giftexchange.StateRevealed})
	if !errors.As(wrapped, &state) || state.State != giftexchange.StateRevealed || state.Op != "join" {
		t.Fatalf("As gave %+v", state)
	}

	var locked *giftexchange.RuleLockedError
	wrapped = fmt.Errorf("op: %w", &giftexchange.RuleLockedError{Rule: giftexchange.RuleRevealAt, State: giftexchange.StateRevealed})
	if !errors.As(wrapped, &locked) || locked.Rule != giftexchange.RuleRevealAt {
		t.Fatalf("As gave %+v", locked)
	}

	var infeasible *giftexchange.InfeasibleError
	wrapped = fmt.Errorf("op: %w", &giftexchange.InfeasibleError{Stranded: 3})
	if !errors.As(wrapped, &infeasible) || infeasible.Stranded != 3 {
		t.Fatalf("As gave %+v", infeasible)
	}
}

func TestErrorTextCarriesOnlyFixedFields(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{&giftexchange.InputError{Field: "name", Reason: "is empty"}, "giftexchange: invalid input: name is empty"},
		{&giftexchange.StateError{Op: "draw", State: giftexchange.StateOpen}, "giftexchange: not allowed in the current state: draw in state open"},
		{&giftexchange.RuleLockedError{Rule: giftexchange.RuleDrawAt, State: giftexchange.StateDrawn}, "giftexchange: rule can no longer change: draw_at in state drawn"},
		{&giftexchange.InfeasibleError{Stranded: 2}, "giftexchange: exclusions leave no valid assignment: 2 givers stranded"},
	}
	for _, tc := range tests {
		if got := tc.err.Error(); got != tc.want {
			t.Errorf("Error() = %q, want %q", got, tc.want)
		}
	}
}

func TestSentinelsAreDistinct(t *testing.T) {
	all := []error{
		giftexchange.ErrUnauthenticated, giftexchange.ErrNotFound, giftexchange.ErrForbidden,
		giftexchange.ErrInvalidInput, giftexchange.ErrInvalidState, giftexchange.ErrAlreadyParticipant,
		giftexchange.ErrNotParticipant, giftexchange.ErrExchangeFull, giftexchange.ErrTooFewParticipants,
		giftexchange.ErrNoValidAssignment, giftexchange.ErrUnknownRule, giftexchange.ErrRuleFixed,
		giftexchange.ErrRuleLocked, giftexchange.ErrQuestionsDisabled, giftexchange.ErrQuestionClosed,
		giftexchange.ErrAlreadyAnswered, giftexchange.ErrRateLimited, giftexchange.ErrRephraseUnavailable,
		giftexchange.ErrUndeliverable,
	}
	for i, a := range all {
		for j, b := range all {
			if i != j && errors.Is(a, b) {
				t.Errorf("%v matches %v", a, b)
			}
		}
	}
}

func TestAllInputErrorCodesAreNonEmptyAndUnique(t *testing.T) {
	codes := giftexchange.AllInputErrorCodes()
	if len(codes) == 0 {
		t.Fatal("AllInputErrorCodes returned no codes")
	}

	seen := make(map[giftexchange.InputErrorCode]bool, len(codes))
	for _, code := range codes {
		if code == "" {
			t.Error("AllInputErrorCodes contains an empty code")
		}
		if seen[code] {
			t.Errorf("AllInputErrorCodes contains duplicate code %q", code)
		}
		seen[code] = true
	}
}
