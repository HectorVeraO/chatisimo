package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/HectorVeraO/chatisimo/internal/platform/crypto"
	"github.com/HectorVeraO/chatisimo/internal/platform/pgtest"
)

var (
	shared    *pgtest.Database
	masterKey string
)

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	ctx := context.Background()

	key, err := crypto.GenerateMasterKey(rand.Reader)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	masterKey = key

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

// outcome is everything one invocation of run produced.
type outcome struct {
	code   int
	stdout string
	stderr string
}

// all is stdout and stderr together, for confidentiality searches.
func (o outcome) all() string {
	return o.stdout + o.stderr
}

// environment is the process environment of one invocation.
type environment map[string]string

func (e environment) lookup(name string) (string, bool) {
	value, isSet := e[name]

	return value, isSet
}

func envFor(database *pgtest.Database, appEnv string) environment {
	return environment{
		"DATABASE_URL":  database.URL().Reveal(),
		"MASTER_KEY":    masterKey,
		"CHATISIMO_ENV": appEnv,
	}
}

// invoke runs the CLI once.
func invoke(t *testing.T, env environment, stdin io.Reader, args ...string) outcome {
	t.Helper()

	var stdout, stderr bytes.Buffer

	code := run(t.Context(), args, stdin, &stdout, &stderr, env.lookup)

	return outcome{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

// newClone gives a test its own empty database.
func newClone(t *testing.T) *pgtest.Database {
	t.Helper()

	database, err := pgtest.New(t.Context(), pgtest.Options{})
	if err != nil {
		t.Fatalf("clone database: %v", err)
	}

	t.Cleanup(func() {
		closeErr := database.Close(context.Background())
		if closeErr != nil {
			t.Errorf("close clone: %v", closeErr)
		}
	})

	return database
}

// syntheticToken returns n random printable ASCII bytes without white space.
func syntheticToken(t *testing.T, n int) string {
	t.Helper()

	const first, span = 0x21, 0x7E - 0x21 + 1

	token := make([]byte, n)

	for i := range token {
		offset, err := rand.Int(rand.Reader, big.NewInt(span))
		if err != nil {
			t.Fatalf("random byte: %v", err)
		}

		token[i] = byte(first + int(offset.Int64())) //nolint:gosec // first + offset is at most 0x7E
	}

	return string(token)
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

type forbiddenReader struct {
	t *testing.T
}

func (r forbiddenReader) Read([]byte) (int, error) {
	r.t.Helper()
	r.t.Error("stdin was read")

	return 0, io.EOF
}

func TestVersionPrintsLinkTimeVersion(t *testing.T) {
	got := invoke(t, environment{}, strings.NewReader(""), "version")

	if got.code != 0 || got.stdout != version+"\n" || got.stderr != "" {
		t.Errorf("got %+v, want exit 0 and %q on stdout", got, version+"\n")
	}
}

func TestVersionFailsWhenStdoutFails(t *testing.T) {
	var stderr bytes.Buffer

	code := run(t.Context(), []string{"version"}, strings.NewReader(""), failingWriter{}, &stderr, environment{}.lookup)

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}

	if !strings.HasPrefix(stderr.String(), "chatisimo: ") {
		t.Errorf("stderr = %q, want a chatisimo: line", stderr.String())
	}
}

func TestUsageErrorsExitTwoWithoutTouchingTheEnvironment(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "no arguments", args: nil},
		{name: "unknown command", args: []string{"bogus"}},
		{name: "unknown flag", args: []string{"--bogus"}},
		{name: "config without subcommand", args: []string{"config"}},
		{name: "set without key", args: []string{"config", "set"}},
		{name: "get without key", args: []string{"config", "get"}},
		{name: "extra argument", args: []string{"config", "get", "discord.token", "extra"}},
		{name: "secret as set argument", args: []string{"config", "set", "discord.token", "synthetic-secret-value"}},
		{name: "unknown key on set", args: []string{"config", "set", "no.such_key"}},
		{name: "unknown key on get", args: []string{"config", "get", "no.such_key"}},
		{name: "unusual unicode key", args: []string{"config", "get", "k" + string(rune(0x301)) + string(rune(0x202E)) + string(rune(0x200B)) + string(rune(0x1F600))}},
		{name: "control characters in key", args: []string{"config", "set", "discord.token\x00\n"}},
		{name: "very long key", args: []string{"config", "get", strings.Repeat("a.", 5000)}},
		{name: "run is not a command yet", args: []string{"run"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := invoke(t, environment{}, forbiddenReader{t: t}, tc.args...)

			if got.code != 2 {
				t.Errorf("exit code = %d, want 2 (stderr=%q)", got.code, got.stderr)
			}

			if got.stdout != "" {
				t.Errorf("stdout = %q, want empty", got.stdout)
			}

			if !strings.HasPrefix(got.stderr, "chatisimo: ") {
				t.Errorf("stderr = %q, want a chatisimo: line", got.stderr)
			}
		})
	}
}

func TestHelpExitsZeroWithUsageOnStdoutAndRunsNothing(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"config", "--help"}, {"config", "set", "--help"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			got := invoke(t, environment{}, forbiddenReader{t: t}, args...)

			if got.code != 0 {
				t.Errorf("exit code = %d, want 0 (stderr=%q)", got.code, got.stderr)
			}

			if !strings.Contains(got.stdout, "Usage:") {
				t.Errorf("stdout = %q, want usage", got.stdout)
			}

			if got.stderr != "" {
				t.Errorf("stderr = %q, want empty", got.stderr)
			}
		})
	}
}

func TestKeygenPrintsAParsableKeyEachTime(t *testing.T) {
	first := invoke(t, environment{}, strings.NewReader(""), "keygen")
	second := invoke(t, environment{}, strings.NewReader(""), "keygen")

	for _, got := range []outcome{first, second} {
		if got.code != 0 || got.stderr != "" || !strings.HasSuffix(got.stdout, "\n") {
			t.Fatalf("got %+v, want exit 0 and one line", got)
		}

		_, err := crypto.ParseMasterKey(strings.TrimSuffix(got.stdout, "\n"))
		if err != nil {
			t.Errorf("keygen output does not parse: %v", err)
		}
	}

	if first.stdout == second.stdout {
		t.Error("two keygen runs printed the same key")
	}
}

func TestKeygenFailsWhenStdoutFails(t *testing.T) {
	var stderr bytes.Buffer

	code := run(t.Context(), []string{"keygen"}, strings.NewReader(""), failingWriter{}, &stderr, environment{}.lookup)

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
}

func TestMigrateAppliesEverySchemaAndIsRepeatable(t *testing.T) {
	database := newClone(t)
	env := envFor(database, "local")

	for range 2 {
		got := invoke(t, env, strings.NewReader(""), "migrate")

		if got.code != 0 || got.stdout != "migrations up to date\n" {
			t.Errorf("got %+v, want exit 0 and exactly the up-to-date line", got)
		}
	}

	for _, table := range []string{"platform.setting", "river.river_job"} {
		var exists bool

		err := database.Pool().QueryRow(t.Context(), "SELECT to_regclass($1) IS NOT NULL", table).Scan(&exists)
		if err != nil {
			t.Fatalf("look up %s: %v", table, err)
		}

		if !exists {
			t.Errorf("table %s does not exist after migrate", table)
		}
	}
}

func TestUnreachableDatabaseExitsOneWithoutLeakingTheURL(t *testing.T) {
	const password = "synthetic-password-marker-4"

	env := environment{
		"DATABASE_URL":  "postgres://user:" + password + "@127.0.0.1:1/chatisimo",
		"MASTER_KEY":    masterKey,
		"CHATISIMO_ENV": "local",
	}

	for _, args := range [][]string{{"migrate"}, {"config", "list"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()

			var stdout, stderr bytes.Buffer

			code := run(ctx, args, strings.NewReader(""), &stdout, &stderr, env.lookup)

			if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "chatisimo: ") {
				t.Errorf("got code %d, stdout %q, stderr %q, want exit 1 and a chatisimo: line", code, &stdout, &stderr)
			}

			if strings.Contains(stdout.String()+stderr.String(), password) {
				t.Error("output contains the password")
			}
		})
	}
}

func TestEnvironmentErrorsAreReportedTogetherWithoutValues(t *testing.T) {
	const (
		badURL = "not-a-url-marker-1"
		badKey = "not-a-key-marker-2"
		badEnv = "not-an-env-marker-3"
	)

	env := environment{"DATABASE_URL": badURL, "MASTER_KEY": badKey, "CHATISIMO_ENV": badEnv}

	for _, args := range [][]string{{"migrate"}, {"config", "list"}, {"config", "get", "discord.token"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			got := invoke(t, env, strings.NewReader(""), args...)

			lines := strings.Split(strings.TrimSuffix(got.stderr, "\n"), "\n")
			if got.code != 1 || len(lines) != 3 || got.stdout != "" {
				t.Fatalf("got %+v, want exit 1 and three stderr lines", got)
			}

			for _, line := range lines {
				if !strings.HasPrefix(line, "chatisimo: ") {
					t.Errorf("line %q lacks the chatisimo: prefix", line)
				}
			}

			for _, value := range []string{badURL, badKey, badEnv} {
				if strings.Contains(got.all(), value) {
					t.Errorf("output contains the value %q", value)
				}
			}
		})
	}
}

func TestMissingEnvironmentIsReported(t *testing.T) {
	got := invoke(t, environment{}, strings.NewReader(""), "migrate")

	if got.code != 1 || strings.Count(got.stderr, "chatisimo: ") != 3 {
		t.Errorf("got %+v, want exit 1 and three lines", got)
	}
}
