package discord_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
	"github.com/HectorVeraO/chatisimo/internal/platform/config"
	"github.com/HectorVeraO/chatisimo/internal/platform/db"
	"github.com/HectorVeraO/chatisimo/internal/platform/jobs"
	"github.com/HectorVeraO/chatisimo/internal/platform/pgtest"
	"github.com/HectorVeraO/chatisimo/internal/uccalendar"
)

const closeDeadline = 30 * time.Second

var shared *pgtest.Database

func TestMain(m *testing.M) {
	code := run(m)
	if code == 0 {
		// The REST clients keep idle connections to servers that are already gone.
		transport, isTransport := http.DefaultTransport.(*http.Transport)
		if isTransport {
			transport.CloseIdleConnections()
		}
		// disgo starts a rate-limit cleanup goroutine per REST client that has no stop.
		leakErr := goleak.Find(goleak.IgnoreAnyFunction("github.com/disgoorg/disgo/rest.(*rateLimiterImpl).cleanup"))
		if leakErr != nil {
			fmt.Fprintln(os.Stderr, leakErr)
			code = 1
		}
	}

	os.Exit(code)
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

	uccalendarSource, err := uccalendar.MigrationSource()
	if err != nil {
		return nil, fmt.Errorf("uccalendar source: %w", err)
	}

	return []db.Source{platformSource, giftexchangeSource, uccalendarSource, jobs.NewMigrationSource()}, nil
}
