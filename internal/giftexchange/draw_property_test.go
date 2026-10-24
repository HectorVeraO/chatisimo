package giftexchange_test

import (
	"errors"
	"testing"

	"pgregory.net/rapid"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

// bruteForceFindsAssignment is the independent oracle: it tries every permutation of n people
// and reports whether one has no self pair and no excluded pair.
func bruteForceFindsAssignment(n int, excluded [][]bool) bool {
	used := make([]bool, n)

	var place func(giver int) bool
	place = func(giver int) bool {
		if giver == n {
			return true
		}
		for recipient := range n {
			isUsable := !used[recipient] && recipient != giver && !excluded[giver][recipient]
			if !isUsable {
				continue
			}
			used[recipient] = true
			found := place(giver + 1)
			used[recipient] = false
			if found {
				return true
			}
		}
		return false
	}

	return place(0)
}

func TestDrawSucceedsWithAValidPermutationExactlyWhenOneExists(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(3, 8).Draw(rt, "participants")
		density := rapid.IntRange(0, 70).Draw(rt, "exclusion percent")
		isMutual := rapid.Bool().Draw(rt, "mutual exclusions")

		s := newDrawScene(t, n)
		excluded := make([][]bool, n)
		for i := range excluded {
			excluded[i] = make([]bool, n)
		}
		for giver := range n {
			for recipient := range n {
				isChosen := giver != recipient && rapid.IntRange(0, 99).Draw(rt, "exclusion roll") < density
				if !isChosen {
					continue
				}
				excluded[giver][recipient] = true
				if isMutual {
					excluded[recipient][giver] = true
				}
				s.exclude(t, s.people[giver], s.people[recipient], isMutual)
			}
		}

		err := s.draw(t)

		if err != nil && !errors.Is(err, giftexchange.ErrNoValidAssignment) {
			rt.Fatalf("Draw: unexpected error %v", err)
		}
		hasOracle := n <= 8
		if hasOracle {
			exists := bruteForceFindsAssignment(n, excluded)
			if (err == nil) != exists {
				rt.Fatalf("n=%d: Draw error is %v, brute force finds an assignment: %v", n, err, exists)
			}
		}

		if err != nil {
			view, viewErr := s.h.svc.Exchange(t.Context(), s.organizer.actor, s.group, s.exchange)
			if viewErr != nil || view.State != giftexchange.StateOpen {
				rt.Fatalf("a failed draw left the exchange in state %v (%v)", view.State, viewErr)
			}
			return
		}

		recipients := assignmentsOf(t, s.h, s.exchange, s.people)
		if problem := permutationProblem(s.people, recipients); problem != "" {
			rt.Fatalf("n=%d: %s", n, problem)
		}
		for giver, p := range s.people {
			for recipient, q := range s.people {
				if excluded[giver][recipient] && recipients[p.id] == q.id {
					rt.Fatalf("n=%d: an excluded pair was drawn", n)
				}
			}
		}
	})
}
