package jobs

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

const migrationLine = "main"

// MigrationSource is River's db.Source: Schema "river", rivermigrate up on line "main".
type MigrationSource struct{}

// NewMigrationSource returns River's source. Fingerprint hashes AllVersions() (version and SQLUp).
func NewMigrationSource() *MigrationSource {
	return &MigrationSource{}
}

// Schema returns "river".
func (s *MigrationSource) Schema() string {
	return Schema
}

// Fingerprint hashes the version and up SQL of every River migration, so a River upgrade that
// adds a migration changes it.
func (s *MigrationSource) Fingerprint() ([]byte, error) {
	// A pool-less driver is enough to read the migrations River embeds.
	migrator, err := newMigrator(riverpgxv5.New(nil))
	if err != nil {
		return nil, fmt.Errorf("fingerprint river migrations: %w", err)
	}

	hash := sha256.New()

	for _, migration := range migrator.AllVersions() {
		var header [16]byte

		binary.BigEndian.PutUint64(header[:8], uint64(migration.Version))    //nolint:gosec // versions are positive
		binary.BigEndian.PutUint64(header[8:], uint64(len(migration.SQLUp))) //nolint:gosec // a length is never negative

		// hash.Hash writes never return an error.
		_, _ = hash.Write(header[:])
		_, _ = hash.Write([]byte(migration.SQLUp))
	}

	return hash.Sum(nil), nil
}

// Up applies every pending River migration, each in its own transaction. db.Migrate has already
// created the schema and holds its advisory lock.
func (s *MigrationSource) Up(ctx context.Context, pool *pgxpool.Pool) error {
	var problems []error

	if ctx == nil {
		problems = append(problems, errors.New("nil context"))
	}

	if pool == nil {
		problems = append(problems, errors.New("nil pool"))
	}

	if len(problems) > 0 {
		return fmt.Errorf("migrate river: %w", errors.Join(problems...))
	}

	migrator, err := newMigrator(riverpgxv5.New(pool))
	if err != nil {
		return fmt.Errorf("migrate river: %w", err)
	}

	_, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	if err != nil {
		return fmt.Errorf("migrate river: %w", err)
	}

	return nil
}

// newMigrator builds a migrator for schema "river" and line "main" that logs nothing, so the
// output of the CLI's migrate command stays its own.
func newMigrator(driver *riverpgxv5.Driver) (*rivermigrate.Migrator[pgx.Tx], error) {
	config := &rivermigrate.Config{
		Line:   migrationLine,
		Logger: slog.New(slog.DiscardHandler),
		Schema: Schema,
	}

	migrator, err := rivermigrate.New(driver, config)
	if err != nil {
		return nil, fmt.Errorf("new migrator: %w", err)
	}

	return migrator, nil
}
