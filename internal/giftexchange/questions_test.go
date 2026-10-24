package giftexchange_test

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

// cycleDrawn is a drawn exchange of n participants in which people[i] gives to people[(i+1)%n].
func cycleDrawn(t *testing.T, n int) drawScene {
	t.Helper()

	s := newDrawScene(t, n)
	s.forceCycle(t)
	s.mustDraw(t)

	return s
}

func (s drawScene) next(i int) person {
	return s.people[(i+1)%len(s.people)]
}

func (s drawScene) ask(t *testing.T, p person, text string) giftexchange.QuestionID {
	t.Helper()

	id, err := s.h.svc.Ask(t.Context(), p.actor, s.exchange, text)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}

	return id
}

func (s drawScene) answer(t *testing.T, p person, q giftexchange.QuestionID, text string) error {
	t.Helper()

	err := s.h.svc.Answer(t.Context(), p.actor, q, text)

	return err //nolint:wrapcheck // test helper passes the module error through
}

func (s drawScene) asked(t *testing.T, p person) []giftexchange.AskedQuestion {
	t.Helper()

	list, err := s.h.svc.QuestionsAsked(t.Context(), p.actor, s.exchange)
	if err != nil {
		t.Fatalf("QuestionsAsked: %v", err)
	}

	return list
}

func (s drawScene) received(t *testing.T, p person) []giftexchange.ReceivedQuestion {
	t.Helper()

	list, err := s.h.svc.QuestionsReceived(t.Context(), p.actor, s.exchange)
	if err != nil {
		t.Fatalf("QuestionsReceived: %v", err)
	}

	return list
}

// isClosed reports whether the asker's question q is closed, read through the asker's listing.
func (s drawScene) isClosed(t *testing.T, asker person, q giftexchange.QuestionID) bool {
	t.Helper()

	for _, got := range s.asked(t, asker) {
		if got.ID == q {
			return got.ClosedAt != nil
		}
	}

	t.Fatal("the question is not in its asker's list")

	return false
}

func (s drawScene) setQuestions(t *testing.T, value string) {
	t.Helper()

	_, err := s.h.svc.SetRule(t.Context(), s.organizer.actor, s.group, s.exchange,
		giftexchange.RuleChange{Name: giftexchange.RuleQuestionsEnabled, Raw: value})
	if err != nil {
		t.Fatalf("SetRule: %v", err)
	}
}

func TestAskReachesTheCallersCurrentRecipientOnly(t *testing.T) {
	s := cycleDrawn(t, 4)
	s.h.clock.Advance(time.Minute)

	id := s.ask(t, s.people[0], "What size do you wear?")

	got := s.received(t, s.people[1])
	if len(got) != 1 || got[0].ID != id || got[0].Text != "What size do you wear?" {
		t.Fatalf("recipient's list = %+v, want the one question", got)
	}
	if got[0].Exchange != s.exchange || !got[0].AskedAt.Equal(s.h.clock.Now()) || got[0].AnsweredAt != nil ||
		got[0].ClosedAt != nil || got[0].Answer != "" {
		t.Errorf("recipient's question = %+v, want asked now and open", got[0])
	}
	asked := s.asked(t, s.people[0])
	if len(asked) != 1 || asked[0].ID != id {
		t.Errorf("asker's list = %+v, want the one question", asked)
	}
	for _, other := range []person{s.people[0], s.people[2], s.people[3]} {
		if len(s.received(t, other)) != 0 {
			t.Error("a question reached someone other than the asker's recipient")
		}
	}
	for _, other := range []person{s.people[1], s.people[2], s.people[3]} {
		if len(s.asked(t, other)) != 0 {
			t.Error("a question is listed as asked by someone else")
		}
	}
	rows := notificationRows(t, s.exchange)
	var queued []notificationRow
	for _, row := range rows {
		if row.kind == "question_received" {
			queued = append(queued, row)
		}
	}
	if len(queued) != 1 || queued[0].person != s.people[1].id.String() ||
		queued[0].question == nil || *queued[0].question != id.String() {
		t.Errorf("question_received rows = %+v, want one for the recipient naming the question", queued)
	}
}

func TestQuestionListsAreOldestFirst(t *testing.T) {
	s := cycleDrawn(t, 3)

	var want []giftexchange.QuestionID
	for _, text := range []string{"first", "second", "third"} {
		want = append(want, s.ask(t, s.people[0], text))
		s.h.clock.Advance(time.Second)
	}

	var gotAsked, gotReceived []giftexchange.QuestionID
	for _, q := range s.asked(t, s.people[0]) {
		gotAsked = append(gotAsked, q.ID)
	}
	for _, q := range s.received(t, s.people[1]) {
		gotReceived = append(gotReceived, q.ID)
	}
	if !slices.Equal(gotAsked, want) || !slices.Equal(gotReceived, want) {
		t.Errorf("asked = %v, received = %v, want %v", gotAsked, gotReceived, want)
	}
}

func TestAskAfterAPatchGoesToTheNewRecipient(t *testing.T) {
	s, assigned := drawnWhere(t, 5, nil, func(s drawScene, a assignmentMap) bool {
		return slices.ContainsFunc(s.people, func(p person) bool { return giverOf(a, p.id) != a[p.id] })
	})
	dropout := dropoutWhere(t, s, assigned, func(giver, recipient giftexchange.PersonID) bool {
		return giver != recipient
	})
	giver := personWithID(s.people, giverOf(assigned, dropout.id))
	newRecipient := personWithID(s.people, assigned[dropout.id])

	outcome := s.leave(t, dropout)
	id := s.ask(t, giver, "Still no allergies?")

	if outcome != giftexchange.OutcomePatched {
		t.Fatalf("outcome = %v, want patched", outcome)
	}
	got := s.received(t, newRecipient)
	if len(got) == 0 || got[len(got)-1].ID != id {
		t.Error("the question did not reach the new recipient")
	}
}

func TestAnswerRepliesOnceAndQueuesTheAnswerForTheAsker(t *testing.T) {
	s := cycleDrawn(t, 4)
	id := s.ask(t, s.people[0], "Favorite color?")
	s.h.clock.Advance(time.Minute)

	err := s.answer(t, s.people[1], id, "Teal")
	second := s.answer(t, s.people[1], id, "Blue")

	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if !errors.Is(second, giftexchange.ErrAlreadyAnswered) {
		t.Errorf("second Answer = %v, want ErrAlreadyAnswered", second)
	}
	asked := s.asked(t, s.people[0])
	if len(asked) != 1 || asked[0].Answer != "Teal" || asked[0].AnsweredAt == nil ||
		!asked[0].AnsweredAt.Equal(s.h.clock.Now()) {
		t.Errorf("asker's question = %+v, want answered Teal now", asked)
	}
	received := s.received(t, s.people[1])
	if len(received) != 1 || received[0].Answer != "Teal" {
		t.Errorf("recipient's question = %+v, want the answer kept", received)
	}
	var queued []notificationRow
	for _, row := range notificationRows(t, s.exchange) {
		if row.kind == "answer_received" {
			queued = append(queued, row)
		}
	}
	if len(queued) != 1 || queued[0].person != s.people[0].id.String() {
		t.Errorf("answer_received rows = %+v, want one for the asker", queued)
	}
}

func TestAnsweringStillWorksAfterQuestionsAreTurnedOff(t *testing.T) {
	s := cycleDrawn(t, 4)
	id := s.ask(t, s.people[0], "Any hobbies?")

	s.setQuestions(t, "off")

	_, askErr := s.h.svc.Ask(t.Context(), s.people[0].actor, s.exchange, "One more?")
	if !errors.Is(askErr, giftexchange.ErrQuestionsDisabled) {
		t.Errorf("Ask = %v, want ErrQuestionsDisabled", askErr)
	}
	if err := s.answer(t, s.people[1], id, "Painting"); err != nil {
		t.Errorf("Answer after turning questions off: %v", err)
	}
}

func TestQuestionsCloseWhenTheirPairStopsBeingTrue(t *testing.T) {
	tests := []struct {
		name   string
		people int
		event  func(t *testing.T, s drawScene)
	}{
		{"a redraw after a dropout", 4, func(t *testing.T, s drawScene) { s.leave(t, s.people[0]) }},
		{"a voided round", 3, func(t *testing.T, s drawScene) { s.leave(t, s.people[0]) }},
		{"the reveal", 4, func(t *testing.T, s drawScene) { s.mustReveal(t) }},
		{"a cancel", 4, func(t *testing.T, s drawScene) {
			err := s.h.svc.Cancel(t.Context(), s.organizer.actor, s.group, s.exchange)
			if err != nil {
				t.Fatalf("Cancel: %v", err)
			}
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := cycleDrawn(t, tc.people)
			ids := make([]giftexchange.QuestionID, 0, tc.people)
			for _, p := range s.people {
				ids = append(ids, s.ask(t, p, "Anything I should know?"))
			}
			answeredFirst := s.answer(t, s.next(0), ids[0], "No")
			if answeredFirst != nil {
				t.Fatalf("Answer: %v", answeredFirst)
			}

			tc.event(t, s)

			for i, p := range s.people {
				if !s.isClosed(t, p, ids[i]) {
					t.Errorf("question %d is still open", i)
				}
				err := s.answer(t, s.next(i), ids[i], "Late")
				wantErr := giftexchange.ErrQuestionClosed
				if !errors.Is(err, wantErr) {
					t.Errorf("Answer to closed question %d = %v, want ErrQuestionClosed", i, err)
				}
			}
			asked := s.asked(t, s.people[0])
			if asked[0].Answer != "No" {
				t.Error("closing a question removed its answer")
			}
		})
	}
}

func TestAPatchClosesOnlyTheQuestionsOfTheDropout(t *testing.T) {
	s, assigned := drawnWhere(t, 5, nil, func(s drawScene, a assignmentMap) bool {
		return slices.ContainsFunc(s.people, func(p person) bool { return giverOf(a, p.id) != a[p.id] })
	})
	dropout := dropoutWhere(t, s, assigned, func(giver, recipient giftexchange.PersonID) bool {
		return giver != recipient
	})
	ids := make(map[giftexchange.PersonID]giftexchange.QuestionID, len(s.people))
	for _, p := range s.people {
		ids[p.id] = s.ask(t, p, "Anything I should know?")
	}

	outcome := s.leave(t, dropout)

	if outcome != giftexchange.OutcomePatched {
		t.Fatalf("outcome = %v, want patched", outcome)
	}
	for _, p := range s.people {
		recipient := personWithID(s.people, assigned[p.id])
		touchesDropout := p.id == dropout.id || recipient.id == dropout.id
		if got := s.isClosed(t, p, ids[p.id]); got != touchesDropout {
			t.Errorf("closed = %v for a question touching the dropout = %v", got, touchesDropout)
		}
		if touchesDropout {
			continue
		}
		if err := s.answer(t, recipient, ids[p.id], "Fine"); err != nil {
			t.Errorf("Answer to a question unrelated to the dropout: %v", err)
		}
	}
}

func TestTwentyAsksInARollingDayPassAndTheNextIsRateLimited(t *testing.T) {
	s := cycleDrawn(t, 4)
	for range giftexchange.QuestionsPerDay {
		s.ask(t, s.people[0], "Another one")
	}

	_, limited := s.h.svc.Ask(t.Context(), s.people[0].actor, s.exchange, "One too many")
	s.h.clock.Advance(24*time.Hour - time.Microsecond)
	_, stillLimited := s.h.svc.Ask(t.Context(), s.people[0].actor, s.exchange, "Just before the day ends")
	s.h.clock.Advance(time.Microsecond)
	_, allowed := s.h.svc.Ask(t.Context(), s.people[0].actor, s.exchange, "A day later")
	_, otherAsker := s.h.svc.Ask(t.Context(), s.people[1].actor, s.exchange, "Someone else is not limited")

	if !errors.Is(limited, giftexchange.ErrRateLimited) || !errors.Is(stillLimited, giftexchange.ErrRateLimited) {
		t.Errorf("21st ask = %v, %v, want ErrRateLimited", limited, stillLimited)
	}
	if allowed != nil || otherAsker != nil {
		t.Errorf("asks = %v, %v, want both allowed", allowed, otherAsker)
	}
}

func TestConcurrentAsksNeverPassTheDailyLimit(t *testing.T) {
	s := cycleDrawn(t, 3)
	const attempts = 30
	results := make(chan error, attempts)
	var wg sync.WaitGroup
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()

			_, err := s.h.svc.Ask(t.Context(), s.people[0].actor, s.exchange, "Race")
			results <- err
		}()
	}
	wg.Wait()
	close(results)

	passed, limited := 0, 0
	for err := range results {
		switch {
		case err == nil:
			passed++
		case errors.Is(err, giftexchange.ErrRateLimited):
			limited++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if passed != giftexchange.QuestionsPerDay || limited != attempts-giftexchange.QuestionsPerDay {
		t.Errorf("passed = %d, limited = %d, want %d and %d", passed, limited,
			giftexchange.QuestionsPerDay, attempts-giftexchange.QuestionsPerDay)
	}
}

func TestAnswerRacingACancelEndsAsExactlyOneOfTheTwoSerialOrders(t *testing.T) {
	const rounds = 8
	for range rounds {
		s := cycleDrawn(t, 3)
		id := s.ask(t, s.people[0], "Race?")

		var answerErr, cancelErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()

			answerErr = s.answer(t, s.next(0), id, "Race!")
		}()
		go func() {
			defer wg.Done()

			cancelErr = s.h.svc.Cancel(t.Context(), s.organizer.actor, s.group, s.exchange)
		}()
		wg.Wait()

		if cancelErr != nil {
			t.Fatalf("Cancel: %v", cancelErr)
		}
		if !s.isClosed(t, s.people[0], id) {
			t.Fatal("the question is still open after the cancel")
		}
		stored := s.asked(t, s.people[0])[0].Answer
		switch {
		case answerErr == nil && stored == "Race!":
		case errors.Is(answerErr, giftexchange.ErrQuestionClosed) && stored == "":
		default:
			t.Fatalf("Answer = %v with stored answer %q, want a serial outcome", answerErr, stored)
		}
	}
}

func TestListingsWorkInEveryStateAndAfterLeaving(t *testing.T) {
	s := newDrawScene(t, 4)
	s.mustDraw(t)
	assigned := assignmentsOf(t, s.h, s.exchange, s.people)
	asker := s.people[0]
	recipient := personWithID(s.people, assigned[asker.id])
	id := s.ask(t, asker, "Before I leave")
	s.leave(t, asker)

	askedByLeaver := s.asked(t, asker)
	s.mustReveal(t)
	afterReveal := s.received(t, recipient)

	if len(askedByLeaver) != 1 || askedByLeaver[0].ID != id || askedByLeaver[0].ClosedAt == nil {
		t.Errorf("leaver's list = %+v, want the closed question", askedByLeaver)
	}
	if len(afterReveal) != 1 || afterReveal[0].ID != id {
		t.Errorf("recipient's list after the reveal = %+v, want the question", afterReveal)
	}
}

func TestAnOrganizerWhoDidNotJoinHasEmptyListsAndAStrangerNotFound(t *testing.T) {
	s := cycleDrawn(t, 3)
	s.ask(t, s.people[0], "Hello")
	stranger := s.h.newPerson(t)

	asked, askedErr := s.h.svc.QuestionsAsked(t.Context(), s.organizer.actor, s.exchange)
	received, receivedErr := s.h.svc.QuestionsReceived(t.Context(), s.organizer.actor, s.exchange)
	_, strangerAsked := s.h.svc.QuestionsAsked(t.Context(), stranger.actor, s.exchange)
	_, strangerReceived := s.h.svc.QuestionsReceived(t.Context(), stranger.actor, s.exchange)

	if askedErr != nil || receivedErr != nil || len(asked) != 0 || len(received) != 0 {
		t.Errorf("organizer's lists = %v, %v (%v, %v), want empty and no error", asked, received, askedErr, receivedErr)
	}
	if !errors.Is(strangerAsked, giftexchange.ErrNotFound) || !errors.Is(strangerReceived, giftexchange.ErrNotFound) {
		t.Errorf("stranger's errors = %v, %v, want ErrNotFound", strangerAsked, strangerReceived)
	}
}

func TestQuestionsAuthorizationDeniesAndChangesNothing(t *testing.T) {
	s := cycleDrawn(t, 4)
	id := s.ask(t, s.people[0], "Allergies?")
	stranger := s.h.newPerson(t)
	var zero giftexchange.Actor
	unknownQuestion := mustQuestionID(t, "0198a5e0-0000-7000-8000-00000000abcd")
	missingExchange := unknownExchange(t)

	tests := []struct {
		name string
		call func() error
		want error
	}{
		{"Ask by an anonymous caller", func() error { return errOf(s.h.svc.Ask(t.Context(), zero, s.exchange, "hi")) }, giftexchange.ErrUnauthenticated},
		{"Ask by an organizer who did not join", func() error { return errOf(s.h.svc.Ask(t.Context(), s.organizer.actor, s.exchange, "hi")) }, giftexchange.ErrNotParticipant},
		{"Ask by a stranger", func() error { return errOf(s.h.svc.Ask(t.Context(), stranger.actor, s.exchange, "hi")) }, giftexchange.ErrNotFound},
		{"Ask in an unknown exchange", func() error { return errOf(s.h.svc.Ask(t.Context(), s.people[0].actor, missingExchange, "hi")) }, giftexchange.ErrNotFound},
		{"Answer by an anonymous caller", func() error { return s.h.svc.Answer(t.Context(), zero, id, "hi") }, giftexchange.ErrUnauthenticated},
		{"Answer by the asker", func() error { return s.h.svc.Answer(t.Context(), s.people[0].actor, id, "hi") }, giftexchange.ErrNotFound},
		{"Answer by a third participant", func() error { return s.h.svc.Answer(t.Context(), s.people[2].actor, id, "hi") }, giftexchange.ErrNotFound},
		{"Answer by the organizer", func() error { return s.h.svc.Answer(t.Context(), s.organizer.actor, id, "hi") }, giftexchange.ErrNotFound},
		{"Answer by a stranger holding a valid question ID", func() error { return s.h.svc.Answer(t.Context(), stranger.actor, id, "hi") }, giftexchange.ErrNotFound},
		{"Answer to a question that does not exist", func() error { return s.h.svc.Answer(t.Context(), s.people[1].actor, unknownQuestion, "hi") }, giftexchange.ErrNotFound},
		{"QuestionsAsked by an anonymous caller", func() error { return errOf(s.h.svc.QuestionsAsked(t.Context(), zero, s.exchange)) }, giftexchange.ErrUnauthenticated},
		{"QuestionsAsked by a stranger", func() error { return errOf(s.h.svc.QuestionsAsked(t.Context(), stranger.actor, s.exchange)) }, giftexchange.ErrNotFound},
		{"QuestionsReceived by a stranger", func() error { return errOf(s.h.svc.QuestionsReceived(t.Context(), stranger.actor, s.exchange)) }, giftexchange.ErrNotFound},
		{"QuestionsReceived of an unknown exchange", func() error { return errOf(s.h.svc.QuestionsReceived(t.Context(), s.people[0].actor, missingExchange)) }, giftexchange.ErrNotFound},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := takeSnapshot(t, s.h, s.exchange)

			err := tc.call()

			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
			assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))
		})
	}
}

func mustQuestionID(t *testing.T, raw string) giftexchange.QuestionID {
	t.Helper()

	id, err := giftexchange.ParseQuestionID(raw)
	if err != nil {
		t.Fatalf("ParseQuestionID: %v", err)
	}

	return id
}

func TestAskIsDeniedOutsideDrawnAndToParticipantsWhoLeft(t *testing.T) {
	open := newDrawScene(t, 3)
	revealed := cycleDrawn(t, 3)
	revealed.mustReveal(t)
	cancelled := cycleDrawn(t, 3)
	cancelErr := cancelled.h.svc.Cancel(t.Context(), cancelled.organizer.actor, cancelled.group, cancelled.exchange)
	if cancelErr != nil {
		t.Fatalf("Cancel: %v", cancelErr)
	}
	left := cycleDrawn(t, 4)
	left.leave(t, left.people[0])

	tests := []struct {
		name  string
		scene drawScene
		want  error
	}{
		{"an Open exchange", open, giftexchange.ErrInvalidState},
		{"a Revealed exchange", revealed, giftexchange.ErrInvalidState},
		{"a Cancelled exchange", cancelled, giftexchange.ErrInvalidState},
		{"a participant who left", left, giftexchange.ErrNotParticipant},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := takeSnapshot(t, tc.scene.h, tc.scene.exchange)

			_, err := tc.scene.h.svc.Ask(t.Context(), tc.scene.people[0].actor, tc.scene.exchange, "May I ask?")

			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
			assertUnchanged(t, before, takeSnapshot(t, tc.scene.h, tc.scene.exchange))
		})
	}
}

func TestAskTextIsStoredAsPlainTextOrRejected(t *testing.T) {
	s := cycleDrawn(t, 4)
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"stores one rune", "a", "a"},
		{"stores 1000 runes", strings.Repeat("a", 1000), strings.Repeat("a", 1000)},
		{"stores emoji", "Regalo \U0001F381", "Regalo \U0001F381"},
		{"stores right-to-left text", "\U000005e9\U000005dc\U000005d5\U000005dd", "\U000005e9\U000005dc\U000005d5\U000005dd"},
		{"stores a base letter with a combining mark", "e\U00000301", "e\U00000301"},
		{"stores a mention of everyone as text", "@everyone", "@everyone"},
		{"stores user mention syntax as text", "<@" + fakeUserA + ">", "<@" + fakeUserA + ">"},
		{"turns CRLF into LF", "one\r\ntwo", "one\ntwo"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s.h.clock.Advance(25 * time.Hour)
			id := s.ask(t, s.people[0], tc.input)

			var got string
			for _, q := range s.received(t, s.people[1]) {
				if q.ID == id {
					got = q.Text
				}
			}
			if got != tc.want {
				t.Errorf("stored text differs from the want")
			}
		})
	}

	rejected := []struct {
		name  string
		input string
	}{
		{"rejects empty text", ""},
		{"rejects white space only", " \t "},
		{"rejects 1001 runes", strings.Repeat("a", 1001)},
		{"rejects 10000 runes", strings.Repeat("a", 10_000)},
		{"rejects only combining marks", "\U00000301\U00000301"},
		{"rejects only zero-width characters", "\U0000200b\U0000200d"},
		{"rejects a bidi override", "evil\U0000202etxt"},
		{"rejects a bidi isolate", "evil\U00002066txt"},
		{"rejects invalid UTF-8", "bad\xff\xfetext"},
		{"rejects a NUL", "nul\x00text"},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			before := takeSnapshot(t, s.h, s.exchange)

			_, err := s.h.svc.Ask(t.Context(), s.people[0].actor, s.exchange, tc.input)

			if !errors.Is(err, giftexchange.ErrInvalidInput) {
				t.Errorf("err = %v, want ErrInvalidInput", err)
			}
			assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))
		})
	}
}

func TestAnswerRejectsBadTextAndStoresMentionsAsText(t *testing.T) {
	s := cycleDrawn(t, 4)
	id := s.ask(t, s.people[0], "Question")
	before := takeSnapshot(t, s.h, s.exchange)

	for _, bad := range []string{"", strings.Repeat("a", 1001), "evil\U0000202etxt", "bad\xffutf", "\U0000200b"} {
		err := s.answer(t, s.people[1], id, bad)
		if !errors.Is(err, giftexchange.ErrInvalidInput) {
			t.Errorf("Answer(%q) = %v, want ErrInvalidInput", bad, err)
		}
	}
	assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))
	err := s.answer(t, s.people[1], id, "@everyone <@"+fakeUserB+">")
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if got := s.asked(t, s.people[0])[0].Answer; got != "@everyone <@"+fakeUserB+">" {
		t.Error("the mention was not stored as plain text")
	}
}

func TestQuestionCallsReportEveryInvalidArgumentTogether(t *testing.T) {
	s := cycleDrawn(t, 3)
	var zeroExchange giftexchange.ExchangeID
	var zeroQuestion giftexchange.QuestionID

	_, askErr := s.h.svc.Ask(t.Context(), s.people[0].actor, zeroExchange, "")
	answerErr := s.h.svc.Answer(t.Context(), s.people[0].actor, zeroQuestion, "")
	_, listErr := s.h.svc.QuestionsAsked(t.Context(), s.people[0].actor, zeroExchange)

	if got := inputFields(askErr); !slices.Equal(got, []string{"exchange_id", "text"}) {
		t.Errorf("Ask fields = %v, want exchange_id and text", got)
	}
	if got := inputFields(answerErr); !slices.Equal(got, []string{"question_id", "text"}) {
		t.Errorf("Answer fields = %v, want question_id and text", got)
	}
	if !errors.Is(listErr, giftexchange.ErrInvalidInput) {
		t.Errorf("QuestionsAsked = %v, want ErrInvalidInput", listErr)
	}
}

func TestAQuestionOfAnotherExchangeCannotBeAnsweredFromThisOne(t *testing.T) {
	a := cycleDrawn(t, 3)
	b := cycleDrawn(t, 3)
	id := a.ask(t, a.people[0], "Private to exchange A")

	err := b.answer(t, b.people[1], id, "Probing")

	if !errors.Is(err, giftexchange.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestQuestionNoticesAreDeliveredWithoutTheAsker(t *testing.T) {
	s := cycleDrawn(t, 4)
	id := s.ask(t, s.people[0], "Shoe size?")
	answerErr := s.answer(t, s.people[1], id, "42")
	if answerErr != nil {
		t.Fatalf("Answer: %v", answerErr)
	}
	s.h.startRunner(t)

	waitForFinished(t, s.exchange, len(s.people)+2)

	received := receivedOfKind(s.h.notifier.Received(), giftexchange.KindQuestionReceived)
	answers := receivedOfKind(s.h.notifier.Received(), giftexchange.KindAnswerReceived)
	if len(received) != 1 || len(answers) != 1 {
		t.Fatalf("delivered %d question and %d answer notices, want 1 and 1", len(received), len(answers))
	}
	question, answer := received[0], answers[0]
	if question.To.ID != s.people[1].id || question.Question == nil || question.Question.ID != id ||
		question.Question.Text != "Shoe size?" || question.Question.Answer != "" {
		t.Errorf("question notice = %v with %+v, want the question for the recipient", question, question.Question)
	}
	if answer.To.ID != s.people[0].id || answer.Question == nil || answer.Question.ID != id ||
		answer.Question.Text != "Shoe size?" || answer.Question.Answer != "42" {
		t.Errorf("answer notice = %v with %+v, want the answer for the asker", answer, answer.Question)
	}
	for _, got := range []giftexchange.Notification{question, answer} {
		if got.Counterpart != nil || got.Reason != "" {
			t.Errorf("a %v notice carries a counterpart or a reason", got.Kind)
		}
		if got.Exchange.ID != s.exchange {
			t.Errorf("a %v notice describes another exchange", got.Kind)
		}
	}
}

func TestPendingQuestionNoticeIsStaleOnceClosedButTheAnswerNoticeIsDelivered(t *testing.T) {
	s := cycleDrawn(t, 4)
	s.ask(t, s.people[0], "Shoe size?")
	answered := s.ask(t, s.people[2], "Hat size?")
	answerErr := s.answer(t, s.people[3], answered, "M")
	if answerErr != nil {
		t.Fatalf("Answer: %v", answerErr)
	}
	s.mustReveal(t)
	s.h.startRunner(t)

	waitForFinished(t, s.exchange, len(s.people)+3+len(s.people))

	if got := finishedCount(t, s.exchange, "question_received", "stale"); got != 2 {
		t.Errorf("%d stale question_received notices, want 2", got)
	}
	if got := receivedOfKind(s.h.notifier.Received(), giftexchange.KindQuestionReceived); len(got) != 0 {
		t.Errorf("%d question notices of closed questions delivered, want 0", len(got))
	}
	answers := receivedOfKind(s.h.notifier.Received(), giftexchange.KindAnswerReceived)
	if len(answers) != 1 || answers[0].Question == nil || answers[0].Question.ID != answered ||
		answers[0].Question.Answer != "M" {
		t.Errorf("answer notices = %v, want the answer delivered although its question closed", answers)
	}
}

func TestNoQuestionOrAnswerTextReachesLogsErrorsOrJobs(t *testing.T) {
	s := cycleDrawn(t, 4)
	questionText := "NEEDLE-question-" + newSubject()
	answerText := "NEEDLE-answer-" + newSubject()
	secrets := []string{questionText, answerText}
	for _, p := range s.people {
		secrets = append(secrets, p.id.String(), p.subject)
	}
	var errs []error

	id := s.ask(t, s.people[0], questionText)
	errs = append(errs, s.answer(t, s.people[1], id, answerText))
	errs = append(errs, s.answer(t, s.people[1], id, answerText))
	errs = append(errs, s.answer(t, s.people[2], id, answerText))
	for range giftexchange.QuestionsPerDay {
		_, err := s.h.svc.Ask(t.Context(), s.people[0].actor, s.exchange, questionText)
		errs = append(errs, err)
	}
	s.setQuestions(t, "off")
	_, disabled := s.h.svc.Ask(t.Context(), s.people[0].actor, s.exchange, questionText)
	errs = append(errs, disabled)
	_, tooLong := s.h.svc.Ask(t.Context(), s.people[0].actor, s.exchange, questionText+strings.Repeat("a", 1000))
	errs = append(errs, tooLong)
	s.h.notifier.FailWith(errors.New("transport down"))
	s.h.startRunner(t)
	waitFor(t, deliveryDeadline, "a question notice to fail once", func() bool {
		return slices.ContainsFunc(jobStates(t, s.h), func(job storedJob) bool { return job.attempt > 0 })
	})

	assertConfidential(t, s.h, errs, secrets)
}

func TestReceivedQuestionsAndNoticesHaveNoAskerField(t *testing.T) {
	s := cycleDrawn(t, 4)
	s.ask(t, s.people[0], "Anything?")
	s.h.startRunner(t)
	waitForFinished(t, s.exchange, len(s.people)+1)

	got := s.received(t, s.people[1])
	notices := receivedOfKind(s.h.notifier.Received(), giftexchange.KindQuestionReceived)

	if len(got) != 1 || len(notices) != 1 {
		t.Fatalf("got %d questions and %d notices, want 1 each", len(got), len(notices))
	}
	asker := s.people[0]
	rendered := []string{
		formatAll(got[0]), formatAll(notices[0]), formatAll(notices[0].Question),
		formatAll(notices[0].Exchange), formatAll(notices[0].To),
	}
	for _, text := range rendered {
		if strings.Contains(text, asker.id.String()) || strings.Contains(text, asker.subject) {
			t.Error("a value the recipient sees names the asker")
		}
	}
	if notices[0].To.ID == asker.id || notices[0].Counterpart != nil {
		t.Error("the question notice is addressed to, or names, the asker")
	}
}

// formatAll renders v with the verbs a log line or a debugger might use.
func formatAll(v any) string {
	return fmt.Sprintf("%v %+v %#v", v, v, v)
}
