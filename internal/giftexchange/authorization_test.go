package giftexchange_test

import (
	"errors"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

// denial is one call a caller without the role makes.
type denial struct {
	name string
	want error
	call func(t *testing.T, s scene, caller person) error
}

func organizerOperations() []denial {
	return []denial{
		{"rename", giftexchange.ErrForbidden, func(t *testing.T, s scene, c person) error {
			return s.h.svc.Rename(t.Context(), c.actor, s.group, s.exchange, "Hijacked")
		}},
		{"cancel", giftexchange.ErrForbidden, func(t *testing.T, s scene, c person) error {
			return s.h.svc.Cancel(t.Context(), c.actor, s.group, s.exchange)
		}},
		{"remove participant", giftexchange.ErrForbidden, func(t *testing.T, s scene, c person) error {
			return errOf(s.h.svc.RemoveParticipant(t.Context(), c.actor, s.group, s.exchange, c.id))
		}},
	}
}

func TestOrganizerOperationsDenyEveryoneButTheOrganizer(t *testing.T) {
	for _, op := range organizerOperations() {
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

func TestRemoveParticipantByANonOrganizerLeavesTheTargetInPlace(t *testing.T) {
	s := newScene(t)
	attacker := s.h.newPerson(t)
	victim := s.h.newPerson(t)
	s.h.join(t, attacker, s.group, s.exchange)
	s.h.join(t, victim, s.group, s.exchange)
	before := takeSnapshot(t, s.h, s.exchange)

	_, err := s.h.svc.RemoveParticipant(t.Context(), attacker.actor, s.group, s.exchange, victim.id)

	if !errors.Is(err, giftexchange.ErrForbidden) {
		t.Errorf("err = %v, want ErrForbidden", err)
	}
	assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))
}

func TestMembersOfAnotherGroupGetNotFoundForEveryOperation(t *testing.T) {
	s := newScene(t)
	joined := s.h.newPerson(t)
	s.h.join(t, joined, s.group, s.exchange)
	otherGroup := s.h.newGroup(t)
	outsider := s.h.newPerson(t)
	before := takeSnapshot(t, s.h, s.exchange)

	// The organizer and a participant also name the wrong group: the exchange stays hidden
	// from them there too.
	callers := map[string]person{"outsider": outsider, "organizer": s.organizer, "participant": joined}
	for who, caller := range callers {
		calls := map[string]func() error{
			"exchange": func() error {
				return errOf(s.h.svc.Exchange(t.Context(), caller.actor, otherGroup, s.exchange))
			},
			"rename": func() error {
				return s.h.svc.Rename(t.Context(), caller.actor, otherGroup, s.exchange, "Hijacked")
			},
			"cancel": func() error { return s.h.svc.Cancel(t.Context(), caller.actor, otherGroup, s.exchange) },
			"join":   func() error { return s.h.svc.Join(t.Context(), caller.actor, otherGroup, s.exchange) },
			"leave": func() error {
				return errOf(s.h.svc.Leave(t.Context(), caller.actor, otherGroup, s.exchange))
			},
			"remove participant": func() error {
				return errOf(s.h.svc.RemoveParticipant(t.Context(), caller.actor, otherGroup, s.exchange, joined.id))
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

func TestUnknownExchangeIDIsNotFoundAndIndistinguishableFromAnotherGroups(t *testing.T) {
	s := newScene(t)
	unknown := unknownExchange(t)
	otherGroup := s.h.newGroup(t)

	_, errUnknown := s.h.svc.Exchange(t.Context(), s.organizer.actor, s.group, unknown)
	_, errForeign := s.h.svc.Exchange(t.Context(), s.organizer.actor, otherGroup, s.exchange)

	if !errors.Is(errUnknown, giftexchange.ErrNotFound) || !errors.Is(errForeign, giftexchange.ErrNotFound) {
		t.Fatalf("errors = %v and %v, want ErrNotFound for both", errUnknown, errForeign)
	}
	if errUnknown.Error() != errForeign.Error() {
		t.Errorf("error texts differ: %q and %q", errUnknown, errForeign)
	}
}

func unknownExchange(t *testing.T) giftexchange.ExchangeID {
	t.Helper()

	id, err := giftexchange.ParseExchangeID("018f0000-0000-7000-8000-000000000002")
	if err != nil {
		t.Fatalf("ParseExchangeID: %v", err)
	}

	return id
}

func TestLeaveByANonParticipantReportsNotParticipant(t *testing.T) {
	s := newScene(t)
	participant := s.h.newPerson(t)
	stranger := s.h.newPerson(t)
	s.h.join(t, participant, s.group, s.exchange)
	before := takeSnapshot(t, s.h, s.exchange)

	for who, caller := range map[string]person{"stranger": stranger, "organizer who never joined": s.organizer} {
		_, err := s.h.svc.Leave(t.Context(), caller.actor, s.group, s.exchange)
		if !errors.Is(err, giftexchange.ErrNotParticipant) {
			t.Errorf("%s: err = %v, want ErrNotParticipant", who, err)
		}
	}
	assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))
}

func TestJoinByAnActiveParticipantChangesNothing(t *testing.T) {
	s := newScene(t)
	participant := s.h.newPerson(t)
	s.h.join(t, participant, s.group, s.exchange)
	before := takeSnapshot(t, s.h, s.exchange)

	err := s.h.svc.Join(t.Context(), participant.actor, s.group, s.exchange)

	if !errors.Is(err, giftexchange.ErrAlreadyParticipant) {
		t.Errorf("err = %v, want ErrAlreadyParticipant", err)
	}
	assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))
}

func TestZeroActorIsUnauthenticatedEverywhereAndChangesNothing(t *testing.T) {
	s := newScene(t)
	participant := s.h.newPerson(t)
	s.h.join(t, participant, s.group, s.exchange)
	before := takeSnapshot(t, s.h, s.exchange)
	var zero giftexchange.Actor

	calls := map[string]func() error{
		"create": func() error {
			return errOf(s.h.svc.CreateExchange(t.Context(), zero,
				giftexchange.CreateExchangeRequest{Group: s.group, Name: "Winter exchange"}))
		},
		"exchange": func() error {
			return errOf(s.h.svc.Exchange(t.Context(), zero, s.group, s.exchange))
		},
		"exchanges for group": func() error {
			return errOf(s.h.svc.ExchangesFor(t.Context(), zero, &s.group))
		},
		"exchanges for caller": func() error {
			return errOf(s.h.svc.ExchangesFor(t.Context(), zero, nil))
		},
		"rename": func() error { return s.h.svc.Rename(t.Context(), zero, s.group, s.exchange, "Hijacked") },
		"cancel": func() error { return s.h.svc.Cancel(t.Context(), zero, s.group, s.exchange) },
		"join":   func() error { return s.h.svc.Join(t.Context(), zero, s.group, s.exchange) },
		"leave": func() error {
			return errOf(s.h.svc.Leave(t.Context(), zero, s.group, s.exchange))
		},
		"remove participant": func() error {
			return errOf(s.h.svc.RemoveParticipant(t.Context(), zero, s.group, s.exchange, participant.id))
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

func TestZeroIDsAreInvalidInputBeforeAnyLookup(t *testing.T) {
	s := newScene(t)
	var zeroGroup giftexchange.GroupID
	var zeroExchange giftexchange.ExchangeID
	var zeroPerson giftexchange.PersonID

	calls := map[string]func() error{
		"exchange with zero group": func() error {
			return errOf(s.h.svc.Exchange(t.Context(), s.organizer.actor, zeroGroup, s.exchange))
		},
		"exchange with zero exchange": func() error {
			return errOf(s.h.svc.Exchange(t.Context(), s.organizer.actor, s.group, zeroExchange))
		},
		"create with zero group": func() error {
			return errOf(s.h.svc.CreateExchange(t.Context(), s.organizer.actor,
				giftexchange.CreateExchangeRequest{Name: "Winter exchange"}))
		},
		"exchanges for zero group": func() error {
			return errOf(s.h.svc.ExchangesFor(t.Context(), s.organizer.actor, &zeroGroup))
		},
		"cancel with zero exchange": func() error {
			return s.h.svc.Cancel(t.Context(), s.organizer.actor, s.group, zeroExchange)
		},
		"join with zero exchange": func() error {
			return s.h.svc.Join(t.Context(), s.organizer.actor, s.group, zeroExchange)
		},
		"remove zero person": func() error {
			return errOf(s.h.svc.RemoveParticipant(t.Context(), s.organizer.actor, s.group, s.exchange, zeroPerson))
		},
	}

	for name, call := range calls {
		err := call()
		if !errors.Is(err, giftexchange.ErrInvalidInput) {
			t.Errorf("%s: err = %v, want ErrInvalidInput", name, err)
		}
	}
}
