package giftexchange_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

const (
	drawDeadlineKind = "giftexchange.draw_deadline"
	reminderKind     = "giftexchange.reminder"
	revealJobKind    = "giftexchange.reveal"
)

// jobCount counts the jobs of the test queue of a kind, in a River state ("" for any state).
func jobCount(t *testing.T, h *harness, kind, state string) int {
	t.Helper()

	var count int
	err := shared.Pool().QueryRow(t.Context(),
		"SELECT count(*) FROM river.river_job WHERE queue = $1 AND kind = $2 AND ($3 = '' OR state::text = $3)",
		h.queue, kind, state).Scan(&count)
	if err != nil {
		t.Fatalf("count %s jobs: %v", kind, err)
	}

	return count
}

// scheduledAts returns the scheduled_at of every job of a kind, sorted.
func scheduledAts(t *testing.T, h *harness, kind string) []time.Time {
	t.Helper()

	rows, err := shared.Pool().Query(t.Context(),
		"SELECT scheduled_at FROM river.river_job WHERE queue = $1 AND kind = $2 ORDER BY scheduled_at",
		h.queue, kind)
	if err != nil {
		t.Fatalf("read %s schedule: %v", kind, err)
	}
	defer rows.Close()

	var found []time.Time
	for rows.Next() {
		var at time.Time
		scanErr := rows.Scan(&at)
		if scanErr != nil {
			t.Fatalf("scan schedule: %v", scanErr)
		}
		found = append(found, at.UTC())
	}
	if rows.Err() != nil {
		t.Fatalf("read schedule rows: %v", rows.Err())
	}

	return found
}

func (s scene) view(t *testing.T) giftexchange.ExchangeView {
	t.Helper()

	view, err := s.h.svc.Exchange(t.Context(), s.organizer.actor, s.group, s.exchange)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}

	return view
}

func (s scene) mustSet(t *testing.T, name giftexchange.RuleName, raw string) {
	t.Helper()

	_, err := s.set(t, s.organizer, name, raw)
	if err != nil {
		t.Fatalf("SetRule %s=%s: %v", name, raw, err)
	}
}

// waitForJobs waits until n jobs of the kind are completed.
func waitForJobs(t *testing.T, h *harness, kind string, n int) {
	t.Helper()

	waitFor(t, deliveryDeadline, fmt.Sprintf("%d %s jobs to complete", n, kind), func() bool {
		return jobCount(t, h, kind, "completed") == n
	})
}

func stateOf(t *testing.T, s scene) giftexchange.State {
	t.Helper()

	return s.view(t).State
}

// newTimedScene is a drawScene of n participants with a Runner working the test queue.
func newTimedScene(t *testing.T, n int) drawScene {
	t.Helper()

	s := newDrawScene(t, n)
	s.h.startRunner(t)

	return s
}

func TestSetRuleSchedulesTheDeadlineDrawAndItsReminderAtTheirTimes(t *testing.T) {
	s := newScene(t)
	s.mustSet(t, giftexchange.RuleReminderLead, "5m")
	drawAt := s.h.futureText(time.Hour)

	s.mustSet(t, giftexchange.RuleDrawAt, drawAt)

	want, parseErr := time.Parse(time.RFC3339, drawAt)
	if parseErr != nil {
		t.Fatalf("parse %q: %v", drawAt, parseErr)
	}
	deadlines := scheduledAts(t, s.h, drawDeadlineKind)
	if len(deadlines) != 1 || !deadlines[0].Equal(want) {
		t.Errorf("deadline jobs at %v, want one at %v", deadlines, want)
	}
	reminders := scheduledAts(t, s.h, reminderKind)
	if len(reminders) != 1 || !reminders[0].Equal(want.Add(-5*time.Minute)) {
		t.Errorf("reminder jobs at %v, want one at %v", reminders, want.Add(-5*time.Minute))
	}
	if got := countJobs(t, s.h); got != 2 {
		t.Errorf("jobs = %d, want 2", got)
	}
}

func TestSetRuleSchedulesTheRevealAndItsReminder(t *testing.T) {
	s := newScene(t)
	s.mustSet(t, giftexchange.RuleReminderLead, "5m")

	s.mustSet(t, giftexchange.RuleRevealAt, s.h.futureText(2*time.Hour))

	if got := jobCount(t, s.h, revealJobKind, ""); got != 1 {
		t.Errorf("reveal jobs = %d, want 1", got)
	}
	if got := jobCount(t, s.h, reminderKind, ""); got != 1 {
		t.Errorf("reminder jobs = %d, want 1", got)
	}
}

func TestSetRuleSchedulesNoReminderWhenItWouldBeDueAlready(t *testing.T) {
	// The default lead of one day puts the reminder before now.
	s := newScene(t)

	s.mustSet(t, giftexchange.RuleDrawAt, s.h.futureText(time.Hour))

	if got := jobCount(t, s.h, drawDeadlineKind, ""); got != 1 {
		t.Errorf("deadline jobs = %d, want 1", got)
	}
	if got := jobCount(t, s.h, reminderKind, ""); got != 0 {
		t.Errorf("reminder jobs = %d, want 0", got)
	}
}

func TestSetRuleSchedulesNothingForUnsetZeroAndNonTimeRules(t *testing.T) {
	s := newScene(t)
	s.mustSet(t, giftexchange.RuleDrawAt, s.h.futureText(time.Hour))
	before := countJobs(t, s.h)

	changes := []struct {
		name giftexchange.RuleName
		raw  string
	}{
		{giftexchange.RuleDrawAt, "unset"},
		{giftexchange.RuleReminderLead, "0"},
		{giftexchange.RuleDropoutPolicy, "redraw"},
		{giftexchange.RuleAvoidLastRound, "on"},
		{giftexchange.RuleQuestionsEnabled, "off"},
	}
	for _, change := range changes {
		s.mustSet(t, change.name, change.raw)
	}

	if after := countJobs(t, s.h); after != before {
		t.Errorf("jobs = %d, want %d", after, before)
	}
}

func TestSetRuleToTheCurrentValueSchedulesNothing(t *testing.T) {
	s := newScene(t)
	drawAt := s.h.futureText(time.Hour)
	s.mustSet(t, giftexchange.RuleDrawAt, drawAt)
	before := countJobs(t, s.h)

	s.mustSet(t, giftexchange.RuleDrawAt, drawAt)

	if after := countJobs(t, s.h); after != before {
		t.Errorf("jobs = %d, want %d", after, before)
	}
}

func TestSettingReminderLeadSchedulesTheRemindersItMakesDue(t *testing.T) {
	s := newScene(t)
	s.mustSet(t, giftexchange.RuleDrawAt, s.h.futureText(2*time.Hour))
	s.mustSet(t, giftexchange.RuleRevealAt, s.h.futureText(4*time.Hour))
	before := jobCount(t, s.h, reminderKind, "")

	s.mustSet(t, giftexchange.RuleReminderLead, "30m")

	if got := jobCount(t, s.h, reminderKind, "") - before; got != 2 {
		t.Errorf("reminders added = %d, want one for the draw and one for the reveal", got)
	}
}

func TestSettingReminderLeadInDrawnStateRemindsTheRevealOnly(t *testing.T) {
	s := newDrawScene(t, 3)
	s.mustSet(t, giftexchange.RuleDrawAt, s.h.futureText(2*time.Hour))
	s.mustDraw(t)
	s.mustSet(t, giftexchange.RuleRevealAt, s.h.futureText(4*time.Hour))
	before := jobCount(t, s.h, reminderKind, "")

	s.mustSet(t, giftexchange.RuleReminderLead, "30m")

	if got := jobCount(t, s.h, reminderKind, "") - before; got != 1 {
		t.Errorf("reminders added = %d, want the reveal reminder only", got)
	}
}

func TestCreateExchangeSchedulesTheJobsOfItsInitialRules(t *testing.T) {
	h := newService(t)
	group := h.newGroup(t)
	organizer := h.newPerson(t)

	_, err := h.svc.CreateExchange(t.Context(), organizer.actor, giftexchange.CreateExchangeRequest{
		Group: group,
		Name:  "Winter exchange",
		Rules: []giftexchange.RuleChange{
			{Name: giftexchange.RuleReminderLead, Raw: "5m"},
			{Name: giftexchange.RuleDrawAt, Raw: h.futureText(time.Hour)},
			{Name: giftexchange.RuleRevealAt, Raw: h.futureText(2 * time.Hour)},
		},
	})
	if err != nil {
		t.Fatalf("CreateExchange: %v", err)
	}

	got := map[string]int{
		drawDeadlineKind: jobCount(t, h, drawDeadlineKind, ""),
		revealJobKind:    jobCount(t, h, revealJobKind, ""),
		reminderKind:     jobCount(t, h, reminderKind, ""),
	}
	want := map[string]int{drawDeadlineKind: 1, revealJobKind: 1, reminderKind: 2}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("jobs = %v, want %v", got, want)
	}
}

func TestDeniedSetRuleInsertsNoJob(t *testing.T) {
	s := newScene(t)
	stranger := s.h.newPerson(t)
	before := takeSnapshot(t, s.h, s.exchange)

	_, err := s.set(t, stranger, giftexchange.RuleDrawAt, s.h.futureText(time.Hour))
	if err == nil {
		t.Fatal("SetRule by a stranger succeeded")
	}

	assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))
}

func TestSetRuleThatFailsItsChecksInsertsNoJob(t *testing.T) {
	s := newScene(t)
	before := takeSnapshot(t, s.h, s.exchange)

	_, err := s.set(t, s.organizer, giftexchange.RuleDrawAt, s.h.futureText(-time.Hour))
	if err == nil {
		t.Fatal("SetRule with a past time succeeded")
	}

	assertUnchanged(t, before, takeSnapshot(t, s.h, s.exchange))
}

func TestDeadlineDrawsOnceAtDrawAt(t *testing.T) {
	s := newTimedScene(t, 3)
	s.mustSet(t, giftexchange.RuleDrawAt, s.h.futureText(time.Hour))

	s.h.clock.Advance(2 * time.Hour)

	waitFor(t, deliveryDeadline, "the deadline draw", func() bool {
		return stateOf(t, s.scene) == giftexchange.StateDrawn
	})
	waitForFinished(t, s.exchange, 3)
	view := s.view(t)
	if view.DrawAt != nil {
		t.Errorf("draw_at = %v, want cleared by the draw", view.DrawAt)
	}
	if got := len(receivedOfKind(s.h.notifier.Received(), giftexchange.KindDrawn)); got != 3 {
		t.Errorf("drawn notices = %d, want 3", got)
	}
	secrets := confidentialityTerms(s)
	assertConfidential(t, s.h, nil, secrets)
}

func TestMovedDrawAtDrawsOnceAtTheNewTimeAndNeverAtTheOld(t *testing.T) {
	s := newTimedScene(t, 3)
	s.mustSet(t, giftexchange.RuleDrawAt, s.h.futureText(time.Hour))
	s.mustSet(t, giftexchange.RuleDrawAt, s.h.futureText(3*time.Hour))

	s.h.clock.Advance(2 * time.Hour)
	waitForJobs(t, s.h, drawDeadlineKind, 1)

	if got := stateOf(t, s.scene); got != giftexchange.StateOpen {
		t.Fatalf("state after the old time = %s, want open", got)
	}

	s.h.clock.Advance(2 * time.Hour)
	waitFor(t, deliveryDeadline, "the draw at the new time", func() bool {
		return stateOf(t, s.scene) == giftexchange.StateDrawn
	})
	waitForJobs(t, s.h, drawDeadlineKind, 2)
	rows := notificationRows(t, s.exchange)
	if len(rows) != 3 {
		t.Errorf("notifications = %d, want 3 from one draw", len(rows))
	}
}

func TestManualDrawBeforeTheDeadlineMakesTheDeadlineJobANoOp(t *testing.T) {
	s := newTimedScene(t, 3)
	s.mustSet(t, giftexchange.RuleDrawAt, s.h.futureText(time.Hour))
	s.mustDraw(t)

	s.h.clock.Advance(2 * time.Hour)
	waitForJobs(t, s.h, drawDeadlineKind, 1)

	view := s.view(t)
	if view.State != giftexchange.StateDrawn {
		t.Errorf("state = %s, want drawn", view.State)
	}
	if _, round := s.stateAndRound(t); round != 1 {
		t.Errorf("round = %d, want 1: the deadline job must not draw again", round)
	}
}

func TestDeadlineDrawWithTooFewParticipantsTellsTheOrganizerAndClearsDrawAt(t *testing.T) {
	s := newTimedScene(t, 2)
	s.mustSet(t, giftexchange.RuleDrawAt, s.h.futureText(time.Hour))

	s.h.clock.Advance(2 * time.Hour)

	waitForFinished(t, s.exchange, 1)
	assertDrawFailed(t, s.scene, giftexchange.ReasonTooFewParticipants)
}

func TestDeadlineDrawWithoutAValidAssignmentTellsTheOrganizerAndClearsDrawAt(t *testing.T) {
	s := newTimedScene(t, 3)
	s.exclude(t, s.people[0], s.people[1], false)
	s.exclude(t, s.people[0], s.people[2], false)
	s.mustSet(t, giftexchange.RuleDrawAt, s.h.futureText(time.Hour))

	s.h.clock.Advance(2 * time.Hour)

	waitForFinished(t, s.exchange, 1)
	assertDrawFailed(t, s.scene, giftexchange.ReasonNoValidAssignment)
	secrets := confidentialityTerms(s)
	assertConfidential(t, s.h, nil, secrets)
}

func assertDrawFailed(t *testing.T, s scene, reason giftexchange.Reason) {
	t.Helper()

	view := s.view(t)
	if view.State != giftexchange.StateOpen || view.DrawAt != nil {
		t.Errorf("state = %s, draw_at = %v; want open with draw_at cleared", view.State, view.DrawAt)
	}
	failed := receivedOfKind(s.h.notifier.Received(), giftexchange.KindDrawFailed)
	if len(failed) != 1 {
		t.Fatalf("draw_failed notices = %d, want 1", len(failed))
	}
	if failed[0].To.ID != s.organizer.id || failed[0].Reason != reason || failed[0].Counterpart != nil {
		t.Errorf("draw_failed = to organizer %t, reason %q, counterpart %v; want the organizer, %q, none",
			failed[0].To.ID == s.organizer.id, failed[0].Reason, failed[0].Counterpart, reason)
	}
	if got := len(s.h.notifier.Received()); got != 1 {
		t.Errorf("notices delivered = %d, want only the failure", got)
	}
}

func TestRevealJobRevealsOnceAtRevealAt(t *testing.T) {
	s := newTimedScene(t, 3)
	s.mustDraw(t)
	s.mustSet(t, giftexchange.RuleRevealAt, s.h.futureText(time.Hour))

	s.h.clock.Advance(2 * time.Hour)

	waitFor(t, deliveryDeadline, "the reveal", func() bool {
		return stateOf(t, s.scene) == giftexchange.StateRevealed
	})
	waitForFinished(t, s.exchange, 6)
	if got := len(receivedOfKind(s.h.notifier.Received(), giftexchange.KindRevealed)); got != 3 {
		t.Errorf("revealed notices = %d, want 3", got)
	}
	secrets := confidentialityTerms(s)
	assertConfidential(t, s.h, nil, secrets)
}

func TestMovedRevealAtRevealsOnceAtTheNewTime(t *testing.T) {
	s := newTimedScene(t, 3)
	s.mustDraw(t)
	s.mustSet(t, giftexchange.RuleRevealAt, s.h.futureText(time.Hour))
	s.mustSet(t, giftexchange.RuleRevealAt, s.h.futureText(3*time.Hour))

	s.h.clock.Advance(2 * time.Hour)
	waitForJobs(t, s.h, revealJobKind, 1)

	if got := stateOf(t, s.scene); got != giftexchange.StateDrawn {
		t.Fatalf("state after the old time = %s, want drawn", got)
	}

	s.h.clock.Advance(2 * time.Hour)
	waitFor(t, deliveryDeadline, "the reveal at the new time", func() bool {
		return stateOf(t, s.scene) == giftexchange.StateRevealed
	})
	waitForJobs(t, s.h, revealJobKind, 2)
}

func TestRevealJobAfterCancelIsANoOp(t *testing.T) {
	s := newTimedScene(t, 3)
	s.mustDraw(t)
	s.mustSet(t, giftexchange.RuleRevealAt, s.h.futureText(time.Hour))
	err := s.h.svc.Cancel(t.Context(), s.organizer.actor, s.group, s.exchange)
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	s.h.clock.Advance(2 * time.Hour)
	waitForJobs(t, s.h, revealJobKind, 1)

	if got := stateOf(t, s.scene); got != giftexchange.StateCancelled {
		t.Errorf("state = %s, want cancelled", got)
	}

	if got := receivedOfKind(s.h.notifier.Received(), giftexchange.KindRevealed); len(got) != 0 {
		t.Errorf("revealed notices after cancel = %d, want 0", len(got))
	}
}

func TestRevealJobFiringWhileOpenChangesNothingAndALaterDrawClearsThePastRevealAt(t *testing.T) {
	s := newTimedScene(t, 3)
	s.mustSet(t, giftexchange.RuleRevealAt, s.h.futureText(time.Hour))

	s.h.clock.Advance(2 * time.Hour)
	waitForJobs(t, s.h, revealJobKind, 1)

	if got := stateOf(t, s.scene); got != giftexchange.StateOpen {
		t.Fatalf("state = %s, want open: the stale job must not reveal", got)
	}
	s.mustDraw(t)

	view := s.view(t)
	if view.RevealAt != nil {
		t.Errorf("reveal_at = %v, want cleared because it had passed", view.RevealAt)
	}
	if got := jobCount(t, s.h, revealJobKind, ""); got != 1 {
		t.Errorf("reveal jobs = %d, want no new one", got)
	}
}

func TestDrawFromOpenReinsertsTheRevealJobsAndTheRevealRemindsOnce(t *testing.T) {
	s := newTimedScene(t, 3)
	s.mustSet(t, giftexchange.RuleReminderLead, "5m")
	s.mustSet(t, giftexchange.RuleRevealAt, s.h.futureText(2*time.Hour))
	s.mustDraw(t)

	if got := jobCount(t, s.h, revealJobKind, ""); got != 2 {
		t.Fatalf("reveal jobs = %d, want the original and the one inserted by the draw", got)
	}
	if got := jobCount(t, s.h, reminderKind, ""); got != 2 {
		t.Fatalf("reminder jobs = %d, want the original and the one inserted by the draw", got)
	}

	s.h.clock.Advance(116 * time.Minute)
	waitForJobs(t, s.h, reminderKind, 2)
	waitForFinished(t, s.exchange, 6)

	if got := len(receivedOfKind(s.h.notifier.Received(), giftexchange.KindRevealSoon)); got != 3 {
		t.Errorf("reveal_soon notices = %d, want one round of 3: the duplicate job is a no-op", got)
	}

	s.h.clock.Advance(10 * time.Minute)
	waitFor(t, deliveryDeadline, "the reveal", func() bool {
		return stateOf(t, s.scene) == giftexchange.StateRevealed
	})
	waitForJobs(t, s.h, revealJobKind, 2)
	waitForFinished(t, s.exchange, 9)
	if got := finishedCount(t, s.exchange, "revealed", "delivered"); got != 3 {
		t.Errorf("delivered revealed notices = %d, want 3 from one reveal", got)
	}
	if got := finishedCount(t, s.exchange, "revealed", "stale"); got != 0 {
		t.Errorf("stale revealed notices = %d, want 0", got)
	}
}

func TestDropoutRedrawDoesNotInsertRevealJobs(t *testing.T) {
	s := newDrawScene(t, 5)
	s.mustSet(t, giftexchange.RuleDropoutPolicy, "redraw")
	s.mustSet(t, giftexchange.RuleRevealAt, s.h.futureText(2*time.Hour))
	s.mustDraw(t)
	before := timedJobCount(t, s.h)

	outcome := s.leave(t, s.people[0])

	if outcome != giftexchange.OutcomeRedrawn {
		t.Fatalf("outcome = %s, want redrawn", outcome)
	}
	if after := timedJobCount(t, s.h); after != before {
		t.Errorf("timed jobs = %d, want %d: a redraw stays in Drawn", after, before)
	}
}

// timedJobCount counts the draw deadline, reminder and reveal jobs of the test queue.
func timedJobCount(t *testing.T, h *harness) int {
	t.Helper()

	return jobCount(t, h, drawDeadlineKind, "") + jobCount(t, h, reminderKind, "") + jobCount(t, h, revealJobKind, "")
}

func TestReminderLeadZeroSendsNoReminder(t *testing.T) {
	s := newTimedScene(t, 3)
	s.mustSet(t, giftexchange.RuleReminderLead, "5m")
	s.mustSet(t, giftexchange.RuleReminderLead, "0")
	s.mustSet(t, giftexchange.RuleDrawAt, s.h.futureText(time.Hour))

	s.h.clock.Advance(2 * time.Hour)

	waitFor(t, deliveryDeadline, "the deadline draw", func() bool {
		return stateOf(t, s.scene) == giftexchange.StateDrawn
	})
	if got := jobCount(t, s.h, reminderKind, ""); got != 0 {
		t.Errorf("reminder jobs = %d, want 0", got)
	}
}

func TestReminderQueuedUnderAnOldLeadSendsNothingAfterTheLeadChanges(t *testing.T) {
	s := newTimedScene(t, 3)
	s.mustSet(t, giftexchange.RuleReminderLead, "5m")
	s.mustSet(t, giftexchange.RuleDrawAt, s.h.futureText(time.Hour))
	s.mustSet(t, giftexchange.RuleReminderLead, "0")

	s.h.clock.Advance(58 * time.Minute)
	waitForJobs(t, s.h, reminderKind, 1)

	for _, row := range notificationRows(t, s.exchange) {
		if row.kind == "draw_soon" {
			t.Fatalf("a draw_soon notice was queued by a reminder of a lead that no longer holds")
		}
	}
	if got := len(receivedOfKind(s.h.notifier.Received(), giftexchange.KindDrawSoon)); got != 0 {
		t.Errorf("draw_soon notices delivered = %d, want 0", got)
	}
}

func TestDrawReminderGoesOutOnceToTheOrganizerAndEachParticipantWhenDrawAtMovesAndReturns(t *testing.T) {
	s := newTimedScene(t, 3)
	s.mustSet(t, giftexchange.RuleReminderLead, "5m")
	first := s.h.futureText(time.Hour)
	s.mustSet(t, giftexchange.RuleDrawAt, first)
	s.mustSet(t, giftexchange.RuleDrawAt, s.h.futureText(3*time.Hour))
	s.mustSet(t, giftexchange.RuleDrawAt, first)

	s.h.clock.Advance(58 * time.Minute)
	waitForJobs(t, s.h, reminderKind, 2)
	waitForFinished(t, s.exchange, 4)

	soon := receivedOfKind(s.h.notifier.Received(), giftexchange.KindDrawSoon)
	addressees := make([]giftexchange.PersonID, 0, len(soon))
	for _, notice := range soon {
		addressees = append(addressees, notice.To.ID)
		if notice.Counterpart != nil {
			t.Errorf("draw_soon carries a counterpart")
		}
	}
	want := []giftexchange.PersonID{s.organizer.id}
	for _, p := range s.people {
		want = append(want, p.id)
	}
	slices.SortFunc(addressees, func(a, b giftexchange.PersonID) int { return strings.Compare(a.String(), b.String()) })
	slices.SortFunc(want, func(a, b giftexchange.PersonID) int { return strings.Compare(a.String(), b.String()) })
	if !slices.Equal(addressees, want) {
		t.Errorf("draw_soon went to %d people, want the organizer and 3 participants once each", len(addressees))
	}
}

func TestDrawReminderCountsAnOrganizerWhoJoinedOnce(t *testing.T) {
	s := newTimedScene(t, 2)
	s.h.join(t, s.organizer, s.group, s.exchange)
	s.mustSet(t, giftexchange.RuleReminderLead, "5m")
	s.mustSet(t, giftexchange.RuleDrawAt, s.h.futureText(time.Hour))

	s.h.clock.Advance(58 * time.Minute)
	waitForJobs(t, s.h, reminderKind, 1)
	waitForFinished(t, s.exchange, 3)

	if got := len(receivedOfKind(s.h.notifier.Received(), giftexchange.KindDrawSoon)); got != 3 {
		t.Errorf("draw_soon notices = %d, want 3", got)
	}
}

func TestRevealReminderGoesToEachGiverWithTheirOwnRecipient(t *testing.T) {
	s := newTimedScene(t, 4)
	s.forceCycle(t)
	s.mustDraw(t)
	recipients := assignmentsOf(t, s.h, s.exchange, s.people)
	s.mustSet(t, giftexchange.RuleReminderLead, "5m")
	s.mustSet(t, giftexchange.RuleRevealAt, s.h.futureText(time.Hour))

	s.h.clock.Advance(58 * time.Minute)
	waitForJobs(t, s.h, reminderKind, 1)
	waitForFinished(t, s.exchange, 8)

	soon := receivedOfKind(s.h.notifier.Received(), giftexchange.KindRevealSoon)
	if len(soon) != 4 {
		t.Fatalf("reveal_soon notices = %d, want 4", len(soon))
	}
	for _, notice := range soon {
		if notice.Counterpart == nil || notice.Counterpart.ID != recipients[notice.To.ID] {
			t.Errorf("reveal_soon counterpart is not the addressee's own recipient")
		}
	}
	secrets := confidentialityTerms(s)
	assertConfidential(t, s.h, nil, secrets)
}

func TestSoonNoticesOfAChangedExchangeAreStaleAndNotDelivered(t *testing.T) {
	s := newDrawScene(t, 3)
	s.mustDraw(t)
	drawSoon := insertNotice(t, s.scene, "draw_soon", s.people[0], 1, "")
	revealSoon := insertNotice(t, s.scene, "reveal_soon", s.people[0], 2, "")
	s.h.startRunner(t)

	waitFor(t, deliveryDeadline, "both notices to finish", func() bool {
		return noticeOf(t, drawSoon).hasOutcome("stale") && noticeOf(t, revealSoon).hasOutcome("stale")
	})

	soon := len(receivedOfKind(s.h.notifier.Received(), giftexchange.KindDrawSoon)) +
		len(receivedOfKind(s.h.notifier.Received(), giftexchange.KindRevealSoon))
	if soon != 0 {
		t.Errorf("soon notices delivered = %d, want 0", soon)
	}
}

func TestDrawFailedNoticeIsAlwaysDelivered(t *testing.T) {
	s := newDrawScene(t, 3)
	s.mustDraw(t)
	failed := insertNotice(t, s.scene, "draw_failed", s.organizer, 0, "too_few_participants")
	s.h.startRunner(t)

	waitFor(t, deliveryDeadline, "the notice to finish", func() bool {
		return noticeOf(t, failed).hasOutcome("delivered")
	})

	received := receivedOfKind(s.h.notifier.Received(), giftexchange.KindDrawFailed)
	if len(received) != 1 {
		t.Fatalf("draw_failed notices delivered = %d, want 1", len(received))
	}
	if received[0].To.ID != s.organizer.id || received[0].Reason != giftexchange.ReasonTooFewParticipants {
		t.Errorf("draw_failed went to the organizer %t with reason %q; want the organizer and %q",
			received[0].To.ID == s.organizer.id, received[0].Reason, giftexchange.ReasonTooFewParticipants)
	}
}

func TestTimedJobsCancelMalformedArgsWithoutRetry(t *testing.T) {
	cases := []struct {
		name string
		kind string
		args string
	}{
		{"deadline with a bad ID", drawDeadlineKind, `{"exchange_id": "x", "draw_at": "2030-01-01T00:00:00Z"}`},
		{"deadline with a bad instant", drawDeadlineKind, `{"exchange_id": "00000000-0000-7000-8000-0000000000ff", "draw_at": "soon"}`},
		{"reveal with a bad ID", revealJobKind, `{"exchange_id": "", "reveal_at": "2030-01-01T00:00:00Z"}`},
		{
			"reminder with an unknown target", reminderKind,
			`{"exchange_id": "00000000-0000-7000-8000-0000000000ff", "target": "x", "target_at": "2030-01-01T00:00:00Z", "lead_seconds": 60}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newScene(t)
			insertJob(t, s.h, tc.kind, tc.args)
			s.h.startRunner(t)

			waitFor(t, deliveryDeadline, "the job to be cancelled", func() bool {
				return firstJobIs(t, s.h, "cancelled")
			})

			if attempt := jobStates(t, s.h)[0].attempt; attempt != 1 {
				t.Errorf("attempt = %d, want 1 (no retry)", attempt)
			}
		})
	}
}

func TestTimedJobsOfAnUnknownExchangeComplete(t *testing.T) {
	s := newScene(t)
	id := "00000000-0000-7000-8000-0000000000ff"
	at := time.Now().UTC().Format(time.RFC3339Nano)
	insertJob(t, s.h, drawDeadlineKind, fmt.Sprintf(`{"exchange_id": %q, "draw_at": %q}`, id, at))
	insertJob(t, s.h, revealJobKind, fmt.Sprintf(`{"exchange_id": %q, "reveal_at": %q}`, id, at))
	insertJob(t, s.h, reminderKind,
		fmt.Sprintf(`{"exchange_id": %q, "target": "draw", "target_at": %q, "lead_seconds": 60}`, id, at))
	s.h.startRunner(t)

	waitFor(t, deliveryDeadline, "the jobs to complete", func() bool {
		return jobCount(t, s.h, drawDeadlineKind, "completed")+
			jobCount(t, s.h, revealJobKind, "completed")+
			jobCount(t, s.h, reminderKind, "completed") == 3
	})
}

func TestTimedJobArgsCarryNoPersonIdentity(t *testing.T) {
	s := newDrawScene(t, 3)
	s.mustSet(t, giftexchange.RuleReminderLead, "5m")
	s.mustSet(t, giftexchange.RuleDrawAt, s.h.futureText(time.Hour))
	s.mustSet(t, giftexchange.RuleRevealAt, s.h.futureText(2*time.Hour))

	for _, kind := range []string{drawDeadlineKind, revealJobKind, reminderKind} {
		if got := jobCount(t, s.h, kind, ""); got == 0 {
			t.Errorf("no %s job found", kind)
		}
	}
	secrets := confidentialityTerms(s)
	assertConfidential(t, s.h, nil, secrets)
}

// confidentialityTerms lists every person ID and identity subject of the scene.
func confidentialityTerms(s drawScene) []string {
	secrets := []string{s.organizer.id.String(), s.organizer.subject}
	for _, p := range s.people {
		secrets = append(secrets, p.id.String(), p.subject)
	}

	return secrets
}

// insertNotice writes an outbox row of the kind and a notify job for it, available now on the
// harness queue. A non-empty reason is stored with it.
func insertNotice(t *testing.T, s scene, kind string, addressee person, round int, reason string) string {
	t.Helper()

	noticeID := newUUIDText(t)
	var storedReason *string
	if reason != "" {
		storedReason = &reason
	}
	_, err := shared.Pool().Exec(t.Context(),
		"INSERT INTO giftexchange.notifications (id, kind, exchange_id, person_id, round, reason, created_at) "+
			"VALUES ($1, $2, $3, $4, $5, $6, $7)",
		noticeID, kind, s.exchange.String(), addressee.id.String(), round, storedReason, s.h.clock.Now())
	if err != nil {
		t.Fatalf("insert notification: %v", err)
	}

	insertNotifyJob(t, s.h, fmt.Sprintf(`{"notification_id": %q}`, noticeID))

	return noticeID
}

func newUUIDText(t *testing.T) string {
	t.Helper()

	var id string
	err := shared.Pool().QueryRow(t.Context(), "SELECT gen_random_uuid()::text").Scan(&id)
	if err != nil {
		t.Fatalf("generate uuid: %v", err)
	}

	return id
}

// insertJob adds a job of the kind with raw args, available now on the harness queue.
func insertJob(t *testing.T, h *harness, kind, args string) {
	t.Helper()

	_, err := shared.Pool().Exec(t.Context(),
		"INSERT INTO river.river_job (state, queue, kind, args, max_attempts, priority, scheduled_at) "+
			"VALUES ('available', $1, $2, $3::jsonb, 25, 1, $4)",
		h.queue, kind, args, h.clock.Now())
	if err != nil {
		t.Fatalf("insert job: %v", err)
	}
}

func TestRevealReminderAfterAManualRevealSendsNothing(t *testing.T) {
	s := newTimedScene(t, 3)
	s.mustDraw(t)
	s.mustSet(t, giftexchange.RuleReminderLead, "5m")
	s.mustSet(t, giftexchange.RuleRevealAt, s.h.futureText(time.Hour))
	err := s.h.svc.Reveal(t.Context(), s.organizer.actor, s.group, s.exchange)
	if err != nil {
		t.Fatalf("Reveal: %v", err)
	}

	s.h.clock.Advance(58 * time.Minute)
	waitForJobs(t, s.h, reminderKind, 1)

	for _, row := range notificationRows(t, s.exchange) {
		if row.kind == "reveal_soon" {
			t.Fatalf("a reveal_soon notice was queued for an exchange that is no longer Drawn")
		}
	}
}
