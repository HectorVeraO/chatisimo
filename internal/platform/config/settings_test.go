package config_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/goleak"

	"github.com/HectorVeraO/chatisimo/internal/platform/config"
	"github.com/HectorVeraO/chatisimo/internal/platform/crypto"
	"github.com/HectorVeraO/chatisimo/internal/platform/db"
	"github.com/HectorVeraO/chatisimo/internal/platform/pgtest"
)

var (
	shared    *pgtest.Database
	sharedKey crypto.MasterKey
)

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	ctx := context.Background()

	text, err := crypto.GenerateMasterKey(rand.Reader)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	sharedKey, err = crypto.ParseMasterKey(text)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	source, err := config.MigrationSource()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	database, err := pgtest.New(ctx, pgtest.Options{Sources: []db.Source{source}})
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

func sourcesOf(t *testing.T) []db.Source {
	t.Helper()

	source, err := config.MigrationSource()
	if err != nil {
		t.Fatalf("MigrationSource: %v", err)
	}

	return []db.Source{source}
}

func suffix(t *testing.T) string {
	t.Helper()

	raw := make([]byte, 6)

	_, err := rand.Read(raw)
	if err != nil {
		t.Fatalf("random suffix: %v", err)
	}

	return hex.EncodeToString(raw)
}

func localOnly(_ string, env config.Environment) error {
	if env != config.EnvLocal {
		return errors.New("allowed only with the local environment")
	}

	return nil
}

func newStore(t *testing.T, env config.Environment, keys ...config.Key) *config.Store {
	t.Helper()

	store, err := config.NewStore(t.Context(), shared.Pool(), sharedKey, env, keys)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	return store
}

func TestPlainValueRoundTrips(t *testing.T) {
	t.Parallel()

	name := "test.plain_" + suffix(t)
	store := newStore(t, config.EnvLocal, config.Key{Name: name})

	err := store.Set(t.Context(), name, "first value")
	if err != nil {
		t.Fatalf("Set: %v", err)
	}

	err = store.Set(t.Context(), name, "second value")
	if err != nil {
		t.Fatalf("Set again: %v", err)
	}

	got, err := store.Get(t.Context(), name)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got != "second value" {
		t.Errorf("Get = %q, want the last value", got)
	}
}

func TestSecretRoundTripsAndIsSealedAtRest(t *testing.T) {
	t.Parallel()

	name := "test.secret_" + suffix(t)
	plain := "plain-secret-" + suffix(t)
	store := newStore(t, config.EnvLocal, config.Key{Name: name, Secret: true})

	err := store.Set(t.Context(), name, plain)
	if err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := store.Secret(t.Context(), name)
	if err != nil {
		t.Fatalf("Secret: %v", err)
	}

	if got.Reveal() != plain {
		t.Error("Secret did not round trip")
	}

	var (
		value  *string
		sealed []byte
	)

	err = shared.Pool().QueryRow(t.Context(),
		"SELECT value, sealed FROM platform.setting WHERE key = $1", name).Scan(&value, &sealed)
	if err != nil {
		t.Fatalf("read row: %v", err)
	}

	if value != nil {
		t.Error("value column is set for a secret")
	}

	if len(sealed) == 0 || strings.Contains(string(sealed), plain) {
		t.Error("sealed column is empty or holds the plaintext")
	}
}

func TestSealedValueMovedToAnotherKeyFailsToOpen(t *testing.T) {
	t.Parallel()

	nameA := "test.secret_a" + suffix(t)
	nameB := "test.secret_b" + suffix(t)
	store := newStore(t, config.EnvLocal, config.Key{Name: nameA, Secret: true}, config.Key{Name: nameB, Secret: true})

	for _, name := range []string{nameA, nameB} {
		err := store.Set(t.Context(), name, "value-for-"+name)
		if err != nil {
			t.Fatalf("Set %s: %v", name, err)
		}
	}

	_, err := shared.Pool().Exec(t.Context(),
		"UPDATE platform.setting SET sealed = (SELECT sealed FROM platform.setting WHERE key = $1) WHERE key = $2",
		nameA, nameB)
	if err != nil {
		t.Fatalf("move sealed value: %v", err)
	}

	_, err = store.Secret(t.Context(), nameB)
	if !errors.Is(err, crypto.ErrOpen) {
		t.Errorf("Secret = %v, want crypto.ErrOpen", err)
	}
}

func TestUnknownKeyIsRefused(t *testing.T) {
	t.Parallel()

	store := newStore(t, config.EnvLocal)
	unknown := "test.unknown_" + suffix(t)

	calls := map[string]error{
		"Set":     store.Set(t.Context(), unknown, "value"),
		"Require": store.Require(t.Context(), unknown),
	}

	_, calls["Get"] = store.Get(t.Context(), unknown)
	_, calls["Secret"] = store.Secret(t.Context(), unknown)

	for call, err := range calls {
		if !errors.Is(err, config.ErrUnknownKey) {
			t.Errorf("%s = %v, want ErrUnknownKey", call, err)
		}
	}
}

func TestWrongKindIsRefused(t *testing.T) {
	t.Parallel()

	plainName := "test.plain_" + suffix(t)
	secretName := "test.secret_" + suffix(t)
	store := newStore(t, config.EnvLocal, config.Key{Name: plainName}, config.Key{Name: secretName, Secret: true})

	_, err := store.Secret(t.Context(), plainName)
	if !errors.Is(err, config.ErrWrongKind) {
		t.Errorf("Secret of a plain key = %v, want ErrWrongKind", err)
	}

	_, err = store.Get(t.Context(), secretName)
	if !errors.Is(err, config.ErrWrongKind) {
		t.Errorf("Get of a secret key = %v, want ErrWrongKind", err)
	}
}

func TestUnsetKeyUsesDefaultElseErrNotSet(t *testing.T) {
	t.Parallel()

	withDefault := "test.with_default_" + suffix(t)
	without := "test.without_" + suffix(t)
	secret := "test.secret_" + suffix(t)
	store := newStore(t, config.EnvLocal,
		config.Key{Name: withDefault, Default: "fallback"},
		config.Key{Name: without},
		config.Key{Name: secret, Secret: true},
	)

	got, err := store.Get(t.Context(), withDefault)
	if err != nil || got != "fallback" {
		t.Errorf("Get = %q, %v, want the default", got, err)
	}

	_, err = store.Get(t.Context(), without)
	if !errors.Is(err, config.ErrNotSet) {
		t.Errorf("Get without default = %v, want ErrNotSet", err)
	}

	_, err = store.Secret(t.Context(), secret)
	if !errors.Is(err, config.ErrNotSet) {
		t.Errorf("Secret unset = %v, want ErrNotSet", err)
	}

	err = store.Set(t.Context(), withDefault, "stored")
	if err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err = store.Get(t.Context(), withDefault)
	if err != nil || got != "stored" {
		t.Errorf("Get = %q, %v, want the stored value over the default", got, err)
	}
}

func TestSetRejectsEmptyAndInvalidValues(t *testing.T) {
	t.Parallel()

	name := "test.local_only_" + suffix(t)
	key := config.Key{Name: name, Validate: localOnly}

	local := newStore(t, config.EnvLocal, key)
	production := newStore(t, config.EnvProduction, key)

	err := local.Set(t.Context(), name, "")
	if !errors.Is(err, config.ErrInvalidValue) {
		t.Errorf("Set empty = %v, want ErrInvalidValue", err)
	}

	err = production.Set(t.Context(), name, "http://example.com")
	if !errors.Is(err, config.ErrInvalidValue) {
		t.Fatalf("Set in production = %v, want ErrInvalidValue", err)
	}

	if !strings.Contains(err.Error(), name) || strings.Contains(err.Error(), "http://example.com") {
		t.Errorf("error must name the key and never the value: %v", err)
	}

	err = local.Set(t.Context(), name, "http://example.com")
	if err != nil {
		t.Errorf("Set in local: %v", err)
	}
}

func TestStoredValueThatBecameInvalidFailsOnRead(t *testing.T) {
	t.Parallel()

	name := "test.local_only_" + suffix(t)
	secretName := "test.secret_local_only_" + suffix(t)
	keys := []config.Key{
		{Name: name, Validate: localOnly},
		{Name: secretName, Secret: true, Validate: localOnly},
	}

	local := newStore(t, config.EnvLocal, keys...)
	production := newStore(t, config.EnvProduction, keys...)

	for _, key := range keys {
		err := local.Set(t.Context(), key.Name, "http://example.com")
		if err != nil {
			t.Fatalf("Set %s: %v", key.Name, err)
		}
	}

	_, err := production.Get(t.Context(), name)
	if !errors.Is(err, config.ErrInvalidValue) {
		t.Errorf("Get = %v, want ErrInvalidValue", err)
	}

	_, err = production.Secret(t.Context(), secretName)
	if !errors.Is(err, config.ErrInvalidValue) {
		t.Errorf("Secret = %v, want ErrInvalidValue", err)
	}
}

func TestRequireNamesEveryMissingKey(t *testing.T) {
	t.Parallel()

	missingA := "test.missing_a" + suffix(t)
	missingB := "test.missing_b" + suffix(t)
	present := "test.present_" + suffix(t)
	defaulted := "test.defaulted_" + suffix(t)
	store := newStore(t, config.EnvLocal,
		config.Key{Name: missingA, Secret: true},
		config.Key{Name: missingB},
		config.Key{Name: present},
		config.Key{Name: defaulted, Default: "d"},
	)

	err := store.Set(t.Context(), present, "here")
	if err != nil {
		t.Fatalf("Set: %v", err)
	}

	err = store.Require(t.Context(), missingA, missingB, present, defaulted)
	if err == nil {
		t.Fatal("Require passed with missing keys")
	}

	if !errors.Is(err, config.ErrNotSet) {
		t.Errorf("error does not match ErrNotSet: %v", err)
	}

	text := err.Error()
	for _, name := range []string{missingA, missingB} {
		want := name + " is not set: run chatisimo config set " + name
		if !strings.Contains(text, want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}

	for _, name := range []string{present, defaulted} {
		if strings.Contains(text, name) {
			t.Errorf("error names %s, which is satisfied", name)
		}
	}

	err = store.Require(t.Context(), present, defaulted)
	if err != nil {
		t.Errorf("Require of satisfied keys = %v", err)
	}
}

func TestListIsSortedAndNeverShowsSecrets(t *testing.T) {
	t.Parallel()

	id := suffix(t)
	secretName := "test.a_secret_" + id
	plainName := "test.b_plain_" + id
	defaultName := "test.c_default_" + id
	unsetSecret := "test.d_unset_secret_" + id
	store := newStore(t, config.EnvLocal,
		config.Key{Name: unsetSecret, Secret: true, Doc: "unset secret"},
		config.Key{Name: defaultName, Default: "the-default", Doc: "has default"},
		config.Key{Name: plainName, Doc: "plain"},
		config.Key{Name: secretName, Secret: true, Doc: "secret"},
	)

	secretValue := "list-secret-" + id

	for name, value := range map[string]string{secretName: secretValue, plainName: "plain-value"} {
		err := store.Set(t.Context(), name, value)
		if err != nil {
			t.Fatalf("Set %s: %v", name, err)
		}
	}

	statuses, err := store.List(t.Context())
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	names := make([]string, 0, len(statuses))
	for _, status := range statuses {
		names = append(names, status.Name)
	}

	want := []string{secretName, plainName, defaultName, unsetSecret}
	if !slices.Equal(names, want) {
		t.Fatalf("names = %v, want %v", names, want)
	}

	checks := []struct {
		index   int
		set     bool
		value   string
		hasTime bool
	}{
		{0, true, "[redacted]", true},
		{1, true, "plain-value", true},
		{2, false, "the-default", false},
		{3, false, "", false},
	}

	for _, check := range checks {
		status := statuses[check.index]

		if status.Set != check.set || status.Value != check.value || (status.UpdatedAt != nil) != check.hasTime {
			t.Errorf("%s = %+v, want set=%v value=%q time=%v", status.Name, status, check.set, check.value, check.hasTime)
		}

		if strings.Contains(fmt.Sprintf("%+v", status), secretValue) {
			t.Errorf("status of %s leaks the secret", status.Name)
		}
	}

	if statuses[2].Default != "the-default" || statuses[2].Doc != "has default" {
		t.Errorf("default status = %+v", statuses[2])
	}
}

func TestNewStoreJoinsKeyProblems(t *testing.T) {
	t.Parallel()

	good := "test.dup_" + suffix(t)

	_, err := config.NewStore(t.Context(), shared.Pool(), sharedKey, config.EnvLocal, []config.Key{
		{Name: "Bad Name"},
		{Name: good},
		{Name: good},
		{Name: "test.secret_default", Secret: true, Default: "x"},
		{Name: "test.secret_multiline", Secret: true, Multiline: true},
		{Name: "nodot"},
	})
	if err == nil {
		t.Fatal("NewStore accepted bad keys")
	}

	text := err.Error()
	for _, fragment := range []string{`"Bad Name"`, "declared twice", "cannot have a default", "cannot be multiline", `"nodot"`} {
		if !strings.Contains(text, fragment) {
			t.Errorf("error lacks %q: %v", fragment, err)
		}
	}
}

func TestNewStoreRejectsBadArguments(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		ctx    context.Context
		pool   *pgxpool.Pool
		master crypto.MasterKey
		env    config.Environment
		reason string
	}{
		{"nil pool", t.Context(), nil, sharedKey, config.EnvLocal, "nil pool"},
		{"unknown environment", t.Context(), shared.Pool(), sharedKey, config.Environment("staging"), "environment must be local or production"},
		{"zero master key", t.Context(), shared.Pool(), crypto.MasterKey{}, config.EnvLocal, "master key is not set"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := config.NewStore(tc.ctx, tc.pool, tc.master, tc.env, nil)
			if err == nil {
				t.Fatal("NewStore accepted bad arguments")
			}

			if !strings.Contains(err.Error(), tc.reason) {
				t.Errorf("error = %v, want it to contain %q", err, tc.reason)
			}
		})
	}
}

func TestWrongMasterKeyIsRefusedBeforeWriting(t *testing.T) {
	t.Parallel()

	own, err := pgtest.New(t.Context(), pgtest.Options{Sources: sourcesOf(t)})
	if err != nil {
		t.Fatalf("pgtest.New: %v", err)
	}

	t.Cleanup(func() {
		closeErr := own.Close(context.WithoutCancel(t.Context()))
		if closeErr != nil {
			t.Errorf("close clone: %v", closeErr)
		}
	})

	name := "test.secret_" + suffix(t)
	keys := []config.Key{{Name: name, Secret: true}}

	storeA, err := config.NewStore(t.Context(), own.Pool(), sharedKey, config.EnvLocal, keys)
	if err != nil {
		t.Fatalf("NewStore A: %v", err)
	}

	err = storeA.Set(t.Context(), name, "sealed-by-a")
	if err != nil {
		t.Fatalf("Set: %v", err)
	}

	before := snapshot(t, own)

	otherText, err := crypto.GenerateMasterKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	other, err := crypto.ParseMasterKey(otherText)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	_, err = config.NewStore(t.Context(), own.Pool(), other, config.EnvLocal, keys)
	if !errors.Is(err, config.ErrMasterKeyMismatch) {
		t.Fatalf("NewStore with another key = %v, want ErrMasterKeyMismatch", err)
	}

	if after := snapshot(t, own); after != before {
		t.Errorf("database changed: before %q, after %q", before, after)
	}
}

func TestFirstSecretWritesKeyCheckInOneStep(t *testing.T) {
	t.Parallel()

	own, err := pgtest.New(t.Context(), pgtest.Options{Sources: sourcesOf(t)})
	if err != nil {
		t.Fatalf("pgtest.New: %v", err)
	}

	t.Cleanup(func() {
		closeErr := own.Close(context.WithoutCancel(t.Context()))
		if closeErr != nil {
			t.Errorf("close clone: %v", closeErr)
		}
	})

	plainName := "test.plain_" + suffix(t)
	secretName := "test.secret_" + suffix(t)

	store, err := config.NewStore(t.Context(), own.Pool(), sharedKey, config.EnvLocal,
		[]config.Key{{Name: plainName}, {Name: secretName, Secret: true}})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	err = store.Set(t.Context(), plainName, "plain")
	if err != nil {
		t.Fatalf("Set plain: %v", err)
	}

	otherText, err := crypto.GenerateMasterKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	otherKey, err := crypto.ParseMasterKey(otherText)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	keys := []config.Key{{Name: plainName}, {Name: secretName, Secret: true}}

	_, err = config.NewStore(t.Context(), own.Pool(), otherKey, config.EnvLocal, keys)
	if err != nil {
		t.Errorf("a plain value bound the database to a master key: %v", err)
	}

	err = store.Set(t.Context(), secretName, "secret")
	if err != nil {
		t.Fatalf("Set secret: %v", err)
	}

	_, err = config.NewStore(t.Context(), own.Pool(), otherKey, config.EnvLocal, keys)
	if !errors.Is(err, config.ErrMasterKeyMismatch) {
		t.Errorf("NewStore with another key after the first secret = %v, want ErrMasterKeyMismatch", err)
	}
}

func TestSecondMasterKeyCannotSealAfterFirstSecret(t *testing.T) {
	t.Parallel()

	own, err := pgtest.New(t.Context(), pgtest.Options{Sources: sourcesOf(t)})
	if err != nil {
		t.Fatalf("pgtest.New: %v", err)
	}

	t.Cleanup(func() {
		closeErr := own.Close(context.WithoutCancel(t.Context()))
		if closeErr != nil {
			t.Errorf("close clone: %v", closeErr)
		}
	})

	name := "test.secret_" + suffix(t)
	keys := []config.Key{{Name: name, Secret: true}}

	// Both stores are built while the database is still empty, like two processes starting together.
	otherText, err := crypto.GenerateMasterKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	other, err := crypto.ParseMasterKey(otherText)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	first, err := config.NewStore(t.Context(), own.Pool(), sharedKey, config.EnvLocal, keys)
	if err != nil {
		t.Fatalf("NewStore first: %v", err)
	}

	second, err := config.NewStore(t.Context(), own.Pool(), other, config.EnvLocal, keys)
	if err != nil {
		t.Fatalf("NewStore second: %v", err)
	}

	err = first.Set(t.Context(), name, "by-first")
	if err != nil {
		t.Fatalf("Set first: %v", err)
	}

	before := snapshot(t, own)

	err = second.Set(t.Context(), name, "by-second")
	if !errors.Is(err, config.ErrMasterKeyMismatch) {
		t.Fatalf("Set with the other key = %v, want ErrMasterKeyMismatch", err)
	}

	if after := snapshot(t, own); after != before {
		t.Errorf("database changed: before %q, after %q", before, after)
	}

	got, err := first.Secret(t.Context(), name)
	if err != nil || got.Reveal() != "by-first" {
		t.Errorf("secret after the refused write = %v", err)
	}
}

func TestErrorsNeverIncludeSecretValues(t *testing.T) {
	t.Parallel()

	name := "test.secret_" + suffix(t)
	plain := "never-in-errors-" + suffix(t)
	store := newStore(t, config.EnvLocal, config.Key{
		Name: name, Secret: true,
		Validate: func(string, config.Environment) error { return errors.New("rejected by the validator") },
	})

	err := store.Set(t.Context(), name, plain)
	if err == nil {
		t.Fatal("validator did not reject")
	}

	if strings.Contains(err.Error(), plain) {
		t.Errorf("error leaks the value: %v", err)
	}

	if !strings.Contains(err.Error(), "rejected by the validator") {
		t.Errorf("validator message missing: %v", err)
	}
}

func TestSettingConstraintsRejectBadRows(t *testing.T) {
	t.Parallel()

	const checkViolation = "23514"

	rows := map[string]string{
		"malformed key": "INSERT INTO platform.setting (key, value, updated_at) VALUES ('Bad Key', 'x', now())",
		"both forms":    "INSERT INTO platform.setting (key, value, sealed, updated_at) VALUES ('test.both', 'x', '\x01', now())",
		"neither form":  "INSERT INTO platform.setting (key, updated_at) VALUES ('test.neither', now())",
	}

	for name, statement := range rows {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := shared.Pool().Exec(t.Context(), statement)

			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != checkViolation {
				t.Errorf("insert = %v, want check violation %s", err, checkViolation)
			}
		})
	}
}

func snapshot(t *testing.T, database *pgtest.Database) string {
	t.Helper()

	var settings, checks int

	err := database.Pool().QueryRow(t.Context(), `
		SELECT (SELECT count(*) FROM platform.setting), (SELECT count(*) FROM platform.key_check)`,
	).Scan(&settings, &checks)
	if err != nil {
		t.Fatalf("snapshot counts: %v", err)
	}

	var digest string

	err = database.Pool().QueryRow(t.Context(), `
		SELECT COALESCE((SELECT md5(string_agg(key || coalesce(value, '') || encode(coalesce(sealed, ''), 'hex') || updated_at::text, ',' ORDER BY key))
		FROM platform.setting), '') || COALESCE((SELECT encode(sealed, 'hex') FROM platform.key_check), '')`,
	).Scan(&digest)
	if err != nil {
		t.Fatalf("snapshot digest: %v", err)
	}

	return fmt.Sprintf("%s settings=%d checks=%d", digest, settings, checks)
}
