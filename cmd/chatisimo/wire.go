package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"

	"github.com/HectorVeraO/chatisimo/internal/discord"
	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
	"github.com/HectorVeraO/chatisimo/internal/platform/config"
	"github.com/HectorVeraO/chatisimo/internal/platform/db"
	"github.com/HectorVeraO/chatisimo/internal/platform/jobs"
	"github.com/HectorVeraO/chatisimo/internal/platform/logging"
)

// migrationSources lists every schema the binary owns, in apply order.
func migrationSources() ([]db.Source, error) {
	platform, err := config.MigrationSource()
	if err != nil {
		return nil, fmt.Errorf("migration sources: %w", err)
	}

	exchanges, err := giftexchange.MigrationSource()
	if err != nil {
		return nil, fmt.Errorf("migration sources: %w", err)
	}

	sources := []db.Source{platform, exchanges, jobs.NewMigrationSource()}

	return sources, nil
}

// openDatabase opens the pool and applies pending migrations under the advisory lock. The caller
// closes the pool.
func openDatabase(ctx context.Context, env config.Env, logger *slog.Logger) (*pgxpool.Pool, error) {
	sources, err := migrationSources()
	if err != nil {
		return nil, err
	}

	pool, err := db.Open(ctx, env.DatabaseURL.Reveal(), logger)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	err = db.Migrate(ctx, pool, sources...)
	if err != nil {
		pool.Close()

		return nil, fmt.Errorf("migrate database: %w", err)
	}

	return pool, nil
}

// withDatabase loads the environment, opens the migrated database, calls fn and closes the pool.
// Logs go to logOut, never to stdout.
func withDatabase(
	ctx context.Context, lookup func(string) (string, bool), logOut io.Writer,
	fn func(env config.Env, pool *pgxpool.Pool) error,
) error {
	env, err := config.LoadEnv(lookup)
	if err != nil {
		return err //nolint:wrapcheck // LoadEnv already prefixes its joined errors
	}

	logger := logging.New(logOut, env.Environment)

	pool, err := openDatabase(ctx, env, logger)
	if err != nil {
		return err
	}
	defer pool.Close()

	return fn(env, pool)
}

// withStore is withDatabase plus the settings store over the registered keys.
func withStore(
	ctx context.Context, lookup func(string) (string, bool), logOut io.Writer,
	fn func(store *config.Store) error,
) error {
	return withDatabase(ctx, lookup, logOut, func(env config.Env, pool *pgxpool.Pool) error {
		store, err := config.NewStore(ctx, pool, env.MasterKey, env.Environment, settingKeys())
		if err != nil {
			return fmt.Errorf("open settings: %w", err)
		}

		return fn(store)
	})
}

const (
	// The four timeouts add up to the 40 s stop_grace_period of the production compose file.
	gatewayCloseTimeout = 5 * time.Second
	runnerDrainTimeout  = 25 * time.Second
	runnerCancelTimeout = 5 * time.Second
	restCloseTimeout    = 5 * time.Second
)

// systemClock is the production time source of the modules.
type systemClock struct{}

// Now returns the current time.
func (systemClock) Now() time.Time {
	return time.Now()
}

// server is the running platform. A field is set when its startup step succeeds, so shutdown
// closes exactly what was built, whichever step failed.
type server struct {
	logger     *slog.Logger
	pool       *pgxpool.Pool
	notifier   *discord.Notifier
	runner     *jobs.Runner
	isRunnerUp bool
	adapter    *discord.Adapter

	// drainTimeout and cancelTimeout bound the two phases of stopping the runner.
	drainTimeout  time.Duration
	cancelTimeout time.Duration
}

// serve is the body of the run command: it loads the environment, starts the platform and
// blocks until ctx ends or the Gateway fails. stop releases the signal handlers at the start
// of shutdown, so a second signal gets the default behavior.
func serve(ctx context.Context, stop context.CancelFunc, a *app) error {
	env, err := config.LoadEnv(a.lookup)
	if err != nil {
		return err //nolint:wrapcheck // LoadEnv already prefixes its joined errors
	}

	logger := logging.New(a.stderr, env.Environment)
	slog.SetDefault(logger)

	runErr := runServer(ctx, stop, env, logger)
	if runErr != nil {
		logger.Error("run failed", "error", runErr)
	}

	return runErr
}

// runServer starts the platform, serves until the signal or a Gateway failure, then shuts down.
func runServer(ctx context.Context, stop context.CancelFunc, env config.Env, logger *slog.Logger) error {
	s := &server{logger: logger, drainTimeout: runnerDrainTimeout, cancelTimeout: runnerCancelTimeout}

	// The signal context has ended by the time shutdown runs, so shutdown works under a context
	// the signal cannot cancel and bounds each step with its own timeout.
	shutdownCtx := context.WithoutCancel(ctx)

	err := s.build(ctx, env)
	if err != nil {
		stop()

		return errors.Join(err, s.shutdown(shutdownCtx)) //nolint:wrapcheck // joins this function's own errors
	}

	serveErr := s.serveGateway(ctx)

	stop()
	logger.Info("shutting down")

	return errors.Join(serveErr, s.shutdown(shutdownCtx)) //nolint:wrapcheck // joins this function's own errors
}

// build runs the startup steps in order. On error the caller shuts down what was built.
func (s *server) build(ctx context.Context, env config.Env) error {
	settings, err := s.openStorage(ctx, env)
	if err != nil {
		return err
	}

	err = s.construct(settings)
	if err != nil {
		return err
	}

	// River's lifetime ends by shutdown order, not by the signal, so it gets a context the
	// signal cannot cancel.
	err = s.runner.Start(context.WithoutCancel(ctx))
	if err != nil {
		return fmt.Errorf("start jobs: %w", err)
	}

	s.isRunnerUp = true

	return nil
}

// openStorage opens the migrated database and reads the Discord settings from it.
func (s *server) openStorage(ctx context.Context, env config.Env) (discord.Settings, error) {
	pool, err := openDatabase(ctx, env, s.logger)
	if err != nil {
		return discord.Settings{}, err
	}

	s.pool = pool

	store, err := config.NewStore(ctx, pool, env.MasterKey, env.Environment, settingKeys())
	if err != nil {
		return discord.Settings{}, fmt.Errorf("open settings: %w", err)
	}

	err = store.Require(ctx, settingDiscordToken)
	if err != nil {
		return discord.Settings{}, fmt.Errorf("check settings: %w", err)
	}

	token, err := store.Secret(ctx, settingDiscordToken)
	if err != nil {
		return discord.Settings{}, fmt.Errorf("read settings: %w", err)
	}

	baseURL, err := store.Get(ctx, settingDiscordAPIBaseURL)
	if err != nil {
		return discord.Settings{}, fmt.Errorf("read settings: %w", err)
	}

	return discord.Settings{Token: token, APIBaseURL: baseURL}, nil
}

// construct builds the notifier, the scheduler, the module, the runner and the adapter. Every
// constructor receives all it needs: the notifier exists before the module, the adapter after
// it, and the module registers its jobs before the runner is built because River fixes its
// workers at construction.
func (s *server) construct(settings discord.Settings) error {
	notifier, err := discord.NewNotifier(settings, s.logger)
	if err != nil {
		return fmt.Errorf("create notifier: %w", err)
	}

	s.notifier = notifier

	scheduler, err := jobs.NewScheduler(s.pool, jobs.SchedulerOptions{Logger: s.logger})
	if err != nil {
		return fmt.Errorf("create scheduler: %w", err)
	}

	exchanges, err := giftexchange.New(giftexchange.Deps{
		Pool:      s.pool,
		Scheduler: scheduler,
		Clock:     systemClock{},
		Random:    rand.New(giftexchange.CryptoSource{}), //nolint:gosec // the source reads crypto/rand
		Notifier:  notifier,
		Logger:    s.logger,
	})
	if err != nil {
		return fmt.Errorf("create giftexchange: %w", err)
	}

	registry := jobs.NewRegistry()

	err = exchanges.RegisterJobs(registry)
	if err != nil {
		return fmt.Errorf("register jobs: %w", err)
	}

	runner, err := jobs.NewRunner(s.pool, registry, jobs.RunnerOptions{Logger: s.logger.With("component", "river")})
	if err != nil {
		return fmt.Errorf("create runner: %w", err)
	}

	s.runner = runner

	adapter, err := discord.New(notifier, exchanges, s.logger)
	if err != nil {
		return fmt.Errorf("create discord adapter: %w", err)
	}

	s.adapter = adapter

	return nil
}

// serveGateway opens the Gateway and blocks until ctx ends or the Gateway fails to open. A
// failure counts as a runtime failure; the end of ctx is the clean stop.
func (s *server) serveGateway(ctx context.Context) error {
	group, groupCtx := errgroup.WithContext(ctx)

	group.Go(func() error {
		return s.openGateway(ctx, groupCtx)
	})
	group.Go(func() error {
		<-groupCtx.Done()

		return nil
	})

	err := group.Wait()
	if err != nil {
		return err //nolint:wrapcheck // openGateway adds the context
	}

	return nil
}

// openGateway opens the Gateway under groupCtx. An Open that fails once the stop signal has
// ended parent is part of the clean stop, not a failure: the adapter reduces transport errors,
// so the cancellation cause is not reliably visible in err.
func (s *server) openGateway(parent, groupCtx context.Context) error {
	err := s.adapter.Open(groupCtx)
	if err == nil {
		s.logger.Info("discord gateway open")

		return nil
	}

	isStopped := parent.Err() != nil
	if isStopped {
		return nil //nolint:nilerr // the stop signal makes the Open failure part of the clean stop
	}

	return fmt.Errorf("open discord gateway: %w", err)
}

// shutdown closes what was built, in the order that keeps in-flight work alive: the Gateway
// first, then the runner drains, then the REST client and last the pool. It uses fresh
// timeouts under ctx, which the signal cannot cancel.
func (s *server) shutdown(ctx context.Context) error {
	if s.adapter != nil {
		closeCtx, cancel := context.WithTimeout(ctx, gatewayCloseTimeout)
		s.adapter.CloseGateway(closeCtx)
		cancel()
	}

	var problems []error

	if s.isRunnerUp {
		problems = append(problems, s.stopRunner(ctx))
	}

	s.closeREST(ctx)

	if s.pool != nil {
		s.pool.Close()
	}

	return errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
}

// stopRunner lets running jobs finish for drainTimeout, then cancels them and waits
// cancelTimeout more. A cancelled job is retried by River.
func (s *server) stopRunner(ctx context.Context) error {
	drainCtx, cancelDrain := context.WithTimeout(ctx, s.drainTimeout)
	defer cancelDrain()

	err := s.runner.Stop(drainCtx)
	if err == nil {
		return nil
	}

	isStillRunning := errors.Is(err, context.DeadlineExceeded)
	if !isStillRunning {
		return fmt.Errorf("stop jobs: %w", err)
	}

	s.logger.Warn("jobs still running after drain timeout, cancelling them", "timeout", s.drainTimeout)

	cancelCtx, cancelHard := context.WithTimeout(ctx, s.cancelTimeout)
	defer cancelHard()

	err = s.runner.StopAndCancel(cancelCtx)
	if err != nil {
		return fmt.Errorf("cancel jobs: %w", err)
	}

	return nil
}

// closeREST releases the REST client: through the adapter when it exists, else directly.
func (s *server) closeREST(ctx context.Context) {
	closeCtx, cancel := context.WithTimeout(ctx, restCloseTimeout)
	defer cancel()

	switch {
	case s.adapter != nil:
		s.adapter.Close(closeCtx)
	case s.notifier != nil:
		s.notifier.Close(closeCtx)
	}
}
