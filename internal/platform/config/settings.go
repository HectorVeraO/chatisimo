package config

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/HectorVeraO/chatisimo/internal/platform/config/internal/store"
	"github.com/HectorVeraO/chatisimo/internal/platform/crypto"
	"github.com/HectorVeraO/chatisimo/internal/platform/db"
)

// PurposeSettings is the HKDF purpose of sealed settings.
const PurposeSettings crypto.Purpose = "chatisimo/config-secret/v1"

const (
	keyCheckPlaintext = "chatisimo-key-check"
	keyCheckAAD       = "platform.key_check"
	settingAADPrefix  = "platform.setting:"
)

var (
	// ErrUnknownKey is returned for a name that is not in the registry.
	ErrUnknownKey = errors.New("config: unknown setting")
	// ErrNotSet is returned when a setting has no stored value and no default.
	ErrNotSet = errors.New("config: setting not set")
	// ErrWrongKind is returned when a secret is read as plain or a plain setting as secret.
	ErrWrongKind = errors.New("config: secret read as plain or plain read as secret")
	// ErrInvalidValue is returned for a value that fails validation.
	ErrInvalidValue = errors.New("config: invalid setting value")
	// ErrMasterKeyMismatch is returned when MASTER_KEY is not the key that sealed the stored secrets.
	ErrMasterKeyMismatch = errors.New("config: MASTER_KEY differs from the key that sealed the stored secrets")
)

var keyNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)

// Key declares one setting. Owners declare their keys; the composition root passes the full list
// to NewStore.
type Key struct {
	// Name is dotted lowercase, owner first: ^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$.
	Name string
	// Secret values are sealed at rest and never printed.
	Secret bool
	// Multiline allows interior line feeds ("\n") in a plain setting's input. Secrets cannot be
	// multiline.
	Multiline bool
	// Default is returned by Get when the key is unset; "" means no default. Secrets have none.
	Default string
	// Validate checks a value for env; nil accepts any non-empty value. It runs on Set and on
	// every Get or Secret, so a value that became invalid fails at startup. Its error must never
	// include the value.
	Validate func(value string, env Environment) error
	// Doc is the one line config list shows.
	Doc string
}

// Status describes one key for config list. It never holds a secret value.
type Status struct {
	Name    string
	Secret  bool
	Set     bool
	Default string
	// Value is the stored or default plain value; "[redacted]" for a set secret; "" otherwise.
	Value     string
	UpdatedAt *time.Time
	Doc       string
}

// Store reads and writes settings. Values are read at startup; a change needs a restart.
type Store struct {
	pool   *pgxpool.Pool
	sealer *crypto.Sealer
	env    Environment
	keys   map[string]Key
}

// notSetError reports one unset key and the command that sets it.
type notSetError struct {
	name string
}

func (e *notSetError) Error() string {
	return e.name + " is not set: run chatisimo config set " + e.name
}

func (e *notSetError) Is(target error) bool {
	return target == ErrNotSet
}

// MigrationSource returns the goose source of schema platform.
func MigrationSource() (*db.GooseSource, error) {
	files, err := store.Migrations()
	if err != nil {
		return nil, fmt.Errorf("config migrations: %w", err)
	}

	source, err := db.NewGooseSource("platform", files)
	if err != nil {
		return nil, fmt.Errorf("config migrations: %w", err)
	}

	return source, nil
}

// NewStore validates keys (name format, duplicates, a secret with a default; errors joined) and,
// when platform.key_check holds a row, checks that master opens it, returning
// ErrMasterKeyMismatch before anything is written.
func NewStore(
	ctx context.Context, pool *pgxpool.Pool, master crypto.MasterKey, env Environment, keys []Key,
) (*Store, error) {
	var problems []error

	if ctx == nil {
		problems = append(problems, errors.New("nil context"))
	}

	if pool == nil {
		problems = append(problems, errors.New("nil pool"))
	}

	isKnownEnv := env == EnvLocal || env == EnvProduction
	if !isKnownEnv {
		problems = append(problems, errors.New("environment must be local or production"))
	}

	keysByName, keysErr := indexKeys(keys)
	if keysErr != nil {
		problems = append(problems, keysErr)
	}

	if len(problems) > 0 {
		return nil, fmt.Errorf("new settings store: %w", errors.Join(problems...))
	}

	sealer, err := master.NewSealer(PurposeSettings)
	if err != nil {
		return nil, fmt.Errorf("new settings store: %w", err)
	}

	s := &Store{pool: pool, sealer: sealer, env: env, keys: keysByName}

	err = s.checkKeyCheck(ctx)
	if err != nil {
		return nil, err
	}

	return s, nil
}

// indexKeys validates every key and returns them by name, reporting all problems together.
func indexKeys(keys []Key) (map[string]Key, error) {
	var problems []error

	byName := make(map[string]Key, len(keys))

	for _, key := range keys {
		isValidName := keyNamePattern.MatchString(key.Name)
		if !isValidName {
			problems = append(problems, fmt.Errorf("setting %q: name must match %s", key.Name, keyNamePattern))
		}

		_, isDuplicate := byName[key.Name]
		if isDuplicate {
			problems = append(problems, fmt.Errorf("setting %q: declared twice", key.Name))
		}

		if key.Secret && key.Default != "" {
			problems = append(problems, fmt.Errorf("setting %q: a secret cannot have a default", key.Name))
		}

		if key.Secret && key.Multiline {
			problems = append(problems, fmt.Errorf("setting %q: a secret cannot be multiline", key.Name))
		}

		byName[key.Name] = key
	}

	if len(problems) > 0 {
		return nil, fmt.Errorf("invalid setting keys: %w", errors.Join(problems...))
	}

	return byName, nil
}

// checkKeyCheck returns ErrMasterKeyMismatch when a key check exists and the master key does not
// open it. It only reads.
func (s *Store) checkKeyCheck(ctx context.Context) error {
	sealed, found, err := readKeyCheck(ctx, s.pool)
	if err != nil {
		return err
	}

	if !found {
		return nil
	}

	return s.verifyKeyCheck(sealed)
}

func (s *Store) verifyKeyCheck(sealed []byte) error {
	plain, err := s.sealer.Open(sealed, []byte(keyCheckAAD))
	isMatch := err == nil && string(plain) == keyCheckPlaintext

	if !isMatch {
		return ErrMasterKeyMismatch
	}

	return nil
}

type queryRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func readKeyCheck(ctx context.Context, q queryRower) ([]byte, bool, error) {
	var sealed []byte

	err := q.QueryRow(ctx, "SELECT sealed FROM platform.key_check WHERE id").Scan(&sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}

	if err != nil {
		return nil, false, fmt.Errorf("read key check: %w", err)
	}

	return sealed, true, nil
}

// lookup returns the registered key or ErrUnknownKey naming it.
func (s *Store) lookup(name string) (Key, error) {
	key, found := s.keys[name]
	if !found {
		return Key{}, fmt.Errorf("setting %s: %w", name, ErrUnknownKey)
	}

	return key, nil
}

// validate runs the key's own validation, or the non-empty default one.
func (s *Store) validate(key Key, value string) error {
	if value == "" {
		return fmt.Errorf("setting %s: %w: value is empty", key.Name, ErrInvalidValue)
	}

	if key.Validate == nil {
		return nil
	}

	err := key.Validate(value, s.env)
	if err != nil {
		return fmt.Errorf("setting %s: %w: %s", key.Name, ErrInvalidValue, err.Error())
	}

	return nil
}

// Set validates and stores value for name. A secret is sealed with AAD "platform.setting:<name>";
// the first secret ever stored also writes platform.key_check in the same transaction.
func (s *Store) Set(ctx context.Context, name, value string) error {
	if ctx == nil {
		return errors.New("set setting: nil context")
	}

	key, err := s.lookup(name)
	if err != nil {
		return err
	}

	err = s.validate(key, value)
	if err != nil {
		return err
	}

	now := time.Now().UTC()

	if !key.Secret {
		return s.setPlain(ctx, name, value, now)
	}

	return s.setSecret(ctx, name, value, now)
}

func (s *Store) setPlain(ctx context.Context, name, value string, now time.Time) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO platform.setting (key, value, sealed, updated_at) VALUES ($1, $2, NULL, $3)
		ON CONFLICT (key) DO UPDATE
		SET value = EXCLUDED.value, sealed = NULL, updated_at = EXCLUDED.updated_at`,
		name, value, now)
	if err != nil {
		return fmt.Errorf("setting %s: store value: %w", name, err)
	}

	return nil
}

func (s *Store) setSecret(ctx context.Context, name, value string, now time.Time) error {
	sealed, err := s.sealer.Seal([]byte(value), []byte(settingAADPrefix+name))
	if err != nil {
		return fmt.Errorf("setting %s: seal value: %w", name, err)
	}

	check, err := s.sealer.Seal([]byte(keyCheckPlaintext), []byte(keyCheckAAD))
	if err != nil {
		return fmt.Errorf("setting %s: seal key check: %w", name, err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("setting %s: begin: %w", name, err)
	}

	// After a commit the rollback is a no-op; its error carries no information either way.
	defer func() {
		_ = tx.Rollback(context.WithoutCancel(ctx))
	}()

	err = s.ensureKeyCheck(ctx, tx, check, now)
	if err != nil {
		return fmt.Errorf("setting %s: %w", name, err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO platform.setting (key, value, sealed, updated_at) VALUES ($1, NULL, $2, $3)
		ON CONFLICT (key) DO UPDATE
		SET value = NULL, sealed = EXCLUDED.sealed, updated_at = EXCLUDED.updated_at`,
		name, sealed, now)
	if err != nil {
		return fmt.Errorf("setting %s: store sealed value: %w", name, err)
	}

	err = tx.Commit(ctx)
	if err != nil {
		return fmt.Errorf("setting %s: commit: %w", name, err)
	}

	return nil
}

// ensureKeyCheck records check when no key check exists yet, then verifies that the stored one
// was sealed by this store's master key. A mismatch is returned as ErrMasterKeyMismatch.
func (s *Store) ensureKeyCheck(ctx context.Context, tx pgx.Tx, check []byte, now time.Time) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO platform.key_check (sealed, created_at) VALUES ($1, $2)
		ON CONFLICT (id) DO NOTHING`, check, now)
	if err != nil {
		return fmt.Errorf("write key check: %w", err)
	}

	// A concurrent first writer with another master key may have won the insert above.
	existing, _, err := readKeyCheck(ctx, tx)
	if err != nil {
		return err
	}

	return s.verifyKeyCheck(existing)
}

// readRow returns the stored forms of name. found is false when no row exists.
func (s *Store) readRow(ctx context.Context, name string) (value *string, sealed []byte, found bool, err error) {
	err = s.pool.QueryRow(ctx,
		"SELECT value, sealed FROM platform.setting WHERE key = $1", name,
	).Scan(&value, &sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, false, nil
	}

	if err != nil {
		return nil, nil, false, fmt.Errorf("setting %s: read: %w", name, err)
	}

	return value, sealed, true, nil
}

// Get returns a plain key's stored value, else its default, else ErrNotSet.
func (s *Store) Get(ctx context.Context, name string) (string, error) {
	if ctx == nil {
		return "", errors.New("get setting: nil context")
	}

	key, err := s.lookup(name)
	if err != nil {
		return "", err
	}

	if key.Secret {
		return "", fmt.Errorf("setting %s: %w", name, ErrWrongKind)
	}

	value, _, found, err := s.readRow(ctx, name)
	if err != nil {
		return "", err
	}

	result := key.Default

	if found {
		if value == nil {
			return "", fmt.Errorf("setting %s: %w: stored value is sealed", name, ErrWrongKind)
		}

		result = *value
	}

	if result == "" {
		return "", fmt.Errorf("setting %s: %w", name, ErrNotSet)
	}

	err = s.validate(key, result)
	if err != nil {
		return "", err
	}

	return result, nil
}

// Secret returns a secret key's value, or ErrNotSet.
func (s *Store) Secret(ctx context.Context, name string) (Secret, error) {
	if ctx == nil {
		return Secret{}, errors.New("get secret: nil context")
	}

	key, err := s.lookup(name)
	if err != nil {
		return Secret{}, err
	}

	if !key.Secret {
		return Secret{}, fmt.Errorf("setting %s: %w", name, ErrWrongKind)
	}

	_, sealed, found, err := s.readRow(ctx, name)
	if err != nil {
		return Secret{}, err
	}

	if !found {
		return Secret{}, fmt.Errorf("setting %s: %w", name, ErrNotSet)
	}

	if sealed == nil {
		return Secret{}, fmt.Errorf("setting %s: %w: stored value is plain", name, ErrWrongKind)
	}

	plain, err := s.sealer.Open(sealed, []byte(settingAADPrefix+name))
	if err != nil {
		return Secret{}, fmt.Errorf("setting %s: open stored secret: %w", name, err)
	}

	err = s.validate(key, string(plain))
	if err != nil {
		return Secret{}, err
	}

	return NewSecret(string(plain)), nil
}

// Require returns one joined error naming every unset key without a default and the command
// that sets it ("discord.token is not set: run chatisimo config set discord.token").
func (s *Store) Require(ctx context.Context, names ...string) error {
	if ctx == nil {
		return errors.New("require settings: nil context")
	}

	var problems []error

	needed := make([]string, 0, len(names))

	for _, name := range names {
		key, err := s.lookup(name)
		if err != nil {
			problems = append(problems, err)

			continue
		}

		hasDefault := key.Default != ""
		if !hasDefault {
			needed = append(needed, name)
		}
	}

	stored, err := s.storedNames(ctx, needed)
	if err != nil {
		return fmt.Errorf("require settings: %w", errors.Join(append(problems, err)...))
	}

	for _, name := range needed {
		if !stored[name] {
			problems = append(problems, &notSetError{name: name})
		}
	}

	if len(problems) == 0 {
		return nil
	}

	return fmt.Errorf("require settings: %w", errors.Join(problems...))
}

// storedNames returns the subset of names that have a row.
func (s *Store) storedNames(ctx context.Context, names []string) (map[string]bool, error) {
	stored := make(map[string]bool, len(names))

	if len(names) == 0 {
		return stored, nil
	}

	rows, err := s.pool.Query(ctx, "SELECT key FROM platform.setting WHERE key = ANY($1)", names)
	if err != nil {
		return nil, fmt.Errorf("read settings: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var name string

		scanErr := rows.Scan(&name)
		if scanErr != nil {
			return nil, fmt.Errorf("read settings: %w", scanErr)
		}

		stored[name] = true
	}

	err = rows.Err()
	if err != nil {
		return nil, fmt.Errorf("read settings: %w", err)
	}

	return stored, nil
}

// List returns every registered key's Status, sorted by name.
func (s *Store) List(ctx context.Context) ([]Status, error) {
	if ctx == nil {
		return nil, errors.New("list settings: nil context")
	}

	rows, err := s.pool.Query(ctx, "SELECT key, value, updated_at FROM platform.setting")
	if err != nil {
		return nil, fmt.Errorf("list settings: %w", err)
	}
	defer rows.Close()

	type stored struct {
		value     *string
		updatedAt time.Time
	}

	byName := make(map[string]stored)

	for rows.Next() {
		var (
			name string
			row  stored
		)

		scanErr := rows.Scan(&name, &row.value, &row.updatedAt)
		if scanErr != nil {
			return nil, fmt.Errorf("list settings: %w", scanErr)
		}

		byName[name] = row
	}

	err = rows.Err()
	if err != nil {
		return nil, fmt.Errorf("list settings: %w", err)
	}

	names := make([]string, 0, len(s.keys))
	for name := range s.keys {
		names = append(names, name)
	}

	slices.Sort(names)

	statuses := make([]Status, 0, len(names))

	for _, name := range names {
		key := s.keys[name]
		status := Status{Name: name, Secret: key.Secret, Default: key.Default, Doc: key.Doc}

		row, isStored := byName[name]
		if isStored {
			updated := row.updatedAt
			status.Set = true
			status.UpdatedAt = &updated
		}

		status.Value = statusValue(key, row.value, isStored)

		statuses = append(statuses, status)
	}

	return statuses, nil
}

// statusValue is the plain value a listing may show for key.
func statusValue(key Key, stored *string, isStored bool) string {
	if key.Secret {
		if isStored {
			return redacted
		}

		return ""
	}

	if isStored && stored != nil {
		return *stored
	}

	return key.Default
}
