package store_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/HectorVeraO/chatisimo/internal/platform/db"
	"github.com/HectorVeraO/chatisimo/internal/platform/pgtest"
	"github.com/HectorVeraO/chatisimo/internal/uccalendar"
	"github.com/HectorVeraO/chatisimo/internal/uccalendar/internal/store"
)

const closeDeadline = 30 * time.Second

var shared *pgtest.Database

func TestMain(m *testing.M) {
	code := run(m)
	if code == 0 {
		if leakErr := goleak.Find(); leakErr != nil {
			fmt.Fprintln(os.Stderr, leakErr)
			code = 1
		}
	}

	os.Exit(code)
}

func run(m *testing.M) int {
	ctx := context.Background()

	ucSource, err := uccalendar.MigrationSource()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	database, err := pgtest.New(ctx, pgtest.Options{Sources: []db.Source{ucSource}})
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

func TestMigrationsEmbedSQL(t *testing.T) {
	files, err := store.Migrations()
	if err != nil {
		t.Fatalf("Migrations: %v", err)
	}

	entry, err := files.Open("0001_init.sql")
	if err != nil {
		t.Fatalf("open 0001_init.sql: %v", err)
	}

	err = entry.Close()
	if err != nil {
		t.Fatalf("close 0001_init.sql: %v", err)
	}
}

func TestClaimAndReleaseAlert(t *testing.T) {
	s, err := store.New(shared.Pool())
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}

	sentAt := time.Date(2026, 10, 30, 20, 0, 0, 0, time.UTC)

	claimed, err := s.ClaimAlert(t.Context(), "friday:2026-10-30", sentAt)
	if err != nil {
		t.Fatalf("first ClaimAlert: %v", err)
	}

	if !claimed {
		t.Fatal("first ClaimAlert = false, want true")
	}

	claimedAgain, err := s.ClaimAlert(t.Context(), "friday:2026-10-30", sentAt.Add(time.Minute))
	if err != nil {
		t.Fatalf("second ClaimAlert: %v", err)
	}

	if claimedAgain {
		t.Fatal("second ClaimAlert = true, want false for duplicate alert key")
	}

	otherClaimed, err := s.ClaimAlert(t.Context(), "matchday:701001:2026-10-31", sentAt)
	if err != nil {
		t.Fatalf("distinct ClaimAlert: %v", err)
	}

	if !otherClaimed {
		t.Fatal("distinct ClaimAlert = false, want true")
	}

	err = s.ReleaseAlert(t.Context(), "friday:2026-10-30")
	if err != nil {
		t.Fatalf("ReleaseAlert: %v", err)
	}

	reclaimed, err := s.ClaimAlert(t.Context(), "friday:2026-10-30", sentAt.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("ClaimAlert after ReleaseAlert: %v", err)
	}

	if !reclaimed {
		t.Fatal("ClaimAlert after ReleaseAlert = false, want true")
	}
}

func TestStoreValidationGuards(t *testing.T) {
	if _, err := store.New(nil); err == nil {
		t.Fatal("store.New(nil) succeeded, want error")
	}

	validStore, err := store.New(shared.Pool())
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}

	nilPoolStore := &store.Store{}
	var nilStore *store.Store
	now := time.Date(2026, 10, 30, 20, 0, 0, 0, time.UTC)

	//nolint:staticcheck // testing nil context guard
	_, err = validStore.ClaimAlert(nil, "key", now)
	if err == nil {
		t.Fatal("ClaimAlert(nil, ...) succeeded, want error")
	}

	_, err = nilStore.ClaimAlert(t.Context(), "key", now)
	if err == nil {
		t.Fatal("nilStore.ClaimAlert succeeded, want error")
	}

	_, err = nilPoolStore.ClaimAlert(t.Context(), "key", now)
	if err == nil {
		t.Fatal("nilPoolStore.ClaimAlert succeeded, want error")
	}

	_, err = validStore.ClaimAlert(t.Context(), "", now)
	if err == nil {
		t.Fatal("ClaimAlert with empty key succeeded, want error")
	}

	//nolint:staticcheck // testing aggregated validation errors
	_, joinedClaimErr := nilStore.ClaimAlert(nil, "", now)
	if joinedClaimErr == nil {
		t.Fatal("nilStore.ClaimAlert(nil, empty) succeeded, want joined error")
	}

	//nolint:staticcheck // testing nil context guard
	err = validStore.ReleaseAlert(nil, "key")
	if err == nil {
		t.Fatal("ReleaseAlert(nil, ...) succeeded, want error")
	}

	err = nilStore.ReleaseAlert(t.Context(), "key")
	if err == nil {
		t.Fatal("nilStore.ReleaseAlert succeeded, want error")
	}

	err = nilPoolStore.ReleaseAlert(t.Context(), "key")
	if err == nil {
		t.Fatal("nilPoolStore.ReleaseAlert succeeded, want error")
	}

	err = validStore.ReleaseAlert(t.Context(), "")
	if err == nil {
		t.Fatal("ReleaseAlert with empty key succeeded, want error")
	}

	//nolint:staticcheck // testing aggregated validation errors
	joinedReleaseErr := nilStore.ReleaseAlert(nil, "")
	if joinedReleaseErr == nil {
		t.Fatal("nilStore.ReleaseAlert(nil, empty) succeeded, want joined error")
	}

	canceledCtx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err = validStore.ClaimAlert(canceledCtx, "canceled-key", now)
	if err == nil {
		t.Fatal("ClaimAlert with canceled context succeeded, want error")
	}

	err = validStore.ReleaseAlert(canceledCtx, "canceled-key")
	if err == nil {
		t.Fatal("ReleaseAlert with canceled context succeeded, want error")
	}
}
