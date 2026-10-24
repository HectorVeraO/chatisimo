// Package db opens the Postgres pool and applies every schema's migrations under one lock.
package db

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	openDeadline      = 30 * time.Second
	attemptTimeout    = 5 * time.Second
	initialRetryDelay = 100 * time.Millisecond
	maxRetryDelay     = 2 * time.Second
)

// ErrInvalidURL is returned by Open when the connection string cannot be parsed. Its text never
// carries any part of the string.
var ErrInvalidURL = errors.New("invalid DATABASE_URL")

// Open builds a pool from url (pool options ride in the URL) and pings it, retrying with bounded
// backoff for up to 30 s so the binary survives Postgres starting after it. Errors never include
// url (pgx's own parse error text is replaced by "invalid DATABASE_URL").
func Open(ctx context.Context, url string, logger *slog.Logger) (*pgxpool.Pool, error) {
	var problems []error

	if ctx == nil {
		problems = append(problems, errors.New("nil context"))
	}

	if logger == nil {
		problems = append(problems, errors.New("nil logger"))
	}

	if len(problems) > 0 {
		return nil, fmt.Errorf("open pool: %w", errors.Join(problems...))
	}

	// pgx parse errors can echo the connection string, so the original is dropped.
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, ErrInvalidURL
	}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("connect to Postgres: %w", err)
	}

	pingErr := pingWithBackoff(ctx, pool, logger)
	if pingErr != nil {
		pool.Close()

		return nil, pingErr
	}

	return pool, nil
}

// pingWithBackoff pings until one attempt succeeds or openDeadline passes. It logs only the attempt
// number and the next delay: a failed connect's error text can carry the host and user.
func pingWithBackoff(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	deadlineCtx, cancel := context.WithTimeout(ctx, openDeadline)
	defer cancel()

	delay := initialRetryDelay

	for attempt := 1; ; attempt++ {
		attemptCtx, cancelAttempt := context.WithTimeout(deadlineCtx, attemptTimeout)
		err := pool.Ping(attemptCtx)

		cancelAttempt()

		if err == nil {
			return nil
		}

		if deadlineCtx.Err() != nil {
			return fmt.Errorf("connect to Postgres: %w", err)
		}

		logger.WarnContext(ctx, "Postgres not ready, retrying", "attempt", attempt, "next_delay", delay)

		timer := time.NewTimer(delay)
		select {
		case <-deadlineCtx.Done():
			timer.Stop()

			return fmt.Errorf("connect to Postgres: %w", err)
		case <-timer.C:
		}

		delay = min(delay*2, maxRetryDelay)
	}
}
