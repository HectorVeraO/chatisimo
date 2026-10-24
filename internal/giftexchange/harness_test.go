package giftexchange_test

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
	"github.com/HectorVeraO/chatisimo/internal/platform/jobs"
)

// Fake snowflakes the tests name directly. Tests that need a fresh identity use newSubject.
const (
	fakeUserA = "100000000000000001"
	fakeUserB = "100000000000000002"
)

// fakeClock only moves forward, like the clock River needs. It satisfies giftexchange.Clock and
// jobs.Clock.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Now().UTC().Truncate(time.Microsecond)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

// Advance moves the clock forward by d; a negative d panics because time never goes back.
func (c *fakeClock) Advance(d time.Duration) {
	if d < 0 {
		panic("fakeClock: cannot move backward")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)
}

// lockedRandom is a mutex-guarded ChaCha8 generator that can be reseeded.
type lockedRandom struct {
	mu     sync.Mutex
	source *rand.ChaCha8
	rng    *rand.Rand
}

func newLockedRandom(seed [32]byte) *lockedRandom {
	source := rand.NewChaCha8(seed)
	return &lockedRandom{source: source, rng: rand.New(source)} //nolint:gosec // seeded test randomness
}

func (r *lockedRandom) IntN(n int) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.rng.IntN(n)
}

func (r *lockedRandom) Shuffle(n int, swap func(i, j int)) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.rng.Shuffle(n, swap)
}

// Reseed restarts the sequence from seed.
func (r *lockedRandom) Reseed(seed [32]byte) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.source.Seed(seed)
}

// recordingNotifier captures every Notification and can be told to fail.
type recordingNotifier struct {
	mu       sync.Mutex
	received []giftexchange.Notification
	failWith error
}

func (n *recordingNotifier) Notify(_ context.Context, notification giftexchange.Notification) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.received = append(n.received, notification)

	return n.failWith
}

// FailWith makes every later Notify return err; nil restores success.
func (n *recordingNotifier) FailWith(err error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.failWith = err
}

// Received returns a copy of every notification captured so far.
func (n *recordingNotifier) Received() []giftexchange.Notification {
	n.mu.Lock()
	defer n.mu.Unlock()

	return slices.Clone(n.received)
}

// syncBuffer is a bytes.Buffer safe for concurrent writes, the sink of the JSON log handler.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	n, err := b.buf.Write(p)
	if err != nil {
		return n, fmt.Errorf("write log buffer: %w", err)
	}

	return n, nil
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// harness is one test's Service and the doubles around it.
type harness struct {
	svc      *giftexchange.Service
	clock    *fakeClock
	random   *lockedRandom
	notifier *recordingNotifier
	logs     *syncBuffer
	queue    string
}

// newService builds a Service over the shared database with its own queue name, so the jobs it
// inserts are told apart from every other test's.
func newService(t testing.TB) *harness {
	t.Helper()

	clock := newFakeClock()
	random := newLockedRandom([32]byte{1})
	notifier := &recordingNotifier{}
	logs := &syncBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	queue := newQueueName(t)

	scheduler, err := jobs.NewScheduler(shared.Pool(), jobs.SchedulerOptions{
		Logger: logger,
		Clock:  clock,
		Queue:  queue,
	})
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}

	svc, err := giftexchange.New(giftexchange.Deps{
		Pool:      shared.Pool(),
		Scheduler: scheduler,
		Clock:     clock,
		Random:    random,
		Notifier:  notifier,
		Logger:    logger,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return &harness{svc: svc, clock: clock, random: random, notifier: notifier, logs: logs, queue: queue}
}

func newQueueName(t testing.TB) string {
	t.Helper()

	var raw [8]byte
	_, err := cryptorand.Read(raw[:])
	if err != nil {
		t.Fatalf("read randomness: %v", err)
	}

	return "t_" + hex.EncodeToString(raw[:])
}

// newSubject returns a fresh fake snowflake, so parallel tests never share an identity.
func newSubject() string {
	var raw [8]byte
	_, err := cryptorand.Read(raw[:])
	if err != nil {
		panic("read randomness: " + err.Error())
	}

	return fmt.Sprintf("1%017d", binary.BigEndian.Uint64(raw[:])%100_000_000_000_000_000)
}

// errOf keeps only the error of a two-result call, so a test closure can return it unwrapped.
func errOf[T any](_ T, err error) error {
	return err
}

// person is a resolved test user: the actor, the person ID and the fake subject behind it.
type person struct {
	actor   giftexchange.Actor
	id      giftexchange.PersonID
	subject string
}

func (h *harness) newPerson(t *testing.T) person {
	t.Helper()

	return h.newPersonWithSubject(t, newSubject())
}

func (h *harness) newPersonWithSubject(t *testing.T, subject string) person {
	t.Helper()

	ref := giftexchange.IdentityRef{Provider: giftexchange.ProviderDiscord, Subject: subject}

	actor, err := h.svc.ResolveActor(t.Context(), ref)
	if err != nil {
		t.Fatalf("ResolveActor: %v", err)
	}

	return person{actor: actor, id: actor.Person(), subject: subject}
}

func (h *harness) newGroup(t *testing.T) giftexchange.GroupID {
	t.Helper()

	ref := giftexchange.GroupRef{Provider: giftexchange.ProviderDiscord, Subject: newSubject()}

	group, err := h.svc.ResolveGroup(t.Context(), ref)
	if err != nil {
		t.Fatalf("ResolveGroup: %v", err)
	}

	return group
}

// newExchange creates an exchange in group g organized by organizer.
func (h *harness) newExchange(
	t *testing.T,
	organizer person,
	g giftexchange.GroupID,
) giftexchange.ExchangeView {
	t.Helper()

	view, err := h.svc.CreateExchange(t.Context(), organizer.actor, giftexchange.CreateExchangeRequest{
		Group: g,
		Name:  "Winter exchange",
	})
	if err != nil {
		t.Fatalf("CreateExchange: %v", err)
	}

	return view
}

// join makes p a participant of exchange x in group g.
func (h *harness) join(t *testing.T, p person, g giftexchange.GroupID, x giftexchange.ExchangeID) {
	t.Helper()

	err := h.svc.Join(t.Context(), p.actor, g, x)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
}

// scene is an open exchange with an organizer, and a group to put others in.
type scene struct {
	h         *harness
	group     giftexchange.GroupID
	organizer person
	exchange  giftexchange.ExchangeID
}

func newScene(t *testing.T) scene {
	t.Helper()

	h := newService(t)
	group := h.newGroup(t)
	organizer := h.newPerson(t)
	view := h.newExchange(t, organizer, group)

	return scene{h: h, group: group, organizer: organizer, exchange: view.ID}
}

// snapshot is every stored fact about one exchange plus the jobs of the test queue, as text
// lines, so two snapshots compare with slices.Equal.
type snapshot []string

func takeSnapshot(t *testing.T, h *harness, x giftexchange.ExchangeID) snapshot {
	t.Helper()

	queries := []struct {
		label string
		sql   string
	}{
		{"exchange", "SELECT t::text FROM giftexchange.exchanges t WHERE t.id = $1"},
		{"participant", "SELECT t::text FROM giftexchange.participants t WHERE t.exchange_id = $1"},
		{"exclusion", "SELECT t::text FROM giftexchange.exclusions t WHERE t.exchange_id = $1"},
		{"assignment", "SELECT t::text FROM giftexchange.assignments t WHERE t.exchange_id = $1"},
		{"question", "SELECT t::text FROM giftexchange.questions t WHERE t.exchange_id = $1"},
		{"notification", "SELECT t::text FROM giftexchange.notifications t WHERE t.exchange_id = $1"},
	}

	var lines snapshot
	for _, query := range queries {
		rows, err := shared.Pool().Query(t.Context(), query.sql, x.String())
		if err != nil {
			t.Fatalf("snapshot %s: %v", query.label, err)
		}

		var found []string
		for rows.Next() {
			var text string
			scanErr := rows.Scan(&text)
			if scanErr != nil {
				rows.Close()
				t.Fatalf("snapshot %s scan: %v", query.label, scanErr)
			}
			found = append(found, query.label+": "+text)
		}
		rows.Close()
		if rows.Err() != nil {
			t.Fatalf("snapshot %s rows: %v", query.label, rows.Err())
		}

		slices.Sort(found)
		lines = append(lines, found...)
	}

	lines = append(lines, fmt.Sprintf("jobs: %d", countJobs(t, h)))

	return lines
}

func countJobs(t *testing.T, h *harness) int {
	t.Helper()

	var count int
	err := shared.Pool().QueryRow(t.Context(),
		"SELECT count(*) FROM river.river_job WHERE queue = $1", h.queue).Scan(&count)
	if err != nil {
		t.Fatalf("count jobs: %v", err)
	}

	return count
}

func assertUnchanged(t *testing.T, before, after snapshot) {
	t.Helper()

	if slices.Equal(before, after) {
		return
	}

	t.Errorf("stored state changed:\nbefore:\n  %s\nafter:\n  %s",
		strings.Join(before, "\n  "), strings.Join(after, "\n  "))
}

// jobRow is one job of the test queue as stored.
type jobRow struct {
	args   string
	errors string
}

func jobsOf(t *testing.T, h *harness) []jobRow {
	t.Helper()

	rows, err := shared.Pool().Query(t.Context(),
		"SELECT args::text, errors::text FROM river.river_job WHERE queue = $1 ORDER BY id", h.queue)
	if err != nil {
		t.Fatalf("read jobs: %v", err)
	}
	defer rows.Close()

	var found []jobRow
	for rows.Next() {
		var args string
		var errorsText *string
		scanErr := rows.Scan(&args, &errorsText)
		if scanErr != nil {
			t.Fatalf("scan job: %v", scanErr)
		}

		row := jobRow{args: args}
		if errorsText != nil {
			row.errors = *errorsText
		}
		found = append(found, row)
	}
	if rows.Err() != nil {
		t.Fatalf("read jobs rows: %v", rows.Err())
	}

	return found
}

// assertNoSecrets fails when text contains any of the secrets (person IDs, identity subjects).
func assertNoSecrets(t *testing.T, where, text string, secrets []string) {
	t.Helper()

	for _, secret := range secrets {
		if strings.Contains(text, secret) {
			t.Errorf("%s contains a person ID or identity subject", where)
		}
	}
}

// assertConfidential checks confidentiality items 1 to 3: no log record, returned error or job
// row of the test queue holds a secret.
func assertConfidential(t *testing.T, h *harness, errs []error, secrets []string) {
	t.Helper()

	assertNoSecrets(t, "log output", h.logs.String(), secrets)
	for _, err := range errs {
		if err != nil {
			assertNoSecrets(t, "returned error", err.Error(), secrets)
		}
	}
	for _, job := range jobsOf(t, h) {
		assertNoSecrets(t, "job args", job.args, secrets)
		assertNoSecrets(t, "job errors", job.errors, secrets)
	}
}

// startRunner works the harness queue with a Runner over the module's workers until the test
// ends. Tests that call it do not run in parallel, so only one Runner is alive at a time.
func (h *harness) startRunner(t *testing.T) {
	t.Helper()

	registry := jobs.NewRegistry()
	registerErr := h.svc.RegisterJobs(registry)
	if registerErr != nil {
		t.Fatalf("RegisterJobs: %v", registerErr)
	}

	runner, err := jobs.NewRunner(shared.Pool(), registry, jobs.RunnerOptions{
		Logger:            slog.New(slog.NewJSONHandler(h.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Clock:             h.clock,
		FetchPollInterval: 50 * time.Millisecond,
		Queue:             h.queue,
		TestOnly:          true,
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	startErr := runner.Start(context.Background())
	if startErr != nil {
		t.Fatalf("Runner.Start: %v", startErr)
	}

	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), closeDeadline)
		defer cancel()

		stopErr := runner.Stop(stopCtx)
		if stopErr != nil {
			t.Errorf("Runner.Stop: %v", stopErr)
		}
	})
}

// waitFor polls cond until it holds or the deadline passes, then fails the test with what.
func waitFor(t *testing.T, deadline time.Duration, what string, cond func() bool) {
	t.Helper()

	end := time.Now().Add(deadline)
	for !cond() {
		if time.Now().After(end) {
			t.Fatalf("timed out after %v waiting for %s", deadline, what)
		}

		<-time.After(25 * time.Millisecond)
	}
}

// inputFields returns the Field of every InputError inside err, walking errors.Join trees.
func inputFields(err error) []string {
	var fields []string

	var input *giftexchange.InputError
	if errors.As(err, &input) {
		fields = append(fields, input.Field)
	}

	var joined interface{ Unwrap() []error }
	if errors.As(err, &joined) {
		fields = nil
		for _, child := range joined.Unwrap() {
			fields = append(fields, inputFields(child)...)
		}
	}

	return fields
}

// warnRecords returns every Warn-level record the harness logger captured, as decoded JSON.
func warnRecords(t *testing.T, h *harness) []map[string]any {
	t.Helper()

	var found []map[string]any
	for _, line := range strings.Split(h.logs.String(), "\n") {
		if line == "" {
			continue
		}

		var record map[string]any
		err := json.Unmarshal([]byte(line), &record)
		if err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		if record["level"] == "WARN" {
			found = append(found, record)
		}
	}

	return found
}
