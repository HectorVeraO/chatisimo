package db

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// GooseSource is a Source over embedded goose SQL migrations.
type GooseSource struct {
	schema string
	fsys   fs.FS
}

// NewGooseSource wraps fsys (already fs.Sub'd to the migrations directory). Up runs a goose
// Provider over stdlib.OpenDBFromPool(pool) with WithTableName("<schema>.goose_db_version") and
// WithDisableGlobalRegistry(true), then closes the *sql.DB. Fingerprint hashes every file name and
// content in lexical order.
func NewGooseSource(schema string, fsys fs.FS) (*GooseSource, error) {
	var problems []error

	if fsys == nil {
		problems = append(problems, errors.New("nil migrations filesystem"))
	}

	err := validateSchema(schema)
	if err != nil {
		problems = append(problems, err)
	}

	if len(problems) > 0 {
		return nil, fmt.Errorf("goose source: %w", errors.Join(problems...))
	}

	return &GooseSource{schema: schema, fsys: fsys}, nil
}

// Schema returns the schema the migrations build.
func (s *GooseSource) Schema() string {
	return s.schema
}

// Fingerprint hashes every file name and content, in lexical order, with length prefixes so no two
// different trees share the same byte stream.
func (s *GooseSource) Fingerprint() ([]byte, error) {
	hash := sha256.New()

	writeChunk := func(data []byte) {
		var size [8]byte

		binary.BigEndian.PutUint64(size[:], uint64(len(data)))
		// hash.Hash writes never return an error.
		_, _ = hash.Write(size[:])
		_, _ = hash.Write(data)
	}

	walkErr := fs.WalkDir(s.fsys, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if entry.IsDir() {
			return nil
		}

		content, readErr := fs.ReadFile(s.fsys, name)
		if readErr != nil {
			return fmt.Errorf("read %s: %w", name, readErr)
		}

		writeChunk([]byte(name))
		writeChunk(content)

		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("fingerprint schema %s: %w", s.schema, walkErr)
	}

	return hash.Sum(nil), nil
}

// Up applies every pending migration, recording them in <schema>.goose_db_version.
func (s *GooseSource) Up(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return errors.New("goose up: nil pool")
	}

	sqlDB := stdlib.OpenDBFromPool(pool)

	provider, err := goose.NewProvider(
		goose.DialectPostgres,
		sqlDB,
		s.fsys,
		goose.WithTableName(s.schema+".goose_db_version"),
		goose.WithDisableGlobalRegistry(true),
	)
	if err != nil {
		closeErr := sqlDB.Close()

		// A schema whose migrations directory is still empty has nothing to apply.
		if errors.Is(err, goose.ErrNoMigrations) {
			if closeErr != nil {
				return fmt.Errorf("close database handle: %w", closeErr)
			}

			return nil
		}

		joined := errors.Join(err, closeErr)

		return fmt.Errorf("goose provider for schema %s: %w", s.schema, joined)
	}

	_, upErr := provider.Up(ctx)
	if upErr != nil {
		upErr = fmt.Errorf("apply migrations of schema %s: %w", s.schema, upErr)
	}

	closeErr := sqlDB.Close()
	if closeErr != nil {
		closeErr = fmt.Errorf("close database handle: %w", closeErr)
	}

	joined := errors.Join(upErr, closeErr)
	if joined != nil {
		return fmt.Errorf("goose up: %w", joined)
	}

	return nil
}
