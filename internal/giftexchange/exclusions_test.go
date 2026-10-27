package giftexchange_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

// trio is a scene with an organizer and three joined participants.
func newTrio(t *testing.T) (scene, [3]person) {
	t.Helper()

	s := newScene(t)
	var people [3]person
	for i := range people {
		people[i] = s.h.newPerson(t)
		s.h.join(t, people[i], s.group, s.exchange)
	}

	return s, people
}

func (s scene) add(t *testing.T, caller, giver, recipient person, mutual bool) error {
	t.Helper()

	err := s.h.svc.AddExclusion(t.Context(), caller.actor, s.group, s.exchange, giver.id, recipient.id, mutual)

	return err //nolint:wrapcheck // test helper passes the module error through
}

func (s scene) remove(t *testing.T, caller, giver, recipient person) error {
	t.Helper()

	err := s.h.svc.RemoveExclusion(t.Context(), caller.actor, s.group, s.exchange, giver.id, recipient.id)

	return err //nolint:wrapcheck // test helper passes the module error through
}

// pairs lists the stored exclusions as "giver>recipient" person IDs, sorted.
func (s scene) pairs(t *testing.T) []string {
	t.Helper()

	listed, err := s.h.svc.Exclusions(t.Context(), s.organizer.actor, s.group, s.exchange)
	if err != nil {
		t.Fatalf("Exclusions: %v", err)
	}

	var found []string
	for _, exclusion := range listed {
		found = append(found, exclusion.Giver.ID.String()+">"+exclusion.Recipient.ID.String())
	}
	slices.Sort(found)

	return found
}

func pairOf(giver, recipient person) string {
	return giver.id.String() + ">" + recipient.id.String()
}

func TestAddExclusionStoresOneDirectionAndCountsIt(t *testing.T) {
	s, p := newTrio(t)

	if err := s.add(t, s.organizer, p[0], p[1], false); err != nil {
		t.Fatalf("AddExclusion: %v", err)
	}

	if got := s.pairs(t); !slices.Equal(got, []string{pairOf(p[0], p[1])}) {
		t.Errorf("pairs = %v, want only the one direction", got)
	}
	view, err := s.h.svc.Exchange(t.Context(), p[2].actor, s.group, s.exchange)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if view.ExclusionCount != 1 || ruleNamed(t, view.Rules, giftexchange.RuleExclusions).Value != "1" {
		t.Errorf("count = %d, rule = %q; want 1 and 1", view.ExclusionCount,
			ruleNamed(t, view.Rules, giftexchange.RuleExclusions).Value)
	}
}

func TestAddExclusionMutualStoresBothDirections(t *testing.T) {
	s, p := newTrio(t)

	if err := s.add(t, s.organizer, p[0], p[1], true); err != nil {
		t.Fatalf("AddExclusion: %v", err)
	}

	want := []string{pairOf(p[0], p[1]), pairOf(p[1], p[0])}
	slices.Sort(want)
	if got := s.pairs(t); !slices.Equal(got, want) {
		t.Errorf("pairs = %v, want %v", got, want)
	}
}

func TestAddExclusionIsIdempotentAndMutualCompletesTheMissingDirection(t *testing.T) {
	s, p := newTrio(t)
	if err := s.add(t, s.organizer, p[0], p[1], false); err != nil {
		t.Fatalf("AddExclusion: %v", err)
	}
	before := takeSnapshot(t, s.h, s.exchange)

	if err := s.add(t, s.organizer, p[0], p[1], false); err != nil {
		t.Fatalf("AddExclusion again: %v", err)
	}
	assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))

	if err := s.add(t, s.organizer, p[1], p[0], true); err != nil {
		t.Fatalf("AddExclusion mutual: %v", err)
	}
	want := []string{pairOf(p[0], p[1]), pairOf(p[1], p[0])}
	slices.Sort(want)
	if got := s.pairs(t); !slices.Equal(got, want) {
		t.Errorf("pairs = %v, want both directions once: %v", got, want)
	}
}

func TestRemoveExclusionDeletesOnlyThatDirectionAndMissingIsNotAnError(t *testing.T) {
	s, p := newTrio(t)
	if err := s.add(t, s.organizer, p[0], p[1], true); err != nil {
		t.Fatalf("AddExclusion: %v", err)
	}

	if err := s.remove(t, s.organizer, p[0], p[1]); err != nil {
		t.Fatalf("RemoveExclusion: %v", err)
	}
	if got := s.pairs(t); !slices.Equal(got, []string{pairOf(p[1], p[0])}) {
		t.Errorf("pairs = %v, want only the reverse direction", got)
	}

	before := takeSnapshot(t, s.h, s.exchange)
	if err := s.remove(t, s.organizer, p[0], p[1]); err != nil {
		t.Errorf("removing a missing exclusion: %v", err)
	}
	if err := s.remove(t, s.organizer, p[2], p[0]); err != nil {
		t.Errorf("removing a never-stored exclusion: %v", err)
	}
	assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))
}

func TestAddExclusionRejectsPeopleWhoAreNotActiveParticipants(t *testing.T) {
	s, p := newTrio(t)
	outsider := s.h.newPerson(t)
	left := s.h.newPerson(t)
	s.h.join(t, left, s.group, s.exchange)
	if _, err := s.h.svc.Leave(t.Context(), left.actor, s.group, s.exchange); err != nil {
		t.Fatalf("Leave: %v", err)
	}
	before := takeSnapshot(t, s.h, s.exchange)

	cases := map[string][2]person{
		"never joined as recipient":  {p[0], outsider},
		"never joined as giver":      {outsider, p[0]},
		"organizer who never joined": {s.organizer, p[0]},
		"already left":               {p[0], left},
	}
	for name, pair := range cases {
		err := s.add(t, s.organizer, pair[0], pair[1], true)
		if !errors.Is(err, giftexchange.ErrNotParticipant) {
			t.Errorf("%s: err = %v, want ErrNotParticipant", name, err)
		}
	}
	assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))
}

func TestAddExclusionRejectsTheSamePersonTwiceAndZeroIDs(t *testing.T) {
	s, p := newTrio(t)
	before := takeSnapshot(t, s.h, s.exchange)

	sameErr := s.add(t, s.organizer, p[0], p[0], false)
	var sameInputErr *giftexchange.InputError
	if !errors.As(sameErr, &sameInputErr) || sameInputErr.Field != "recipient" || sameInputErr.Code != giftexchange.InputCodeSameAsGiver {
		t.Errorf("same person: err = %v, want *InputError for recipient with code %q", sameErr, giftexchange.InputCodeSameAsGiver)
	}

	var zero giftexchange.PersonID
	zeroErr := s.h.svc.AddExclusion(t.Context(), s.organizer.actor, s.group, s.exchange, zero, zero, false)
	if !slices.Equal(inputFields(zeroErr), []string{"giver_id", "recipient_id"}) {
		t.Errorf("zero IDs: err = %v, want giver_id and recipient_id reported together", zeroErr)
	}
	assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))
}

func TestExclusionsAreOrganizerOnlyAndOthersSeeTheCount(t *testing.T) {
	s, p := newTrio(t)
	if err := s.add(t, s.organizer, p[0], p[1], true); err != nil {
		t.Fatalf("AddExclusion: %v", err)
	}

	listed, err := s.h.svc.Exclusions(t.Context(), s.organizer.actor, s.group, s.exchange)
	if err != nil || len(listed) != 2 {
		t.Fatalf("organizer list = %d, %v; want 2 pairs", len(listed), err)
	}
	for _, exclusion := range listed {
		if len(exclusion.Giver.Identities) != 1 || len(exclusion.Recipient.Identities) != 1 {
			t.Errorf("exclusion identities = %v / %v, want one each", exclusion.Giver.Identities, exclusion.Recipient.Identities)
		}
	}

	for who, caller := range map[string]person{"participant": p[2], "excluded person": p[0], "stranger": s.h.newPerson(t)} {
		_, denied := s.h.svc.Exclusions(t.Context(), caller.actor, s.group, s.exchange)
		if !errors.Is(denied, giftexchange.ErrForbidden) {
			t.Errorf("%s: err = %v, want ErrForbidden", who, denied)
		}
		view, viewErr := s.h.svc.Exchange(t.Context(), caller.actor, s.group, s.exchange)
		if viewErr != nil || view.ExclusionCount != 2 {
			t.Errorf("%s: Exchange count = %d, %v; want 2", who, view.ExclusionCount, viewErr)
		}
	}
}

func TestExclusionsListingFollowsTheStateTable(t *testing.T) {
	for _, state := range []giftexchange.State{
		giftexchange.StateOpen, giftexchange.StateDrawn, giftexchange.StateRevealed, giftexchange.StateCancelled,
	} {
		t.Run(string(state), func(t *testing.T) {
			s, p := newTrio(t)
			if err := s.add(t, s.organizer, p[0], p[1], false); err != nil {
				t.Fatalf("AddExclusion: %v", err)
			}
			s.moveTo(t, state)

			listed, err := s.h.svc.Exclusions(t.Context(), s.organizer.actor, s.group, s.exchange)

			if state == giftexchange.StateCancelled {
				var stateErr *giftexchange.StateError
				if !errors.As(err, &stateErr) || stateErr.State != state {
					t.Errorf("err = %v, want *StateError in cancelled", err)
				}
				return
			}
			if err != nil || len(listed) != 1 {
				t.Errorf("Exclusions = %d, %v; want 1 pair", len(listed), err)
			}
		})
	}
}

func TestExclusionsCanOnlyChangeWhileOpen(t *testing.T) {
	for _, state := range []giftexchange.State{
		giftexchange.StateDrawn, giftexchange.StateRevealed, giftexchange.StateCancelled,
	} {
		t.Run(string(state), func(t *testing.T) {
			s, p := newTrio(t)
			if err := s.add(t, s.organizer, p[0], p[1], false); err != nil {
				t.Fatalf("AddExclusion: %v", err)
			}
			s.moveTo(t, state)
			before := takeSnapshot(t, s.h, s.exchange)

			addErr := s.add(t, s.organizer, p[1], p[2], false)
			removeErr := s.remove(t, s.organizer, p[0], p[1])

			for name, err := range map[string]error{"add": addErr, "remove": removeErr} {
				var stateErr *giftexchange.StateError
				if !errors.As(err, &stateErr) || stateErr.State != state {
					t.Errorf("%s: err = %v, want *StateError in %s", name, err, state)
				}
			}
			assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))
		})
	}
}

func TestLeavingDeletesTheLeaversExclusionsInBothRoles(t *testing.T) {
	s, p := newTrio(t)
	if err := s.add(t, s.organizer, p[0], p[1], false); err != nil {
		t.Fatalf("AddExclusion: %v", err)
	}
	if err := s.add(t, s.organizer, p[2], p[0], false); err != nil {
		t.Fatalf("AddExclusion: %v", err)
	}
	if err := s.add(t, s.organizer, p[1], p[2], false); err != nil {
		t.Fatalf("AddExclusion: %v", err)
	}

	if _, err := s.h.svc.Leave(t.Context(), p[0].actor, s.group, s.exchange); err != nil {
		t.Fatalf("Leave: %v", err)
	}

	if got := s.pairs(t); !slices.Equal(got, []string{pairOf(p[1], p[2])}) {
		t.Errorf("pairs = %v, want only the pair that never involved the leaver", got)
	}

	s.h.clock.Advance(1)
	s.h.join(t, p[0], s.group, s.exchange)
	if got := s.pairs(t); !slices.Equal(got, []string{pairOf(p[1], p[2])}) {
		t.Errorf("pairs after rejoin = %v, want the leaver to come back without exclusions", got)
	}
}

func TestRemovingAParticipantDeletesTheirExclusions(t *testing.T) {
	s, p := newTrio(t)
	if err := s.add(t, s.organizer, p[0], p[1], true); err != nil {
		t.Fatalf("AddExclusion: %v", err)
	}

	if _, err := s.h.svc.RemoveParticipant(t.Context(), s.organizer.actor, s.group, s.exchange, p[1].id); err != nil {
		t.Fatalf("RemoveParticipant: %v", err)
	}

	if got := s.pairs(t); len(got) != 0 {
		t.Errorf("pairs = %v, want none", got)
	}
}
