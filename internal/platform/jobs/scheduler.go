package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

// SchedulerOptions configures the insert-only client.
type SchedulerOptions struct {
	Logger *slog.Logger
	Clock  Clock
	// Queue is the queue every inserted job goes to (InsertOpts.Queue). "" means
	// river.QueueDefault. Tests give each test its own queue so one test's Runner never works
	// another test's jobs.
	Queue string
}

// Scheduler inserts jobs inside the caller's transaction. It wraps an insert-only River client
// (riverpgxv5 over pool, Schema "river", no Queues, no Workers, SkipUnknownJobCheck: true because
// its bundle is empty; the Runner validates kinds when it works them). Safe for concurrent use.
type Scheduler struct {
	client *river.Client[pgx.Tx]
	queue  string
}

// NewScheduler builds the insert-only client over pool.
func NewScheduler(pool *pgxpool.Pool, opts SchedulerOptions) (*Scheduler, error) {
	if pool == nil {
		return nil, errors.New("new scheduler: nil pool")
	}

	config := &river.Config{
		Logger:              loggerOrDiscard(opts.Logger),
		Schema:              Schema,
		SkipUnknownJobCheck: true,
	}

	if opts.Clock != nil {
		config.Test.Time = riverClock{clock: opts.Clock}
	}

	client, err := river.NewClient(riverpgxv5.New(pool), config)
	if err != nil {
		return nil, fmt.Errorf("new scheduler: %w", err)
	}

	return &Scheduler{client: client, queue: opts.Queue}, nil
}

// EnqueueTx inserts args as available now (no ScheduledAt), so the Runner picks it up within its
// fetch poll. The job is invisible until tx commits and gone if it rolls back.
func (s *Scheduler) EnqueueTx(ctx context.Context, tx pgx.Tx, args river.JobArgs) error {
	validationErr := s.validateInsert(ctx, tx, args)
	if validationErr != nil {
		return fmt.Errorf("enqueue job: %w", validationErr)
	}

	_, err := s.client.InsertTx(ctx, tx, args, s.insertOpts(time.Time{}))
	if err != nil {
		return fmt.Errorf("enqueue job %s: %w", args.Kind(), err)
	}

	return nil
}

// ScheduleTx inserts args to run at or after at (non-zero; a zero at is an error). Scheduled jobs
// become available on River's leader tick (about 5 s). Same transaction semantics as EnqueueTx.
func (s *Scheduler) ScheduleTx(ctx context.Context, tx pgx.Tx, args river.JobArgs, at time.Time) error {
	validationErr := s.validateInsert(ctx, tx, args)
	if at.IsZero() {
		validationErr = joinProblems([]error{validationErr, errors.New("zero schedule time")})
	}

	if validationErr != nil {
		return fmt.Errorf("schedule job: %w", validationErr)
	}

	_, err := s.client.InsertTx(ctx, tx, args, s.insertOpts(at))
	if err != nil {
		return fmt.Errorf("schedule job %s: %w", args.Kind(), err)
	}

	return nil
}

func (s *Scheduler) validateInsert(ctx context.Context, tx pgx.Tx, args river.JobArgs) error {
	var problems []error

	if s == nil || s.client == nil {
		problems = append(problems, errors.New("nil scheduler"))
	}

	if ctx == nil {
		problems = append(problems, errors.New("nil context"))
	}

	if tx == nil {
		problems = append(problems, errors.New("nil transaction"))
	}

	if args == nil {
		problems = append(problems, errors.New("nil args"))
	}

	return joinProblems(problems)
}

func (s *Scheduler) insertOpts(at time.Time) *river.InsertOpts {
	return &river.InsertOpts{Queue: s.queue, ScheduledAt: at}
}

func loggerOrDiscard(logger *slog.Logger) *slog.Logger {
	if logger == nil {
		return slog.New(slog.DiscardHandler)
	}

	return logger
}
