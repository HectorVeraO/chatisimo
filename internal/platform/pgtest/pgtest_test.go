package pgtest_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/goleak"

	"github.com/HectorVeraO/chatisimo/internal/platform/db"
	"github.com/HectorVeraO/chatisimo/internal/platform/pgtest"
)

const demoSchema = "pgtestdemo"

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

func demoSource(t *testing.T, table string) db.Source {
	t.Helper()

	files := fstest.MapFS{
		"00001_create.sql": &fstest.MapFile{Data: []byte(
			"-- +goose Up\nCREATE TABLE " + demoSchema + "." + table + " (id bigint PRIMARY KEY);\n" +
				"-- +goose Down\nDROP TABLE " + demoSchema + "." + table + ";\n")},
	}

	source, err := db.NewGooseSource(demoSchema, files)
	if err != nil {
		t.Fatalf("new goose source: %v", err)
	}

	return source
}

func newDemo(t *testing.T, table string) *pgtest.Database {
	t.Helper()

	return newDemoAt(t, table, time.Now())
}

// newDemoAt builds a clone whose clone name and template stamp carry the given time.
func newDemoAt(t *testing.T, table string, at time.Time) *pgtest.Database {
	t.Helper()

	options := pgtest.Options{Sources: []db.Source{demoSource(t, table)}}

	database, err := pgtest.NewAt(t.Context(), options, at)
	if err != nil {
		t.Fatalf("pgtest.New: %v", err)
	}

	t.Cleanup(func() {
		closeErr := database.Close(context.WithoutCancel(t.Context()))
		if closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	})

	return database
}

func tableExists(t *testing.T, pool *pgxpool.Pool, table string) bool {
	t.Helper()

	var exists bool

	err := pool.QueryRow(t.Context(), "SELECT to_regclass($1) IS NOT NULL", demoSchema+"."+table).Scan(&exists)
	if err != nil {
		t.Fatalf("look up table: %v", err)
	}

	return exists
}

func adminConn(t *testing.T) *pgx.Conn {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatalf("connect as admin: %v", err)
	}

	t.Cleanup(func() {
		_ = conn.Close(context.WithoutCancel(t.Context()))
	})

	return conn
}

func databaseExists(t *testing.T, conn *pgx.Conn, name string) bool {
	t.Helper()

	var exists bool

	err := conn.QueryRow(t.Context(), "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", name).Scan(&exists)
	if err != nil {
		t.Fatalf("look up database: %v", err)
	}

	return exists
}

func currentDatabase(t *testing.T, database *pgtest.Database) string {
	t.Helper()

	var name string

	err := database.Pool().QueryRow(t.Context(), "SELECT current_database()").Scan(&name)
	if err != nil {
		t.Fatalf("current database: %v", err)
	}

	return name
}

func TestNewClonesMigratedTemplate(t *testing.T) {
	t.Parallel()

	database := newDemo(t, "items")

	if !tableExists(t, database.Pool(), "items") {
		t.Fatal("clone lacks the table the migrations create")
	}
}

func TestNewGivesIsolatedDatabases(t *testing.T) {
	t.Parallel()

	first := newDemo(t, "items")
	second := newDemo(t, "items")

	_, err := first.Pool().Exec(t.Context(), "CREATE TABLE "+demoSchema+".only_in_first (id bigint)")
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	if tableExists(t, second.Pool(), "only_in_first") {
		t.Fatal("a table created in one clone is visible in another")
	}
}

func TestNewWithoutSourcesBuildsEmptyDatabase(t *testing.T) {
	t.Parallel()

	database, err := pgtest.New(t.Context(), pgtest.Options{})
	if err != nil {
		t.Fatalf("new without sources: %v", err)
	}

	t.Cleanup(func() {
		closeErr := database.Close(context.WithoutCancel(t.Context()))
		if closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	})

	var schemaCount int

	err = database.Pool().QueryRow(t.Context(),
		"SELECT count(*) FROM pg_namespace WHERE nspname = $1", demoSchema).Scan(&schemaCount)
	if err != nil {
		t.Fatalf("count schemas: %v", err)
	}

	if schemaCount != 0 {
		t.Fatalf("empty database has schema %s", demoSchema)
	}
}

func TestChangedFingerprintGetsItsOwnTemplate(t *testing.T) {
	t.Parallel()

	original := newDemo(t, "items")
	changed := newDemo(t, "items_changed")

	if tableExists(t, original.Pool(), "items_changed") {
		t.Fatal("clone of the original migrations sees the changed migrations")
	}

	if !tableExists(t, changed.Pool(), "items_changed") {
		t.Fatal("clone of the changed migrations lacks its table")
	}

	if tableExists(t, changed.Pool(), "items") {
		t.Fatal("clone of the changed migrations got the stale template")
	}
}

func TestCloseDropsTheClone(t *testing.T) {
	t.Parallel()

	database, err := pgtest.New(t.Context(), pgtest.Options{})
	if err != nil {
		t.Fatalf("pgtest.New: %v", err)
	}

	name := currentDatabase(t, database)
	admin := adminConn(t)

	if !databaseExists(t, admin, name) {
		t.Fatalf("clone %s not listed before Close", name)
	}

	err = database.Close(t.Context())
	if err != nil {
		t.Fatalf("close: %v", err)
	}

	if databaseExists(t, admin, name) {
		t.Fatalf("clone %s still listed after Close", name)
	}
}

func TestParallelNewSucceeds(t *testing.T) {
	t.Parallel()

	const workers = 6

	var group sync.WaitGroup

	errs := make(chan error, workers)

	for range workers {
		group.Add(1)

		go func() {
			defer group.Done()

			database, err := pgtest.New(t.Context(), pgtest.Options{Sources: []db.Source{demoSource(t, "items")}})
			if err != nil {
				errs <- err

				return
			}

			errs <- database.Close(context.WithoutCancel(t.Context()))
		}()
	}

	group.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("parallel New: %v", err)
		}
	}
}

func TestURLPointsAtTheClone(t *testing.T) {
	t.Parallel()

	database := newDemo(t, "items")

	pool, err := db.Open(t.Context(), database.URL(), discardLogger())
	if err != nil {
		t.Fatalf("open with URL: %v", err)
	}
	defer pool.Close()

	if !tableExists(t, pool, "items") {
		t.Fatal("URL does not lead to the clone")
	}
}

func TestNewFailsWithoutDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")

	_, err := pgtest.New(t.Context(), pgtest.Options{})
	if err == nil {
		t.Fatal("New succeeded without DATABASE_URL")
	}

	for _, fragment := range []string{"DATABASE_URL is not set", ".example.env", "docker compose", "mise"} {
		if !strings.Contains(err.Error(), fragment) {
			t.Errorf("error %q does not mention %q", err, fragment)
		}
	}
}

func TestNewRejectsUnreachableDatabaseWithoutLeakingURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://tester:synthetic-marker-pw@127.0.0.1:1/postgres?sslmode=disable")

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	_, err := pgtest.New(ctx, pgtest.Options{})
	if err == nil {
		t.Fatal("New succeeded against an unreachable database")
	}

	if strings.Contains(err.Error(), "synthetic-marker-pw") {
		t.Fatalf("error leaks the password: %v", err)
	}
}

func TestSweepNeverTouchesForeignDatabases(t *testing.T) {
	admin := adminConn(t)

	const foreign = "othertest_x"

	_, err := admin.Exec(t.Context(), "DROP DATABASE IF EXISTS "+foreign)
	if err != nil {
		t.Fatalf("clean up: %v", err)
	}

	_, err = admin.Exec(t.Context(), "CREATE DATABASE "+foreign)
	if err != nil {
		t.Fatalf("create foreign database: %v", err)
	}

	t.Cleanup(func() {
		_, _ = admin.Exec(context.WithoutCancel(t.Context()), "DROP DATABASE IF EXISTS "+foreign)
	})

	newDemo(t, "items")

	if !databaseExists(t, admin, foreign) {
		t.Fatal("sweep dropped a database without the chatisimo prefixes")
	}
}

func TestSweepDropsAbandonedBuildDatabase(t *testing.T) {
	admin := adminConn(t)

	// A failed build reveals the name of a real build database; recreating it stands for the
	// leftover of a run that was killed mid-build.
	upErr := errors.New("synthetic migration failure " + strconv.FormatInt(time.Now().UnixNano(), 10))
	source := failingSource{upErr: upErr, database: make(chan string, 1)}

	_, err := pgtest.New(t.Context(), pgtest.Options{Sources: []db.Source{source}})
	if !errors.Is(err, upErr) {
		t.Fatalf("New error = %v, want it to wrap the migration failure", err)
	}

	abandoned := <-source.database

	_, err = admin.Exec(t.Context(), "CREATE DATABASE "+abandoned)
	if err != nil {
		t.Fatalf("create %s: %v", abandoned, err)
	}

	t.Cleanup(func() {
		_, _ = admin.Exec(context.WithoutCancel(t.Context()), "DROP DATABASE IF EXISTS "+abandoned+" WITH (FORCE)")
	})

	newDemo(t, "items")

	if databaseExists(t, admin, abandoned) {
		t.Error("sweep kept a build database left by an interrupted run")
	}
}

func TestSweepDropsStaleCloneButKeepsFreshOne(t *testing.T) {
	admin := adminConn(t)

	// A clone left by a run killed seven hours ago, created directly so that no concurrent
	// sweep can drop it between its creation and the assertions.
	staleName := fmt.Sprintf("chatisimo_t_%d_%016x", time.Now().Add(-7*time.Hour).Unix(), time.Now().UnixNano())

	_, err := admin.Exec(t.Context(), "CREATE DATABASE "+staleName)
	if err != nil {
		t.Fatalf("create stale clone: %v", err)
	}

	t.Cleanup(func() {
		_, _ = admin.Exec(context.WithoutCancel(t.Context()), "DROP DATABASE IF EXISTS "+staleName)
	})

	fresh := newDemo(t, "items")
	freshName := currentDatabase(t, fresh)

	if databaseExists(t, admin, staleName) {
		t.Error("sweep kept a clone older than 6 hours")
	}

	if !databaseExists(t, admin, freshName) {
		t.Error("sweep dropped a recent clone")
	}
}

func TestSweepDropsOnlyOldTemplates(t *testing.T) {
	admin := adminConn(t)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	oldTable := "old_" + suffix
	recentTable := "recent_" + suffix

	newDemoAt(t, oldTable, time.Now().Add(-8*24*time.Hour))
	newDemoAt(t, recentTable, time.Now())

	oldTemplate := templateNameForTable(t, oldTable)
	recentTemplate := templateNameForTable(t, recentTable)

	t.Cleanup(func() {
		for _, name := range []string{oldTemplate, recentTemplate} {
			_, _ = admin.Exec(context.WithoutCancel(t.Context()), "ALTER DATABASE "+name+" WITH IS_TEMPLATE false")
			_, _ = admin.Exec(context.WithoutCancel(t.Context()), "DROP DATABASE IF EXISTS "+name)
		}
	})

	newDemo(t, "items")

	if databaseExists(t, admin, oldTemplate) {
		t.Error("sweep kept a template built over 7 days ago")
	}

	if !databaseExists(t, admin, recentTemplate) {
		t.Error("sweep dropped a recent template")
	}
}

func templateNameForTable(t *testing.T, table string) string {
	t.Helper()

	name, err := pgtest.TemplateNameFor([]db.Source{demoSource(t, table)})
	if err != nil {
		t.Fatalf("template name: %v", err)
	}

	return name
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestSweepKeepsTheCurrentTemplateEvenWhenOld(t *testing.T) {
	admin := adminConn(t)
	table := "aged_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	sources := []db.Source{demoSource(t, table)}

	current, err := pgtest.TemplateNameFor(sources)
	if err != nil {
		t.Fatalf("template name: %v", err)
	}

	t.Cleanup(func() {
		_, _ = admin.Exec(context.WithoutCancel(t.Context()), "ALTER DATABASE "+current+" WITH IS_TEMPLATE false")
		_, _ = admin.Exec(context.WithoutCancel(t.Context()), "DROP DATABASE IF EXISTS "+current)
	})

	newDemoAt(t, table, time.Now().Add(-30*24*time.Hour))

	second := newDemo(t, table)

	if !databaseExists(t, admin, current) {
		t.Fatal("sweep dropped the template the clone was just made from")
	}

	if !tableExists(t, second.Pool(), table) {
		t.Fatal("clone from the aged template lacks its table")
	}
}

// failingSource is a migration source whose Up always fails, recording the database it ran in.
type failingSource struct {
	upErr    error
	database chan string
}

func (f failingSource) Schema() string { return demoSchema }

func (f failingSource) Fingerprint() ([]byte, error) {
	return []byte("failing-source-" + f.upErr.Error()), nil
}

func (f failingSource) Up(_ context.Context, pool *pgxpool.Pool) error {
	f.database <- pool.Config().ConnConfig.Database

	return f.upErr
}

func TestNewReportsFailingMigrationAndLeavesNoBuildDatabase(t *testing.T) {
	admin := adminConn(t)
	upErr := errors.New("synthetic migration failure " + strconv.FormatInt(time.Now().UnixNano(), 10))
	source := failingSource{upErr: upErr, database: make(chan string, 1)}

	_, err := pgtest.New(t.Context(), pgtest.Options{Sources: []db.Source{source}})
	if !errors.Is(err, upErr) {
		t.Fatalf("New error = %v, want it to wrap the migration failure", err)
	}

	buildDatabase := <-source.database
	if databaseExists(t, admin, buildDatabase) {
		t.Errorf("build database %s survived the failed build", buildDatabase)
	}

	template, nameErr := pgtest.TemplateNameFor([]db.Source{source})
	if nameErr != nil {
		t.Fatalf("template name: %v", nameErr)
	}

	if databaseExists(t, admin, template) {
		t.Errorf("template %s exists after a failed build", template)
	}
}
