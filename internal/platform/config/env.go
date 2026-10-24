// Package config loads the process environment and the settings stored in schema platform.
package config

import (
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/HectorVeraO/chatisimo/internal/platform/crypto"
)

const (
	envDatabaseURL = "DATABASE_URL"
	envMasterKey   = "MASTER_KEY"
	envEnvironment = "CHATISIMO_ENV"
)

// Environment names the deployment.
type Environment string

const (
	// EnvLocal is a developer machine or a test run.
	EnvLocal Environment = "local"
	// EnvProduction is the deployed process.
	EnvProduction Environment = "production"
)

// Env holds the only three values the process reads from its environment.
type Env struct {
	DatabaseURL Secret
	MasterKey   crypto.MasterKey
	Environment Environment
}

// String renders the Env with its secrets redacted.
func (e Env) String() string {
	return fmt.Sprintf("{database_url=%s master_key=%s environment=%s}", redacted, redacted, string(e.Environment))
}

// GoString renders the Env with its secrets redacted.
func (e Env) GoString() string {
	return "config.Env" + e.String()
}

// Format writes the redacted rendering for every verb.
func (e Env) Format(f fmt.State, _ rune) {
	// A failed write to the formatter's destination cannot be reported from Format.
	_, _ = io.WriteString(f, e.String())
}

// LogValue renders a group with the secrets redacted.
func (e Env) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("database_url", redacted),
		slog.String("master_key", redacted),
		slog.String("environment", string(e.Environment)),
	)
}

// EnvError reports one variable by name and reason; it never includes the value.
type EnvError struct {
	Name   string
	Reason string
}

// Error returns "<NAME> <reason>".
func (e *EnvError) Error() string {
	return e.Name + " " + e.Reason
}

// LoadEnv reads DATABASE_URL, MASTER_KEY and CHATISIMO_ENV through lookup (os.LookupEnv in
// production) and returns every unset, empty or invalid variable at once (errors.Join of
// *EnvError). DATABASE_URL must pass pgxpool.ParseConfig; MASTER_KEY must pass
// crypto.ParseMasterKey; CHATISIMO_ENV must be local or production.
func LoadEnv(lookup func(string) (string, bool)) (Env, error) {
	if lookup == nil {
		return Env{}, errors.New("load environment: nil lookup")
	}

	var (
		env      Env
		problems []error
	)

	databaseURL, problem := readVariable(lookup, envDatabaseURL)
	if problem == nil {
		problem = checkDatabaseURL(databaseURL)
	}

	if problem != nil {
		problems = append(problems, problem)
	} else {
		env.DatabaseURL = NewSecret(databaseURL)
	}

	masterText, problem := readVariable(lookup, envMasterKey)
	if problem == nil {
		env.MasterKey, problem = parseMasterKey(masterText)
	}

	if problem != nil {
		problems = append(problems, problem)
	}

	environment, problem := readVariable(lookup, envEnvironment)
	if problem == nil {
		env.Environment, problem = parseEnvironment(environment)
	}

	if problem != nil {
		problems = append(problems, problem)
	}

	if len(problems) > 0 {
		return Env{}, fmt.Errorf("load environment: %w", errors.Join(problems...))
	}

	return env, nil
}

// readVariable returns the value of name, or an EnvError when it is unset or empty.
func readVariable(lookup func(string) (string, bool), name string) (string, *EnvError) {
	value, isSet := lookup(name)
	if !isSet {
		return "", &EnvError{Name: name, Reason: "is not set"}
	}

	if value == "" {
		return "", &EnvError{Name: name, Reason: "is empty"}
	}

	return value, nil
}

// checkDatabaseURL never wraps pgx's error: its text can echo the connection string.
func checkDatabaseURL(value string) *EnvError {
	_, err := pgxpool.ParseConfig(value)
	if err != nil {
		return &EnvError{Name: envDatabaseURL, Reason: "is not a valid Postgres connection string"}
	}

	return nil
}

// parseMasterKey uses the crypto problem text, which names the problem and never the input.
func parseMasterKey(value string) (crypto.MasterKey, *EnvError) {
	key, err := crypto.ParseMasterKey(value)
	if err != nil {
		return crypto.MasterKey{}, &EnvError{Name: envMasterKey, Reason: err.Error()}
	}

	return key, nil
}

func parseEnvironment(value string) (Environment, *EnvError) {
	environment := Environment(value)

	isKnown := environment == EnvLocal || environment == EnvProduction
	if !isKnown {
		return "", &EnvError{Name: envEnvironment, Reason: "must be local or production"}
	}

	return environment, nil
}
