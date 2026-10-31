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
	"github.com/HectorVeraO/chatisimo/internal/platform/llm"
	"github.com/HectorVeraO/chatisimo/internal/platform/logging"
	"github.com/HectorVeraO/chatisimo/internal/uccalendar"
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

	ucCalendar, err := uccalendar.MigrationSource()
	if err != nil {
		return nil, fmt.Errorf("migration sources: %w", err)
	}

	sources := []db.Source{platform, exchanges, ucCalendar, jobs.NewMigrationSource()}

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
	uc         *uccalendar.Service
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

// runtimeSettings holds the settings read from storage at startup.
type runtimeSettings struct {
	store   *config.Store
	discord discord.Settings
	llm     llm.Config
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

// openStorage opens the migrated database and reads the Discord and LLM settings from it.
func (s *server) openStorage(ctx context.Context, env config.Env) (runtimeSettings, error) {
	pool, err := openDatabase(ctx, env, s.logger)
	if err != nil {
		return runtimeSettings{}, err
	}

	s.pool = pool

	store, err := config.NewStore(ctx, pool, env.MasterKey, env.Environment, settingKeys())
	if err != nil {
		return runtimeSettings{}, fmt.Errorf("open settings: %w", err)
	}

	err = store.Require(ctx, settingDiscordToken, settingLLMKey)
	if err != nil {
		return runtimeSettings{}, fmt.Errorf("check settings: %w", err)
	}

	token, err := store.Secret(ctx, settingDiscordToken)
	if err != nil {
		return runtimeSettings{}, fmt.Errorf("read settings: %w", err)
	}

	baseURL, err := store.Get(ctx, settingDiscordAPIBaseURL)
	if err != nil {
		return runtimeSettings{}, fmt.Errorf("read settings: %w", err)
	}

	llmKey, err := store.Secret(ctx, settingLLMKey)
	if err != nil {
		return runtimeSettings{}, fmt.Errorf("read settings: %w", err)
	}

	llmModel, err := store.Get(ctx, settingLLMModel)
	if err != nil {
		return runtimeSettings{}, fmt.Errorf("read settings: %w", err)
	}

	llmBaseURL, err := store.Get(ctx, settingLLMBaseURL)
	if err != nil {
		return runtimeSettings{}, fmt.Errorf("read settings: %w", err)
	}

	llmPrompt, err := store.Get(ctx, settingLLMPrompt)
	if err != nil {
		return runtimeSettings{}, fmt.Errorf("read settings: %w", err)
	}

	err = checkUCSettings(ctx, store)
	if err != nil {
		return runtimeSettings{}, err
	}

	out := runtimeSettings{
		store:   store,
		discord: discord.Settings{Token: token, APIBaseURL: baseURL},
		llm: llm.Config{
			APIKey:  llmKey.Reveal(),
			Model:   llmModel,
			BaseURL: llmBaseURL,
			Prompt:  llmPrompt,
			Logger:  s.logger,
		},
	}

	return out, nil
}

// checkUCSettings validates stored platform-wide UC settings at boot.
func checkUCSettings(ctx context.Context, store *config.Store) error {
	for _, name := range []string{
		settingUCAPIBaseURL,
		settingUCFridayAlertTime,
		settingUCMatchdayAlertTime,
		settingUCSlangCacheTTL,
	} {
		_, err := store.Get(ctx, name)
		if err != nil {
			return fmt.Errorf("read settings: %w", err)
		}
	}

	return nil
}

// construct builds the notifier, the scheduler, the LLM client, the module, the runner and the
// adapter. Every constructor receives all it needs: the notifier exists before the module, the
// adapter after it, and the module registers its jobs before the runner is built because River
// fixes its workers at construction.
func (s *server) construct(settings runtimeSettings) error {
	notifier, err := discord.NewNotifier(settings.discord, s.logger)
	if err != nil {
		return fmt.Errorf("create notifier: %w", err)
	}

	s.notifier = notifier

	scheduler, err := jobs.NewScheduler(s.pool, jobs.SchedulerOptions{Logger: s.logger})
	if err != nil {
		return fmt.Errorf("create scheduler: %w", err)
	}

	rephraser, err := llm.New(settings.llm)
	if err != nil {
		return fmt.Errorf("create llm client: %w", err)
	}

	const ucBanterTemperature = 0.7

	banterCfg := settings.llm
	banterCfg.Prompt = uccalendar.DefaultBanterPrompt
	banterCfg.Temperature = ucBanterTemperature

	banterLLM, err := llm.New(banterCfg)
	if err != nil {
		return fmt.Errorf("create uc banter llm client: %w", err)
	}

	uc, err := uccalendar.New(uccalendar.Deps{
		Pool:     s.pool,
		Settings: settings.store,
		Clock:    systemClock{},
		Alerts:   notifier,
		Banter:   banterLLM,
		Logger:   s.logger,
	})
	if err != nil {
		return fmt.Errorf("create uccalendar: %w", err)
	}

	s.uc = uc

	exchanges, err := giftexchange.New(giftexchange.Deps{
		Pool:      s.pool,
		Scheduler: scheduler,
		Clock:     systemClock{},
		Random:    rand.New(giftexchange.CryptoSource{}), //nolint:gosec // the source reads crypto/rand
		Notifier:  notifier,
		Rephraser: rephraser,
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

	adapter, err := discord.New(notifier, exchanges, uc, s.logger)
	if err != nil {
		return fmt.Errorf("create discord adapter: %w", err)
	}

	s.adapter = adapter

	return nil
}

// serveGateway performs the initial UC schedule sync, opens the Gateway and runs the UC
// background worker until ctx ends or the Gateway fails to open. A failure counts as a runtime
// failure; the end of ctx is the clean stop.
func (s *server) serveGateway(ctx context.Context) error {
	if !s.syncInitialUCSchedule(ctx) {
		return nil
	}

	group, groupCtx := errgroup.WithContext(ctx)

	group.Go(func() error {
		return s.openGateway(ctx, groupCtx)
	})
	group.Go(func() error {
		return s.uc.Run(groupCtx)
	})

	err := group.Wait()
	if err != nil {
		return err //nolint:wrapcheck // openGateway adds the context
	}

	return nil
}

// syncInitialUCSchedule runs a best-effort UC schedule sync before opening the Gateway so the
// in-memory schedule is ready as soon as commands arrive. It returns false only when ctx ends
// during the sync; an upstream feed failure logs a warning and returns true so boot continues.
func (s *server) syncInitialUCSchedule(ctx context.Context) bool {
	err := s.uc.Sync(ctx)
	if err == nil {
		return true
	}

	isStopped := ctx.Err() != nil
	if isStopped {
		return false
	}

	s.logger.WarnContext(ctx, "uc initial schedule sync failed")

	return true
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

	if s.uc != nil {
		problems = append(problems, s.uc.Close())
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
