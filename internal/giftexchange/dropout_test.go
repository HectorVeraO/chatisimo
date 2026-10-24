package giftexchange_test

import (
	"encoding/binary"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

type assignmentMap = map[giftexchange.PersonID]giftexchange.PersonID

// seedOf returns a distinct Random seed per attempt.
func seedOf(attempt uint64) [32]byte {
	var seed [32]byte
	binary.BigEndian.PutUint64(seed[:], attempt+1)

	return seed
}

// drawnWhere builds drawn exchanges of n participants, reseeding the Random per attempt, until
// one has the shape want asks for. setup runs on the Open exchange before the draw.
func drawnWhere(
	t *testing.T,
	n int,
	setup func(s drawScene),
	want func(s drawScene, a assignmentMap) bool,
) (drawScene, assignmentMap) {
	t.Helper()

	const attempts = 400
	for attempt := range uint64(attempts) {
		s := newDrawScene(t, n)
		s.h.random.Reseed(seedOf(attempt))
		if setup != nil {
			setup(s)
		}

		drawErr := s.draw(t)
		if drawErr != nil {
			continue
		}

		assigned := assignmentsOf(t, s.h, s.exchange, s.people)
		if want(s, assigned) {
			return s, assigned
		}
	}

	t.Fatalf("no draw of %d participants had the wanted shape in %d attempts", n, attempts)

	return drawScene{}, nil
}

// anyDraw accepts the first draw.
func anyDraw(drawScene, assignmentMap) bool {
	return true
}

func giverOf(a assignmentMap, recipient giftexchange.PersonID) giftexchange.PersonID {
	for giver, got := range a {
		if got == recipient {
			return giver
		}
	}

	return giftexchange.PersonID{}
}

// dropoutWhere returns a participant d whose giver and recipient satisfy cond, or fails.
func dropoutWhere(
	t *testing.T,
	s drawScene,
	a assignmentMap,
	cond func(giver, recipient giftexchange.PersonID) bool,
) person {
	t.Helper()

	for _, p := range s.people {
		if cond(giverOf(a, p.id), a[p.id]) {
			return p
		}
	}

	t.Fatal("no participant has the wanted giver and recipient")

	return person{}
}

func without(people []person, dropout person) []person {
	rest := make([]person, 0, len(people))
	for _, p := range people {
		if p.id != dropout.id {
			rest = append(rest, p)
		}
	}

	return rest
}

func (s drawScene) leave(t *testing.T, p person) giftexchange.DropoutOutcome {
	t.Helper()

	outcome, err := s.h.svc.Leave(t.Context(), p.actor, s.group, s.exchange)
	if err != nil {
		t.Fatalf("Leave: %v", err)
	}

	return outcome
}

func (s scene) stateAndRound(t *testing.T) (string, int) {
	t.Helper()

	var state string
	var round int
	err := shared.Pool().QueryRow(t.Context(),
		"SELECT state, round FROM giftexchange.exchanges WHERE id = $1", s.exchange.String()).
		Scan(&state, &round)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}

	return state, round
}

func (s scene) assignmentRows(t *testing.T) int {
	t.Helper()

	var count int
	err := shared.Pool().QueryRow(t.Context(),
		"SELECT count(*) FROM giftexchange.assignments WHERE exchange_id = $1", s.exchange.String()).
		Scan(&count)
	if err != nil {
		t.Fatalf("count assignments: %v", err)
	}

	return count
}

// noticesOf returns the addressees (person IDs) of the stored notices of one kind, sorted, and
// the reasons they carry.
func noticesOf(t *testing.T, x giftexchange.ExchangeID, kind string) ([]string, []string) {
	t.Helper()

	var addressees, reasons []string
	for _, row := range notificationRows(t, x) {
		if row.kind != kind {
			continue
		}
		addressees = append(addressees, row.person)
		if row.reason != nil {
			reasons = append(reasons, *row.reason)
		}
	}
	slices.Sort(addressees)

	return addressees, reasons
}

func idsOf(people ...person) []string {
	ids := make([]string, 0, len(people))
	for _, p := range people {
		ids = append(ids, p.id.String())
	}
	slices.Sort(ids)

	return ids
}

func assertSameIDs(t *testing.T, what string, got, want []string) {
	t.Helper()

	if !slices.Equal(got, want) {
		t.Errorf("%s: got %d addressees, want %d (%v vs %v)", what, len(got), len(want), got, want)
	}
}

// assertRepairedRound checks the remaining participants hold a valid permutation of themselves.
func assertRepairedRound(t *testing.T, s drawScene, remaining []person) assignmentMap {
	t.Helper()

	assigned := assignmentsOf(t, s.h, s.exchange, remaining)
	problem := permutationProblem(remaining, assigned)
	if problem != "" {
		t.Error(problem)
	}

	return assigned
}

func assertState(t *testing.T, s scene, wantState string, wantRound int) {
	t.Helper()

	state, round := s.stateAndRound(t)
	if state != wantState || round != wantRound {
		t.Errorf("state/round = %s/%d, want %s/%d", state, round, wantState, wantRound)
	}
}

// assertNoDropoutNotice checks the dropout got no notice beyond the ones the draw queued.
func assertNoDropoutNotice(t *testing.T, s scene, dropout person) {
	t.Helper()

	for _, row := range notificationRows(t, s.exchange) {
		isAfterDraw := row.kind != "drawn"
		if isAfterDraw && row.person == dropout.id.String() {
			t.Errorf("the dropout got a %s notice", row.kind)
		}
	}
}

func TestDropoutBelowThreeVoidsTheRoundAndReopensTheExchange(t *testing.T) {
	s := newDrawScene(t, 3)
	s.mustDraw(t)

	outcome := s.leave(t, s.people[0])

	if outcome != giftexchange.OutcomeReopened {
		t.Fatalf("outcome = %v, want reopened", outcome)
	}
	assertState(t, s.scene, "open", 1)
	if got := s.assignmentRows(t); got != 0 {
		t.Errorf("%d assignment rows survive the void, want 0", got)
	}
	var drawnAt *string
	err := shared.Pool().QueryRow(t.Context(),
		"SELECT drawn_at::text FROM giftexchange.exchanges WHERE id = $1", s.exchange.String()).Scan(&drawnAt)
	if err != nil || drawnAt != nil {
		t.Errorf("drawn_at = %v (%v), want it cleared", drawnAt, err)
	}
	addressees, reasons := noticesOf(t, s.exchange, "round_voided")
	assertSameIDs(t, "round_voided", addressees, idsOf(append(without(s.people, s.people[0]), s.organizer)...))
	for _, reason := range reasons {
		if reason != "too_few_participants" {
			t.Errorf("reason = %s, want too_few_participants", reason)
		}
	}
	assertNoDropoutNotice(t, s.scene, s.people[0])
	_, myErr := s.h.svc.MyAssignment(t.Context(), s.people[1].actor, s.exchange)
	if !errors.Is(myErr, giftexchange.ErrInvalidState) {
		t.Errorf("MyAssignment after the void: err = %v, want a *StateError", myErr)
	}
}

func TestDrawAfterAVoidUsesTheNextRound(t *testing.T) {
	s := newDrawScene(t, 3)
	s.mustDraw(t)
	s.leave(t, s.people[0])
	replacement := s.h.newPerson(t)
	s.h.join(t, replacement, s.group, s.exchange)

	s.mustDraw(t)

	assertState(t, s.scene, "drawn", 2)
	assertRepairedRound(t, s, append(without(s.people, s.people[0]), replacement))
}

func TestDropoutVoidNotifiesAnOrganizerWhoIsAParticipantOnce(t *testing.T) {
	s := newScene(t)
	others := []person{s.h.newPerson(t), s.h.newPerson(t)}
	s.h.join(t, s.organizer, s.group, s.exchange)
	for _, p := range others {
		s.h.join(t, p, s.group, s.exchange)
	}
	if err := s.draw(t); err != nil {
		t.Fatalf("Draw: %v", err)
	}

	outcome, err := s.h.svc.Leave(t.Context(), others[0].actor, s.group, s.exchange)

	if err != nil || outcome != giftexchange.OutcomeReopened {
		t.Fatalf("Leave = %v, %v, want reopened", outcome, err)
	}
	addressees, _ := noticesOf(t, s.exchange, "round_voided")
	assertSameIDs(t, "round_voided", addressees, idsOf(s.organizer, others[1]))
}

func TestOrganizerWhoIsAParticipantLeavingGetsNoNotice(t *testing.T) {
	s := newScene(t)
	others := []person{s.h.newPerson(t), s.h.newPerson(t)}
	s.h.join(t, s.organizer, s.group, s.exchange)
	for _, p := range others {
		s.h.join(t, p, s.group, s.exchange)
	}
	if err := s.draw(t); err != nil {
		t.Fatalf("Draw: %v", err)
	}

	outcome, err := s.h.svc.Leave(t.Context(), s.organizer.actor, s.group, s.exchange)

	if err != nil || outcome != giftexchange.OutcomeReopened {
		t.Fatalf("Leave = %v, %v, want reopened", outcome, err)
	}
	addressees, _ := noticesOf(t, s.exchange, "round_voided")
	assertSameIDs(t, "round_voided", addressees, idsOf(others...))
}

func TestDropoutUnderTheRedrawPolicyRedrawsEveryone(t *testing.T) {
	s, _ := drawnWhere(t, 5, func(s drawScene) {
		_, err := s.set(t, s.organizer, giftexchange.RuleDropoutPolicy, "redraw")
		if err != nil {
			t.Fatalf("SetRule: %v", err)
		}
	}, anyDraw)
	dropout := s.people[0]
	remaining := without(s.people, dropout)

	outcome := s.leave(t, dropout)

	if outcome != giftexchange.OutcomeRedrawn {
		t.Fatalf("outcome = %v, want redrawn", outcome)
	}
	assertState(t, s.scene, "drawn", 2)
	assertRepairedRound(t, s, remaining)
	addressees, _ := noticesOf(t, s.exchange, "redrawn")
	assertSameIDs(t, "redrawn", addressees, idsOf(remaining...))
	assignedChanged, _ := noticesOf(t, s.exchange, "assignment_changed")
	if len(assignedChanged) != 0 {
		t.Error("a redraw queued an assignment_changed notice")
	}
	assertNoDropoutNotice(t, s.scene, dropout)
}

func TestDropoutLeavingThreeRedrawsWhateverThePolicy(t *testing.T) {
	s, _ := drawnWhere(t, 4, nil, anyDraw)
	remaining := without(s.people, s.people[1])

	outcome := s.leave(t, s.people[1])

	if outcome != giftexchange.OutcomeRedrawn {
		t.Fatalf("outcome = %v, want redrawn", outcome)
	}
	assertState(t, s.scene, "drawn", 2)
	assertRepairedRound(t, s, remaining)
	addressees, _ := noticesOf(t, s.exchange, "redrawn")
	assertSameIDs(t, "redrawn", addressees, idsOf(remaining...))
}

func TestDropoutWhoseGiverAndRecipientAreTheSamePersonRedraws(t *testing.T) {
	s, assigned := drawnWhere(t, 5, nil, func(s drawScene, a assignmentMap) bool {
		for _, p := range s.people {
			if giverOf(a, p.id) == a[p.id] {
				return true
			}
		}
		return false
	})
	dropout := dropoutWhere(t, s, assigned, func(giver, recipient giftexchange.PersonID) bool {
		return giver == recipient
	})
	remaining := without(s.people, dropout)

	outcome := s.leave(t, dropout)

	if outcome != giftexchange.OutcomeRedrawn {
		t.Fatalf("outcome = %v, want redrawn", outcome)
	}
	assertState(t, s.scene, "drawn", 2)
	assertRepairedRound(t, s, remaining)
	addressees, _ := noticesOf(t, s.exchange, "redrawn")
	assertSameIDs(t, "redrawn", addressees, idsOf(remaining...))
}

func TestDropoutWhosePatchWouldBreakAnExclusionRedraws(t *testing.T) {
	var first, second person
	s, assigned := drawnWhere(t, 5, func(s drawScene) {
		first, second = s.people[0], s.people[1]
		s.exclude(t, first, second, false)
	}, func(s drawScene, a assignmentMap) bool {
		for _, p := range s.people {
			if giverOf(a, p.id) == first.id && a[p.id] == second.id {
				return true
			}
		}
		return false
	})
	dropout := dropoutWhere(t, s, assigned, func(giver, recipient giftexchange.PersonID) bool {
		return giver == first.id && recipient == second.id
	})

	outcome := s.leave(t, dropout)

	if outcome != giftexchange.OutcomeRedrawn {
		t.Fatalf("outcome = %v, want redrawn", outcome)
	}
	assertState(t, s.scene, "drawn", 2)
	after := assertRepairedRound(t, s, without(s.people, dropout))
	if after[first.id] == second.id {
		t.Error("the redraw used the excluded pair")
	}
}

// revealPreviousRound stores a Revealed exchange of the same group where each person gave to the
// next one in order, so avoid_last_round has a last round to read.
func revealPreviousRound(t *testing.T, s drawScene) {
	t.Helper()

	revealRound(t, s, s.group, 1, s.h.clock.Now())
}

// revealRound stores a Revealed exchange of group where person i gave to person i+shift, revealed
// at revealedAt. The people join the new exchange in group first.
func revealRound(t *testing.T, s drawScene, group giftexchange.GroupID, shift int, revealedAt time.Time) {
	t.Helper()

	previous := s.h.newExchange(t, s.organizer, group)
	for _, p := range s.people {
		s.h.join(t, p, group, previous.ID)
	}
	for i, giver := range s.people {
		recipient := s.people[(i+shift)%len(s.people)]
		_, err := shared.Pool().Exec(t.Context(),
			"INSERT INTO giftexchange.assignments (exchange_id, giver_id, recipient_id, round) VALUES ($1, $2, $3, 1)",
			previous.ID.String(), giver.id.String(), recipient.id.String())
		if err != nil {
			t.Fatalf("insert previous assignment: %v", err)
		}
	}
	_, err := shared.Pool().Exec(t.Context(),
		"UPDATE giftexchange.exchanges SET state = 'revealed', round = 1, drawn_at = $2, revealed_at = $2 WHERE id = $1",
		previous.ID.String(), revealedAt)
	if err != nil {
		t.Fatalf("reveal previous exchange: %v", err)
	}
}

// shiftPairs counts the givers of a whose recipient is shift places after them in join order.
func shiftPairs(s drawScene, a assignmentMap, shift int) int {
	count := 0
	for i, giver := range s.people {
		if a[giver.id] == s.people[(i+shift)%len(s.people)].id {
			count++
		}
	}

	return count
}

const lastRoundDrawAttempts = 30

func TestDrawWithAvoidLastRoundNeverRepeatsThePreviousRoundsPairs(t *testing.T) {
	repeatedElsewhere := 0
	for attempt := range uint64(lastRoundDrawAttempts) {
		s := newDrawScene(t, 4)
		s.h.random.Reseed(seedOf(attempt))
		revealPreviousRound(t, s)
		_, err := s.set(t, s.organizer, giftexchange.RuleAvoidLastRound, "on")
		if err != nil {
			t.Fatalf("SetRule: %v", err)
		}
		drawErr := s.draw(t)
		if drawErr != nil {
			t.Fatalf("draw: %v", drawErr)
		}

		assigned := assignmentsOf(t, s.h, s.exchange, s.people)
		if shiftPairs(s, assigned, 1) != 0 {
			t.Fatalf("attempt %d repeated a pair of the last round", attempt)
		}
		repeatedElsewhere += shiftPairs(s, assigned, 2) + shiftPairs(s, assigned, 3)
	}

	// Only the last round is forbidden: the other pairs must still be drawn.
	if repeatedElsewhere == 0 {
		t.Error("no draw used any pair outside the last round")
	}
}

func TestAvoidLastRoundReadsTheLatestRevealedRoundOfTheSameGroupOnly(t *testing.T) {
	const (
		olderShift   = 1
		latestShift  = 2
		foreignShift = 3
	)
	olderPairsSeen := 0
	foreignPairsSeen := 0
	for attempt := range uint64(lastRoundDrawAttempts) {
		s := newDrawScene(t, 4)
		s.h.random.Reseed(seedOf(attempt))
		start := s.h.clock.Now()
		revealRound(t, s, s.group, olderShift, start)
		revealRound(t, s, s.group, latestShift, start.Add(time.Hour))
		revealRound(t, s, s.h.newGroup(t), foreignShift, start.Add(2*time.Hour))
		_, err := s.set(t, s.organizer, giftexchange.RuleAvoidLastRound, "on")
		if err != nil {
			t.Fatalf("SetRule: %v", err)
		}
		drawErr := s.draw(t)
		if drawErr != nil {
			t.Fatalf("draw: %v", drawErr)
		}

		assigned := assignmentsOf(t, s.h, s.exchange, s.people)
		if shiftPairs(s, assigned, latestShift) != 0 {
			t.Fatalf("attempt %d repeated a pair of the latest round of the group", attempt)
		}
		olderPairsSeen += shiftPairs(s, assigned, olderShift)
		foreignPairsSeen += shiftPairs(s, assigned, foreignShift)
	}

	if olderPairsSeen == 0 {
		t.Error("an older round of the group was treated as the last round")
	}
	if foreignPairsSeen == 0 {
		t.Error("a round of another group was treated as the last round")
	}
}

func TestDropoutWhosePatchWouldRepeatLastRoundsPairRedraws(t *testing.T) {
	nextOf := func(s drawScene, id giftexchange.PersonID) giftexchange.PersonID {
		for i, p := range s.people {
			if p.id == id {
				return s.people[(i+1)%len(s.people)].id
			}
		}
		return giftexchange.PersonID{}
	}
	s, assigned := drawnWhere(t, 5, func(s drawScene) {
		revealPreviousRound(t, s)
		_, err := s.set(t, s.organizer, giftexchange.RuleAvoidLastRound, "on")
		if err != nil {
			t.Fatalf("SetRule: %v", err)
		}
	}, func(s drawScene, a assignmentMap) bool {
		for _, p := range s.people {
			giver := giverOf(a, p.id)
			if a[p.id] == nextOf(s, giver) {
				return true
			}
		}
		return false
	})
	var lastRoundGiver giftexchange.PersonID
	dropout := dropoutWhere(t, s, assigned, func(giver, recipient giftexchange.PersonID) bool {
		isLastRoundPair := recipient == nextOf(s, giver)
		if isLastRoundPair {
			lastRoundGiver = giver
		}
		return isLastRoundPair
	})

	outcome := s.leave(t, dropout)

	if outcome != giftexchange.OutcomeRedrawn {
		t.Fatalf("outcome = %v, want redrawn", outcome)
	}
	after := assertRepairedRound(t, s, without(s.people, dropout))
	if after[lastRoundGiver] == nextOf(s, lastRoundGiver) {
		t.Error("the redraw repeated a pair of the last round")
	}
}

func TestDropoutPatchHandsTheDropoutsRecipientToTheDropoutsGiver(t *testing.T) {
	s, assigned := drawnWhere(t, 5, nil, func(s drawScene, a assignmentMap) bool {
		for _, p := range s.people {
			if giverOf(a, p.id) != a[p.id] {
				return true
			}
		}
		return false
	})
	dropout := dropoutWhere(t, s, assigned, func(giver, recipient giftexchange.PersonID) bool {
		return giver != recipient
	})
	giver := giverOf(assigned, dropout.id)
	recipient := assigned[dropout.id]
	remaining := without(s.people, dropout)

	outcome := s.leave(t, dropout)

	if outcome != giftexchange.OutcomePatched {
		t.Fatalf("outcome = %v, want patched", outcome)
	}
	assertState(t, s.scene, "drawn", 1)
	after := assertRepairedRound(t, s, remaining)
	if after[giver] != recipient {
		t.Error("the dropout's giver did not take over the dropout's recipient")
	}
	for _, p := range remaining {
		isGiver := p.id == giver
		if !isGiver && after[p.id] != assigned[p.id] {
			t.Error("a patch changed the recipient of someone other than the dropout's giver")
		}
	}
	addressees, _ := noticesOf(t, s.exchange, "assignment_changed")
	assertSameIDs(t, "assignment_changed", addressees, []string{giver.String()})
	redrawn, _ := noticesOf(t, s.exchange, "redrawn")
	if len(redrawn) != 0 {
		t.Error("a patch queued redrawn notices")
	}
	assertNoDropoutNotice(t, s.scene, dropout)
}

func TestOrganizerRemovingAPersonRepairsLikeThatPersonLeaving(t *testing.T) {
	s, _ := drawnWhere(t, 5, nil, anyDraw)
	target := s.people[2]

	outcome, err := s.h.svc.RemoveParticipant(t.Context(), s.organizer.actor, s.group, s.exchange, target.id)

	if err != nil || outcome != giftexchange.OutcomePatched {
		t.Fatalf("RemoveParticipant = %v, %v, want patched (the default policy)", outcome, err)
	}
	assertRepairedRound(t, s, without(s.people, target))
}

func TestDropoutWithExclusionsTheRemainingCannotSatisfyVoidsTheRound(t *testing.T) {
	s := newDrawScene(t, 4)
	s.exclude(t, s.people[0], s.people[1], false)
	s.exclude(t, s.people[0], s.people[2], false)
	s.mustDraw(t)

	outcome := s.leave(t, s.people[3])

	if outcome != giftexchange.OutcomeReopened {
		t.Fatalf("outcome = %v, want reopened", outcome)
	}
	assertState(t, s.scene, "open", 1)
	if got := s.assignmentRows(t); got != 0 {
		t.Errorf("%d assignment rows survive the void, want 0", got)
	}
	addressees, reasons := noticesOf(t, s.exchange, "round_voided")
	assertSameIDs(t, "round_voided", addressees, idsOf(append(without(s.people, s.people[3]), s.organizer)...))
	for _, reason := range reasons {
		if reason != "no_valid_assignment" {
			t.Errorf("reason = %s, want no_valid_assignment", reason)
		}
	}
	redrawn, _ := noticesOf(t, s.exchange, "redrawn")
	if len(redrawn) != 0 {
		t.Error("a voided redraw left redrawn notices")
	}
}

func TestOrganizerCannotRemoveTheirOwnRecipientAndNothingChanges(t *testing.T) {
	s := newScene(t)
	people := []person{s.organizer}
	for range 4 {
		p := s.h.newPerson(t)
		people = append(people, p)
	}
	for _, p := range people {
		s.h.join(t, p, s.group, s.exchange)
	}
	if err := s.draw(t); err != nil {
		t.Fatalf("Draw: %v", err)
	}
	own, err := s.h.svc.MyAssignment(t.Context(), s.organizer.actor, s.exchange)
	if err != nil {
		t.Fatalf("MyAssignment: %v", err)
	}
	before := takeSnapshot(t, s.h, s.exchange)

	_, removeErr := s.h.svc.RemoveParticipant(t.Context(), s.organizer.actor, s.group, s.exchange, own.Recipient.ID)

	if !errors.Is(removeErr, giftexchange.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", removeErr)
	}
	assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))
}

func TestOrganizerWhoIsAParticipantMayRemoveOthersAndLeaveThemself(t *testing.T) {
	s := newScene(t)
	people := []person{s.organizer}
	for range 5 {
		people = append(people, s.h.newPerson(t))
	}
	for _, p := range people {
		s.h.join(t, p, s.group, s.exchange)
	}
	if err := s.draw(t); err != nil {
		t.Fatalf("Draw: %v", err)
	}
	own, err := s.h.svc.MyAssignment(t.Context(), s.organizer.actor, s.exchange)
	if err != nil {
		t.Fatalf("MyAssignment: %v", err)
	}
	var other person
	for _, p := range people[1:] {
		if p.id != own.Recipient.ID {
			other = p
			break
		}
	}

	_, removeErr := s.h.svc.RemoveParticipant(t.Context(), s.organizer.actor, s.group, s.exchange, other.id)
	_, leaveErr := s.h.svc.Leave(t.Context(), s.organizer.actor, s.group, s.exchange)

	if removeErr != nil || leaveErr != nil {
		t.Fatalf("RemoveParticipant = %v, Leave = %v, want both to succeed", removeErr, leaveErr)
	}
	scene := drawScene{scene: s, people: people}
	assertRepairedRound(t, scene, without(without(people, other), s.organizer))
}

func TestOrganizerWhoIsNotAParticipantMayRemoveAnyone(t *testing.T) {
	s, _ := drawnWhere(t, 5, nil, anyDraw)
	target := s.people[0]

	_, err := s.h.svc.RemoveParticipant(t.Context(), s.organizer.actor, s.group, s.exchange, target.id)
	if err != nil {
		t.Fatalf("RemoveParticipant: %v", err)
	}
	assertRepairedRound(t, s, without(s.people, target))
}
