package db

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MigrateLockKey is the advisory lock key Migrate holds.
const MigrateLockKey int64 = 0x4348415449534d4f

var schemaPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// Source is one schema and the migrations that build it.
type Source interface {
	// Schema returns the schema name, matching ^[a-z][a-z0-9_]{0,62}$.
	Schema() string
	// Fingerprint returns bytes that change whenever the migrations change (pgtest templates).
	Fingerprint() ([]byte, error)
	// Up applies every pending migration. The schema already exists when it is called.
	Up(ctx context.Context, pool *pgxpool.Pool) error
}

// Migrate acquires one connection, takes pg_advisory_lock(MigrateLockKey) on it, creates each
// source's schema (CREATE SCHEMA IF NOT EXISTS with a validated, quoted identifier), runs each
// source's Up in the given order, and releases the lock. It is idempotent and safe to run from
// several processes at once. Duplicate schemas are an error.
//
// The lock connection is held while Up uses others, so the pool needs MaxConns of at least 2.
func Migrate(ctx context.Context, pool *pgxpool.Pool, sources ...Source) error {
	validationErr := validateArguments(ctx, pool, sources)
	if validationErr != nil {
		return validationErr
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("migrate: acquire connection: %w", err)
	}

	_, err = conn.Exec(ctx, "SELECT pg_advisory_lock($1)", MigrateLockKey)
	if err != nil {
		conn.Release()

		return fmt.Errorf("migrate: take advisory lock: %w", err)
	}

	runErr := runSources(ctx, pool, conn, sources)
	unlockErr := unlock(ctx, conn)

	joined := errors.Join(runErr, unlockErr)
	if joined != nil {
		return fmt.Errorf("migrate: %w", joined)
	}

	return nil
}

// validateArguments reports every problem with the arguments of Migrate together.
func validateArguments(ctx context.Context, pool *pgxpool.Pool, sources []Source) error {
	var problems []error

	if ctx == nil {
		problems = append(problems, errors.New("migrate: nil context"))
	}

	if pool == nil {
		problems = append(problems, errors.New("migrate: nil pool"))
	} else if pool.Config().MaxConns < 2 {
		problems = append(problems, errors.New("migrate: pool needs MaxConns >= 2 (one connection holds the lock while migrations use another)"))
	}

	sourcesErr := validateSources(sources)
	if sourcesErr != nil {
		problems = append(problems, sourcesErr)
	}

	joined := errors.Join(problems...)
	if joined != nil {
		return fmt.Errorf("migrate: invalid arguments: %w", joined)
	}

	return nil
}

// unlock releases the advisory lock and the connection. A connection whose lock could not be
// released is closed instead of returned to the pool, which ends the session and the lock with it.
func unlock(ctx context.Context, conn *pgxpool.Conn) error {
	// The caller's context may be the reason the run failed; the lock must still be released.
	cleanupCtx := context.WithoutCancel(ctx)

	var released bool

	err := conn.QueryRow(cleanupCtx, "SELECT pg_advisory_unlock($1)", MigrateLockKey).Scan(&released)
	if err == nil && released {
		conn.Release()

		return nil
	}

	closeErr := conn.Hijack().Close(cleanupCtx)

	if err == nil {
		err = errors.New("lock was not held")
	}

	return fmt.Errorf("migrate: release advisory lock: %w", errors.Join(err, closeErr))
}

func runSources(ctx context.Context, pool *pgxpool.Pool, conn *pgxpool.Conn, sources []Source) error {
	for _, source := range sources {
		schema := source.Schema()

		_, err := conn.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+pgx.Identifier{schema}.Sanitize())
		if err != nil {
			return fmt.Errorf("migrate: create schema %s: %w", schema, err)
		}

		err = source.Up(ctx, pool)
		if err != nil {
			return fmt.Errorf("migrate: schema %s: %w", schema, err)
		}
	}

	return nil
}

// validateSources reports every invalid or duplicate schema together, before anything is created.
func validateSources(sources []Source) error {
	var problems []error

	seen := make(map[string]bool, len(sources))

	for index, source := range sources {
		if source == nil {
			problems = append(problems, fmt.Errorf("migrate: source %d is nil", index))

			continue
		}

		schema := source.Schema()

		err := validateSchema(schema)
		if err != nil {
			problems = append(problems, err)
		}

		if seen[schema] {
			problems = append(problems, fmt.Errorf("migrate: duplicate schema %q", schema))
		}

		seen[schema] = true
	}

	if len(problems) == 0 {
		return nil
	}

	return fmt.Errorf("migrate: invalid sources: %w", errors.Join(problems...))
}

func validateSchema(schema string) error {
	isValid := schemaPattern.MatchString(schema)
	if !isValid {
		return fmt.Errorf("invalid schema name %q: must match %s", schema, schemaPattern)
	}

	return nil
}
