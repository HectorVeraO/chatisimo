package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/riverqueue/river"

	"github.com/HectorVeraO/chatisimo/internal/platform/db"
	"github.com/HectorVeraO/chatisimo/internal/platform/jobs"
)

const (
	blockingKind  = "cmdtest.blocking"
	stopTestLimit = 20 * time.Second
)

type blockingArgs struct{}

func (blockingArgs) Kind() string {
	return blockingKind
}

// blockingWorker holds its job until the job context ends.
type blockingWorker struct {
	river.WorkerDefaults[blockingArgs]

	started   chan struct{}
	cancelled chan struct{}
}

func (w *blockingWorker) Work(ctx context.Context, _ *river.Job[blockingArgs]) error {
	close(w.started)
	<-ctx.Done()
	close(w.cancelled)

	return ctx.Err() //nolint:wrapcheck // the test worker reports the cancellation as is
}

func TestStopRunnerCancelsAJobThatOutlivesTheDrainTimeout(t *testing.T) {
	noLeaks(t)

	database := newClone(t)
	pool := database.Pool()

	sources, err := migrationSources()
	if err != nil {
		t.Fatalf("migration sources: %v", err)
	}

	err = db.Migrate(t.Context(), pool, sources...)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	worker := &blockingWorker{started: make(chan struct{}), cancelled: make(chan struct{})}
	registry := jobs.NewRegistry()

	err = jobs.Register[blockingArgs](registry, worker)
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	scheduler, err := jobs.NewScheduler(pool, jobs.SchedulerOptions{})
	if err != nil {
		t.Fatalf("new scheduler: %v", err)
	}

	runner, err := jobs.NewRunner(pool, registry, jobs.RunnerOptions{FetchPollInterval: 50 * time.Millisecond, TestOnly: true})
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}

	err = runner.Start(context.Background())
	if err != nil {
		t.Fatalf("start runner: %v", err)
	}

	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	err = scheduler.EnqueueTx(t.Context(), tx, blockingArgs{})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	err = tx.Commit(t.Context())
	if err != nil {
		t.Fatalf("commit: %v", err)
	}

	select {
	case <-worker.started:
	case <-time.After(stopTestLimit):
		t.Fatal("the job never started")
	}

	s := &server{
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		runner:        runner,
		drainTimeout:  200 * time.Millisecond,
		cancelTimeout: stopTestLimit,
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), stopTestLimit)
	defer cancel()

	err = s.stopRunner(stopCtx)
	if err != nil {
		t.Fatalf("stopRunner = %v, want nil", err)
	}

	select {
	case <-worker.cancelled:
	default:
		t.Error("the running job was not cancelled after the drain timeout")
	}
}
