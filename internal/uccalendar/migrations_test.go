package uccalendar_test

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/HectorVeraO/chatisimo/internal/platform/db"
	"github.com/HectorVeraO/chatisimo/internal/platform/pgtest"
	"github.com/HectorVeraO/chatisimo/internal/uccalendar"
	"github.com/HectorVeraO/chatisimo/internal/uccalendar/internal/store"
)

func listTables(t *testing.T, schema string) []string {
	t.Helper()

	rows, err := shared.Pool().Query(
		t.Context(),
		"SELECT table_name FROM information_schema.tables WHERE table_schema = $1 ORDER BY table_name",
		schema,
	)
	if err != nil {
		t.Fatalf("list tables in %s: %v", schema, err)
	}
	defer rows.Close()

	var names []string

	for rows.Next() {
		var name string

		scanErr := rows.Scan(&name)
		if scanErr != nil {
			t.Fatalf("scan table name: %v", scanErr)
		}

		names = append(names, name)
	}

	if rows.Err() != nil {
		t.Fatalf("iterate tables in %s: %v", schema, rows.Err())
	}

	return names
}

func TestMigrationSourceNamesSchemaUCCalendar(t *testing.T) {
	source, err := uccalendar.MigrationSource()
	if err != nil {
		t.Fatalf("MigrationSource: %v", err)
	}

	if source.Schema() != "uccalendar" {
		t.Errorf("Schema() = %q, want uccalendar", source.Schema())
	}
}

func TestMigrationSourceFingerprintTracksContent(t *testing.T) {
	source, err := uccalendar.MigrationSource()
	if err != nil {
		t.Fatalf("MigrationSource: %v", err)
	}

	got, err := source.Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}

	files, err := store.Migrations()
	if err != nil {
		t.Fatalf("store.Migrations: %v", err)
	}

	copied := fstest.MapFS{}
	modified := fstest.MapFS{}

	walkErr := fs.WalkDir(files, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}

		raw, readErr := fs.ReadFile(files, path)
		if readErr != nil {
			return fmt.Errorf("read %s: %w", path, readErr)
		}

		copied[path] = &fstest.MapFile{Data: slices.Clone(raw)}
		modified[path] = &fstest.MapFile{Data: append(slices.Clone(raw), []byte("\n-- changed")...)}

		return nil
	})
	if walkErr != nil {
		t.Fatalf("WalkDir: %v", walkErr)
	}

	sameSrc, err := db.NewGooseSource("uccalendar", copied)
	if err != nil {
		t.Fatalf("NewGooseSource same: %v", err)
	}

	sameFP, err := sameSrc.Fingerprint()
	if err != nil {
		t.Fatalf("sameSrc.Fingerprint: %v", err)
	}

	modSrc, err := db.NewGooseSource("uccalendar", modified)
	if err != nil {
		t.Fatalf("NewGooseSource mod: %v", err)
	}

	modFP, err := modSrc.Fingerprint()
	if err != nil {
		t.Fatalf("modSrc.Fingerprint: %v", err)
	}

	if len(got) == 0 || !bytes.Equal(got, sameFP) {
		t.Errorf("Fingerprint() = %x, want %x", got, sameFP)
	}

	if bytes.Equal(got, modFP) {
		t.Error("Fingerprint() did not change after SQL content changed")
	}
}

func TestMigrateCreatesSentAlertInUCCalendarAndNothingInPublic(t *testing.T) {
	got := listTables(t, "uccalendar")
	want := []string{"goose_db_version", "sent_alert"}

	if !slices.Equal(got, want) {
		t.Errorf("tables in uccalendar = %v, want %v", got, want)
	}

	pub := listTables(t, "public")
	if len(pub) != 0 {
		t.Errorf("tables in public = %v, want empty", pub)
	}
}

func TestMigration0001InitUpAndDown(t *testing.T) {
	source, err := uccalendar.MigrationSource()
	if err != nil {
		t.Fatalf("MigrationSource: %v", err)
	}

	isolated, err := pgtest.New(t.Context(), pgtest.Options{Sources: []db.Source{source}})
	if err != nil {
		t.Fatalf("pgtest.New: %v", err)
	}
	t.Cleanup(func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), closeDeadline)
		defer cancel()

		closeErr := isolated.Close(closeCtx)
		if closeErr != nil {
			t.Errorf("isolated.Close: %v", closeErr)
		}
	})

	pool := isolated.Pool()

	files, err := store.Migrations()
	if err != nil {
		t.Fatalf("store.Migrations: %v", err)
	}

	rawSQL, err := fs.ReadFile(files, "0001_init.sql")
	if err != nil {
		t.Fatalf("ReadFile 0001_init.sql: %v", err)
	}

	_, downSQL, hasDown := strings.Cut(string(rawSQL), "-- +goose Down")
	if !hasDown {
		t.Fatal("0001_init.sql is missing -- +goose Down")
	}

	_, err = pool.Exec(t.Context(), downSQL+"; DELETE FROM uccalendar.goose_db_version WHERE version_id = 1;")
	if err != nil {
		t.Fatalf("exec Down migration: %v", err)
	}

	var count int
	err = pool.QueryRow(
		t.Context(),
		"SELECT count(*) FROM information_schema.tables WHERE table_schema = 'uccalendar' AND table_name = 'sent_alert'",
	).Scan(&count)
	if err != nil {
		t.Fatalf("count sent_alert after Down: %v", err)
	}

	if count != 0 {
		t.Errorf("sent_alert count after Down = %d, want 0", count)
	}

	err = db.Migrate(t.Context(), pool, source)
	if err != nil {
		t.Fatalf("re-apply Migrate after Down: %v", err)
	}

	err = pool.QueryRow(
		t.Context(),
		"SELECT count(*) FROM information_schema.tables WHERE table_schema = 'uccalendar' AND table_name = 'sent_alert'",
	).Scan(&count)
	if err != nil {
		t.Fatalf("count sent_alert after Up: %v", err)
	}

	if count != 1 {
		t.Errorf("sent_alert count after Up = %d, want 1", count)
	}
}
