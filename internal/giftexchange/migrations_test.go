package giftexchange_test

import (
	"bytes"
	"fmt"
	"io/fs"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
	"github.com/HectorVeraO/chatisimo/internal/giftexchange/internal/store"
	"github.com/HectorVeraO/chatisimo/internal/platform/db"
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
