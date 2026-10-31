// Package store holds the schema, goose migrations, and alert deduplication queries of the
// uccalendar module.
package store

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Migrations returns the goose migration files of schema uccalendar, rooted at the directory
// that holds them.
func Migrations() (fs.FS, error) {
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("uccalendar migrations: %w", err)
	}

	return sub, nil
}

// Store persists proactive alert deduplication claims in schema uccalendar.
type Store struct {
	pool *pgxpool.Pool
}

// New constructs a Store backed by pool.
func New(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("uccalendar store: nil pool")
	}

	st := &Store{pool: pool}

	return st, nil
}

// ClaimAlert atomically records alertKey as sent at sentAt. It returns true when this call won
// the claim, and false when alertKey was already present.
func (s *Store) ClaimAlert(ctx context.Context, alertKey string, sentAt time.Time) (bool, error) {
	var problems []error
	if ctx == nil {
		problems = append(problems, errors.New("uccalendar store: nil context"))
	}

	if s == nil || s.pool == nil {
		problems = append(problems, errors.New("uccalendar store: nil pool"))
	}

	if alertKey == "" {
		problems = append(problems, errors.New("uccalendar store: empty alert key"))
	}

	if len(problems) > 0 {
		return false, errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
	}

	tag, err := s.pool.Exec(
		ctx,
		`INSERT INTO uccalendar.sent_alert (alert_key, sent_at)
		 VALUES ($1, $2)
		 ON CONFLICT (alert_key) DO NOTHING`,
		alertKey,
		sentAt.UTC(),
	)
	if err != nil {
		return false, fmt.Errorf("claim alert %s: %w", alertKey, err)
	}

	claimed := tag.RowsAffected() == 1

	return claimed, nil
}

// ReleaseAlert removes alertKey so a failed notification delivery can be retried on a subsequent
// check.
func (s *Store) ReleaseAlert(ctx context.Context, alertKey string) error {
	var problems []error
	if ctx == nil {
		problems = append(problems, errors.New("uccalendar store: nil context"))
	}

	if s == nil || s.pool == nil {
		problems = append(problems, errors.New("uccalendar store: nil pool"))
	}

	if alertKey == "" {
		problems = append(problems, errors.New("uccalendar store: empty alert key"))
	}

	if len(problems) > 0 {
		return errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
	}

	_, err := s.pool.Exec(
		ctx,
		`DELETE FROM uccalendar.sent_alert WHERE alert_key = $1`,
		alertKey,
	)
	if err != nil {
		return fmt.Errorf("release alert %s: %w", alertKey, err)
	}

	return nil
}
