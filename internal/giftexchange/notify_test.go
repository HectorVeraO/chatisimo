package giftexchange_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
	"github.com/HectorVeraO/chatisimo/internal/platform/jobs"
)

const (
	deliveryDeadline = 20 * time.Second
	sanitizedFailure = "discord status 403 code 50007"
)

// storedNotice is the delivery state of one outbox row.
type storedNotice struct {
	outcome    *string
	finishedAt *time.Time
}

func noticeOf(t *testing.T, id string) storedNotice {
	t.Helper()

	var notice storedNotice
	err := shared.Pool().QueryRow(t.Context(),
		"SELECT outcome, finished_at FROM giftexchange.notifications WHERE id = $1", id).
		Scan(&notice.outcome, &notice.finishedAt)
	if err != nil {
		t.Fatalf("read notification %s: %v", id, err)
	}

	return notice
}

func (n storedNotice) hasOutcome(want string) bool {
	return n.outcome != nil && *n.outcome == want
}

// storedJob is the River state of one job of the test queue.
type storedJob struct {
	state       string
	attempt     int
	scheduledAt time.Time
	errors      string
}

func jobStates(t *testing.T, h *harness) []storedJob {
	t.Helper()

	rows, err := shared.Pool().Query(t.Context(),
		"SELECT state::text, attempt, scheduled_at, coalesce(to_jsonb(errors)::text, '') "+
			"FROM river.river_job WHERE queue = $1 ORDER BY id", h.queue)
	if err != nil {
		t.Fatalf("read job states: %v", err)
	}
	defer rows.Close()

	var found []storedJob
	for rows.Next() {
		var job storedJob
		scanErr := rows.Scan(&job.state, &job.attempt, &job.scheduledAt, &job.errors)
		if scanErr != nil {
			t.Fatalf("scan job state: %v", scanErr)
		}
		found = append(found, job)
	}
	if rows.Err() != nil {
		t.Fatalf("read job states rows: %v", rows.Err())
	}

	return found
}

// firstJobIs reports whether the only job of the queue is in state.
func firstJobIs(t *testing.T, h *harness, state string) bool {
	t.Helper()

	states := jobStates(t, h)
	isInState := len(states) == 1 && states[0].state == state

	return isInState
}

// firstJobFailedOnce reports whether the only job of the queue ran once, failed and waits for a
// retry. River marks a retry due within its scheduler tick as available instead of retryable.
func firstJobFailedOnce(t *testing.T, h *harness) bool {
	t.Helper()

	states := jobStates(t, h)
	hasFailedOnce := len(states) == 1 && states[0].attempt == 1 && states[0].errors != "" &&
		(states[0].state == "retryable" || states[0].state == "available")

	return hasFailedOnce
}

// cancelledScene is an exchange with one participant who gets the cancelled notice.
type cancelledScene struct {
	scene
	participant person
	noticeID    string
}

func newCancelledScene(t *testing.T, subject string) cancelledScene {
	t.Helper()

	s := newScene(t)
	participant := s.h.newPersonWithSubject(t, subject)
	s.h.join(t, participant, s.group, s.exchange)

	err := s.h.svc.Cancel(t.Context(), s.organizer.actor, s.group, s.exchange)
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	rows := notificationRows(t, s.exchange)
	if len(rows) != 1 {
		t.Fatalf("cancel wrote %d notifications, want 1", len(rows))
	}

	return cancelledScene{scene: s, participant: participant, noticeID: rows[0].id}
}

func TestRegisterJobsRejectsANilRegistry(t *testing.T) {
	s := newScene(t)

	err := s.h.svc.RegisterJobs(nil)

	if !errors.Is(err, giftexchange.ErrInvalidInput) {
		t.Errorf("err = %v, want ErrInvalidInput", err)
	}
}

func TestRegisterJobsTwiceOnOneRegistryFails(t *testing.T) {
	s := newScene(t)
	registry := jobs.NewRegistry()

	first := s.h.svc.RegisterJobs(registry)
	second := s.h.svc.RegisterJobs(registry)

	if first != nil {
		t.Fatalf("first RegisterJobs: %v", first)
	}
	if second == nil || !strings.Contains(second.Error(), "giftexchange.notify") {
		t.Errorf("second RegisterJobs = %v, want a duplicate-kind error naming giftexchange.notify", second)
	}
}

func deliverCancelledNotice(t *testing.T) (cancelledScene, []giftexchange.Notification) {
	t.Helper()
	s := newCancelledScene(t, newSubject())
	s.h.startRunner(t)

	waitFor(t, deliveryDeadline, "the notice to be delivered", func() bool {
		return noticeOf(t, s.noticeID).hasOutcome("delivered")
	})

	return s, s.h.notifier.Received()
}

func TestCancelledNoticeIsDeliveredOnceAndRecorded(t *testing.T) {
	s, received := deliverCancelledNotice(t)

	if len(received) != 1 {
		t.Fatalf("notifier received %d notifications, want 1", len(received))
	}
	notice := noticeOf(t, s.noticeID)
	if notice.finishedAt == nil || !notice.finishedAt.Equal(s.h.clock.Now()) {
		t.Errorf("finished_at = %v, want the module clock %v", notice.finishedAt, s.h.clock.Now())
	}
}

func TestCancelledNoticeCarriesTheAddresseeAndTheExchange(t *testing.T) {
	s, received := deliverCancelledNotice(t)

	if len(received) != 1 {
		t.Fatalf("notifier received %d notifications, want 1", len(received))
	}
	got := received[0]
	if got.ID.String() != s.noticeID || got.Kind != giftexchange.KindCancelled {
		t.Errorf("notification = (%v, %v), want (%s, cancelled)", got.ID, got.Kind, s.noticeID)
	}
	if got.To.ID != s.participant.id {
		t.Error("addressee is not the participant")
	}
	wantIdentity := giftexchange.ExternalIdentity{
		Provider: giftexchange.ProviderDiscord,
		Subject:  s.participant.subject,
	}
	if len(got.To.Identities) != 1 || got.To.Identities[0] != wantIdentity {
		t.Error("addressee identities are not the participant's Discord identity")
	}
	if got.Exchange.ID != s.exchange || got.Exchange.State != giftexchange.StateCancelled {
		t.Errorf("exchange = (%v, %v), want (%v, cancelled)", got.Exchange.ID, got.Exchange.State, s.exchange)
	}
	if got.Exchange.CallerIsOrganizer || !got.Exchange.CallerIsParticipant {
		t.Errorf("exchange relation = (organizer %v, participant %v), want the addressee as participant",
			got.Exchange.CallerIsOrganizer, got.Exchange.CallerIsParticipant)
	}
	if got.Counterpart != nil || got.Question != nil || got.Reason != "" {
		t.Error("cancelled notice carries a counterpart, question or reason")
	}
}

func TestCancelledNoticeDeliveryLeaksNothingToLogsOrErrors(t *testing.T) {
	s, _ := deliverCancelledNotice(t)

	secrets := []string{
		s.participant.id.String(), s.participant.subject,
		s.organizer.id.String(), s.organizer.subject,
	}
	assertConfidential(t, s.h, nil, secrets)
}

func TestFinishedNoticeIsNotDeliveredAgainWhenItsJobRunsAgain(t *testing.T) {
	s := newCancelledScene(t, newSubject())
	s.h.startRunner(t)
	waitFor(t, deliveryDeadline, "the job to complete", func() bool {
		return firstJobIs(t, s.h, "completed")
	})

	// Simulates River running the job again after a crash between Notify and the completion of
	// the job.
	_, err := shared.Pool().Exec(t.Context(),
		"UPDATE river.river_job SET state = 'available', finalized_at = NULL, scheduled_at = $2 "+
			"WHERE queue = $1", s.h.queue, s.h.clock.Now())
	if err != nil {
		t.Fatalf("requeue job: %v", err)
	}

	waitFor(t, deliveryDeadline, "the job to run twice", func() bool {
		states := jobStates(t, s.h)
		return len(states) == 1 && states[0].state == "completed" && states[0].attempt == 2
	})
	if got := len(s.h.notifier.Received()); got != 1 {
		t.Errorf("notifier received %d notifications, want 1", got)
	}
}

func TestUndeliverableNoticeIsRecordedAndItsJobCancelledWithoutRetry(t *testing.T) {
	s := newCancelledScene(t, newSubject())
	s.h.notifier.FailWith(fmt.Errorf("%s: %w", sanitizedFailure, giftexchange.ErrUndeliverable))
	s.h.startRunner(t)

	waitFor(t, deliveryDeadline, "the job to be cancelled", func() bool {
		return firstJobIs(t, s.h, "cancelled")
	})

	if !noticeOf(t, s.noticeID).hasOutcome("undeliverable") {
		t.Error("notice is not marked undeliverable")
	}
	if attempt := jobStates(t, s.h)[0].attempt; attempt != 1 {
		t.Errorf("job attempts = %d, want 1", attempt)
	}
	if got := len(s.h.notifier.Received()); got != 1 {
		t.Errorf("notifier received %d notifications, want 1", got)
	}

	warnings := warnRecords(t, s.h)
	if len(warnings) != 1 {
		t.Fatalf("%d warn records, want 1: %v", len(warnings), warnings)
	}
	want := map[string]any{"notification_id": s.noticeID, "kind": "cancelled"}
	for key, value := range warnings[0] {
		if key == "time" || key == "level" || key == "msg" {
			continue
		}
		if want[key] != value {
			t.Errorf("warn field %s = %v, want only the notification ID and kind", key, value)
		}
		delete(want, key)
	}
	if len(want) != 0 {
		t.Errorf("warn record misses fields %v", want)
	}

	secrets := []string{
		s.participant.id.String(), s.participant.subject,
		s.organizer.id.String(), s.organizer.subject,
	}
	assertConfidential(t, s.h, nil, secrets)
}

func TestTransientFailureRetriesUntilTheNotifierSucceeds(t *testing.T) {
	s := newCancelledScene(t, newSubject())
	s.h.notifier.FailWith(errors.New(sanitizedFailure))
	s.h.startRunner(t)

	waitFor(t, deliveryDeadline, "the job to fail once", func() bool {
		return firstJobFailedOnce(t, s.h)
	})
	if notice := noticeOf(t, s.noticeID); notice.outcome != nil {
		t.Errorf("outcome = %v after a failed attempt, want unfinished", *notice.outcome)
	}

	s.h.notifier.FailWith(nil)
	retryAt := jobStates(t, s.h)[0].scheduledAt
	wait := retryAt.Sub(s.h.clock.Now())
	if wait > 0 {
		s.h.clock.Advance(wait + time.Second)
	}

	waitFor(t, deliveryDeadline, "the retry to deliver", func() bool {
		return noticeOf(t, s.noticeID).hasOutcome("delivered")
	})
	if got := len(s.h.notifier.Received()); got != 2 {
		t.Errorf("notifier received %d notifications, want 2 (one failed, one delivered)", got)
	}
}

func TestFailedAttemptStoresTheNotifierTextWithTheNoticeIDAndKindOnly(t *testing.T) {
	s := newCancelledScene(t, fakeUserA)
	s.h.notifier.FailWith(errors.New(sanitizedFailure))
	s.h.startRunner(t)

	waitFor(t, deliveryDeadline, "the job to fail once", func() bool {
		return firstJobFailedOnce(t, s.h)
	})

	stored := jobStates(t, s.h)[0].errors
	var entries []struct {
		Error string `json:"error"`
	}
	err := json.Unmarshal([]byte(stored), &entries)
	if err != nil {
		t.Fatalf("job errors %q: %v", stored, err)
	}
	if len(entries) != 1 {
		t.Fatalf("stored errors = %+v, want exactly one entry", entries)
	}
	for _, part := range []string{s.noticeID, "cancelled", sanitizedFailure} {
		if !strings.Contains(entries[0].Error, part) {
			t.Errorf("stored error %q misses %q", entries[0].Error, part)
		}
	}

	secrets := []string{
		s.participant.id.String(), s.participant.subject, fakeUserA,
		s.organizer.id.String(), s.organizer.subject,
	}
	assertConfidential(t, s.h, nil, secrets)
}

func TestKindWithoutADeliveryRuleFailsTheJobNamingOnlyTheKind(t *testing.T) {
	s := newScene(t)
	participant := s.h.newPerson(t)
	s.h.join(t, participant, s.group, s.exchange)
	noticeID := insertUnruledNotice(t, s, participant)
	s.h.startRunner(t)

	waitFor(t, deliveryDeadline, "the job to fail", func() bool {
		return firstJobFailedOnce(t, s.h)
	})

	if got := len(s.h.notifier.Received()); got != 0 {
		t.Errorf("notifier received %d notifications, want 0", got)
	}
	if notice := noticeOf(t, noticeID); notice.outcome != nil {
		t.Errorf("outcome = %v, want unfinished", *notice.outcome)
	}
	stored := jobStates(t, s.h)[0].errors
	assertNoSecrets(t, "job errors", stored, []string{participant.id.String(), participant.subject})
	if !strings.Contains(stored, "giftexchange: notify: no delivery rule for kind draw_soon") {
		t.Errorf("job errors %q do not name the missing delivery rule", stored)
	}
}

func TestUnusableNotifyJobsAreCancelledWithoutRetryOrDelivery(t *testing.T) {
	cases := []struct {
		name string
		args string
	}{
		{"malformed notification ID", `{"notification_id": "not-a-uuid"}`},
		{"notification row not found", `{"notification_id": "00000000-0000-7000-8000-0000000000ff"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newScene(t)
			insertNotifyJob(t, s.h, tc.args)
			s.h.startRunner(t)

			waitFor(t, deliveryDeadline, "the job to be cancelled", func() bool {
				return firstJobIs(t, s.h, "cancelled")
			})

			if got := len(s.h.notifier.Received()); got != 0 {
				t.Errorf("notifier received %d notifications, want 0", got)
			}
			if attempt := jobStates(t, s.h)[0].attempt; attempt != 1 {
				t.Errorf("attempt = %d, want 1 (no retry)", attempt)
			}
		})
	}
}

// insertUnruledNotice writes an outbox row of a kind no delivery rule covers yet, and a notify job
// for it that is available now on the harness queue.
func insertUnruledNotice(t *testing.T, s scene, addressee person) string {
	t.Helper()

	noticeID := "00000000-0000-7000-8000-000000000001"
	_, err := shared.Pool().Exec(t.Context(),
		"INSERT INTO giftexchange.notifications (id, kind, exchange_id, person_id, round, created_at) "+
			"VALUES ($1, 'draw_soon', $2, $3, 0, $4)",
		noticeID, s.exchange.String(), addressee.id.String(), s.h.clock.Now())
	if err != nil {
		t.Fatalf("insert notification: %v", err)
	}

	insertNotifyJob(t, s.h, fmt.Sprintf(`{"notification_id": %q}`, noticeID))

	return noticeID
}

// insertNotifyJob adds a notify job with the given raw args, available now on the harness queue.
func insertNotifyJob(t *testing.T, h *harness, args string) {
	t.Helper()

	_, err := shared.Pool().Exec(t.Context(),
		"INSERT INTO river.river_job (state, queue, kind, args, max_attempts, priority, scheduled_at) "+
			"VALUES ('available', $1, 'giftexchange.notify', $2::jsonb, 25, 1, $3)",
		h.queue, args, h.clock.Now())
	if err != nil {
		t.Fatalf("insert job: %v", err)
	}
}

// finishedCount returns how many notices of the exchange ended with the given kind and outcome.
func finishedCount(t *testing.T, x giftexchange.ExchangeID, kind, outcome string) int {
	t.Helper()

	var count int
	err := shared.Pool().QueryRow(t.Context(),
		"SELECT count(*) FROM giftexchange.notifications WHERE exchange_id = $1 AND kind = $2 AND outcome = $3",
		x.String(), kind, outcome).Scan(&count)
	if err != nil {
		t.Fatalf("count finished notices: %v", err)
	}

	return count
}

// waitForFinished waits until n notices of the exchange have an outcome.
func waitForFinished(t *testing.T, x giftexchange.ExchangeID, n int) {
	t.Helper()

	waitFor(t, deliveryDeadline, "the notices to finish", func() bool {
		var count int
		err := shared.Pool().QueryRow(t.Context(),
			"SELECT count(*) FROM giftexchange.notifications WHERE exchange_id = $1 AND outcome IS NOT NULL",
			x.String()).Scan(&count)
		if err != nil {
			t.Fatalf("count finished notices: %v", err)
		}

		return count == n
	})
}

func receivedOfKind(
	received []giftexchange.Notification,
	kind giftexchange.NotificationKind,
) []giftexchange.Notification {
	var found []giftexchange.Notification
	for _, notification := range received {
		if notification.Kind == kind {
			found = append(found, notification)
		}
	}

	return found
}

func personWithID(people []person, id giftexchange.PersonID) person {
	for _, p := range people {
		if p.id == id {
			return p
		}
	}

	return person{}
}

func TestDrawnNoticeIsDeliveredToEachGiverNamingTheirOwnRecipient(t *testing.T) {
	s := newDrawScene(t, 4)
	s.forceCycle(t)
	s.mustDraw(t)
	recipients := assignmentsOf(t, s.h, s.exchange, s.people)
	s.h.startRunner(t)

	waitForFinished(t, s.exchange, len(s.people))

	received := s.h.notifier.Received()
	if len(received) != len(s.people) {
		t.Fatalf("notifier received %d notifications, want %d", len(received), len(s.people))
	}
	seen := make(map[giftexchange.PersonID]bool)
	for _, got := range received {
		seen[got.To.ID] = true
		if got.Kind != giftexchange.KindDrawn || got.Exchange.State != giftexchange.StateDrawn {
			t.Errorf("notification = (%v, %v), want a drawn notice of a drawn exchange", got.Kind, got.Exchange.State)
		}
		if got.Counterpart == nil || got.Counterpart.ID != recipients[got.To.ID] {
			t.Error("the counterpart is not the addressee's own recipient")
		}
		if got.Question != nil || got.Reason != "" {
			t.Error("drawn notice carries a question or reason")
		}
	}
	for _, p := range s.people {
		if !seen[p.id] {
			t.Error("a giver got no drawn notice")
		}
	}
	if finishedCount(t, s.exchange, "drawn", "delivered") != len(s.people) {
		t.Error("not every drawn notice is recorded as delivered")
	}
}

func TestDrawnNoticeCounterpartCarriesTheRecipientsIdentity(t *testing.T) {
	s := newDrawScene(t, 3)
	s.mustDraw(t)
	s.h.startRunner(t)
	waitForFinished(t, s.exchange, len(s.people))

	for _, got := range s.h.notifier.Received() {
		recipient := personWithID(s.people, got.Counterpart.ID)
		want := giftexchange.ExternalIdentity{Provider: giftexchange.ProviderDiscord, Subject: recipient.subject}
		if len(got.Counterpart.Identities) != 1 || got.Counterpart.Identities[0] != want {
			t.Error("the counterpart's identities are not the recipient's Discord identity")
		}
	}
}

func TestDrawnNoticeOfAnExchangeCancelledBeforeDeliveryIsStaleAndNotDelivered(t *testing.T) {
	s := newDrawScene(t, 3)
	s.mustDraw(t)
	if err := s.h.svc.Cancel(t.Context(), s.organizer.actor, s.group, s.exchange); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	s.h.startRunner(t)

	waitForFinished(t, s.exchange, 2*len(s.people))

	if got := len(receivedOfKind(s.h.notifier.Received(), giftexchange.KindDrawn)); got != 0 {
		t.Errorf("%d drawn notices were delivered after the cancel, want 0", got)
	}
	if got := len(receivedOfKind(s.h.notifier.Received(), giftexchange.KindCancelled)); got != len(s.people) {
		t.Errorf("%d cancelled notices delivered, want %d: the cancel is always delivered", got, len(s.people))
	}
	if finishedCount(t, s.exchange, "drawn", "stale") != len(s.people) {
		t.Error("not every drawn notice is marked stale")
	}
}

func TestDrawnNoticeOfAnEarlierRoundIsStale(t *testing.T) {
	s := newDrawScene(t, 3)
	s.mustDraw(t)
	_, err := shared.Pool().Exec(t.Context(),
		"UPDATE giftexchange.notifications SET round = 0 WHERE exchange_id = $1", s.exchange.String())
	if err != nil {
		t.Fatalf("age notices: %v", err)
	}
	s.h.startRunner(t)

	waitForFinished(t, s.exchange, len(s.people))

	if got := len(s.h.notifier.Received()); got != 0 {
		t.Errorf("notifier received %d notifications, want 0", got)
	}
	if finishedCount(t, s.exchange, "drawn", "stale") != len(s.people) {
		t.Error("not every drawn notice is marked stale")
	}
}

func TestDrawnNoticeIsStaleWhenTheAddresseeNoLongerGives(t *testing.T) {
	s := newDrawScene(t, 3)
	s.mustDraw(t)
	_, err := shared.Pool().Exec(t.Context(),
		"DELETE FROM giftexchange.assignments WHERE exchange_id = $1 AND giver_id = $2",
		s.exchange.String(), s.people[0].id.String())
	if err != nil {
		t.Fatalf("remove assignment: %v", err)
	}
	s.h.startRunner(t)

	waitForFinished(t, s.exchange, len(s.people))

	if finishedCount(t, s.exchange, "drawn", "stale") != 1 || finishedCount(t, s.exchange, "drawn", "delivered") != 2 {
		t.Error("want exactly the notice of the giver without an assignment marked stale")
	}
	for _, got := range s.h.notifier.Received() {
		if got.To.ID == s.people[0].id {
			t.Error("the giver without an assignment was notified")
		}
	}
}
