package jobs_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"go.uber.org/goleak"

	"github.com/HectorVeraO/chatisimo/internal/platform/db"
	"github.com/HectorVeraO/chatisimo/internal/platform/jobs"
	"github.com/HectorVeraO/chatisimo/internal/platform/pgtest"
)

const (
	outcomeDeadline = 20 * time.Second
	stopDeadline    = 10 * time.Second
	echoKind        = "jobstest.echo"
)

var shared *pgtest.Database

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	ctx := context.Background()

	database, err := pgtest.New(ctx, pgtest.Options{Sources: []db.Source{jobs.NewMigrationSource()}})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	shared = database
	code := m.Run()

	err = database.Close(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	err = goleak.Find()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	return code
}

// echoArgs carries only a synthetic correlation token.
type echoArgs struct {
	Token string `json:"token"`
}

func (echoArgs) Kind() string {
	return echoKind
}

// echoWorker reports each run on ran and then applies behavior.
type echoWorker struct {
	river.WorkerDefaults[echoArgs]

	ran      chan string
	behavior func(ctx context.Context, job *river.Job[echoArgs]) error
}

func (w *echoWorker) Work(ctx context.Context, job *river.Job[echoArgs]) error {
	w.ran <- job.Args.Token

	if w.behavior == nil {
		return nil
	}

	return w.behavior(ctx, job)
}

// fakeClock starts at the real now and only moves forward.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Now()}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)
}

// rig is one test's queue, worker, scheduler and (once built) runner.
type rig struct {
	queue     string
	worker    *echoWorker
	registry  *jobs.Registry
	scheduler *jobs.Scheduler
	runner    *jobs.Runner
	pool      *pgxpool.Pool
}

func randomHex(t *testing.T, size int) string {
	t.Helper()

	buf := make([]byte, size)

	_, err := rand.Read(buf)
	if err != nil {
		t.Fatalf("random bytes: %v", err)
	}

	return hex.EncodeToString(buf)
}

// newToken returns a synthetic correlation value shaped like a UUID.
func newToken(t *testing.T) string {
	t.Helper()

	raw := randomHex(t, 16)

	return raw[0:8] + "-" + raw[8:12] + "-7" + raw[13:16] + "-8" + raw[17:20] + "-" + raw[20:32]
}

func newRig(t *testing.T, clock jobs.Clock, behavior func(context.Context, *river.Job[echoArgs]) error) *rig {
	t.Helper()

	queue := "t_" + randomHex(t, 8)
	worker := &echoWorker{ran: make(chan string, 64), behavior: behavior}
	registry := jobs.NewRegistry()

	err := jobs.Register[echoArgs](registry, worker)
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	scheduler, err := jobs.NewScheduler(shared.Pool(), jobs.SchedulerOptions{Clock: clock, Queue: queue})
	if err != nil {
		t.Fatalf("new scheduler: %v", err)
	}

	return &rig{queue: queue, worker: worker, registry: registry, scheduler: scheduler, pool: shared.Pool()}
}

// start builds and starts the runner and stops it at cleanup, if the test has not already.
func (r *rig) start(t *testing.T, clock jobs.Clock) {
	t.Helper()

	runner, err := jobs.NewRunner(r.pool, r.registry, jobs.RunnerOptions{
		Clock:             clock,
		FetchPollInterval: 50 * time.Millisecond,
		Queue:             r.queue,
		TestOnly:          true,
	})
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}

	r.runner = runner

	err = runner.Start(context.Background())
	if err != nil {
		t.Fatalf("start runner: %v", err)
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), stopDeadline)
		defer cancel()

		stopErr := runner.StopAndCancel(ctx)
		if stopErr != nil {
			t.Errorf("stop runner at cleanup: %v", stopErr)
		}
	})
}

// inTx runs fn in a transaction and commits it.
func (r *rig) inTx(t *testing.T, commit bool, fn func(tx pgx.Tx)) {
	t.Helper()

	ctx := context.Background()

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	fn(tx)

	if commit {
		err = tx.Commit(ctx)
	} else {
		err = tx.Rollback(ctx)
	}

	if err != nil {
		t.Fatalf("finish tx (commit=%t): %v", commit, err)
	}
}

func (r *rig) enqueue(t *testing.T, commit bool, token string) {
	t.Helper()

	r.inTx(t, commit, func(tx pgx.Tx) {
		err := r.scheduler.EnqueueTx(context.Background(), tx, echoArgs{Token: token})
		if err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	})
}

// waitRan waits until the worker reports token, failing at the deadline. Other tokens reported
// before it are returned too.
func (r *rig) waitRan(t *testing.T, token string) []string {
	t.Helper()

	var seen []string

	deadline := time.After(outcomeDeadline)

	for {
		select {
		case got := <-r.worker.ran:
			seen = append(seen, got)
			if got == token {
				return seen
			}
		case <-deadline:
			t.Fatalf("job %s did not run within %s (saw %v)", token, outcomeDeadline, seen)
		}
	}
}

// jobRows reads (state, attempt) of the jobs in the rig's queue for token.
func (r *rig) jobRows(t *testing.T, token string) []jobRow {
	t.Helper()

	rows, err := r.pool.Query(context.Background(),
		"SELECT state::text, attempt, args FROM river.river_job WHERE queue = $1 AND args->>'token' = $2",
		r.queue, token)
	if err != nil {
		t.Fatalf("query jobs: %v", err)
	}

	defer rows.Close()

	var result []jobRow

	for rows.Next() {
		var row jobRow

		err = rows.Scan(&row.State, &row.Attempt, &row.Args)
		if err != nil {
			t.Fatalf("scan job: %v", err)
		}

		result = append(result, row)
	}

	err = rows.Err()
	if err != nil {
		t.Fatalf("read jobs: %v", err)
	}

	return result
}

type jobRow struct {
	State   string
	Attempt int
	Args    []byte
}

// waitState polls the row of token until it has state or the deadline passes.
func (r *rig) waitState(t *testing.T, token, state string) jobRow {
	t.Helper()

	deadline := time.After(outcomeDeadline)
	tick := time.NewTicker(20 * time.Millisecond)

	defer tick.Stop()

	for {
		rows := r.jobRows(t, token)
		if len(rows) == 1 && rows[0].State == state {
			return rows[0]
		}

		select {
		case <-tick.C:
		case <-deadline:
			t.Fatalf("job %s did not reach state %s within %s (rows %v)", token, state, outcomeDeadline, rows)
		}
	}
}

func TestCommittedJobRunsAndRolledBackNeverDoes(t *testing.T) {
	rolledBack := newToken(t)
	committed := newToken(t)
	env := newRig(t, nil, nil)
	env.start(t, nil)

	env.enqueue(t, false, rolledBack)
	env.enqueue(t, true, committed)

	seen := env.waitRan(t, committed)
	env.waitState(t, committed, "completed")

	for _, token := range seen {
		if token == rolledBack {
			t.Fatalf("rolled-back job %s ran", rolledBack)
		}
	}

	if rows := env.jobRows(t, rolledBack); len(rows) != 0 {
		t.Fatalf("rolled-back job left %d rows", len(rows))
	}
}

func TestEnqueuedJobIsInvisibleBeforeCommit(t *testing.T) {
	token := newToken(t)
	env := newRig(t, nil, nil)

	env.inTx(t, true, func(tx pgx.Tx) {
		err := env.scheduler.EnqueueTx(context.Background(), tx, echoArgs{Token: token})
		if err != nil {
			t.Fatalf("enqueue: %v", err)
		}

		if rows := env.jobRows(t, token); len(rows) != 0 {
			t.Fatalf("job visible before commit: %v", rows)
		}
	})

	if rows := env.jobRows(t, token); len(rows) != 1 {
		t.Fatalf("job rows after commit = %d, want 1", len(rows))
	}
}

func TestJobArgsHoldOnlyTheToken(t *testing.T) {
	token := newToken(t)
	env := newRig(t, nil, nil)

	env.enqueue(t, true, token)

	rows := env.jobRows(t, token)
	if len(rows) != 1 {
		t.Fatalf("job rows = %d, want 1", len(rows))
	}

	var decoded map[string]any

	err := json.Unmarshal(rows[0].Args, &decoded)
	if err != nil {
		t.Fatalf("decode args: %v", err)
	}

	if len(decoded) != 1 || decoded["token"] != token {
		t.Fatalf("args = %v, want only the token", decoded)
	}
}

func TestScheduledJobRunsOnlyAfterTheClockPassesItsTime(t *testing.T) {
	clock := newFakeClock()
	reference := newToken(t)
	later := newToken(t)
	env := newRig(t, clock, nil)
	env.start(t, clock)

	env.enqueue(t, true, reference)
	env.inTx(t, true, func(tx pgx.Tx) {
		err := env.scheduler.ScheduleTx(context.Background(), tx, echoArgs{Token: later}, clock.Now().Add(time.Hour))
		if err != nil {
			t.Fatalf("schedule: %v", err)
		}
	})

	seen := env.waitRan(t, reference)
	env.waitState(t, reference, "completed")

	for _, token := range seen {
		if token == later {
			t.Fatalf("scheduled job ran before its time")
		}
	}

	rows := env.jobRows(t, later)
	if len(rows) != 1 || rows[0].State != "scheduled" {
		t.Fatalf("scheduled job rows = %v, want one scheduled", rows)
	}

	clock.Advance(2 * time.Hour)

	env.waitRan(t, later)
	env.waitState(t, later, "completed")
}

func TestScheduleTxRejectsAZeroTime(t *testing.T) {
	env := newRig(t, nil, nil)

	env.inTx(t, false, func(tx pgx.Tx) {
		err := env.scheduler.ScheduleTx(context.Background(), tx, echoArgs{Token: newToken(t)}, time.Time{})
		if err == nil {
			t.Fatal("ScheduleTx with a zero time succeeded")
		}
	})
}

func TestSchedulerRejectsInvalidArguments(t *testing.T) {
	env := newRig(t, nil, nil)

	t.Run("nil pool", func(t *testing.T) {
		_, err := jobs.NewScheduler(nil, jobs.SchedulerOptions{})
		if err == nil {
			t.Fatal("NewScheduler(nil) succeeded")
		}
	})

	t.Run("nil args", func(t *testing.T) {
		env.inTx(t, false, func(tx pgx.Tx) {
			err := env.scheduler.EnqueueTx(context.Background(), tx, nil)
			if err == nil {
				t.Fatal("EnqueueTx(nil args) succeeded")
			}
		})
	})

	t.Run("nil transaction", func(t *testing.T) {
		err := env.scheduler.EnqueueTx(context.Background(), nil, echoArgs{Token: newToken(t)})
		if err == nil {
			t.Fatal("EnqueueTx(nil tx) succeeded")
		}
	})

	t.Run("nil context", func(t *testing.T) {
		var nilContext context.Context

		env.inTx(t, false, func(tx pgx.Tx) {
			args := echoArgs{Token: newToken(t)}

			err := env.scheduler.EnqueueTx(nilContext, tx, args)
			if err == nil {
				t.Fatal("EnqueueTx(nil context) succeeded")
			}

			err = env.scheduler.ScheduleTx(nilContext, tx, args, time.Now().Add(time.Hour))
			if err == nil {
				t.Fatal("ScheduleTx(nil context) succeeded")
			}
		})
	})
}

func TestRegisterRejectsDuplicatesAndNils(t *testing.T) {
	registry := jobs.NewRegistry()
	worker := &echoWorker{ran: make(chan string, 1)}

	err := jobs.Register[echoArgs](registry, worker)
	if err != nil {
		t.Fatalf("first register: %v", err)
	}

	t.Run("duplicate kind", func(t *testing.T) {
		err := jobs.Register[echoArgs](registry, worker)
		if err == nil {
			t.Fatal("duplicate kind registered")
		}
	})

	t.Run("nil registry", func(t *testing.T) {
		err := jobs.Register[echoArgs](nil, worker)
		if err == nil {
			t.Fatal("nil registry accepted")
		}
	})

	t.Run("nil worker", func(t *testing.T) {
		err := jobs.Register[echoArgs](registry, nil)
		if err == nil {
			t.Fatal("nil worker accepted")
		}
	})
}

func TestNewRunnerRejectsInvalidArguments(t *testing.T) {
	filled := newRig(t, nil, nil)

	tests := []struct {
		name     string
		pool     *pgxpool.Pool
		registry *jobs.Registry
		opts     jobs.RunnerOptions
	}{
		{name: "empty registry", pool: shared.Pool(), registry: jobs.NewRegistry()},
		{name: "nil registry", pool: shared.Pool()},
		{name: "nil pool", registry: filled.registry},
		{name: "negative workers", pool: shared.Pool(), registry: filled.registry, opts: jobs.RunnerOptions{MaxWorkers: -1}},
		{
			name: "negative poll", pool: shared.Pool(), registry: filled.registry,
			opts: jobs.RunnerOptions{FetchPollInterval: -time.Second},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner, err := jobs.NewRunner(test.pool, test.registry, test.opts)
			if err == nil || runner != nil {
				t.Fatalf("NewRunner = (%v, %v), want an error", runner, err)
			}
		})
	}
}

func TestZeroValueOptionsWorkTheDefaultQueue(t *testing.T) {
	token := newToken(t)
	env := newRig(t, nil, nil)

	scheduler, err := jobs.NewScheduler(env.pool, jobs.SchedulerOptions{})
	if err != nil {
		t.Fatalf("new scheduler: %v", err)
	}

	runner, err := jobs.NewRunner(env.pool, env.registry, jobs.RunnerOptions{})
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}

	err = runner.Start(context.Background())
	if err != nil {
		t.Fatalf("start runner: %v", err)
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), stopDeadline)
		defer cancel()

		stopErr := runner.StopAndCancel(ctx)
		if stopErr != nil {
			t.Errorf("stop runner at cleanup: %v", stopErr)
		}
	})

	env.queue = river.QueueDefault
	env.scheduler = scheduler
	env.enqueue(t, true, token)
	env.waitRan(t, token)
	env.waitState(t, token, "completed")
}

func TestStopDrainsAnInFlightJob(t *testing.T) {
	token := newToken(t)
	release := make(chan struct{})

	var finished atomic.Bool

	env := newRig(t, nil, func(_ context.Context, _ *river.Job[echoArgs]) error {
		<-release
		finished.Store(true)

		return nil
	})
	env.start(t, nil)
	env.enqueue(t, true, token)
	env.waitRan(t, token)

	stopped := make(chan error, 1)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), stopDeadline)
		defer cancel()

		stopped <- env.runner.Stop(ctx)
	}()

	close(release)

	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("Stop = %v, want nil", err)
		}
	case <-time.After(stopDeadline):
		t.Fatal("Stop did not return after the job finished")
	}

	if !finished.Load() {
		t.Fatal("Stop returned before the in-flight job finished")
	}

	row := env.waitState(t, token, "completed")
	if row.Attempt != 1 {
		t.Fatalf("attempt = %d, want 1", row.Attempt)
	}
}

func TestStopAndCancelCancelsTheJobContext(t *testing.T) {
	token := newToken(t)
	observed := make(chan error, 1)
	env := newRig(t, nil, func(ctx context.Context, _ *river.Job[echoArgs]) error {
		<-ctx.Done()
		observed <- ctx.Err()

		return ctx.Err()
	})
	env.start(t, nil)
	env.enqueue(t, true, token)
	env.waitRan(t, token)

	ctx, cancel := context.WithTimeout(context.Background(), stopDeadline)
	defer cancel()

	err := env.runner.StopAndCancel(ctx)
	if err != nil {
		t.Fatalf("StopAndCancel = %v, want nil", err)
	}

	select {
	case got := <-observed:
		if !errors.Is(got, context.Canceled) {
			t.Fatalf("worker context error = %v, want canceled", got)
		}
	default:
		t.Fatal("worker did not observe the cancellation")
	}

	// Stopping again after the client has stopped is a no-op.
	err = env.runner.Stop(ctx)
	if err != nil {
		t.Fatalf("Stop after StopAndCancel = %v, want nil", err)
	}
}

func TestJobCancelIsNotRetried(t *testing.T) {
	token := newToken(t)
	env := newRig(t, nil, func(_ context.Context, _ *river.Job[echoArgs]) error {
		return river.JobCancel(errors.New("no longer needed"))
	})
	env.start(t, nil)
	env.enqueue(t, true, token)

	row := env.waitState(t, token, "cancelled")
	if row.Attempt != 1 {
		t.Fatalf("attempt = %d, want 1", row.Attempt)
	}

	// Only one run was reported: a retry would report a second one.
	if got := len(env.worker.ran); got != 1 {
		t.Fatalf("worker ran %d times, want 1", got)
	}
}

func TestMigrationSourceIsIdempotent(t *testing.T) {
	source := jobs.NewMigrationSource()

	if source.Schema() != "river" || jobs.Schema != "river" {
		t.Fatalf("schema = %q, want river", source.Schema())
	}

	var applied [2]int

	for run := 0; run < 2; run++ {
		err := db.Migrate(context.Background(), shared.Pool(), source)
		if err != nil {
			t.Fatalf("migrate run %d: %v", run+1, err)
		}

		var hasJobTable bool

		err = shared.Pool().QueryRow(context.Background(),
			"SELECT to_regclass('river.river_job') IS NOT NULL").Scan(&hasJobTable)
		if err != nil || !hasJobTable {
			t.Fatalf("after run %d: river.river_job exists = %v, err = %v", run+1, hasJobTable, err)
		}

		err = shared.Pool().QueryRow(context.Background(),
			"SELECT count(*) FROM river.river_migration").Scan(&applied[run])
		if err != nil {
			t.Fatalf("count river migrations after run %d: %v", run+1, err)
		}
	}

	if applied[0] == 0 || applied[0] != applied[1] {
		t.Fatalf("applied river migrations = %d then %d, want equal and non-zero", applied[0], applied[1])
	}
}

// pinnedRiverFingerprint is the digest of the River migrations of the pinned River version.
// Upgrading River to a release with a different migration set must update it on purpose.
const pinnedRiverFingerprint = "15901c2e337ed302d695a5f3b75a750075dfa2dacc05cd2bf99e28d59e31ed7e"

func TestMigrationSourceFingerprintTracksRiverMigrations(t *testing.T) {
	got, err := jobs.NewMigrationSource().Fingerprint()
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}

	if hex.EncodeToString(got) != pinnedRiverFingerprint {
		t.Fatalf("fingerprint = %x, want %s", got, pinnedRiverFingerprint)
	}
}

func TestMigrationSourceUpRejectsNilArguments(t *testing.T) {
	var nilContext context.Context

	tests := []struct {
		name string
		ctx  context.Context
		pool *pgxpool.Pool
	}{
		{name: "nil pool", ctx: context.Background()},
		{name: "nil context", ctx: nilContext, pool: shared.Pool()},
		{name: "nil context and nil pool"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := jobs.NewMigrationSource().Up(test.ctx, test.pool)
			if err == nil {
				t.Fatal("Up succeeded, want an error")
			}
		})
	}
}
