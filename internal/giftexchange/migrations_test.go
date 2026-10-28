package giftexchange_test

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
	"github.com/HectorVeraO/chatisimo/internal/giftexchange/internal/store"
	"github.com/HectorVeraO/chatisimo/internal/platform/db"
	"github.com/HectorVeraO/chatisimo/internal/platform/pgtest"
)

var wantTables = []string{
	"assignments", "exchanges", "exclusions", "external_identities", "groups",
	"notifications", "participants", "persons", "questions",
}

func tablesIn(t *testing.T, schema string) []string {
	t.Helper()

	rows, err := shared.Pool().Query(t.Context(),
		"SELECT table_name FROM information_schema.tables WHERE table_schema = $1 ORDER BY table_name", schema)
	if err != nil {
		t.Fatalf("list tables of %s: %v", schema, err)
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
		t.Fatalf("read tables of %s: %v", schema, rows.Err())
	}

	return names
}

func TestMigrationSourceNamesSchemaGiftexchange(t *testing.T) {
	source, err := giftexchange.MigrationSource()
	if err != nil {
		t.Fatalf("MigrationSource: %v", err)
	}

	if source.Schema() != "giftexchange" {
		t.Errorf("Schema() = %q, want giftexchange", source.Schema())
	}
}

func copyOfMigrations(t *testing.T, extra string) fstest.MapFS {
	t.Helper()

	files, err := store.Migrations()
	if err != nil {
		t.Fatalf("store.Migrations: %v", err)
	}

	copied := fstest.MapFS{}

	walkErr := fs.WalkDir(files, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}

		content, readErr := fs.ReadFile(files, path)
		if readErr != nil {
			return fmt.Errorf("read %s: %w", path, readErr)
		}

		copied[path] = &fstest.MapFile{Data: append(content, []byte(extra)...)}

		return nil
	})
	if walkErr != nil {
		t.Fatalf("copy migrations: %v", walkErr)
	}

	return copied
}

func fingerprintOf(t *testing.T, files fs.FS) []byte {
	t.Helper()

	source, err := db.NewGooseSource("giftexchange", files)
	if err != nil {
		t.Fatalf("NewGooseSource: %v", err)
	}

	fingerprint, err := source.Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}

	return fingerprint
}

func TestMigrationSourceFingerprintFollowsTheMigrationContent(t *testing.T) {
	source, err := giftexchange.MigrationSource()
	if err != nil {
		t.Fatalf("MigrationSource: %v", err)
	}

	got, err := source.Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}

	same := fingerprintOf(t, copyOfMigrations(t, ""))
	changed := fingerprintOf(t, copyOfMigrations(t, "-- changed"))

	if len(got) == 0 || !bytes.Equal(got, same) {
		t.Errorf("fingerprint %x, want the non-empty fingerprint %x of the same content", got, same)
	}

	if bytes.Equal(got, changed) {
		t.Error("fingerprint is unchanged after the migration content changed, want a different one")
	}
}

func TestMigrateCreatesTheNineTablesInSchemaGiftexchange(t *testing.T) {
	got := tablesIn(t, "giftexchange")

	// goose keeps its version table in the same schema.
	want := append(slices.Clone(wantTables), "goose_db_version")
	slices.Sort(want)

	if !slices.Equal(got, want) {
		t.Errorf("tables in giftexchange = %v, want %v", got, want)
	}
}

func TestMigrateCreatesNothingInPublic(t *testing.T) {
	got := tablesIn(t, "public")

	if len(got) != 0 {
		t.Errorf("tables in public = %v, want none", got)
	}
}

func TestMigrateTwiceChangesNothing(t *testing.T) {
	source, err := giftexchange.MigrationSource()
	if err != nil {
		t.Fatalf("MigrationSource: %v", err)
	}

	tablesBefore := tablesIn(t, "giftexchange")

	err = db.Migrate(t.Context(), shared.Pool(), source)
	if err != nil {
		t.Fatalf("second Migrate: %v", err)
	}

	if tablesAfter := tablesIn(t, "giftexchange"); !slices.Equal(tablesAfter, tablesBefore) {
		t.Errorf("tables = %v after a second Migrate, want %v", tablesAfter, tablesBefore)
	}
}

func TestMigration0002PreferencesUpAndDown(t *testing.T) {
	source, err := giftexchange.MigrationSource()
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
	_, seedErr := pool.Exec(t.Context(), `
		INSERT INTO giftexchange.persons (id, created_at)
		VALUES ('018f0000-0000-7000-8000-000000000001', now());
		INSERT INTO giftexchange.groups (id, provider, subject, created_at)
		VALUES ('018f0000-0000-7000-8000-000000000002', 'discord', '200000000000000001', now());
		INSERT INTO giftexchange.exchanges (
			id, group_id, organizer_id, name, state, round, reminder_lead_seconds,
			dropout_policy, avoid_last_round, questions_enabled, created_at
		) VALUES (
			'018f0000-0000-7000-8000-000000000003',
			'018f0000-0000-7000-8000-000000000002',
			'018f0000-0000-7000-8000-000000000001',
			'Winter', 'open', 0, 0, 'patch', false, true, now()
		);
		INSERT INTO giftexchange.participants (exchange_id, person_id, joined_at)
		VALUES ('018f0000-0000-7000-8000-000000000003', '018f0000-0000-7000-8000-000000000001', now());
	`)
	if seedErr != nil {
		t.Fatalf("seed rows: %v", seedErr)
	}

	var likes, dislikes, details string
	scanErr := pool.QueryRow(t.Context(), `
		SELECT likes, dislikes, details
		FROM giftexchange.participants
		WHERE exchange_id = '018f0000-0000-7000-8000-000000000003'
	`).Scan(&likes, &dislikes, &details)
	if scanErr != nil {
		t.Fatalf("read default preferences: %v", scanErr)
	}
	if likes != "" || dislikes != "" || details != "" {
		t.Errorf("default preferences = (%q, %q, %q), want all empty", likes, dislikes, details)
	}

	tooLong := strings.Repeat("a", 301)
	_, checkErr := pool.Exec(t.Context(), `
		UPDATE giftexchange.participants SET likes = $1
		WHERE exchange_id = '018f0000-0000-7000-8000-000000000003'
	`, tooLong)
	if checkErr == nil {
		t.Error("expected CHECK constraint to reject 301-character likes")
	}

	_, noticeErr := pool.Exec(t.Context(), `
		INSERT INTO giftexchange.notifications (id, kind, exchange_id, person_id, round, created_at)
		VALUES (
			'018f0000-0000-7000-8000-000000000004', 'preferences_updated',
			'018f0000-0000-7000-8000-000000000003', '018f0000-0000-7000-8000-000000000001', 1, now()
		)
	`)
	if noticeErr != nil {
		t.Fatalf("insert preferences_updated notification: %v", noticeErr)
	}

	files, err := store.Migrations()
	if err != nil {
		t.Fatalf("store.Migrations: %v", err)
	}
	downSQL0004 := readMigrationDown(t, files, "0004_drop_exchange_language.sql")
	downSQL0003 := readMigrationDown(t, files, "0003_exchange_language.sql")
	downSQL := readMigrationDown(t, files, "0002_preferences.sql")

	_, downErr := pool.Exec(t.Context(), downSQL0004+"; "+downSQL0003+"; "+downSQL+"; DELETE FROM giftexchange.goose_db_version WHERE version_id >= 2;")
	if downErr != nil {
		t.Fatalf("exec goose Down: %v", downErr)
	}

	var colCount int
	colErr := pool.QueryRow(t.Context(), `
		SELECT count(*)
		FROM information_schema.columns
		WHERE table_schema = 'giftexchange' AND table_name = 'participants'
		  AND column_name IN ('likes', 'dislikes', 'details')
	`).Scan(&colCount)
	if colErr != nil {
		t.Fatalf("count columns after Down: %v", colErr)
	}
	if colCount != 0 {
		t.Errorf("preference columns after Down = %d, want 0", colCount)
	}

	upErr := db.Migrate(t.Context(), pool, source)
	if upErr != nil {
		t.Fatalf("Migrate after Down: %v", upErr)
	}

	colErr = pool.QueryRow(t.Context(), `
		SELECT count(*)
		FROM information_schema.columns
		WHERE table_schema = 'giftexchange' AND table_name = 'participants'
		  AND column_name IN ('likes', 'dislikes', 'details')
	`).Scan(&colCount)
	if colErr != nil {
		t.Fatalf("count columns after Up: %v", colErr)
	}
	if colCount != 3 {
		t.Errorf("preference columns after Up = %d, want 3", colCount)
	}
}

func readMigrationDown(t *testing.T, files fs.FS, name string) string {
	t.Helper()

	rawMigration, err := fs.ReadFile(files, name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}

	_, downSQL, hasDown := strings.Cut(string(rawMigration), "-- +goose Down")
	if !hasDown {
		t.Fatalf("%s is missing -- +goose Down", name)
	}

	return downSQL
}

func TestMigration0003ExchangeLanguageUpAndDown(t *testing.T) {
	source, err := giftexchange.MigrationSource()
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

	files, err := store.Migrations()
	if err != nil {
		t.Fatalf("store.Migrations: %v", err)
	}
	downSQL0004 := readMigrationDown(t, files, "0004_drop_exchange_language.sql")
	downSQL0003 := readMigrationDown(t, files, "0003_exchange_language.sql")

	pool := isolated.Pool()
	_, revert0004Err := pool.Exec(t.Context(), downSQL0004+"; DELETE FROM giftexchange.goose_db_version WHERE version_id = 4;")
	if revert0004Err != nil {
		t.Fatalf("revert 0004 to reach version 3: %v", revert0004Err)
	}

	_, seedErr := pool.Exec(t.Context(), `
		INSERT INTO giftexchange.persons (id, created_at)
		VALUES ('018f0000-0000-7000-8000-000000000011', now());
		INSERT INTO giftexchange.groups (id, provider, subject, created_at)
		VALUES ('018f0000-0000-7000-8000-000000000012', 'discord', '200000000000000002', now());
		INSERT INTO giftexchange.exchanges (
			id, group_id, organizer_id, name, state, round, reminder_lead_seconds,
			dropout_policy, avoid_last_round, questions_enabled, created_at
		) VALUES (
			'018f0000-0000-7000-8000-000000000013',
			'018f0000-0000-7000-8000-000000000012',
			'018f0000-0000-7000-8000-000000000011',
			'Winter', 'open', 0, 0, 'patch', false, true, now()
		);
	`)
	if seedErr != nil {
		t.Fatalf("seed rows: %v", seedErr)
	}

	var lang string
	scanErr := pool.QueryRow(t.Context(), `
		SELECT language
		FROM giftexchange.exchanges
		WHERE id = '018f0000-0000-7000-8000-000000000013'
	`).Scan(&lang)
	if scanErr != nil {
		t.Fatalf("read default language: %v", scanErr)
	}
	if lang != "en" {
		t.Errorf("default language = %q, want en", lang)
	}

	_, updateErr := pool.Exec(t.Context(), `
		UPDATE giftexchange.exchanges SET language = 'es-419'
		WHERE id = '018f0000-0000-7000-8000-000000000013'
	`)
	if updateErr != nil {
		t.Fatalf("update language to es-419: %v", updateErr)
	}

	_, checkErr := pool.Exec(t.Context(), `
		UPDATE giftexchange.exchanges SET language = 'fr'
		WHERE id = '018f0000-0000-7000-8000-000000000013'
	`)
	if checkErr == nil {
		t.Error("expected CHECK constraint to reject unsupported language 'fr'")
	}

	_, downErr := pool.Exec(t.Context(), downSQL0003+"; DELETE FROM giftexchange.goose_db_version WHERE version_id = 3;")
	if downErr != nil {
		t.Fatalf("exec goose Down: %v", downErr)
	}

	var colCount int
	colErr := pool.QueryRow(t.Context(), `
		SELECT count(*)
		FROM information_schema.columns
		WHERE table_schema = 'giftexchange' AND table_name = 'exchanges'
		  AND column_name = 'language'
	`).Scan(&colCount)
	if colErr != nil {
		t.Fatalf("count language column after Down: %v", colErr)
	}
	if colCount != 0 {
		t.Errorf("language column count after Down = %d, want 0", colCount)
	}

	upErr := db.Migrate(t.Context(), pool, source)
	if upErr != nil {
		t.Fatalf("Migrate after Down: %v", upErr)
	}

	colErr = pool.QueryRow(t.Context(), `
		SELECT count(*)
		FROM information_schema.columns
		WHERE table_schema = 'giftexchange' AND table_name = 'exchanges'
		  AND column_name = 'language'
	`).Scan(&colCount)
	if colErr != nil {
		t.Fatalf("count language column after Up: %v", colErr)
	}
	if colCount != 0 {
		t.Errorf("language column count after full Up (including 0004) = %d, want 0", colCount)
	}
}

func TestMigration0004DropExchangeLanguageUpAndDown(t *testing.T) {
	source, err := giftexchange.MigrationSource()
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

	var colCount int
	colErr := pool.QueryRow(t.Context(), `
		SELECT count(*)
		FROM information_schema.columns
		WHERE table_schema = 'giftexchange' AND table_name = 'exchanges'
		  AND column_name = 'language'
	`).Scan(&colCount)
	if colErr != nil {
		t.Fatalf("count language column after 0004 Up: %v", colErr)
	}
	if colCount != 0 {
		t.Errorf("language column count after 0004 Up = %d, want 0", colCount)
	}

	files, err := store.Migrations()
	if err != nil {
		t.Fatalf("store.Migrations: %v", err)
	}
	downSQL := readMigrationDown(t, files, "0004_drop_exchange_language.sql")

	_, downErr := pool.Exec(t.Context(), downSQL+"; DELETE FROM giftexchange.goose_db_version WHERE version_id = 4;")
	if downErr != nil {
		t.Fatalf("exec 0004 Down: %v", downErr)
	}

	colErr = pool.QueryRow(t.Context(), `
		SELECT count(*)
		FROM information_schema.columns
		WHERE table_schema = 'giftexchange' AND table_name = 'exchanges'
		  AND column_name = 'language'
	`).Scan(&colCount)
	if colErr != nil {
		t.Fatalf("count language column after 0004 Down: %v", colErr)
	}
	if colCount != 1 {
		t.Errorf("language column count after 0004 Down = %d, want 1", colCount)
	}

	upErr := db.Migrate(t.Context(), pool, source)
	if upErr != nil {
		t.Fatalf("Migrate after 0004 Down: %v", upErr)
	}

	colErr = pool.QueryRow(t.Context(), `
		SELECT count(*)
		FROM information_schema.columns
		WHERE table_schema = 'giftexchange' AND table_name = 'exchanges'
		  AND column_name = 'language'
	`).Scan(&colCount)
	if colErr != nil {
		t.Fatalf("count language column after re-running 0004 Up: %v", colErr)
	}
	if colCount != 0 {
		t.Errorf("language column count after re-running 0004 Up = %d, want 0", colCount)
	}
}
