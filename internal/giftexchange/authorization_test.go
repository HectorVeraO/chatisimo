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
		{"draw", giftexchange.ErrForbidden, func(t *testing.T, s scene, c person) error {
			return s.h.svc.Draw(t.Context(), c.actor, s.group, s.exchange)
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
			"draw":   func() error { return s.h.svc.Draw(t.Context(), caller.actor, otherGroup, s.exchange) },
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
		"draw":   func() error { return s.h.svc.Draw(t.Context(), zero, s.group, s.exchange) },
		"leave": func() error {
			return errOf(s.h.svc.Leave(t.Context(), zero, s.group, s.exchange))
		},
		"remove participant": func() error {
			return errOf(s.h.svc.RemoveParticipant(t.Context(), zero, s.group, s.exchange, participant.id))
		},
		"preferences": func() error {
			return errOf(s.h.svc.Preferences(t.Context(), zero, s.exchange))
		},
		"set preferences": func() error {
			return errOf(s.h.svc.SetPreferences(t.Context(), zero, s.exchange, giftexchange.Preferences{Likes: " Books "}))
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
		"draw with zero group": func() error {
			return s.h.svc.Draw(t.Context(), s.organizer.actor, zeroGroup, s.exchange)
		},
		"draw with zero exchange": func() error {
			return s.h.svc.Draw(t.Context(), s.organizer.actor, s.group, zeroExchange)
		},
		"my assignment with zero exchange": func() error {
			return errOf(s.h.svc.MyAssignment(t.Context(), s.organizer.actor, zeroExchange))
		},
		"remove zero person": func() error {
			return errOf(s.h.svc.RemoveParticipant(t.Context(), s.organizer.actor, s.group, s.exchange, zeroPerson))
		},
		"preferences with zero exchange": func() error {
			return errOf(s.h.svc.Preferences(t.Context(), s.organizer.actor, zeroExchange))
		},
		"set preferences with zero exchange": func() error {
			return errOf(s.h.svc.SetPreferences(
				t.Context(), s.organizer.actor, zeroExchange, giftexchange.Preferences{Likes: "Books"},
			))
		},
	}

	for name, call := range calls {
		err := call()
		if !errors.Is(err, giftexchange.ErrInvalidInput) {
			t.Errorf("%s: err = %v, want ErrInvalidInput", name, err)
		}
	}
}

// pairScene is a Drawn exchange with three participants, one person who left before the draw
// and a stranger.
type pairScene struct {
	scene
	participants []person
	leaver       person
	stranger     person
}

func newPairScene(t *testing.T) pairScene {
	t.Helper()

	s := newScene(t)
	leaver := s.h.newPerson(t)
	s.h.join(t, leaver, s.group, s.exchange)
	participants := make([]person, 0, 3)
	for range 3 {
		p := s.h.newPerson(t)
		s.h.join(t, p, s.group, s.exchange)
		participants = append(participants, p)
	}
	_, leaveErr := s.h.svc.Leave(t.Context(), leaver.actor, s.group, s.exchange)
	if leaveErr != nil {
		t.Fatalf("Leave: %v", leaveErr)
	}
	s.mustDraw(t)

	return pairScene{scene: s, participants: participants, leaver: leaver, stranger: s.h.newPerson(t)}
}

func TestOwnPairReadsDenyEveryoneWhoIsNotAnActiveParticipantAndChangeNothing(t *testing.T) {
	s := newPairScene(t)
	var zero giftexchange.Actor
	unknown := unknownExchange(t)

	reads := map[string]func(caller giftexchange.Actor, x giftexchange.ExchangeID) error{
		"my assignment": func(caller giftexchange.Actor, x giftexchange.ExchangeID) error {
			return errOf(s.h.svc.MyAssignment(t.Context(), caller, x))
		},
		"preferences": func(caller giftexchange.Actor, x giftexchange.ExchangeID) error {
			return errOf(s.h.svc.Preferences(t.Context(), caller, x))
		},
	}
	callers := []struct {
		name   string
		actor  giftexchange.Actor
		x      giftexchange.ExchangeID
		wanted error
	}{
		{"a stranger with no tie to the exchange", s.stranger.actor, s.exchange, giftexchange.ErrNotFound},
		{
			"a participant asking for an exchange that does not exist", s.participants[0].actor, unknown,
			giftexchange.ErrNotFound,
		},
		{"an organizer who never joined", s.organizer.actor, s.exchange, giftexchange.ErrNotParticipant},
		{"a person who left before the draw", s.leaver.actor, s.exchange, giftexchange.ErrNotParticipant},
		{"the zero actor", zero, s.exchange, giftexchange.ErrUnauthenticated},
	}

	before := takeSnapshot(t, s.h, s.exchange)
	for readName, read := range reads {
		for _, tc := range callers {
			err := read(tc.actor, tc.x)
			if !errors.Is(err, tc.wanted) {
				t.Errorf("%s by %s: err = %v, want %v", readName, tc.name, err, tc.wanted)
			}
		}
	}
	assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))
}

func TestMyAssignmentBeforeTheDrawReportsNotParticipantToWhoNeverJoinedAndStateErrorToWhoDid(t *testing.T) {
	s := newDrawScene(t, 3)

	_, organizerErr := s.h.svc.MyAssignment(t.Context(), s.organizer.actor, s.exchange)
	_, participantErr := s.h.svc.MyAssignment(t.Context(), s.people[0].actor, s.exchange)

	if !errors.Is(organizerErr, giftexchange.ErrNotParticipant) {
		t.Errorf("organizer who never joined: err = %v, want ErrNotParticipant", organizerErr)
	}
	if !errors.Is(participantErr, giftexchange.ErrInvalidState) {
		t.Errorf("participant: err = %v, want ErrInvalidState", participantErr)
	}
}

func TestUnrelatedCallerCannotTellAnUnknownExchangeFromARealOne(t *testing.T) {
	s := newPairScene(t)

	_, realErr := s.h.svc.MyAssignment(t.Context(), s.stranger.actor, s.exchange)
	_, unknownErr := s.h.svc.MyAssignment(t.Context(), s.stranger.actor, unknownExchange(t))

	if !errors.Is(realErr, giftexchange.ErrNotFound) || !errors.Is(unknownErr, giftexchange.ErrNotFound) {
		t.Fatalf("errors are %v and %v, want both ErrNotFound", realErr, unknownErr)
	}
	if realErr.Error() != unknownErr.Error() {
		t.Errorf("errors are %q and %q, want the same text", realErr, unknownErr)
	}
}

func TestDrawDeniesNonOrganizersAfterTheDrawAndChangesNothing(t *testing.T) {
	s := newPairScene(t)
	before := takeSnapshot(t, s.h, s.exchange)

	callers := map[string]person{
		"participant": s.participants[0], "person who left": s.leaver, "stranger": s.stranger,
	}
	for who, caller := range callers {
		err := s.h.svc.Draw(t.Context(), caller.actor, s.group, s.exchange)

		if !errors.Is(err, giftexchange.ErrForbidden) {
			t.Errorf("draw by a %s: err = %v, want ErrForbidden", who, err)
		}
	}
	assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))
}

func TestSetPreferencesDeniesEveryoneWhoIsNotAnActiveParticipantAndChangesNothing(t *testing.T) {
	s := newPairScene(t)
	var zero giftexchange.Actor
	unknown := unknownExchange(t)
	prefs := giftexchange.Preferences{Likes: "Dark chocolate"}

	callers := []struct {
		name   string
		actor  giftexchange.Actor
		x      giftexchange.ExchangeID
		wanted error
	}{
		{"a stranger with no tie to the exchange", s.stranger.actor, s.exchange, giftexchange.ErrNotFound},
		{
			"a participant asking for an exchange that does not exist", s.participants[0].actor, unknown,
			giftexchange.ErrNotFound,
		},
		{"an organizer who never joined", s.organizer.actor, s.exchange, giftexchange.ErrNotParticipant},
		{"a person who left before the draw", s.leaver.actor, s.exchange, giftexchange.ErrNotParticipant},
		{"the zero actor", zero, s.exchange, giftexchange.ErrUnauthenticated},
	}

	before := takeSnapshot(t, s.h, s.exchange)
	for _, tc := range callers {
		_, err := s.h.svc.SetPreferences(t.Context(), tc.actor, tc.x, prefs)
		if !errors.Is(err, tc.wanted) {
			t.Errorf("SetPreferences by %s: err = %v, want %v", tc.name, err, tc.wanted)
		}
	}
	assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))
}

func TestRecipientPreferencesAreVisibleOnlyToTheParticipantAndTheirCurrentGiver(t *testing.T) {
	s := cycleDrawn(t, 4)
	want := giftexchange.Preferences{
		Likes:    "Sci-fi novels",
		Dislikes: "Scented candles",
		Details:  "Size L",
	}

	_, err := s.h.svc.SetPreferences(t.Context(), s.people[1].actor, s.exchange, want)
	if err != nil {
		t.Fatalf("SetPreferences: %v", err)
	}

	own, err := s.h.svc.Preferences(t.Context(), s.people[1].actor, s.exchange)
	if err != nil || own != want {
		t.Errorf("participant's own Preferences = %+v, %v; want %+v", own, err, want)
	}

	giverAssignment, err := s.h.svc.MyAssignment(t.Context(), s.people[0].actor, s.exchange)
	if err != nil || giverAssignment.Preferences != want {
		t.Errorf("giver's MyAssignment.Preferences = %+v, %v; want %+v", giverAssignment.Preferences, err, want)
	}

	for _, other := range []person{s.people[0], s.people[2], s.people[3]} {
		gotPrefs, prefsErr := s.h.svc.Preferences(t.Context(), other.actor, s.exchange)
		if prefsErr != nil || !gotPrefs.IsEmpty() {
			t.Errorf("other participant Preferences = %+v, %v; want empty", gotPrefs, prefsErr)
		}
	}
	for _, nonGiver := range []person{s.people[1], s.people[2], s.people[3]} {
		gotAssign, assignErr := s.h.svc.MyAssignment(t.Context(), nonGiver.actor, s.exchange)
		if assignErr != nil || !gotAssign.Preferences.IsEmpty() {
			t.Errorf("non-giver MyAssignment.Preferences = %+v, %v; want empty", gotAssign.Preferences, assignErr)
		}
	}
}
