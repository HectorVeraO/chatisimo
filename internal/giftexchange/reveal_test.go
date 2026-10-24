package giftexchange_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

func (s scene) reveal(t *testing.T) error {
	t.Helper()

	err := s.h.svc.Reveal(t.Context(), s.organizer.actor, s.group, s.exchange)

	return err //nolint:wrapcheck // test helper passes the module error through
}

func (s scene) mustReveal(t *testing.T) {
	t.Helper()

	err := s.reveal(t)
	if err != nil {
		t.Fatalf("Reveal: %v", err)
	}
}

// revealedScene is a drawn and revealed exchange of n participants.
func revealedScene(t *testing.T, n int) drawScene {
	t.Helper()

	s := newDrawScene(t, n)
	s.mustDraw(t)
	s.mustReveal(t)

	return s
}

func countKind(t *testing.T, x giftexchange.ExchangeID, kind giftexchange.NotificationKind) int {
	t.Helper()

	count := 0
	for _, row := range notificationRows(t, x) {
		if row.kind == string(kind) {
			count++
		}
	}

	return count
}

func TestRevealMovesToRevealedAtTheModuleClock(t *testing.T) {
	s := newDrawScene(t, 4)
	s.mustDraw(t)
	s.h.clock.Advance(time.Hour)

	s.mustReveal(t)

	view, err := s.h.svc.Exchange(t.Context(), s.organizer.actor, s.group, s.exchange)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if view.State != giftexchange.StateRevealed {
		t.Errorf("state = %v, want revealed", view.State)
	}
	if view.RevealedAt == nil || !view.RevealedAt.Equal(s.h.clock.Now().Truncate(time.Microsecond)) {
		t.Errorf("revealed_at = %v, want the module clock %v", view.RevealedAt, s.h.clock.Now())
	}
}

func TestRevealKeepsTheAssignments(t *testing.T) {
	s := newDrawScene(t, 4)
	s.mustDraw(t)
	before := assignmentsOf(t, s.h, s.exchange, s.people)

	s.mustReveal(t)

	after := assignmentsOf(t, s.h, s.exchange, s.people)
	for _, p := range s.people {
		if before[p.id] != after[p.id] {
			t.Error("the reveal changed an assignment")
		}
	}
}

func TestRevealQueuesOneRevealedNoticeAndJobPerRecipient(t *testing.T) {
	s := newDrawScene(t, 4)
	s.mustDraw(t)
	jobsBefore := countJobs(t, s.h)

	s.mustReveal(t)

	if got := countKind(t, s.exchange, giftexchange.KindRevealed); got != len(s.people) {
		t.Errorf("%d revealed notices, want %d", got, len(s.people))
	}
	if got := countJobs(t, s.h) - jobsBefore; got != len(s.people) {
		t.Errorf("%d new jobs, want %d", got, len(s.people))
	}
	addressees := revealedAddressees(t, s.exchange)
	for _, p := range s.people {
		if !slices.Contains(addressees, p.id.String()) {
			t.Error("a recipient got no revealed notice")
		}
	}
}

func TestRevealQueuesNoRevealedNoticeForAnOrganizerWhoDidNotJoin(t *testing.T) {
	s := newDrawScene(t, 4)
	s.mustDraw(t)

	s.mustReveal(t)

	if slices.Contains(revealedAddressees(t, s.exchange), s.organizer.id.String()) {
		t.Error("an organizer who did not join got a revealed notice")
	}
}

func revealedAddressees(t *testing.T, x giftexchange.ExchangeID) []string {
	t.Helper()
	var addressees []string
	for _, row := range notificationRows(t, x) {
		if row.kind == string(giftexchange.KindRevealed) {
			addressees = append(addressees, row.person)
		}
	}

	return addressees
}

func TestRevealedNoticesAreQueuedInTheRoundOfTheFinalAssignments(t *testing.T) {
	s := newDrawScene(t, 4)
	s.mustDraw(t)
	s.leave(t, s.people[0])

	s.mustReveal(t)

	round := 0
	if err := shared.Pool().QueryRow(t.Context(),
		"SELECT round FROM giftexchange.exchanges WHERE id = $1", s.exchange.String()).Scan(&round); err != nil {
		t.Fatalf("read round: %v", err)
	}
	if got := countKind(t, s.exchange, giftexchange.KindRevealed); got != len(s.people)-1 {
		t.Errorf("%d revealed notices after a dropout, want %d", got, len(s.people)-1)
	}
	for _, row := range notificationRows(t, s.exchange) {
		if row.kind == string(giftexchange.KindRevealed) && row.round != round {
			t.Errorf("revealed notice round = %d, want %d", row.round, round)
		}
	}
}

func TestRevealDenialsLeaveTheRowsAndJobsUnchanged(t *testing.T) {
	s := newDrawScene(t, 3)
	s.mustDraw(t)
	stranger := s.h.newPerson(t)
	otherGroup := s.h.newGroup(t)
	before := takeSnapshot(t, s.h, s.exchange)

	cases := []struct {
		name string
		want error
		call func() error
	}{
		{"participant", giftexchange.ErrForbidden, func() error {
			return s.h.svc.Reveal(t.Context(), s.people[0].actor, s.group, s.exchange)
		}},
		{"stranger in the group", giftexchange.ErrForbidden, func() error {
			return s.h.svc.Reveal(t.Context(), stranger.actor, s.group, s.exchange)
		}},
		{"organizer naming another group", giftexchange.ErrNotFound, func() error {
			return s.h.svc.Reveal(t.Context(), s.organizer.actor, otherGroup, s.exchange)
		}},
		{"participant naming another group", giftexchange.ErrNotFound, func() error {
			return s.h.svc.Reveal(t.Context(), s.people[0].actor, otherGroup, s.exchange)
		}},
		{"unknown exchange", giftexchange.ErrNotFound, func() error {
			return s.h.svc.Reveal(t.Context(), s.organizer.actor, s.group, unknownExchange(t))
		}},
		{"zero actor", giftexchange.ErrUnauthenticated, func() error {
			return s.h.svc.Reveal(t.Context(), giftexchange.Actor{}, s.group, s.exchange)
		}},
		{"zero exchange ID", giftexchange.ErrInvalidInput, func() error {
			return s.h.svc.Reveal(t.Context(), s.organizer.actor, s.group, giftexchange.ExchangeID{})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
			assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))
		})
	}
}

func TestRevealOutsideDrawnReportsStateErrorAndChangesNothing(t *testing.T) {
	open := newDrawScene(t, 3)
	revealed := revealedScene(t, 3)
	cancelled := newDrawScene(t, 3)
	cancelled.mustDraw(t)
	if err := cancelled.h.svc.Cancel(t.Context(), cancelled.organizer.actor, cancelled.group, cancelled.exchange); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	for name, s := range map[string]drawScene{"open": open, "revealed": revealed, "cancelled": cancelled} {
		t.Run(name, func(t *testing.T) {
			before := takeSnapshot(t, s.h, s.exchange)

			err := s.reveal(t)

			var stateErr *giftexchange.StateError
			if !errors.As(err, &stateErr) {
				t.Fatalf("err = %v, want a *StateError", err)
			}
			if stateErr.Op != "reveal" {
				t.Errorf("state error op = %q, want reveal", stateErr.Op)
			}
			assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))
		})
	}
}

func TestCancelAfterRevealReportsStateError(t *testing.T) {
	s := revealedScene(t, 3)

	err := s.h.svc.Cancel(t.Context(), s.organizer.actor, s.group, s.exchange)

	if !errors.Is(err, giftexchange.ErrInvalidState) {
		t.Errorf("err = %v, want ErrInvalidState", err)
	}
}

func TestMyGiverReturnsTheCallersOwnGiverAfterTheReveal(t *testing.T) {
	s := newDrawScene(t, 5)
	s.mustDraw(t)
	recipients := assignmentsOf(t, s.h, s.exchange, s.people)
	s.mustReveal(t)

	for _, p := range s.people {
		got, err := s.h.svc.MyGiver(t.Context(), p.actor, s.exchange)
		if err != nil {
			t.Fatalf("MyGiver: %v", err)
		}

		if want := giverOf(recipients, p.id); got.Giver.ID != want {
			t.Error("MyGiver did not return the person who gives to the caller")
		}
		if got.Exchange != s.exchange {
			t.Error("MyGiver names another exchange")
		}
		if len(got.Giver.Identities) != 1 {
			t.Errorf("giver identities = %d, want 1", len(got.Giver.Identities))
		}
	}
}

func TestMyAssignmentStaysReadableAfterTheReveal(t *testing.T) {
	s := newDrawScene(t, 3)
	s.mustDraw(t)
	before := assignmentsOf(t, s.h, s.exchange, s.people)
	s.mustReveal(t)

	after := assignmentsOf(t, s.h, s.exchange, s.people)

	for _, p := range s.people {
		if before[p.id] != after[p.id] {
			t.Error("MyAssignment changed after the reveal")
		}
	}
}

func TestMyGiverDenials(t *testing.T) {
	open := newDrawScene(t, 4)
	drawn := newDrawScene(t, 4)
	drawn.mustDraw(t)
	cancelled := newDrawScene(t, 4)
	cancelled.mustDraw(t)
	if err := cancelled.h.svc.Cancel(t.Context(), cancelled.organizer.actor, cancelled.group, cancelled.exchange); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	for name, s := range map[string]drawScene{"open": open, "drawn": drawn, "cancelled": cancelled} {
		t.Run("before the reveal: "+name, func(t *testing.T) {
			_, err := s.h.svc.MyGiver(t.Context(), s.people[0].actor, s.exchange)

			var stateErr *giftexchange.StateError
			if !errors.As(err, &stateErr) || stateErr.Op != "my_giver" {
				t.Errorf("err = %v, want a my_giver *StateError", err)
			}
		})
	}

	s := newDrawScene(t, 4)
	s.mustDraw(t)
	leaver := s.people[0]
	s.leave(t, leaver)
	s.mustReveal(t)
	stranger := s.h.newPerson(t)
	before := takeSnapshot(t, s.h, s.exchange)

	cases := []struct {
		name   string
		caller giftexchange.Actor
		id     giftexchange.ExchangeID
		want   error
	}{
		{"organizer who never joined", s.organizer.actor, s.exchange, giftexchange.ErrNotParticipant},
		{"participant who left", leaver.actor, s.exchange, giftexchange.ErrNotParticipant},
		{"stranger", stranger.actor, s.exchange, giftexchange.ErrNotFound},
		{"unknown exchange", s.people[1].actor, unknownExchange(t), giftexchange.ErrNotFound},
		{"zero actor", giftexchange.Actor{}, s.exchange, giftexchange.ErrUnauthenticated},
		{"zero exchange ID", s.people[1].actor, giftexchange.ExchangeID{}, giftexchange.ErrInvalidInput},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.h.svc.MyGiver(t.Context(), tc.caller, tc.id)

			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
			if !got.Giver.ID.IsZero() {
				t.Error("a denied MyGiver returned a giver")
			}
			assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))
		})
	}
}

func TestOrganizerWhoDidNotJoinLearnsNoGiverFromTheReveal(t *testing.T) {
	s := newDrawScene(t, 4)
	s.mustDraw(t)
	s.mustReveal(t)

	_, myGiverErr := s.h.svc.MyGiver(t.Context(), s.organizer.actor, s.exchange)
	_, myAssignmentErr := s.h.svc.MyAssignment(t.Context(), s.organizer.actor, s.exchange)
	view, err := s.h.svc.Exchange(t.Context(), s.organizer.actor, s.group, s.exchange)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}

	if !errors.Is(myGiverErr, giftexchange.ErrNotParticipant) || !errors.Is(myAssignmentErr, giftexchange.ErrNotParticipant) {
		t.Errorf("errors = %v and %v, want ErrNotParticipant for both", myGiverErr, myAssignmentErr)
	}
	if view.State != giftexchange.StateRevealed {
		t.Errorf("state = %v, want revealed", view.State)
	}
}

func TestRevealedNoticeIsDeliveredNamingTheAddresseesOwnGiverOnly(t *testing.T) {
	s := newDrawScene(t, 4)
	s.mustDraw(t)
	recipients := assignmentsOf(t, s.h, s.exchange, s.people)
	s.mustReveal(t)
	s.h.startRunner(t)

	waitForFinished(t, s.exchange, 2*len(s.people))

	revealed := receivedOfKind(s.h.notifier.Received(), giftexchange.KindRevealed)
	if len(revealed) != len(s.people) {
		t.Fatalf("%d revealed notices delivered, want %d", len(revealed), len(s.people))
	}
	for _, got := range revealed {
		if got.Counterpart == nil {
			t.Fatal("a revealed notice has no counterpart")
		}
		if want := giverOf(recipients, got.To.ID); got.Counterpart.ID != want {
			t.Error("a revealed notice does not name the addressee's own giver")
		}
		if got.Question != nil || got.Reason != "" {
			t.Error("a revealed notice carries a question or a reason")
		}
		if got.Exchange.ID != s.exchange || got.Exchange.State != giftexchange.StateRevealed {
			t.Error("a revealed notice describes another exchange or state")
		}
		if len(got.Counterpart.Identities) == 0 {
			t.Error("the counterpart has no identities loaded")
		}
	}
}

func TestRevealedNoticeIsSkippedAsStaleWhenTheExchangeIsNotRevealed(t *testing.T) {
	s := newDrawScene(t, 3)
	s.mustDraw(t)
	s.mustReveal(t)
	_, err := shared.Pool().Exec(t.Context(),
		"UPDATE giftexchange.exchanges SET state = 'cancelled', revealed_at = NULL, cancelled_at = $2 WHERE id = $1",
		s.exchange.String(), s.h.clock.Now())
	if err != nil {
		t.Fatalf("cancel after reveal: %v", err)
	}
	s.h.startRunner(t)

	waitForFinished(t, s.exchange, 2*len(s.people))

	if got := receivedOfKind(s.h.notifier.Received(), giftexchange.KindRevealed); len(got) != 0 {
		t.Errorf("%d revealed notices delivered for a cancelled exchange, want 0", len(got))
	}
	var stale int
	countErr := shared.Pool().QueryRow(t.Context(),
		"SELECT count(*) FROM giftexchange.notifications WHERE exchange_id = $1 AND kind = 'revealed' AND outcome = 'stale'",
		s.exchange.String()).Scan(&stale)
	if countErr != nil {
		t.Fatalf("count stale: %v", countErr)
	}
	if stale != len(s.people) {
		t.Errorf("%d stale revealed notices, want %d", stale, len(s.people))
	}
}

func TestRevealedNoticeIsSkippedAsStaleForAnAddresseeWhoNoLongerHasAGiver(t *testing.T) {
	s := newDrawScene(t, 3)
	s.mustDraw(t)
	s.mustReveal(t)
	orphan := s.people[0]
	_, err := shared.Pool().Exec(t.Context(),
		"DELETE FROM giftexchange.assignments WHERE exchange_id = $1 AND recipient_id = $2",
		s.exchange.String(), orphan.id.String())
	if err != nil {
		t.Fatalf("remove the addressee's giver: %v", err)
	}
	s.h.startRunner(t)

	waitForFinished(t, s.exchange, 2*len(s.people))

	for _, got := range receivedOfKind(s.h.notifier.Received(), giftexchange.KindRevealed) {
		if got.To.ID == orphan.id {
			t.Error("a revealed notice was delivered to an addressee without a giver")
		}
	}
	var stale int
	countErr := shared.Pool().QueryRow(t.Context(),
		"SELECT count(*) FROM giftexchange.notifications WHERE exchange_id = $1 AND kind = 'revealed' AND outcome = 'stale'",
		s.exchange.String()).Scan(&stale)
	if countErr != nil {
		t.Fatalf("count stale: %v", countErr)
	}
	if stale != 1 {
		t.Errorf("%d stale revealed notices, want 1", stale)
	}
}

func TestNoPersonIDOrSubjectReachesLogsErrorsOrJobsAcrossTheRevealAndItsDenials(t *testing.T) {
	s := newDrawScene(t, 4)
	stranger := s.h.newPerson(t)
	secrets := []string{s.organizer.id.String(), s.organizer.subject, stranger.id.String(), stranger.subject}
	for _, p := range s.people {
		secrets = append(secrets, p.id.String(), p.subject)
	}

	var errs []error
	errs = append(errs, s.reveal(t))
	_, err := s.h.svc.MyGiver(t.Context(), s.people[0].actor, s.exchange)
	errs = append(errs, err)
	s.mustDraw(t)
	errs = append(errs, s.h.svc.Reveal(t.Context(), stranger.actor, s.group, s.exchange))
	errs = append(errs, s.h.svc.Reveal(t.Context(), s.people[0].actor, s.group, s.exchange))
	s.mustReveal(t)
	errs = append(errs, s.reveal(t))
	for _, p := range append([]person{s.organizer, stranger}, s.people...) {
		_, err = s.h.svc.MyGiver(t.Context(), p.actor, s.exchange)
		errs = append(errs, err)
	}

	assertConfidential(t, s.h, errs, secrets)
}

func TestFailedDeliveryOfRevealedNoticesStoresNoPersonIDOrSubject(t *testing.T) {
	s := revealedScene(t, 3)
	s.h.notifier.FailWith(errors.New(sanitizedFailure))
	s.h.startRunner(t)
	secrets := []string{s.organizer.id.String(), s.organizer.subject, fakeUserA, fakeUserB}
	for _, p := range s.people {
		secrets = append(secrets, p.id.String(), p.subject)
	}

	waitFor(t, deliveryDeadline, "a revealed notice to fail", func() bool {
		for _, job := range jobStates(t, s.h) {
			if job.errors != "" {
				return true
			}
		}

		return false
	})

	assertConfidential(t, s.h, nil, secrets)
}

func TestRevealedNoticesCarryOnlyTheAddresseesOwnCounterpart(t *testing.T) {
	s := newDrawScene(t, 4)
	s.mustDraw(t)
	s.mustReveal(t)
	s.h.startRunner(t)
	waitForFinished(t, s.exchange, 2*len(s.people))

	for _, got := range receivedOfKind(s.h.notifier.Received(), giftexchange.KindRevealed) {
		addressee := personWithID(s.people, got.To.ID)
		own, err := s.h.svc.MyGiver(t.Context(), addressee.actor, s.exchange)
		if err != nil {
			t.Fatalf("MyGiver: %v", err)
		}

		if got.Counterpart == nil || got.Counterpart.ID != own.Giver.ID {
			t.Error("a revealed notice does not carry the addressee's own giver")
		}
		if got.To.ID == s.organizer.id {
			t.Error("an organizer who did not join received a revealed notice")
		}
	}
}

func TestMyGiverResultIsRedactedWhenFormatted(t *testing.T) {
	s := revealedScene(t, 3)

	got, err := s.h.svc.MyGiver(t.Context(), s.people[0].actor, s.exchange)
	if err != nil {
		t.Fatalf("MyGiver: %v", err)
	}

	assertNoLeak(t, "MyGiver result", got, "[redacted]")
}

// cycleRound creates an exchange in group with people joined, forces the draw to the cycle in
// which each person gives to the next one of people, and draws it. It leaves the reveal to the
// caller.
func cycleRound(t *testing.T, s drawScene, group giftexchange.GroupID, people []person) scene {
	t.Helper()

	view := s.h.newExchange(t, s.organizer, group)
	round := scene{h: s.h, group: group, organizer: s.organizer, exchange: view.ID}
	for _, p := range people {
		s.h.join(t, p, group, view.ID)
		s.h.clock.Advance(time.Second)
	}
	n := len(people)
	for giver := range n {
		for recipient := range n {
			isNext := recipient == (giver+1)%n
			if recipient != giver && !isNext {
				round.exclude(t, people[giver], people[recipient], false)
			}
		}
	}
	round.mustDraw(t)

	return round
}

func reversed(people []person) []person {
	out := slices.Clone(people)
	slices.Reverse(out)

	return out
}

// nextPairs counts the givers of a whose recipient is the next person of order, cyclically.
func nextPairs(order []person, a assignmentMap) int {
	count := 0
	for i, giver := range order {
		if a[giver.id] == order[(i+1)%len(order)].id {
			count++
		}
	}

	return count
}

func TestAvoidLastRoundExcludesTheRevealedRoundOfTheSameGroupOnly(t *testing.T) {
	const attempts = 30
	foreignPairs := 0
	for attempt := range uint64(attempts) {
		s := newDrawScene(t, 4)
		s.h.random.Reseed(seedOf(attempt))
		last := cycleRound(t, s, s.group, s.people)
		last.mustReveal(t)
		foreign := cycleRound(t, s, s.h.newGroup(t), reversed(s.people))
		foreign.mustReveal(t)

		next := s.h.newExchange(t, s.organizer, s.group)
		for _, p := range s.people {
			s.h.join(t, p, s.group, next.ID)
		}
		_, err := s.h.svc.SetRule(t.Context(), s.organizer.actor, s.group, next.ID,
			giftexchange.RuleChange{Name: giftexchange.RuleAvoidLastRound, Raw: "on"})
		if err != nil {
			t.Fatalf("SetRule: %v", err)
		}
		drawErr := s.h.svc.Draw(t.Context(), s.organizer.actor, s.group, next.ID)
		if drawErr != nil {
			t.Fatalf("Draw: %v", drawErr)
		}

		assigned := assignmentsOf(t, s.h, next.ID, s.people)
		if nextPairs(s.people, assigned) != 0 {
			t.Fatalf("attempt %d repeated a pair of the last Revealed exchange", attempt)
		}
		foreignPairs += nextPairs(reversed(s.people), assigned)
	}

	if foreignPairs == 0 {
		t.Error("a pair of another group's Revealed exchange was never drawn")
	}
}

func TestAvoidLastRoundBreaksARevealedAtTieByTheNewerExchangeID(t *testing.T) {
	const attempts = 30
	for attempt := range uint64(attempts) {
		s := newDrawScene(t, 4)
		s.h.random.Reseed(seedOf(attempt))
		older := cycleRound(t, s, s.group, reversed(s.people))
		newer := cycleRound(t, s, s.group, s.people)
		older.mustReveal(t)
		newer.mustReveal(t)

		next := s.h.newExchange(t, s.organizer, s.group)
		for _, p := range s.people {
			s.h.join(t, p, s.group, next.ID)
		}
		_, err := s.h.svc.SetRule(t.Context(), s.organizer.actor, s.group, next.ID,
			giftexchange.RuleChange{Name: giftexchange.RuleAvoidLastRound, Raw: "on"})
		if err != nil {
			t.Fatalf("SetRule: %v", err)
		}
		drawErr := s.h.svc.Draw(t.Context(), s.organizer.actor, s.group, next.ID)
		if drawErr != nil {
			t.Fatalf("Draw: %v", drawErr)
		}

		assigned := assignmentsOf(t, s.h, next.ID, s.people)
		if nextPairs(s.people, assigned) != 0 {
			t.Fatalf("attempt %d repeated a pair of the newer of two tied Revealed exchanges", attempt)
		}
	}
}

func TestAvoidLastRoundOffMayRepeatTheRevealedRound(t *testing.T) {
	repeated := false
	for attempt := range uint64(30) {
		s := newDrawScene(t, 4)
		s.h.random.Reseed(seedOf(attempt))
		last := cycleRound(t, s, s.group, s.people)
		last.mustReveal(t)

		next := s.h.newExchange(t, s.organizer, s.group)
		for _, p := range s.people {
			s.h.join(t, p, s.group, next.ID)
		}
		drawErr := s.h.svc.Draw(t.Context(), s.organizer.actor, s.group, next.ID)
		if drawErr != nil {
			t.Fatalf("Draw: %v", drawErr)
		}

		assigned := assignmentsOf(t, s.h, next.ID, s.people)
		if nextPairs(s.people, assigned) > 0 {
			repeated = true
		}
	}

	if !repeated {
		t.Error("with avoid_last_round off no draw ever repeated a pair of the Revealed round")
	}
}

func TestAvoidLastRoundThatLeavesNoDerangementIsInfeasibleAndChangesNothing(t *testing.T) {
	s := newDrawScene(t, 3)
	last := cycleRound(t, s, s.group, s.people)
	last.mustReveal(t)

	next := s.h.newExchange(t, s.organizer, s.group)
	nextScene := scene{h: s.h, group: s.group, organizer: s.organizer, exchange: next.ID}
	for _, p := range s.people {
		s.h.join(t, p, s.group, next.ID)
	}
	// The reverse cycle is the only derangement the last round leaves; excluding it as well
	// leaves none.
	reverse := reversed(s.people)
	for i, giver := range reverse {
		nextScene.exclude(t, giver, reverse[(i+1)%len(reverse)], false)
	}
	_, err := s.h.svc.SetRule(t.Context(), s.organizer.actor, s.group, next.ID,
		giftexchange.RuleChange{Name: giftexchange.RuleAvoidLastRound, Raw: "on"})
	if err != nil {
		t.Fatalf("SetRule: %v", err)
	}
	before := takeSnapshot(t, s.h, next.ID)

	drawErr := nextScene.draw(t)

	var infeasible *giftexchange.InfeasibleError
	if !errors.As(drawErr, &infeasible) {
		t.Fatalf("err = %v, want an *InfeasibleError", drawErr)
	}
	assertUnchanged(t, before, takeSnapshot(t, s.h, next.ID))
}
