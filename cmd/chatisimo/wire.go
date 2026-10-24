package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

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
