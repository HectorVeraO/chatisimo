package giftexchange_test

import (
	"errors"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

func TestConcurrentDropoutsKeepAValidPermutationOrReopen(t *testing.T) {
	for range 5 {
		s, _ := drawnWhere(t, 6, nil, anyDraw)
		leavers := s.people[:3]
		calls := make([]func() error, 0, len(leavers))
		for _, leaver := range leavers {
			calls = append(calls, func() error {
				return errOf(s.h.svc.Leave(t.Context(), leaver.actor, s.group, s.exchange))
			})
		}

		errs := racing(calls...)

		for _, err := range errs {
			if err != nil {
				t.Fatalf("Leave: %v", err)
			}
		}
		remaining := s.people[3:]
		state, _ := s.stateAndRound(t)
		switch state {
		case "drawn":
			assertRepairedRound(t, s, remaining)
		case "open":
			if got := s.assignmentRows(t); got != 0 {
				t.Errorf("%d assignments left in a reopened exchange", got)
			}
		default:
			t.Fatalf("state = %s, want drawn or open", state)
		}
	}
}

func TestDropoutsDenyCallersWithoutTheRightAndChangeNothing(t *testing.T) {
	s, _ := drawnWhere(t, 4, nil, anyDraw)
	stranger := s.h.newPerson(t)
	otherGroup := s.h.newGroup(t)
	var zero giftexchange.Actor
	before := takeSnapshot(t, s.h, s.exchange)

	tests := []struct {
		name   string
		call   func() error
		wanted error
	}{
		{"leave by a stranger", func() error {
			return errOf(s.h.svc.Leave(t.Context(), stranger.actor, s.group, s.exchange))
		}, giftexchange.ErrNotParticipant},
		{"leave by an organizer who never joined", func() error {
			return errOf(s.h.svc.Leave(t.Context(), s.organizer.actor, s.group, s.exchange))
		}, giftexchange.ErrNotParticipant},
		{"leave in another group", func() error {
			return errOf(s.h.svc.Leave(t.Context(), s.people[0].actor, otherGroup, s.exchange))
		}, giftexchange.ErrNotFound},
		{"leave by the zero actor", func() error {
			return errOf(s.h.svc.Leave(t.Context(), zero, s.group, s.exchange))
		}, giftexchange.ErrUnauthenticated},
		{"remove by a participant", func() error {
			return errOf(s.h.svc.RemoveParticipant(t.Context(), s.people[0].actor, s.group, s.exchange, s.people[1].id))
		}, giftexchange.ErrForbidden},
		{"remove by a stranger", func() error {
			return errOf(s.h.svc.RemoveParticipant(t.Context(), stranger.actor, s.group, s.exchange, s.people[1].id))
		}, giftexchange.ErrForbidden},
		{"remove in another group", func() error {
			return errOf(s.h.svc.RemoveParticipant(t.Context(), s.organizer.actor, otherGroup, s.exchange, s.people[1].id))
		}, giftexchange.ErrNotFound},
		{"remove by the zero actor", func() error {
			return errOf(s.h.svc.RemoveParticipant(t.Context(), zero, s.group, s.exchange, s.people[1].id))
		}, giftexchange.ErrUnauthenticated},
		{"remove a stranger", func() error {
			return errOf(s.h.svc.RemoveParticipant(t.Context(), s.organizer.actor, s.group, s.exchange, stranger.id))
		}, giftexchange.ErrNotParticipant},
	}

	for _, tc := range tests {
		err := tc.call()
		if !errors.Is(err, tc.wanted) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.wanted)
		}
	}
	assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))
}

func TestRepeatedAndOutOfStateDropoutsNeverDoubleTheirEffect(t *testing.T) {
	s, _ := drawnWhere(t, 5, nil, anyDraw)
	leaver := s.people[0]
	s.leave(t, leaver)
	afterFirst := takeSnapshot(t, s.h, s.exchange)

	_, secondLeave := s.h.svc.Leave(t.Context(), leaver.actor, s.group, s.exchange)
	_, removeLeft := s.h.svc.RemoveParticipant(t.Context(), s.organizer.actor, s.group, s.exchange, leaver.id)

	if !errors.Is(secondLeave, giftexchange.ErrNotParticipant) {
		t.Errorf("second leave: err = %v, want ErrNotParticipant", secondLeave)
	}
	if !errors.Is(removeLeft, giftexchange.ErrNotParticipant) {
		t.Errorf("remove of a person who left: err = %v, want ErrNotParticipant", removeLeft)
	}
	assertUnchanged(t, afterFirst, takeSnapshot(t, s.h, s.exchange))
}

func TestDropoutsAfterRevealOrCancelReportTheStateAndChangeNothing(t *testing.T) {
	endings := map[string]func(t *testing.T, s drawScene){
		"revealed": func(t *testing.T, s drawScene) {
			t.Helper()
			_, err := shared.Pool().Exec(t.Context(),
				"UPDATE giftexchange.exchanges SET state = 'revealed', revealed_at = $2 WHERE id = $1",
				s.exchange.String(), s.h.clock.Now())
			if err != nil {
				t.Fatalf("reveal: %v", err)
			}
		},
		"cancelled": func(t *testing.T, s drawScene) {
			t.Helper()
			err := s.h.svc.Cancel(t.Context(), s.organizer.actor, s.group, s.exchange)
			if err != nil {
				t.Fatalf("Cancel: %v", err)
			}
		},
	}

	for name, end := range endings {
		t.Run(name, func(t *testing.T) {
			s, _ := drawnWhere(t, 4, nil, anyDraw)
			end(t, s)
			before := takeSnapshot(t, s.h, s.exchange)

			_, leaveErr := s.h.svc.Leave(t.Context(), s.people[0].actor, s.group, s.exchange)
			_, removeErr := s.h.svc.RemoveParticipant(t.Context(), s.organizer.actor, s.group, s.exchange, s.people[1].id)

			for _, err := range []error{leaveErr, removeErr} {
				var stateErr *giftexchange.StateError
				if !errors.As(err, &stateErr) || string(stateErr.State) != name {
					t.Errorf("err = %v, want *StateError in %s", err, name)
				}
			}
			assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))
		})
	}
}
