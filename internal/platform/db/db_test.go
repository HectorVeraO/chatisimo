package db_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/goleak"

	"github.com/HectorVeraO/chatisimo/internal/platform/db"
	"github.com/HectorVeraO/chatisimo/internal/platform/pgtest"
)

var shared *pgtest.Database

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	ctx := context.Background()

	database, err := pgtest.New(ctx, pgtest.Options{})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	shared = database
	code := m.Run()

	err = database.Close(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	err = goleak.Find()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	return code
}

// uniqueSchema returns a valid schema name no other test or run uses.
func uniqueSchema(t *testing.T, prefix string) string {
	t.Helper()

	suffix := make([]byte, 6)

	_, err := rand.Read(suffix)
	if err != nil {
		t.Fatalf("random schema suffix: %v", err)
	}

	return prefix + "_" + hex.EncodeToString(suffix)
}

func migrations(schema string, tables ...string) fstest.MapFS {
	files := fstest.MapFS{}

	for index, table := range tables {
		name := fmt.Sprintf("%05d_create_%s.sql", index+1, table)
		body := "-- +goose Up\nCREATE TABLE " + schema + "." + table + " (id bigint PRIMARY KEY);\n" +
			"-- +goose Down\nDROP TABLE " + schema + "." + table + ";\n"
		files[name] = &fstest.MapFile{Data: []byte(body)}
	}

	return files
}

func newSource(t *testing.T, schema string, files fstest.MapFS) *db.GooseSource {
	t.Helper()

	source, err := db.NewGooseSource(schema, files)
	if err != nil {
		t.Fatalf("new goose source: %v", err)
	}

	return source
}

func discardingLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
}

func openPool(t *testing.T, url string) *pgxpool.Pool {
	t.Helper()

	pool, err := db.Open(t.Context(), url, discardingLogger())
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}

	t.Cleanup(pool.Close)

	return pool
}

func count(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()

	var total int

	err := pool.QueryRow(t.Context(), query, args...).Scan(&total)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}

	return total
}

func TestMigrateCreatesSchemasTablesAndVersionTables(t *testing.T) {
	t.Parallel()

	schemaA := uniqueSchema(t, "dbtest_a")
	schemaB := uniqueSchema(t, "dbtest_b")
	pool := shared.Pool()

	err := db.Migrate(t.Context(), pool,
		newSource(t, schemaA, migrations(schemaA, "alpha", "beta")),
		newSource(t, schemaB, migrations(schemaB, "gamma")),
	)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}

	tables := count(t, pool, `SELECT count(*) FROM information_schema.tables
		WHERE (table_schema = $1 AND table_name IN ('alpha', 'beta', 'goose_db_version'))
		   OR (table_schema = $2 AND table_name IN ('gamma', 'goose_db_version'))`, schemaA, schemaB)
	if tables != 5 {
		t.Fatalf("found %d of the 5 expected tables", tables)
	}
}

func TestMigrateAcceptsASourceWithoutMigrations(t *testing.T) {
	t.Parallel()

	schema := uniqueSchema(t, "dbtest_a")

	err := db.Migrate(t.Context(), shared.Pool(), newSource(t, schema, fstest.MapFS{}))
	if err != nil {
		t.Fatalf("migrate an empty source: %v", err)
	}

	created := count(t, shared.Pool(), "SELECT count(*) FROM pg_namespace WHERE nspname = $1", schema)
	if created != 1 {
		t.Fatal("Migrate did not create the schema of an empty source")
	}
}

func TestNewGooseSourceReportsEveryInvalidArgument(t *testing.T) {
	t.Parallel()

	_, err := db.NewGooseSource("Not Valid", nil)
	if err == nil {
		t.Fatal("NewGooseSource accepted a nil filesystem and an invalid schema")
	}

	for _, fragment := range []string{"nil migrations filesystem", "invalid schema name"} {
		if !strings.Contains(err.Error(), fragment) {
			t.Errorf("error %q does not mention %q", err, fragment)
		}
	}
}

func TestMigrateTwiceIsANoOp(t *testing.T) {
	t.Parallel()

	schema := uniqueSchema(t, "dbtest_a")
	source := newSource(t, schema, migrations(schema, "alpha", "beta"))
	pool := shared.Pool()

	for range 2 {
		err := db.Migrate(t.Context(), pool, source)
		if err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}

	rows := count(t, pool, "SELECT count(*) FROM "+schema+".goose_db_version")
	if rows != 3 {
		t.Fatalf("version table has %d rows, want 3 (initial row plus two migrations)", rows)
	}
}

func TestMigrateAppliesOnlyNewMigrationsOnLaterRuns(t *testing.T) {
	t.Parallel()

	schema := uniqueSchema(t, "dbtest_a")
	pool := shared.Pool()

	err := db.Migrate(t.Context(), pool, newSource(t, schema, migrations(schema, "alpha")))
	if err != nil {
		t.Fatalf("first migrate: %v", err)
	}

	err = db.Migrate(t.Context(), pool, newSource(t, schema, migrations(schema, "alpha", "beta")))
	if err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	tables := count(t, pool,
		"SELECT count(*) FROM information_schema.tables WHERE table_schema = $1 AND table_name IN ('alpha', 'beta')",
		schema)
	if tables != 2 {
		t.Fatalf("found %d of 2 tables", tables)
	}
}

func TestConcurrentMigrateAppliesEachMigrationOnce(t *testing.T) {
	t.Parallel()

	schema := uniqueSchema(t, "dbtest_a")
	files := migrations(schema, "alpha", "beta", "gamma")

	const processes = 4

	var group sync.WaitGroup

	errs := make(chan error, processes)

	// Each caller has its own pool, like separate processes: one pool shared by callers could
	// hold every connection with waiting lock takers.
	for range processes {
		pool := openPool(t, shared.URL())

		group.Add(1)

		go func() {
			defer group.Done()

			source, err := db.NewGooseSource(schema, files)
			if err != nil {
				errs <- err

				return
			}

			errs <- db.Migrate(t.Context(), pool, source)
		}()
	}

	group.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("concurrent migrate: %v", err)
		}
	}

	rows := count(t, shared.Pool(), "SELECT count(*) FROM "+schema+".goose_db_version")
	if rows != 4 {
		t.Fatalf("version table has %d rows, want 4", rows)
	}
}

func TestMigrateRejectsInvalidSchemaNames(t *testing.T) {
	t.Parallel()

	names := map[string]string{
		"empty":            "",
		"uppercase":        "Upper",
		"leading digit":    "1abc",
		"hyphen":           "a-b",
		"64 characters":    "a" + strings.Repeat("b", 63),
		"injection":        "x;drop",
		"quote":            `a"b`,
		"space":            "a b",
		"unicode":          "caf" + string(rune(0xe9)),
		"zero width space": "a" + string(rune(0x200b)) + "b",
	}

	for label, name := range names {
		t.Run(label, func(t *testing.T) {
			t.Parallel()

			_, err := db.NewGooseSource(name, fstest.MapFS{})
			if err == nil {
				t.Fatalf("NewGooseSource accepted %q", name)
			}

			migrateErr := db.Migrate(t.Context(), shared.Pool(), fakeSource{schema: name})
			if migrateErr == nil {
				t.Fatalf("Migrate accepted %q", name)
			}

			if !strings.Contains(migrateErr.Error(), "invalid schema name") {
				t.Fatalf("Migrate error = %q, want an invalid schema name rejection", migrateErr)
			}
		})
	}
}

func TestMigrateAcceptsLongestValidSchemaName(t *testing.T) {
	t.Parallel()

	name := "z" + strings.Repeat("y", 62)

	_, err := db.NewGooseSource(name, fstest.MapFS{})
	if err != nil {
		t.Fatalf("NewGooseSource rejected a 63-character name: %v", err)
	}
}

func TestMigrateRejectsDuplicateSchemasBeforeCreatingAnything(t *testing.T) {
	t.Parallel()

	first := uniqueSchema(t, "dbtest_a")
	second := uniqueSchema(t, "dbtest_b")
	pool := shared.Pool()

	err := db.Migrate(t.Context(), pool,
		newSource(t, first, migrations(first, "alpha")),
		newSource(t, second, migrations(second, "beta")),
		newSource(t, first, migrations(first, "alpha")),
	)
	if err == nil {
		t.Fatal("Migrate accepted duplicate schemas")
	}

	created := count(t, pool, "SELECT count(*) FROM pg_namespace WHERE nspname IN ($1, $2)", first, second)
	if created != 0 {
		t.Fatalf("%d schemas were created before the duplicate was rejected", created)
	}
}

func TestMigrateReportsEveryInvalidSource(t *testing.T) {
	t.Parallel()

	err := db.Migrate(t.Context(), shared.Pool(), fakeSource{schema: "Bad"}, fakeSource{schema: "worse-one"})
	if err == nil {
		t.Fatal("Migrate accepted invalid schemas")
	}

	for _, name := range []string{"Bad", "worse-one"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not mention %q", err, name)
		}
	}
}

func TestMigrateRefusesSingleConnectionPool(t *testing.T) {
	t.Parallel()

	pool := openPool(t, withQuery(shared.URL(), "pool_max_conns=1"))
	schema := uniqueSchema(t, "dbtest_a")

	err := db.Migrate(t.Context(), pool, newSource(t, schema, migrations(schema, "alpha")))
	if err == nil {
		t.Fatal("Migrate accepted a pool with one connection")
	}

	if !strings.Contains(err.Error(), "MaxConns >= 2") {
		t.Fatalf("error = %q, want the pool size refusal", err)
	}

	created := count(t, shared.Pool(), "SELECT count(*) FROM pg_namespace WHERE nspname = $1", schema)
	if created != 0 {
		t.Fatal("Migrate created a schema despite refusing the pool")
	}
}

func TestMigrateReportsFailingMigrationAndReleasesTheLock(t *testing.T) {
	t.Parallel()

	schema := uniqueSchema(t, "dbtest_a")
	broken := fstest.MapFS{
		"00001_broken.sql": &fstest.MapFile{Data: []byte("-- +goose Up\nCREATE TABLE nope.missing (id bigint);\n")},
	}

	err := db.Migrate(t.Context(), shared.Pool(), newSource(t, schema, broken))
	if err == nil {
		t.Fatal("Migrate hid a failing migration")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	fixedSchema := uniqueSchema(t, "dbtest_b")

	err = db.Migrate(ctx, shared.Pool(), newSource(t, fixedSchema, migrations(fixedSchema, "alpha")))
	if err != nil {
		t.Fatalf("migrate after a failure: %v", err)
	}
}

func TestMigrateRejectsNilArguments(t *testing.T) {
	t.Parallel()

	var nilContext context.Context

	schema := uniqueSchema(t, "dbtest_a")
	valid := newSource(t, schema, migrations(schema, "alpha"))

	cases := map[string]func() error{
		"nil context": func() error { return db.Migrate(nilContext, shared.Pool(), valid) },
		"nil pool":    func() error { return db.Migrate(t.Context(), nil, valid) },
		"nil source":  func() error { return db.Migrate(t.Context(), shared.Pool(), valid, nil) },
	}

	for label, call := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()

			err := call()
			if err == nil {
				t.Fatalf("Migrate accepted %s", label)
			}
		})
	}

	created := count(t, shared.Pool(), "SELECT count(*) FROM pg_namespace WHERE nspname = $1", schema)
	if created != 0 {
		t.Fatal("Migrate created a schema despite rejecting its arguments")
	}
}

func TestMigrateFailsAndFreesTheLockWhenItCannotBeReleased(t *testing.T) {
	t.Parallel()

	schema := uniqueSchema(t, "dbtest_a")
	pool := shared.Pool()

	err := db.Migrate(t.Context(), pool, lockKillerSource{schema: schema})
	if err == nil {
		t.Fatal("Migrate hid a lock that could not be released")
	}

	if !strings.Contains(err.Error(), "release advisory lock") {
		t.Fatalf("error = %q, want the lock release failure", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	next := uniqueSchema(t, "dbtest_b")

	err = db.Migrate(ctx, pool, newSource(t, next, migrations(next, "alpha")))
	if err != nil {
		t.Fatalf("migrate after a failed release: %v", err)
	}
}

func TestFingerprint(t *testing.T) {
	t.Parallel()

	fingerprintOf := func(files fstest.MapFS) []byte {
		t.Helper()

		sum, err := newSource(t, "dbtest_a", files).Fingerprint()
		if err != nil {
			t.Fatalf("fingerprint: %v", err)
		}

		return sum
	}

	base := fstest.MapFS{"00001_a.sql": &fstest.MapFile{Data: []byte("-- +goose Up\nSELECT 1;\n")}}
	sameContent := fstest.MapFS{"00001_a.sql": &fstest.MapFile{Data: []byte("-- +goose Up\nSELECT 1;\n")}}
	changedContent := fstest.MapFS{"00001_a.sql": &fstest.MapFile{Data: []byte("-- +goose Up\nSELECT 2;\n")}}
	renamed := fstest.MapFS{"00002_a.sql": &fstest.MapFile{Data: []byte("-- +goose Up\nSELECT 1;\n")}}
	extra := fstest.MapFS{
		"00001_a.sql": &fstest.MapFile{Data: []byte("-- +goose Up\nSELECT 1;\n")},
		"00002_b.sql": &fstest.MapFile{Data: []byte("-- +goose Up\nSELECT 1;\n")},
	}

	reference := fingerprintOf(base)

	if !bytes.Equal(reference, fingerprintOf(sameContent)) {
		t.Error("fingerprint is not stable for identical migrations")
	}

	cases := map[string]fstest.MapFS{
		"a changed file content": changedContent,
		"a renamed file":         renamed,
		"an added file":          extra,
	}

	for label, files := range cases {
		if bytes.Equal(reference, fingerprintOf(files)) {
			t.Errorf("fingerprint does not change with %s", label)
		}
	}
}

func TestOpenRejectsUnparsableURL(t *testing.T) {
	t.Parallel()

	const marker = "synthetic-marker-pw"

	_, err := db.Open(t.Context(), "postgres://user:"+marker+"@host:notaport/db", discardingLogger())
	if !errors.Is(err, db.ErrInvalidURL) {
		t.Fatalf("error = %v, want ErrInvalidURL", err)
	}

	if err.Error() != "invalid DATABASE_URL" {
		t.Fatalf("error text = %q", err)
	}

	if strings.Contains(err.Error(), marker) {
		t.Fatal("error leaks the password")
	}
}

func TestOpenRejectsNilLogger(t *testing.T) {
	t.Parallel()

	_, err := db.Open(t.Context(), shared.URL(), nil)
	if err == nil {
		t.Fatal("Open accepted a nil logger")
	}
}

// attemptRecorder remembers the "attempt" attribute of every record it handles.
type attemptRecorder struct {
	slog.Handler

	mu       sync.Mutex
	attempts []int64
}

func (r *attemptRecorder) Handle(ctx context.Context, record slog.Record) error {
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == "attempt" {
			r.mu.Lock()
			r.attempts = append(r.attempts, attr.Value.Int64())
			r.mu.Unlock()
		}

		return true
	})

	err := r.Handler.Handle(ctx, record)
	if err != nil {
		return fmt.Errorf("handle record: %w", err)
	}

	return nil
}

func (r *attemptRecorder) sawAttempt(want int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Contains(r.attempts, want)
}

func TestOpenUnreachableNeverLeaksURLOrPassword(t *testing.T) {
	t.Parallel()

	const (
		marker = "synthetic-marker-pw"
		url    = "postgres://tester:" + marker + "@127.0.0.1:1/dbtest?sslmode=disable"
	)

	var logs bytes.Buffer

	recorder := &attemptRecorder{Handler: slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})}
	logger := slog.New(recorder)

	ctx, cancel := context.WithTimeout(t.Context(), 600*time.Millisecond)
	defer cancel()

	_, err := db.Open(ctx, url, logger)
	if err == nil {
		t.Fatal("Open succeeded against an unreachable server")
	}

	for label, text := range map[string]string{"error": err.Error(), "logs": logs.String()} {
		if strings.Contains(text, marker) {
			t.Errorf("%s leak the password: %s", label, text)
		}

		if strings.Contains(text, url) {
			t.Errorf("%s leak the URL: %s", label, text)
		}
	}

	if !recorder.sawAttempt(1) {
		t.Errorf("no retry was logged: %q", logs.String())
	}

	if !strings.HasPrefix(err.Error(), "connect to Postgres: ") {
		t.Errorf("error = %q", err)
	}
}

func TestOpenReachableServerReturnsWorkingPool(t *testing.T) {
	t.Parallel()

	pool := openPool(t, shared.URL())

	if count(t, pool, "SELECT 1") != 1 {
		t.Fatal("pool does not answer queries")
	}
}

func FuzzNewGooseSource(f *testing.F) {
	seeds := []string{
		"", "ok", "Upper", "1abc", "a-b", "x;drop", `a"b`, "a b",
		"caf" + string(rune(0xe9)),
		"a" + string(rune(0x200b)) + "b",
		"e" + string(rune(0x301)),
		string(rune(0x202e)) + "abc",
		string(rune(0x1f600)),
		strings.Repeat("a", 63), strings.Repeat("a", 64),
		"a" + string(rune(0)) + "b",
		"public\"; DROP SCHEMA x; --",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, schema string) {
		source, err := db.NewGooseSource(schema, fstest.MapFS{})
		if err != nil {
			return
		}

		accepted := source.Schema()
		if accepted != schema {
			t.Fatalf("accepted %q but reports %q", schema, accepted)
		}

		isLengthValid := len(accepted) >= 1 && len(accepted) <= 63
		if !isLengthValid {
			t.Fatalf("accepted a name of %d bytes", len(accepted))
		}

		isLowerLetter := accepted[0] >= 'a' && accepted[0] <= 'z'
		if !isLowerLetter {
			t.Fatalf("accepted a name starting with %q", accepted[0])
		}

		for _, char := range []byte(accepted) {
			isSafe := (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '_'
			if !isSafe {
				t.Fatalf("accepted unsafe byte %q in %q", char, accepted)
			}
		}
	})
}

type fakeSource struct {
	schema string
}

func (s fakeSource) Schema() string { return s.schema }

func (s fakeSource) Fingerprint() ([]byte, error) { return nil, nil }

func (s fakeSource) Up(context.Context, *pgxpool.Pool) error { return nil }

// lockKillerSource ends the session that holds the migration lock while Up runs, so the release
// afterwards fails on a dead connection.
type lockKillerSource struct {
	schema string
}

func (s lockKillerSource) Schema() string { return s.schema }

func (s lockKillerSource) Fingerprint() ([]byte, error) { return nil, nil }

func (s lockKillerSource) Up(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		SELECT pg_terminate_backend(l.pid)
		FROM pg_locks l
		WHERE l.locktype = 'advisory'
		  AND l.granted
		  AND l.database = (SELECT oid FROM pg_database WHERE datname = current_database())
		  AND ((l.classid::bigint << 32) | l.objid::bigint) = $1`, db.MigrateLockKey)
	if err != nil {
		return fmt.Errorf("terminate lock holder: %w", err)
	}

	return nil
}

func withQuery(url, option string) string {
	if strings.Contains(url, "?") {
		return url + "&" + option
	}

	return url + "?" + option
}
