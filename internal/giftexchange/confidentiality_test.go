package giftexchange_test

import (
	"errors"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

// TestNoPersonIDOrSubjectReachesLogsErrorsOrJobsAcrossTheLifecycle drives every operation of
// this slice, denials included, and checks items 1 to 3 of assignment confidentiality.
func TestNoPersonIDOrSubjectReachesLogsErrorsOrJobsAcrossTheLifecycle(t *testing.T) {
	s := newScene(t)
	ctx := t.Context()
	joiner := s.h.newPerson(t)
	leaver := s.h.newPerson(t)
	stranger := s.h.newPerson(t)
	removed := s.h.newPerson(t)
	otherGroup := s.h.newGroup(t)

	secrets := []string{
		s.organizer.id.String(), s.organizer.subject,
		joiner.id.String(), joiner.subject,
		leaver.id.String(), leaver.subject,
		stranger.id.String(), stranger.subject,
		removed.id.String(), removed.subject,
	}

	var errs []error
	record := func(err error) {
		errs = append(errs, err)
	}

	record(s.h.svc.Join(ctx, joiner.actor, s.group, s.exchange))
	record(s.h.svc.Join(ctx, joiner.actor, s.group, s.exchange))
	record(s.h.svc.Join(ctx, leaver.actor, s.group, s.exchange))
	record(s.h.svc.Join(ctx, removed.actor, s.group, s.exchange))
	_, err := s.h.svc.Leave(ctx, leaver.actor, s.group, s.exchange)
	record(err)
	_, err = s.h.svc.Leave(ctx, stranger.actor, s.group, s.exchange)
	record(err)
	_, err = s.h.svc.RemoveParticipant(ctx, s.organizer.actor, s.group, s.exchange, removed.id)
	record(err)
	_, err = s.h.svc.RemoveParticipant(ctx, stranger.actor, s.group, s.exchange, joiner.id)
	record(err)
	_, err = s.h.svc.RemoveParticipant(ctx, s.organizer.actor, s.group, s.exchange, stranger.id)
	record(err)
	record(s.h.svc.Rename(ctx, stranger.actor, s.group, s.exchange, "Hijacked"))
	record(s.h.svc.Rename(ctx, s.organizer.actor, s.group, s.exchange, ""))
	_, err = s.h.svc.Exchange(ctx, stranger.actor, otherGroup, s.exchange)
	record(err)
	_, err = s.h.svc.CreateExchange(ctx, s.organizer.actor, giftexchange.CreateExchangeRequest{Group: s.group})
	record(err)
	record(s.h.svc.Cancel(ctx, stranger.actor, s.group, s.exchange))
	record(s.h.svc.Cancel(ctx, s.organizer.actor, s.group, s.exchange))
	record(s.h.svc.Cancel(ctx, s.organizer.actor, s.group, s.exchange))
	record(s.h.svc.Join(ctx, stranger.actor, s.group, s.exchange))
	_, err = s.h.svc.ResolveActor(ctx,
		giftexchange.IdentityRef{Provider: "slack", Subject: stranger.subject})
	record(err)

	deniedCount := 0
	for _, err := range errs {
		if err != nil {
			deniedCount++
		}
	}
	if deniedCount == 0 {
		t.Fatal("scenario produced no errors; it checks nothing")
	}
	assertConfidential(t, s.h, errs, secrets)
}

func TestNothingIsDeliveredWhileNoRunnerWorksTheJobs(t *testing.T) {
	s := newScene(t)
	joiner := s.h.newPerson(t)
	s.h.join(t, joiner, s.group, s.exchange)

	if err := s.h.svc.Cancel(t.Context(), s.organizer.actor, s.group, s.exchange); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	if got := s.h.notifier.Received(); len(got) != 0 {
		t.Errorf("notifier received %d notifications, want 0 while no Runner works the jobs", len(got))
	}
}

func TestStateErrorsNameTheOperationAndStateOnly(t *testing.T) {
	s := newScene(t)
	if err := s.h.svc.Cancel(t.Context(), s.organizer.actor, s.group, s.exchange); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	err := s.h.svc.Rename(t.Context(), s.organizer.actor, s.group, s.exchange, "Secret name Z9")

	if !errors.Is(err, giftexchange.ErrInvalidState) {
		t.Fatalf("err = %v, want ErrInvalidState", err)
	}
	var stateErr *giftexchange.StateError
	if !errors.As(err, &stateErr) {
		t.Fatalf("err = %v, want a *StateError", err)
	}
	if stateErr.Op != "rename" || stateErr.State != giftexchange.StateCancelled {
		t.Errorf("state error = (%q, %q), want (rename, cancelled)", stateErr.Op, stateErr.State)
	}
	assertNoSecrets(t, "state error", err.Error(),
		[]string{"Secret name Z9", s.exchange.String(), s.organizer.id.String(), s.organizer.subject})
}

// TestNoPersonIDOrSubjectReachesLogsErrorsOrJobsAcrossTheDrawAndItsDenials drives the draw and
// every denial around it, and checks items 1 to 3 of assignment confidentiality after each step.
func TestNoPersonIDOrSubjectReachesLogsErrorsOrJobsAcrossTheDrawAndItsDenials(t *testing.T) {
	s := newScene(t)
	ctx := t.Context()
	first := s.h.newPerson(t)
	second := s.h.newPerson(t)
	third := s.h.newPerson(t)
	stranger := s.h.newPerson(t)
	people := []person{s.organizer, first, second, third, stranger}

	var secrets []string
	for _, p := range people {
		secrets = append(secrets, p.id.String(), p.subject)
	}

	var errs []error
	record := func(err error) {
		errs = append(errs, err)
	}
	step := func(name string) {
		assertConfidential(t, s.h, errs, secrets)
		if t.Failed() {
			t.Fatalf("a secret leaked after %s", name)
		}
	}

	s.h.join(t, first, s.group, s.exchange)
	s.h.join(t, second, s.group, s.exchange)
	record(s.draw(t))
	step("a draw with too few participants")

	s.h.join(t, third, s.group, s.exchange)
	s.exclude(t, first, second, false)
	s.exclude(t, first, third, false)
	record(s.draw(t))
	step("a draw the exclusions make impossible")

	record(s.h.svc.RemoveExclusion(ctx, s.organizer.actor, s.group, s.exchange, first.id, third.id))
	record(s.h.svc.Draw(ctx, stranger.actor, s.group, s.exchange))
	record(s.h.svc.Draw(ctx, first.actor, s.group, s.exchange))
	_, err := s.h.svc.MyAssignment(ctx, first.actor, s.exchange)
	record(err)
	step("denials before the draw")

	s.mustDraw(t)
	record(s.draw(t))
	for _, p := range people {
		_, err = s.h.svc.MyAssignment(ctx, p.actor, s.exchange)
		record(err)
	}
	step("the draw and the reads that follow it")

	failed := 0
	for _, err := range errs {
		if err != nil {
			failed++
		}
	}
	if failed == 0 {
		t.Fatal("scenario produced no errors; it checks nothing")
	}
}

func TestCancelInDrawnLeaksNoPersonIDOrSubject(t *testing.T) {
	s := newDrawScene(t, 3)
	s.mustDraw(t)
	secrets := []string{s.organizer.id.String(), s.organizer.subject}
	for _, p := range s.people {
		secrets = append(secrets, p.id.String(), p.subject)
	}

	err := s.h.svc.Cancel(t.Context(), s.organizer.actor, s.group, s.exchange)
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	assertConfidential(t, s.h, []error{err}, secrets)
}

func TestFailedDeliveryOfDrawnNoticesStoresNoPersonIDOrSubject(t *testing.T) {
	s := newDrawScene(t, 3)
	s.mustDraw(t)
	s.h.notifier.FailWith(errors.New(sanitizedFailure))
	s.h.startRunner(t)
	secrets := []string{s.organizer.id.String(), s.organizer.subject, fakeUserA, fakeUserB}
	for _, p := range s.people {
		secrets = append(secrets, p.id.String(), p.subject)
	}

	waitFor(t, deliveryDeadline, "a drawn notice to fail", func() bool {
		for _, job := range jobStates(t, s.h) {
			if job.errors != "" {
				return true
			}
		}
		return false
	})

	assertConfidential(t, s.h, nil, secrets)
}

func TestDeliveredNoticesCarryOnlyTheAddresseesOwnCounterpart(t *testing.T) {
	s := newDrawScene(t, 4)
	s.mustDraw(t)
	s.h.startRunner(t)
	waitForFinished(t, s.exchange, len(s.people))

	received := s.h.notifier.Received()
	if len(received) != len(s.people) {
		t.Fatalf("notifier received %d notifications, want %d", len(received), len(s.people))
	}
	for _, got := range received {
		if got.To.ID == s.organizer.id {
			t.Fatal("an organizer who did not join received a notification")
		}
		addressee := personWithID(s.people, got.To.ID)

		if got.Kind != giftexchange.KindDrawn {
			t.Fatalf("unexpected notification kind %v", got.Kind)
		}
		own, err := s.h.svc.MyAssignment(t.Context(), addressee.actor, s.exchange)
		if err != nil {
			t.Fatalf("MyAssignment: %v", err)
		}
		counterpart := own.Recipient.ID
		if got.Counterpart == nil || got.Counterpart.ID != counterpart {
			t.Errorf("a %v notice does not carry the addressee's own counterpart", got.Kind)
		}
		if got.Question != nil {
			t.Error("a pair notice carries a question")
		}
	}
}

func TestNonOrganizersSeeOnlyTheExclusionCountAfterTheDraw(t *testing.T) {
	s := newDrawScene(t, 4)
	s.exclude(t, s.people[0], s.people[1], false)
	s.exclude(t, s.people[2], s.people[3], true)
	s.mustDraw(t)
	viewer := s.people[0]

	view, err := s.h.svc.Exchange(t.Context(), viewer.actor, s.group, s.exchange)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	rules, rulesErr := s.h.svc.Rules(t.Context(), viewer.actor, s.group, s.exchange)
	if rulesErr != nil {
		t.Fatalf("Rules: %v", rulesErr)
	}
	_, exclusionsErr := s.h.svc.Exclusions(t.Context(), viewer.actor, s.group, s.exchange)

	if view.ExclusionCount != 3 {
		t.Errorf("exclusion count = %d, want 3 directed exclusions", view.ExclusionCount)
	}
	if got := ruleNamed(t, rules, giftexchange.RuleExclusions).Value; got != "3" {
		t.Errorf("exclusions rule value = %q, want the count 3", got)
	}
	if !errors.Is(exclusionsErr, giftexchange.ErrForbidden) {
		t.Errorf("Exclusions by a participant: err = %v, want ErrForbidden", exclusionsErr)
	}
}
