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

func readDownSQL(t *testing.T, files fs.FS, name string) string {
	t.Helper()

	rawSQL, err := fs.ReadFile(files, name)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", name, err)
	}

	_, downSQL, hasDown := strings.Cut(string(rawSQL), "-- +goose Down")
	if !hasDown {
		t.Fatalf("%s is missing -- +goose Down", name)
	}

	return downSQL
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

func TestMigrateCreatesTablesInUCCalendarAndNothingInPublic(t *testing.T) {
	got := listTables(t, "uccalendar")
	want := []string{"goose_db_version", "guild_config", "sent_alert"}

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

	down0002 := readDownSQL(t, files, "0002_guild_config.sql")
	down0001 := readDownSQL(t, files, "0001_init.sql")

	_, err = pool.Exec(
		t.Context(),
		down0002+"; "+down0001+"; DELETE FROM uccalendar.goose_db_version WHERE version_id >= 1;",
	)
	if err != nil {
		t.Fatalf("exec Down migrations: %v", err)
	}

	var count int
	err = pool.QueryRow(
		t.Context(),
		"SELECT count(*) FROM information_schema.tables WHERE table_schema = 'uccalendar' AND table_name IN ('guild_config', 'sent_alert')",
	).Scan(&count)
	if err != nil {
		t.Fatalf("count tables after Down: %v", err)
	}

	if count != 0 {
		t.Errorf("table count after Down = %d, want 0", count)
	}

	err = db.Migrate(t.Context(), pool, source)
	if err != nil {
		t.Fatalf("re-apply Migrate after Down: %v", err)
	}

	err = pool.QueryRow(
		t.Context(),
		"SELECT count(*) FROM information_schema.tables WHERE table_schema = 'uccalendar' AND table_name IN ('guild_config', 'sent_alert')",
	).Scan(&count)
	if err != nil {
		t.Fatalf("count tables after Up: %v", err)
	}

	if count != 2 {
		t.Errorf("table count after Up = %d, want 2", count)
	}
}

func TestMigration0002GuildConfigUpAndDown(t *testing.T) {
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

	// Seed per-guild config and duplicate alert_key rows across two guilds before rolling 0002 Down.
	_, err = pool.Exec(t.Context(), `
		INSERT INTO uccalendar.guild_config (guild_id, friend_user_id, channel_id, updated_at)
		VALUES ('200000000000000001', '100000000000000001', '300000000000000001', '2026-10-30T17:00:00Z');

		INSERT INTO uccalendar.sent_alert (guild_id, alert_key, sent_at) VALUES
		('200000000000000001', 'friday:2026-10-30', '2026-10-30T20:00:00Z'),
		('200000000000000002', 'friday:2026-10-30', '2026-10-30T20:05:00Z');
	`)
	if err != nil {
		t.Fatalf("seed 0002 rows: %v", err)
	}

	files, err := store.Migrations()
	if err != nil {
		t.Fatalf("store.Migrations: %v", err)
	}

	down0002 := readDownSQL(t, files, "0002_guild_config.sql")
	_, err = pool.Exec(t.Context(), down0002+"; DELETE FROM uccalendar.goose_db_version WHERE version_id = 2;")
	if err != nil {
		t.Fatalf("exec 0002 Down: %v", err)
	}

	var sentRows int
	err = pool.QueryRow(t.Context(), "SELECT count(*) FROM uccalendar.sent_alert").Scan(&sentRows)
	if err != nil {
		t.Fatalf("count sent_alert after 0002 Down: %v", err)
	}
	if sentRows != 1 {
		t.Errorf("sent_alert rows after 0002 Down deduplication = %d, want 1", sentRows)
	}

	// Re-apply 0002 Up on top of the legacy pre-0002 sent_alert row.
	err = db.Migrate(t.Context(), pool, source)
	if err != nil {
		t.Fatalf("re-apply 0002 Up on legacy sent_alert row: %v", err)
	}

	var migratedGuildID string
	err = pool.QueryRow(
		t.Context(),
		"SELECT guild_id FROM uccalendar.sent_alert WHERE alert_key = 'friday:2026-10-30'",
	).Scan(&migratedGuildID)
	if err != nil {
		t.Fatalf("read migrated sent_alert row: %v", err)
	}
	if migratedGuildID != "" {
		t.Errorf("migrated legacy sent_alert guild_id = %q, want empty sentinel", migratedGuildID)
	}

	var hasDefault bool
	err = pool.QueryRow(t.Context(), `
		SELECT column_default IS NOT NULL
		FROM information_schema.columns
		WHERE table_schema = 'uccalendar'
		  AND table_name = 'sent_alert'
		  AND column_name = 'guild_id'
	`).Scan(&hasDefault)
	if err != nil {
		t.Fatalf("inspect sent_alert.guild_id default: %v", err)
	}
	if hasDefault {
		t.Error("sent_alert.guild_id still has a column DEFAULT after 0002 Up, want DEFAULT dropped")
	}
}
