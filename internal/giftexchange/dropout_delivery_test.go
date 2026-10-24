package giftexchange_test

import (
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

// assertDeliveredNoticeRules checks the confidentiality rules of every captured notification: a
// pair kind carries the addressee's own current recipient, a voided round carries a reason and no
// person, nothing else carries either, and the dropout and a non-participant organizer never get a
// counterpart.
func assertDeliveredNoticeRules(
	t *testing.T,
	s drawScene,
	received []giftexchange.Notification,
	dropout person,
) {
	t.Helper()

	for _, got := range received {
		if got.To.ID == dropout.id {
			t.Errorf("the dropout was sent a %v notice", got.Kind)
		}
		switch got.Kind {
		case giftexchange.KindDrawn, giftexchange.KindRedrawn, giftexchange.KindAssignmentChanged:
			addressee := personWithID(s.people, got.To.ID)
			own, err := s.h.svc.MyAssignment(t.Context(), addressee.actor, s.exchange)
			if err != nil {
				t.Fatalf("MyAssignment: %v", err)
			}
			if got.Counterpart == nil || got.Counterpart.ID != own.Recipient.ID {
				t.Errorf("a %v notice does not carry the addressee's own recipient", got.Kind)
			}
		case giftexchange.KindRoundVoided:
			if got.Counterpart != nil {
				t.Error("a round_voided notice carries a counterpart")
			}
			if got.Reason == "" {
				t.Error("a round_voided notice has no reason")
			}
		default:
			t.Errorf("unexpected notice kind %v", got.Kind)
		}
		isOrganizerOnly := got.To.ID == s.organizer.id
		if isOrganizerOnly && got.Counterpart != nil {
			t.Error("a non-participant organizer was sent a counterpart")
		}
	}
}

func TestPatchDeliversTheChangeToTheGiverAndKeepsPendingDrawnNoticesTrue(t *testing.T) {
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
	s.leave(t, dropout)
	s.h.startRunner(t)

	waitForFinished(t, s.exchange, len(s.people)+1)

	received := s.h.notifier.Received()
	assertDeliveredNoticeRules(t, s, received, dropout)
	if got := len(receivedOfKind(received, giftexchange.KindDrawn)); got != len(s.people)-1 {
		t.Errorf("%d drawn notices delivered, want %d: every giver's but the dropout's", got, len(s.people)-1)
	}
	changed := receivedOfKind(received, giftexchange.KindAssignmentChanged)
	if len(changed) != 1 || changed[0].To.ID != giver {
		t.Fatalf("assignment_changed notices = %d, want one to the dropout's giver", len(changed))
	}
	if finishedCount(t, s.exchange, "drawn", "stale") != 1 {
		t.Error("want exactly the dropout's drawn notice marked stale")
	}
}

func TestRedrawMarksPendingDrawnNoticesStaleAndDeliversRedrawnToEveryGiver(t *testing.T) {
	s, _ := drawnWhere(t, 4, nil, anyDraw)
	dropout := s.people[0]
	s.leave(t, dropout)
	s.h.startRunner(t)

	waitForFinished(t, s.exchange, len(s.people)+len(s.people)-1)

	received := s.h.notifier.Received()
	assertDeliveredNoticeRules(t, s, received, dropout)
	if got := len(receivedOfKind(received, giftexchange.KindDrawn)); got != 0 {
		t.Errorf("%d drawn notices of the old round delivered, want 0", got)
	}
	if got := len(receivedOfKind(received, giftexchange.KindRedrawn)); got != len(s.people)-1 {
		t.Errorf("%d redrawn notices delivered, want %d", got, len(s.people)-1)
	}
	if finishedCount(t, s.exchange, "drawn", "stale") != len(s.people) {
		t.Error("not every drawn notice of the old round is marked stale")
	}
}

func TestVoidDeliversRoundVoidedWithItsReasonAndNoCounterpart(t *testing.T) {
	s := newDrawScene(t, 3)
	s.mustDraw(t)
	dropout := s.people[0]
	s.leave(t, dropout)
	s.h.startRunner(t)

	waitForFinished(t, s.exchange, len(s.people)+len(s.people))

	received := s.h.notifier.Received()
	if len(received) != len(s.people) {
		t.Fatalf("%d notices delivered, want %d round_voided", len(received), len(s.people))
	}
	for _, got := range received {
		if got.Kind != giftexchange.KindRoundVoided || got.Reason != giftexchange.ReasonTooFewParticipants {
			t.Errorf("notice = (%v, %v), want round_voided with too_few_participants", got.Kind, got.Reason)
		}
		if got.Exchange.State != giftexchange.StateOpen {
			t.Errorf("exchange state in the notice = %v, want open", got.Exchange.State)
		}
	}
	assertDeliveredNoticeRules(t, s, received, dropout)
	if finishedCount(t, s.exchange, "drawn", "stale") != len(s.people) {
		t.Error("not every drawn notice of the voided round is marked stale")
	}
}

func TestDropoutsLeakNoPersonIDOrSubjectToLogsErrorsOrJobs(t *testing.T) {
	scenarios := map[string]func() (drawScene, person){
		"patch": func() (drawScene, person) {
			s, _ := drawnWhere(t, 5, nil, func(s drawScene, a assignmentMap) bool {
				for _, p := range s.people {
					if giverOf(a, p.id) != a[p.id] {
						return true
					}
				}
				return false
			})
			return s, s.people[0]
		},
		"redraw": func() (drawScene, person) {
			s, _ := drawnWhere(t, 4, nil, anyDraw)
			return s, s.people[0]
		},
		"void": func() (drawScene, person) {
			s := newDrawScene(t, 3)
			s.mustDraw(t)
			return s, s.people[0]
		},
	}

	for name, build := range scenarios {
		t.Run(name, func(t *testing.T) {
			s, dropout := build()
			secrets := []string{s.organizer.id.String(), s.organizer.subject}
			for _, p := range s.people {
				secrets = append(secrets, p.id.String(), p.subject)
			}

			outcome, err := s.h.svc.Leave(t.Context(), dropout.actor, s.group, s.exchange)
			_, removeErr := s.h.svc.RemoveParticipant(t.Context(), s.organizer.actor, s.group, s.exchange, dropout.id)

			if err != nil {
				t.Fatalf("Leave: %v", err)
			}
			assertNoSecrets(t, "outcome", string(outcome), secrets)
			assertConfidential(t, s.h, []error{err, removeErr}, secrets)
		})
	}
}
