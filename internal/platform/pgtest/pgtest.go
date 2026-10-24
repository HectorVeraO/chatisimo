// Package pgtest gives each test package its own Postgres database, cloned from a template built
// once per migration fingerprint. Test code only (depguard).
package pgtest

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/HectorVeraO/chatisimo/internal/platform/config"
	"github.com/HectorVeraO/chatisimo/internal/platform/db"
)

const (
	// lockKey serializes template builds, clones and sweeps in the maintenance database. It
	// differs from db.MigrateLockKey, which lives inside the build database.
	lockKey int64 = 0x5047544553544c4b

	templatePrefix = "chatisimo_tpl_"
	clonePrefix    = "chatisimo_t_"
	buildPrefix    = "chatisimo_b_"

	staleCloneAge    = 6 * time.Hour
	staleTemplateAge = 7 * 24 * time.Hour

	templateCommentPrefix = "built "
)

var (
	templateNamePattern = regexp.MustCompile(`^chatisimo_tpl_[0-9a-f]{16}$`)
	cloneNamePattern    = regexp.MustCompile(`^chatisimo_t_([0-9]{1,12})_[0-9a-f]{16}$`)
	buildNamePattern    = regexp.MustCompile(`^chatisimo_b_[0-9a-f]{16}$`)
)

// errMissingURL names the fix for the most common setup mistake.
var errMissingURL = errors.New("pgtest: DATABASE_URL is not set: copy .example.env to .env, " +
	"start Postgres with docker compose --env-file .env -f .deploy/local/docker-compose.yaml up -d --wait, " +
	"and run the tests under mise (which loads .env)")

// Options lists the sources the template is migrated with, in Migrate order. Nil builds an empty
// database (the E2E binary migrates it itself).
type Options struct {
	Sources []db.Source
}

// Database is one test package's database.
type Database struct {
	pool     *pgxpool.Pool
	name     string
	adminURL string
	url      string
}

// New connects with DATABASE_URL as the admin URL, ensures the template for opts exists, clones it
// and returns the clone. A missing DATABASE_URL is an error naming the fix: tests fail, never skip.
func New(ctx context.Context, opts Options) (*Database, error) {
	return newAt(ctx, opts, time.Now())
}

// newAt is New with the instant that stamps the clone and the template and ages the sweep.
func newAt(ctx context.Context, opts Options, now time.Time) (*Database, error) {
	if ctx == nil {
		return nil, errors.New("pgtest: nil context")
	}

	adminURL := os.Getenv("DATABASE_URL")
	if adminURL == "" {
		return nil, errMissingURL
	}

	templateName, err := templateNameFor(opts.Sources)
	if err != nil {
		return nil, err
	}

	cloneName, err := newCloneName(now)
	if err != nil {
		return nil, err
	}

	cloneURL, err := withDatabase(adminURL, cloneName)
	if err != nil {
		return nil, err
	}

	err = cloneFromTemplate(ctx, adminURL, templateName, cloneName, opts, now)
	if err != nil {
		return nil, err
	}

	pool, err := db.Open(ctx, cloneURL, slog.Default())
	if err != nil {
		orphan := &Database{name: cloneName, adminURL: adminURL}
		dropErr := orphan.drop(context.WithoutCancel(ctx))

		return nil, fmt.Errorf("pgtest: open clone %s: %w", cloneName, errors.Join(err, dropErr))
	}

	return &Database{pool: pool, name: cloneName, adminURL: adminURL, url: cloneURL}, nil
}

// Pool returns the pool connected to the clone.
func (d *Database) Pool() *pgxpool.Pool {
	return d.pool
}

// URL is the clone's connection string (for a subprocess's DATABASE_URL). It is a config.Secret
// because it carries the password of the admin URL.
func (d *Database) URL() config.Secret {
	return config.NewSecret(d.url)
}

// Close closes the pool and drops the clone (DROP DATABASE ... WITH (FORCE)).
func (d *Database) Close(ctx context.Context) error {
	d.pool.Close()

	return d.drop(ctx)
}

func (d *Database) drop(ctx context.Context) error {
	conn, err := pgx.Connect(ctx, d.adminURL)
	if err != nil {
		return fmt.Errorf("pgtest: connect to drop %s: %w", d.name, err)
	}

	dropErr := dropDatabase(ctx, conn, d.name)

	joined := errors.Join(dropErr, conn.Close(ctx))
	if joined != nil {
		return fmt.Errorf("pgtest: drop clone %s: %w", d.name, joined)
	}

	return nil
}

// cloneFromTemplate holds the maintenance lock while it builds the template if missing, clones it
// and sweeps stale databases.
func cloneFromTemplate(
	ctx context.Context, adminURL, templateName, cloneName string, opts Options, now time.Time,
) error {
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return fmt.Errorf("pgtest: connect to the maintenance database: %w", err)
	}

	// Closing the session releases the advisory lock, even if unlocking fails.
	defer func() {
		_ = conn.Close(context.WithoutCancel(ctx))
	}()

	_, err = conn.Exec(ctx, "SELECT pg_advisory_lock($1)", lockKey)
	if err != nil {
		return fmt.Errorf("pgtest: take maintenance lock: %w", err)
	}

	exists, err := templateExists(ctx, conn, templateName)
	if err != nil {
		return err
	}

	if !exists {
		err = buildTemplate(ctx, conn, adminURL, templateName, opts, now)
		if err != nil {
			return err
		}
	}

	_, err = conn.Exec(ctx, "CREATE DATABASE "+quote(cloneName)+" TEMPLATE "+quote(templateName))
	if err != nil {
		return fmt.Errorf("pgtest: clone %s: %w", cloneName, err)
	}

	sweepErr := sweep(ctx, conn, templateName, now)
	if sweepErr != nil {
		dropErr := dropDatabase(context.WithoutCancel(ctx), conn, cloneName)

		return fmt.Errorf("pgtest: sweep after cloning %s: %w", cloneName, errors.Join(sweepErr, dropErr))
	}

	return nil
}

func templateExists(ctx context.Context, conn *pgx.Conn, name string) (bool, error) {
	var exists bool

	err := conn.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1 AND datistemplate)", name,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("pgtest: look up template %s: %w", name, err)
	}

	return exists, nil
}

// buildTemplate migrates a scratch database and renames it into place only once it is complete,
// so a crash never leaves a half-built template under the final name.
func buildTemplate(
	ctx context.Context, conn *pgx.Conn, adminURL, templateName string, opts Options, now time.Time,
) error {
	suffix := make([]byte, 8)

	_, err := rand.Read(suffix)
	if err != nil {
		return fmt.Errorf("pgtest: random build name: %w", err)
	}

	buildName := buildPrefix + hex.EncodeToString(suffix)

	_, err = conn.Exec(ctx, "CREATE DATABASE "+quote(buildName))
	if err != nil {
		return fmt.Errorf("pgtest: create build database: %w", err)
	}

	migrateErr := migrateBuild(ctx, adminURL, buildName, opts)
	if migrateErr != nil {
		dropErr := dropDatabase(context.WithoutCancel(ctx), conn, buildName)

		return fmt.Errorf("pgtest: build template %s: %w", templateName, errors.Join(migrateErr, dropErr))
	}

	return promote(ctx, conn, buildName, templateName, now)
}

func migrateBuild(ctx context.Context, adminURL, buildName string, opts Options) error {
	if len(opts.Sources) == 0 {
		return nil
	}

	buildURL, err := withDatabase(adminURL, buildName)
	if err != nil {
		return err
	}

	pool, err := db.Open(ctx, buildURL, slog.Default())
	if err != nil {
		return fmt.Errorf("pgtest: open build database: %w", err)
	}

	// The pool must be closed before the rename: Postgres refuses to rename a database in use.
	defer pool.Close()

	err = db.Migrate(ctx, pool, opts.Sources...)
	if err != nil {
		return fmt.Errorf("pgtest: migrate build database: %w", err)
	}

	return nil
}

func promote(ctx context.Context, conn *pgx.Conn, buildName, templateName string, builtAt time.Time) error {
	comment := templateCommentPrefix + strconv.FormatInt(builtAt.Unix(), 10)

	// A previous run killed between the rename and the template mark leaves an unmarked database
	// under the final name, which would make the rename fail on every later run.
	err := dropDatabase(ctx, conn, templateName)
	if err != nil {
		return err
	}

	statements := []string{
		"ALTER DATABASE " + quote(buildName) + " RENAME TO " + quote(templateName),
		"ALTER DATABASE " + quote(templateName) + " WITH IS_TEMPLATE true ALLOW_CONNECTIONS false",
		"COMMENT ON DATABASE " + quote(templateName) + " IS '" + comment + "'",
	}

	for _, statement := range statements {
		_, err = conn.Exec(ctx, statement)
		if err != nil {
			return fmt.Errorf("pgtest: finish template %s: %w", templateName, err)
		}
	}

	return nil
}

// sweep drops clones older than staleCloneAge, templates of other fingerprints older than
// staleTemplateAge and every build database. Builds run only while the caller holds the
// maintenance lock, so a build database seen under it was abandoned by an interrupted run. Only
// names matching the exact prefixed patterns are ever touched.
func sweep(ctx context.Context, conn *pgx.Conn, currentTemplate string, now time.Time) error {
	rows, err := conn.Query(ctx, `
		SELECT d.datname, d.datistemplate, COALESCE(shobj_description(d.oid, 'pg_database'), '')
		FROM pg_database d
		WHERE d.datname LIKE 'chatisimo\_t\_%' OR d.datname LIKE 'chatisimo\_tpl\_%'
		   OR d.datname LIKE 'chatisimo\_b\_%'`)
	if err != nil {
		return fmt.Errorf("pgtest: list databases to sweep: %w", err)
	}

	type candidate struct {
		name       string
		isTemplate bool
	}

	var stale []candidate

	for rows.Next() {
		var (
			name       string
			isTemplate bool
			comment    string
		)

		scanErr := rows.Scan(&name, &isTemplate, &comment)
		if scanErr != nil {
			rows.Close()

			return fmt.Errorf("pgtest: scan database to sweep: %w", scanErr)
		}

		isStale := false

		if isTemplate {
			isStale = name != currentTemplate && isStaleTemplate(name, comment, now)
		} else {
			isStale = buildNamePattern.MatchString(name) || isStaleClone(name, now)
		}

		if isStale {
			stale = append(stale, candidate{name: name, isTemplate: isTemplate})
		}
	}

	rows.Close()

	err = rows.Err()
	if err != nil {
		return fmt.Errorf("pgtest: read databases to sweep: %w", err)
	}

	for _, entry := range stale {
		if entry.isTemplate {
			_, err = conn.Exec(ctx, "ALTER DATABASE "+quote(entry.name)+" WITH IS_TEMPLATE false")
			if err != nil {
				return fmt.Errorf("pgtest: unmark template %s: %w", entry.name, err)
			}
		}

		err = dropDatabase(ctx, conn, entry.name)
		if err != nil {
			return err
		}

		slog.InfoContext(ctx, "dropped stale test database", "database", entry.name)
	}

	return nil
}

func isStaleClone(name string, now time.Time) bool {
	match := cloneNamePattern.FindStringSubmatch(name)
	if match == nil {
		return false
	}

	seconds, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return false
	}

	age := now.Sub(time.Unix(seconds, 0))

	return age > staleCloneAge
}

func isStaleTemplate(name, comment string, now time.Time) bool {
	isTemplateName := templateNamePattern.MatchString(name)
	if !isTemplateName {
		return false
	}

	raw, found := strings.CutPrefix(comment, templateCommentPrefix)
	if !found {
		return false
	}

	seconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return false
	}

	age := now.Sub(time.Unix(seconds, 0))

	return age > staleTemplateAge
}

func dropDatabase(ctx context.Context, conn *pgx.Conn, name string) error {
	_, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+quote(name)+" WITH (FORCE)")
	if err != nil {
		return fmt.Errorf("pgtest: drop %s: %w", name, err)
	}

	return nil
}

// templateNameFor hashes each source's schema and fingerprint, in order, so identical migrations
// share a template and different ones never do.
func templateNameFor(sources []db.Source) (string, error) {
	hash := sha256.New()

	writeChunk := func(data []byte) {
		var size [8]byte

		binary.BigEndian.PutUint64(size[:], uint64(len(data)))
		// hash.Hash writes never return an error.
		_, _ = hash.Write(size[:])
		_, _ = hash.Write(data)
	}

	for index, source := range sources {
		if source == nil {
			return "", fmt.Errorf("pgtest: source %d is nil", index)
		}

		fingerprint, err := source.Fingerprint()
		if err != nil {
			return "", fmt.Errorf("pgtest: fingerprint of schema %s: %w", source.Schema(), err)
		}

		writeChunk([]byte(source.Schema()))
		writeChunk(fingerprint)
	}

	sum := hash.Sum(nil)

	return templatePrefix + hex.EncodeToString(sum[:8]), nil
}

func newCloneName(now time.Time) (string, error) {
	suffix := make([]byte, 8)

	_, err := rand.Read(suffix)
	if err != nil {
		return "", fmt.Errorf("pgtest: random clone name: %w", err)
	}

	return clonePrefix + strconv.FormatInt(now.Unix(), 10) + "_" + hex.EncodeToString(suffix), nil
}

// withDatabase replaces the path of rawURL with name, keeping every other part.
func withDatabase(rawURL, name string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", errors.New("pgtest: DATABASE_URL is not a valid URL")
	}

	parsed.Path = "/" + name
	parsed.RawPath = ""

	return parsed.String(), nil
}

func quote(identifier string) string {
	return pgx.Identifier{identifier}.Sanitize()
}
