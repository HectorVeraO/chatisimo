package giftexchange_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

// racing runs every call at once, released together, and returns their errors in call order.
func racing(calls ...func() error) []error {
	errs := make([]error, len(calls))
	start := make(chan struct{})

	var wg sync.WaitGroup
	for i, call := range calls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = call()
		}()
	}
	close(start)
	wg.Wait()

	return errs
}

func TestTwoConcurrentDrawsLetExactlyOneSucceed(t *testing.T) {
	for range 5 {
		s := newDrawScene(t, 4)
		draw := func() error { return s.h.svc.Draw(t.Context(), s.organizer.actor, s.group, s.exchange) }

		errs := racing(draw, draw)

		succeeded := 0
		for _, err := range errs {
			switch {
			case err == nil:
				succeeded++
			case !errors.Is(err, giftexchange.ErrInvalidState):
				t.Errorf("losing draw: err = %v, want a *StateError", err)
			}
		}
		if succeeded != 1 {
			t.Fatalf("%d draws succeeded, want exactly 1", succeeded)
		}
		if got := len(notificationRows(t, s.exchange)); got != len(s.people) {
			t.Errorf("%d notifications, want %d: one per giver of the single draw", got, len(s.people))
		}
		recipients := assignmentsOf(t, s.h, s.exchange, s.people)
		if problem := permutationProblem(s.people, recipients); problem != "" {
			t.Error(problem)
		}
	}
}

func TestDrawRacingJoinLeavesAConsistentExchange(t *testing.T) {
	for range 5 {
		s := newDrawScene(t, 3)
		latecomer := s.h.newPerson(t)

		errs := racing(
			func() error { return s.h.svc.Draw(t.Context(), s.organizer.actor, s.group, s.exchange) },
			func() error { return s.h.svc.Join(t.Context(), latecomer.actor, s.group, s.exchange) },
		)

		drawErr, joinErr := errs[0], errs[1]
		if drawErr != nil {
			t.Fatalf("Draw: %v", drawErr)
		}
		if joinErr != nil && !errors.Is(joinErr, giftexchange.ErrInvalidState) {
			t.Fatalf("Join: err = %v, want nil or a *StateError", joinErr)
		}

		// The join either made it into the draw or was refused; never half of each.
		people := s.people
		if joinErr == nil {
			people = append(people, latecomer)
		}
		view, err := s.h.svc.Exchange(t.Context(), s.organizer.actor, s.group, s.exchange)
		if err != nil {
			t.Fatalf("Exchange: %v", err)
		}
		if view.ParticipantCount != len(people) {
			t.Fatalf("%d participants, want %d", view.ParticipantCount, len(people))
		}
		recipients := assignmentsOf(t, s.h, s.exchange, people)
		if problem := permutationProblem(people, recipients); problem != "" {
			t.Error(problem)
		}
		if got := len(notificationRows(t, s.exchange)); got != len(people) {
			t.Errorf("%d notifications, want %d", got, len(people))
		}
	}
}
