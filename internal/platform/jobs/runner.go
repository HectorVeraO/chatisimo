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

const (
	defaultMaxWorkers = 4
	softStopTimeout   = 25 * time.Second
)

// RunnerOptions tunes the working client. Zero values: 4 workers on the default queue, River's
// 1 s fetch poll, no test clock.
type RunnerOptions struct {
	Logger            *slog.Logger
	MaxWorkers        int
	Clock             Clock
	FetchPollInterval time.Duration
	// Queue is the only queue the Runner works (Config.Queues). "" means river.QueueDefault.
	// It must match the Scheduler's Queue.
	Queue string
	// TestOnly sets river.Config.TestOnly (no staggered maintenance start). Tests only.
	TestOnly bool
}

// Runner works the registered jobs.
type Runner struct {
	client *river.Client[pgx.Tx]
}

// NewRunner builds the working client from r (an empty registry is an error), Schema "river",
// SoftStopTimeout 25 s as a guard against an unexpected cancellation of the Start context.
func NewRunner(pool *pgxpool.Pool, r *Registry, opts RunnerOptions) (*Runner, error) {
	validationErr := validateRunner(pool, r, opts)
	if validationErr != nil {
		return nil, fmt.Errorf("new runner: %w", validationErr)
	}

	queue := opts.Queue
	if queue == "" {
		queue = river.QueueDefault
	}

	maxWorkers := opts.MaxWorkers
	if maxWorkers == 0 {
		maxWorkers = defaultMaxWorkers
	}

	config := &river.Config{
		FetchCooldown:     fetchCooldown(opts.FetchPollInterval),
		FetchPollInterval: opts.FetchPollInterval,
		Logger:            loggerOrDiscard(opts.Logger),
		Queues:            map[string]river.QueueConfig{queue: {MaxWorkers: maxWorkers}},
		Schema:            Schema,
		SoftStopTimeout:   softStopTimeout,
		TestOnly:          opts.TestOnly,
		Workers:           r.workers,
	}

	if opts.Clock != nil {
		config.Test.Time = riverClock{clock: opts.Clock}
	}

	client, err := river.NewClient(riverpgxv5.New(pool), config)
	if err != nil {
		return nil, fmt.Errorf("new runner: %w", err)
	}

	return &Runner{client: client}, nil
}

// validateRunner reports every problem with the arguments of NewRunner together.
func validateRunner(pool *pgxpool.Pool, r *Registry, opts RunnerOptions) error {
	var problems []error

	if pool == nil {
		problems = append(problems, errors.New("nil pool"))
	}

	if r == nil {
		problems = append(problems, errors.New("nil registry"))
	} else if r.kinds == 0 {
		problems = append(problems, errors.New("empty registry"))
	}

	if opts.MaxWorkers < 0 {
		problems = append(problems, errors.New("negative MaxWorkers"))
	}

	if opts.FetchPollInterval < 0 {
		problems = append(problems, errors.New("negative FetchPollInterval"))
	}

	return joinProblems(problems)
}

// fetchCooldown lowers River's cooldown to the poll interval when the interval is shorter than
// River's default cooldown, because River rejects a poll interval below the cooldown. Zero keeps
// River's defaults.
func fetchCooldown(pollInterval time.Duration) time.Duration {
	if pollInterval > 0 && pollInterval < river.FetchCooldownDefault {
		return pollInterval
	}

	return 0
}

// Start begins fetching and returns immediately.
func (r *Runner) Start(ctx context.Context) error {
	validationErr := r.validateCall(ctx)
	if validationErr != nil {
		return fmt.Errorf("start runner: %w", validationErr)
	}

	err := r.client.Start(ctx)
	if err != nil {
		return fmt.Errorf("start runner: %w", err)
	}

	return nil
}

// Stop drains in-flight jobs; it returns when River's Stopped channel closes or ctx ends.
func (r *Runner) Stop(ctx context.Context) error {
	validationErr := r.validateCall(ctx)
	if validationErr != nil {
		return fmt.Errorf("stop runner: %w", validationErr)
	}

	err := r.client.Stop(ctx)
	if err != nil {
		return fmt.Errorf("stop runner: %w", err)
	}

	return nil
}

// StopAndCancel cancels running jobs' contexts, then waits like Stop.
func (r *Runner) StopAndCancel(ctx context.Context) error {
	validationErr := r.validateCall(ctx)
	if validationErr != nil {
		return fmt.Errorf("stop and cancel runner: %w", validationErr)
	}

	err := r.client.StopAndCancel(ctx)
	if err != nil {
		return fmt.Errorf("stop and cancel runner: %w", err)
	}

	return nil
}

func (r *Runner) validateCall(ctx context.Context) error {
	var problems []error

	if r == nil || r.client == nil {
		problems = append(problems, errors.New("nil runner"))
	}

	if ctx == nil {
		problems = append(problems, errors.New("nil context"))
	}

	return joinProblems(problems)
}
