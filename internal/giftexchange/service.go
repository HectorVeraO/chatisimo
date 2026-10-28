package giftexchange

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange/internal/store/db"
	"github.com/HectorVeraO/chatisimo/internal/platform/jobs"
)

// Deps are the module's collaborators. Every field is required.
type Deps struct {
	Pool      *pgxpool.Pool
	Scheduler *jobs.Scheduler
	Clock     Clock
	Random    Random
	Notifier  Notifier
	Rephraser Rephraser
	Logger    *slog.Logger
}

// Service is the module's public API. It is safe for concurrent use.
type Service struct {
	pool      *pgxpool.Pool
	scheduler *jobs.Scheduler
	clock     Clock
	random    Random
	notifier  Notifier
	rephraser Rephraser
	logger    *slog.Logger
}

// New validates deps, reporting every missing field at once (errors.Join of InputError values
// wrapping ErrInvalidInput), and returns the service. It does no I/O.
func New(deps Deps) (*Service, error) {
	var problems []error
	if deps.Pool == nil {
		problems = append(problems, missingDep("deps.Pool"))
	}
	if deps.Scheduler == nil {
		problems = append(problems, missingDep("deps.Scheduler"))
	}
	if deps.Clock == nil {
		problems = append(problems, missingDep("deps.Clock"))
	}
	if deps.Random == nil {
		problems = append(problems, missingDep("deps.Random"))
	}
	if deps.Notifier == nil {
		problems = append(problems, missingDep("deps.Notifier"))
	}
	if deps.Rephraser == nil {
		problems = append(problems, missingDep("deps.Rephraser"))
	}
	if deps.Logger == nil {
		problems = append(problems, missingDep("deps.Logger"))
	}
	if len(problems) > 0 {
		return nil, errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
	}

	service := &Service{
		pool:      deps.Pool,
		scheduler: deps.Scheduler,
		clock:     deps.Clock,
		random:    deps.Random,
		notifier:  deps.Notifier,
		rephraser: deps.Rephraser,
		logger:    deps.Logger,
	}

	return service, nil
}

func missingDep(field string) error {
	return &InputError{Field: field, Code: InputCodeRequired}
}

// now reads the clock at the precision Postgres stores.
func (s *Service) now() time.Time {
	instant := s.clock.Now().UTC().Truncate(time.Microsecond)
	return instant
}

func timestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// inTx runs fn in one transaction and commits it when fn succeeds. Errors from fn are returned
// as they are, so callers can match sentinels; a failed rollback never hides them.
func (s *Service) inTx(ctx context.Context, fn func(q *db.Queries, tx pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	fnErr := fn(db.New(tx), tx)
	if fnErr != nil {
		// A failed rollback leaves the connection to be discarded by the pool; fnErr is the
		// error the caller needs.
		_ = tx.Rollback(ctx)
		return fnErr
	}

	commitErr := tx.Commit(ctx)
	if commitErr != nil {
		return fmt.Errorf("commit transaction: %w", commitErr)
	}

	return nil
}

func requireActor(a Actor) error {
	if a.person.IsZero() {
		return ErrUnauthenticated
	}
	return nil
}

func requireID(field string, isZero bool) error {
	if isZero {
		return &InputError{Field: field, Code: InputCodeRequired}
	}
	return nil
}
