package giftexchange_test

import (
	"errors"
	"testing"

	"pgregory.net/rapid"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

func TestRandomDropoutSequencesKeepAValidPermutationOrReopen(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(3, 8).Draw(rt, "participants")
		policy := rapid.SampledFrom([]string{"patch", "redraw"}).Draw(rt, "policy")
		density := rapid.IntRange(0, 40).Draw(rt, "exclusion percent")

		s := newDrawScene(t, n)
		_, ruleErr := s.set(t, s.organizer, giftexchange.RuleDropoutPolicy, policy)
		if ruleErr != nil {
			rt.Fatalf("SetRule: %v", ruleErr)
		}
		excluded := make(map[[2]giftexchange.PersonID]bool)
		for _, giver := range s.people {
			for _, recipient := range s.people {
				isChosen := giver.id != recipient.id && rapid.IntRange(0, 99).Draw(rt, "roll") < density
				if isChosen {
					excluded[[2]giftexchange.PersonID{giver.id, recipient.id}] = true
					s.exclude(t, giver, recipient, false)
				}
			}
		}
		drawErr := s.draw(t)
		if errors.Is(drawErr, giftexchange.ErrNoValidAssignment) {
			return
		}
		if drawErr != nil {
			rt.Fatalf("Draw: %v", drawErr)
		}

		remaining := append([]person(nil), s.people...)
		for len(remaining) > 0 {
			leaverAt := rapid.IntRange(0, len(remaining)-1).Draw(rt, "leaver")
			leaver := remaining[leaverAt]
			remaining = append(remaining[:leaverAt:leaverAt], remaining[leaverAt+1:]...)

			outcome, err := s.h.svc.Leave(t.Context(), leaver.actor, s.group, s.exchange)
			if err != nil {
				rt.Fatalf("Leave: %v", err)
			}

			if outcome == giftexchange.OutcomeReopened {
				state, _ := s.stateAndRound(t)
				if state != "open" || s.assignmentRows(t) != 0 {
					rt.Fatalf("a void left state %s with %d assignments", state, s.assignmentRows(t))
				}
				return
			}

			recipients := assignmentsOf(t, s.h, s.exchange, remaining)
			if problem := permutationProblem(remaining, recipients); problem != "" {
				rt.Fatalf("after %v with %d left: %s", outcome, len(remaining), problem)
			}
			for _, p := range remaining {
				if excluded[[2]giftexchange.PersonID{p.id, recipients[p.id]}] {
					rt.Fatalf("after %v: an excluded pair is assigned", outcome)
				}
			}
		}
	})
}
