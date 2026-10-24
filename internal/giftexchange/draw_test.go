package giftexchange_test

import (
	"errors"
	"testing"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

// drawScene is an Open exchange with n participants joined. Its organizer is not one of them.
type drawScene struct {
	scene
	people []person
}

func newDrawScene(t *testing.T, n int) drawScene {
	t.Helper()

	s := newScene(t)
	people := make([]person, 0, n)
	for range n {
		p := s.h.newPerson(t)
		s.h.join(t, p, s.group, s.exchange)
		people = append(people, p)

		// Distinct join times, so the order people joined in is the order the database lists them.
		s.h.clock.Advance(time.Second)
	}

	return drawScene{scene: s, people: people}
}

// forceCycle excludes everything but the next person in join order, so the only valid draw is
// the cycle in which each person gives to the next and the last gives to the first.
func (s drawScene) forceCycle(t *testing.T) {
	t.Helper()

	n := len(s.people)
	for giver := range n {
		for recipient := range n {
			isNext := recipient == (giver+1)%n
			if recipient != giver && !isNext {
				s.exclude(t, s.people[giver], s.people[recipient], false)
			}
		}
	}
}

func (s scene) draw(t *testing.T) error {
	t.Helper()

	err := s.h.svc.Draw(t.Context(), s.organizer.actor, s.group, s.exchange)

	return err //nolint:wrapcheck // test helper passes the module error through
}

func (s scene) mustDraw(t *testing.T) {
	t.Helper()

	err := s.draw(t)
	if err != nil {
		t.Fatalf("Draw: %v", err)
	}
}

func (s scene) exclude(t *testing.T, giver, recipient person, mutual bool) {
	t.Helper()

	err := s.h.svc.AddExclusion(t.Context(), s.organizer.actor, s.group, s.exchange, giver.id, recipient.id, mutual)
	if err != nil {
		t.Fatalf("AddExclusion: %v", err)
	}
}

// assignmentsOf reads every participant's own recipient through MyAssignment.
func assignmentsOf(
	t testing.TB,
	h *harness,
	x giftexchange.ExchangeID,
	people []person,
) map[giftexchange.PersonID]giftexchange.PersonID {
	t.Helper()

	recipients := make(map[giftexchange.PersonID]giftexchange.PersonID, len(people))
	for _, p := range people {
		assignment, err := h.svc.MyAssignment(t.Context(), p.actor, x)
		if err != nil {
			t.Fatalf("MyAssignment: %v", err)
		}
		recipients[p.id] = assignment.Recipient.ID
	}

	return recipients
}

// permutationProblem returns why recipients is not a permutation of people with no self pair, or
// the empty string when it is one.
func permutationProblem(people []person, recipients map[giftexchange.PersonID]giftexchange.PersonID) string {
	received := make(map[giftexchange.PersonID]bool, len(people))
	for _, p := range people {
		recipient, ok := recipients[p.id]
		switch {
		case !ok:
			return "a participant has no recipient"
		case recipient == p.id:
			return "a participant gives to themselves"
		case received[recipient]:
			return "two participants give to the same recipient"
		}
		received[recipient] = true
	}
	for _, p := range people {
		if !received[p.id] {
			return "a participant receives nothing"
		}
	}

	return ""
}

func TestDrawGivesEveryParticipantADistinctOtherRecipient(t *testing.T) {
	for _, n := range []int{3, 4, 10} {
		s := newDrawScene(t, n)

		s.mustDraw(t)

		recipients := assignmentsOf(t, s.h, s.exchange, s.people)
		if problem := permutationProblem(s.people, recipients); problem != "" {
			t.Errorf("n=%d: %s", n, problem)
		}
	}
}

func TestDrawWithTheMaximumOf100ParticipantsSucceeds(t *testing.T) {
	s := newDrawScene(t, giftexchange.MaxParticipants)

	s.mustDraw(t)

	recipients := assignmentsOf(t, s.h, s.exchange, s.people)
	if problem := permutationProblem(s.people, recipients); problem != "" {
		t.Error(problem)
	}
}

func TestDrawMovesTheExchangeToDrawnInRoundOne(t *testing.T) {
	s := newDrawScene(t, 3)
	_, ruleErr := s.set(t, s.organizer, giftexchange.RuleDrawAt, s.h.futureText(24*time.Hour))
	if ruleErr != nil {
		t.Fatalf("SetRule: %v", ruleErr)
	}

	s.mustDraw(t)

	view, err := s.h.svc.Exchange(t.Context(), s.organizer.actor, s.group, s.exchange)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if view.State != giftexchange.StateDrawn {
		t.Errorf("state = %v, want drawn", view.State)
	}
	if view.DrawnAt == nil || !view.DrawnAt.Equal(s.h.clock.Now()) {
		t.Errorf("drawn_at = %v, want the module clock %v", view.DrawnAt, s.h.clock.Now())
	}
	if view.DrawAt != nil {
		t.Errorf("draw_at = %v, want it cleared by the draw", view.DrawAt)
	}
	if view.ParticipantCount != 3 {
		t.Errorf("participant count = %d, want 3", view.ParticipantCount)
	}
	for _, row := range notificationRows(t, s.exchange) {
		if row.round != 1 {
			t.Errorf("notification round = %d, want 1", row.round)
		}
	}
}

func TestDrawQueuesOneDrawnNoticeAndJobPerGiverAndNoneForTheOrganizer(t *testing.T) {
	s := newDrawScene(t, 4)

	s.mustDraw(t)

	rows := notificationRows(t, s.exchange)
	if len(rows) != len(s.people) {
		t.Fatalf("%d notifications, want %d", len(rows), len(s.people))
	}
	addressed := make(map[string]int)
	for _, row := range rows {
		if row.kind != string(giftexchange.KindDrawn) {
			t.Errorf("kind = %s, want drawn", row.kind)
		}
		addressed[row.person]++
	}
	for _, p := range s.people {
		if addressed[p.id.String()] != 1 {
			t.Errorf("a giver got %d drawn notices, want 1", addressed[p.id.String()])
		}
	}
	if addressed[s.organizer.id.String()] != 0 {
		t.Error("an organizer who did not join got a drawn notice")
	}
	if got := countJobs(t, s.h); got != len(s.people) {
		t.Errorf("%d jobs, want %d", got, len(s.people))
	}
}

func TestDrawWithFewerThanThreeParticipantsReportsTooFewAndChangesNothing(t *testing.T) {
	for _, n := range []int{0, 1, 2} {
		s := newDrawScene(t, n)
		before := takeSnapshot(t, s.h, s.exchange)

		err := s.draw(t)

		if !errors.Is(err, giftexchange.ErrTooFewParticipants) {
			t.Errorf("n=%d: err = %v, want ErrTooFewParticipants", n, err)
		}
		assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))
	}
}

func TestDrawWithExclusionsThatLeaveNoAssignmentReportsHowManyAreStranded(t *testing.T) {
	tests := []struct {
		name     string
		exclude  [][2]int
		stranded int
	}{
		{"one giver with no recipient left", [][2]int{{0, 1}, {0, 2}}, 1},
		{"two givers with no recipient left", [][2]int{{0, 1}, {0, 2}, {1, 0}, {1, 2}}, 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newDrawScene(t, 3)
			for _, pair := range tc.exclude {
				s.exclude(t, s.people[pair[0]], s.people[pair[1]], false)
			}
			before := takeSnapshot(t, s.h, s.exchange)

			err := s.draw(t)

			var infeasible *giftexchange.InfeasibleError
			if !errors.As(err, &infeasible) || !errors.Is(err, giftexchange.ErrNoValidAssignment) {
				t.Fatalf("err = %v, want an *InfeasibleError", err)
			}
			if infeasible.Stranded != tc.stranded {
				t.Errorf("stranded = %d, want %d", infeasible.Stranded, tc.stranded)
			}
			assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))
		})
	}
}

// With one allowed recipient per giver the only valid assignment is a cycle that random
// shuffling almost never hits, so the draw has to find it by other means.
func TestDrawFindsTheOnlyValidAssignmentOfEightParticipants(t *testing.T) {
	const n = 8
	s := newDrawScene(t, n)
	s.forceCycle(t)

	s.mustDraw(t)

	recipients := assignmentsOf(t, s.h, s.exchange, s.people)
	for giver, p := range s.people {
		want := s.people[(giver+1)%n].id
		if recipients[p.id] != want {
			t.Errorf("giver %d does not give to the only allowed recipient", giver)
		}
	}
}

func TestDrawNeverUsesAnExcludedPair(t *testing.T) {
	s := newDrawScene(t, 4)
	s.exclude(t, s.people[0], s.people[1], true)
	s.exclude(t, s.people[2], s.people[3], false)

	s.mustDraw(t)

	recipients := assignmentsOf(t, s.h, s.exchange, s.people)
	if recipients[s.people[0].id] == s.people[1].id || recipients[s.people[1].id] == s.people[0].id {
		t.Error("a mutual exclusion was drawn")
	}
	if recipients[s.people[2].id] == s.people[3].id {
		t.Error("a one-way exclusion was drawn")
	}
}

func TestSameSeedAndSameParticipantsGiveTheSameAssignmentWhateverTheJoinOrder(t *testing.T) {
	first := newDrawScene(t, 6)
	second := newScene(t)
	// Same people, joined in reverse order, in a second exchange of the same service.
	for i := len(first.people) - 1; i >= 0; i-- {
		second.h.join(t, first.people[i], second.group, second.exchange)
		second.h.clock.Advance(time.Second)
	}
	seed := [32]byte{7, 7, 7}

	first.h.random.Reseed(seed)
	first.mustDraw(t)
	second.h.random.Reseed(seed)
	second.mustDraw(t)

	got := assignmentsOf(t, second.h, second.exchange, first.people)
	want := assignmentsOf(t, first.h, first.exchange, first.people)
	for _, p := range first.people {
		if got[p.id] != want[p.id] {
			t.Error("a participant's recipient depends on the order people joined in")
		}
	}
}

func TestDrawTwiceReportsStateErrorAndChangesNothingMore(t *testing.T) {
	s := newDrawScene(t, 3)
	s.mustDraw(t)
	before := takeSnapshot(t, s.h, s.exchange)

	err := s.draw(t)

	var stateErr *giftexchange.StateError
	if !errors.As(err, &stateErr) || stateErr.Op != "draw" || stateErr.State != giftexchange.StateDrawn {
		t.Errorf("err = %v, want a draw *StateError in state drawn", err)
	}
	assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))
}

func TestParticipantsAndRulesLockedByTheDrawStayLocked(t *testing.T) {
	s := newDrawScene(t, 3)
	s.mustDraw(t)
	latecomer := s.h.newPerson(t)
	before := takeSnapshot(t, s.h, s.exchange)

	joinErr := s.h.svc.Join(t.Context(), latecomer.actor, s.group, s.exchange)
	exclusionErr := s.h.svc.AddExclusion(t.Context(), s.organizer.actor, s.group, s.exchange,
		s.people[0].id, s.people[1].id, false)

	if !errors.Is(joinErr, giftexchange.ErrInvalidState) {
		t.Errorf("join after the draw: err = %v, want ErrInvalidState", joinErr)
	}
	if !errors.Is(exclusionErr, giftexchange.ErrInvalidState) {
		t.Errorf("exclusion after the draw: err = %v, want ErrInvalidState", exclusionErr)
	}
	assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))
}

func TestDrawAfterCancelReportsStateErrorAndChangesNothing(t *testing.T) {
	s := newDrawScene(t, 3)
	if err := s.h.svc.Cancel(t.Context(), s.organizer.actor, s.group, s.exchange); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	before := takeSnapshot(t, s.h, s.exchange)

	err := s.draw(t)

	if !errors.Is(err, giftexchange.ErrInvalidState) {
		t.Errorf("err = %v, want ErrInvalidState", err)
	}
	assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))
}

func TestMyAssignmentOutsideDrawnReportsStateError(t *testing.T) {
	open := newDrawScene(t, 3)
	cancelled := newDrawScene(t, 3)
	cancelled.mustDraw(t)
	if err := cancelled.h.svc.Cancel(t.Context(), cancelled.organizer.actor, cancelled.group, cancelled.exchange); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	for name, s := range map[string]drawScene{"open": open, "cancelled": cancelled} {
		_, err := s.h.svc.MyAssignment(t.Context(), s.people[0].actor, s.exchange)

		if !errors.Is(err, giftexchange.ErrInvalidState) {
			t.Errorf("%s: err = %v, want ErrInvalidState", name, err)
		}
	}
}

func TestCancelInDrawnDeletesTheAssignmentsAndNotifiesEveryParticipant(t *testing.T) {
	s := newDrawScene(t, 3)
	s.mustDraw(t)

	err := s.h.svc.Cancel(t.Context(), s.organizer.actor, s.group, s.exchange)
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	var stored int
	countErr := shared.Pool().QueryRow(t.Context(),
		"SELECT count(*) FROM giftexchange.assignments WHERE exchange_id = $1", s.exchange.String()).Scan(&stored)
	if countErr != nil {
		t.Fatalf("count assignments: %v", countErr)
	}
	if stored != 0 {
		t.Errorf("%d assignments left after the cancel, want 0", stored)
	}
	cancelledTo := 0
	for _, row := range notificationRows(t, s.exchange) {
		if row.kind == string(giftexchange.KindCancelled) {
			cancelledTo++
		}
	}
	if cancelledTo != len(s.people) {
		t.Errorf("%d cancelled notices, want %d", cancelledTo, len(s.people))
	}
}
