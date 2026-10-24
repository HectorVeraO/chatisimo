package giftexchange_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
	"github.com/HectorVeraO/chatisimo/internal/platform/config"
	"github.com/HectorVeraO/chatisimo/internal/platform/db"
	"github.com/HectorVeraO/chatisimo/internal/platform/jobs"
	"github.com/HectorVeraO/chatisimo/internal/platform/pgtest"
)

const closeDeadline = 30 * time.Second

var shared *pgtest.Database

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	ctx := context.Background()

	sources, err := migrationSources()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	database, err := pgtest.New(ctx, pgtest.Options{Sources: sources})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	shared = database
	code := m.Run()

	closeCtx, cancel := context.WithTimeout(context.Background(), closeDeadline)
	defer cancel()

	err = database.Close(closeCtx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	return code
}

func migrationSources() ([]db.Source, error) {
	platformSource, err := config.MigrationSource()
	if err != nil {
		return nil, fmt.Errorf("platform source: %w", err)
	}

	giftexchangeSource, err := giftexchange.MigrationSource()
	if err != nil {
		return nil, fmt.Errorf("giftexchange source: %w", err)
	}

	return []db.Source{platformSource, giftexchangeSource, jobs.NewMigrationSource()}, nil
}
