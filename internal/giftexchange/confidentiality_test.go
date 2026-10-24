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

func TestNothingIsDeliveredBecauseNoWorkerRunsInThisSlice(t *testing.T) {
	s := newScene(t)
	joiner := s.h.newPerson(t)
	s.h.join(t, joiner, s.group, s.exchange)

	if err := s.h.svc.Cancel(t.Context(), s.organizer.actor, s.group, s.exchange); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	if got := s.h.notifier.Received(); len(got) != 0 {
		t.Errorf("notifier received %d notifications, want 0 until a worker exists", len(got))
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
