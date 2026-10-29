package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"
)

// restLimiterCleanup is the goroutine disgo starts for every REST client and never stops.
const restLimiterCleanup = "github.com/disgoorg/disgo/rest.(*rateLimiterImpl).cleanup"

// noLeaks fails the test when it leaves a goroutine behind. Call it first: its check runs after
// every other cleanup of the test.
func noLeaks(t *testing.T) {
	t.Helper()

	baseline := goleak.IgnoreCurrent()

	t.Cleanup(func() {
		goleak.VerifyNone(t, baseline, goleak.IgnoreAnyFunction(restLimiterCleanup))
	})
}

// maxStartupAbort is how long a startup may take to give up once its context has ended.
const maxStartupAbort = 10 * time.Second

// storeSetting stores one setting through the CLI, as an operator would.
func storeSetting(t *testing.T, env environment, name, value string) {
	t.Helper()

	got := invoke(t, env, strings.NewReader(value+"\n"), "config", "set", name)
	if got.code != 0 {
		t.Fatalf("config set %s: got %+v, want exit 0", name, got)
	}
}

// assertNoneOf fails when the output of got contains any of the secrets.
func assertNoneOf(t *testing.T, got outcome, secrets ...string) {
	t.Helper()

	for _, secret := range secrets {
		if strings.Contains(got.all(), secret) {
			t.Error("output contains a secret value")
		}
	}
}

func TestRunWithoutTokenFailsNamingTheSettingCommand(t *testing.T) {
	noLeaks(t)

	database := newClone(t)
	env := envFor(database, "local")

	got := invoke(t, env, strings.NewReader(""), "run")

	if got.code != 1 || got.stdout != "" {
		t.Fatalf("got code %d and stdout %q, want exit 1 and empty stdout", got.code, got.stdout)
	}

	if !strings.Contains(got.stderr, "chatisimo: ") {
		t.Errorf("stderr = %q, want a chatisimo: line", got.stderr)
	}

	if !strings.Contains(got.stderr, "run chatisimo config set discord.token") {
		t.Errorf("stderr = %q, want the command that sets the token", got.stderr)
	}

	if !strings.Contains(got.stderr, "run chatisimo config set llm.api_key") {
		t.Errorf("stderr = %q, want the command that sets llm.api_key", got.stderr)
	}

	assertNoneOf(t, got, masterKey, database.URL().Reveal())
}

func TestRunRejectsAnAPIBaseURLThatProductionForbids(t *testing.T) {
	noLeaks(t)

	const storedURL = "http://127.0.0.1:10010/api/v10"

	database := newClone(t)
	local := envFor(database, "local")
	token := syntheticToken(t, 48)

	storeSetting(t, local, "discord.token", token)
	storeSetting(t, local, "llm.api_key", token)
	storeSetting(t, local, "discord.api_base_url", storedURL)

	got := invoke(t, envFor(database, "production"), strings.NewReader(""), "run")

	if got.code != 1 || got.stdout != "" {
		t.Fatalf("got code %d and stdout %q, want exit 1 and empty stdout", got.code, got.stdout)
	}

	if !strings.Contains(got.stderr, "discord.api_base_url") {
		t.Errorf("stderr = %q, want the name of the invalid setting", got.stderr)
	}

	assertNoneOf(t, got, storedURL, token, masterKey, database.URL().Reveal())
}

func TestRunRejectsAnLLMBaseURLThatProductionForbids(t *testing.T) {
	noLeaks(t)

	const storedLLMURL = "http://127.0.0.1:10010/v1/publishers/google/models"

	database := newClone(t)
	local := envFor(database, "local")
	token := syntheticToken(t, 48)

	storeSetting(t, local, "discord.token", token)
	storeSetting(t, local, "llm.api_key", token)
	storeSetting(t, local, "llm.base_url", storedLLMURL)

	got := invoke(t, envFor(database, "production"), strings.NewReader(""), "run")

	if got.code != 1 || got.stdout != "" {
		t.Fatalf("got code %d and stdout %q, want exit 1 and empty stdout", got.code, got.stdout)
	}

	if !strings.Contains(got.stderr, "llm.base_url") {
		t.Errorf("stderr = %q, want the name of the invalid setting", got.stderr)
	}

	assertNoneOf(t, got, storedLLMURL, token, masterKey, database.URL().Reveal())
}

func TestRunRejectsAnAIzaLLMKeyThatProductionForbids(t *testing.T) {
	noLeaks(t)

	const aizaKey = "AIza-synthetic-test-key"

	database := newClone(t)
	local := envFor(database, "local")
	token := syntheticToken(t, 48)

	storeSetting(t, local, "discord.token", token)
	storeSetting(t, local, "llm.api_key", aizaKey)

	got := invoke(t, envFor(database, "production"), strings.NewReader(""), "run")

	if got.code != 1 || got.stdout != "" {
		t.Fatalf("got code %d and stdout %q, want exit 1 and empty stdout", got.code, got.stdout)
	}

	if !strings.Contains(got.stderr, "llm.api_key") {
		t.Errorf("stderr = %q, want the name of the invalid setting", got.stderr)
	}

	assertNoneOf(t, got, aizaKey, "synthetic-test-key", token, masterKey, database.URL().Reveal())
}

func TestRunReportsEveryEnvironmentErrorWithoutValues(t *testing.T) {
	noLeaks(t)

	const (
		badURL = "not-a-url-marker-1"
		badKey = "not-a-key-marker-2"
		badEnv = "not-an-env-marker-3"
	)

	tests := []struct {
		name      string
		env       environment
		wantLines int
	}{
		{name: "nothing set", env: environment{}, wantLines: 3},
		{
			name:      "every variable invalid",
			env:       environment{"DATABASE_URL": badURL, "MASTER_KEY": badKey, "CHATISIMO_ENV": badEnv},
			wantLines: 3,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := invoke(t, tc.env, strings.NewReader(""), "run")

			lines := strings.Split(strings.TrimSuffix(got.stderr, "\n"), "\n")
			if got.code != 1 || got.stdout != "" || len(lines) != tc.wantLines {
				t.Fatalf("got %+v, want exit 1 and %d stderr lines", got, tc.wantLines)
			}

			for _, line := range lines {
				if !strings.HasPrefix(line, "chatisimo: ") {
					t.Errorf("line %q lacks the chatisimo: prefix", line)
				}
			}

			for _, name := range []string{"DATABASE_URL", "MASTER_KEY", "CHATISIMO_ENV"} {
				namingLines := 0

				for _, line := range lines {
					if strings.Contains(line, name) {
						namingLines++
					}
				}

				if namingLines != 1 {
					t.Errorf("%d stderr lines name %s, want exactly 1 (stderr=%q)", namingLines, name, got.stderr)
				}
			}

			assertNoneOf(t, got, badURL, badKey, badEnv)
		})
	}
}

func TestRunWithAnUnreachableDatabaseFailsWithoutLeakingTheURL(t *testing.T) {
	noLeaks(t)

	const password = "synthetic-password-marker-5"

	env := environment{
		"DATABASE_URL":  "postgres://user:" + password + "@127.0.0.1:1/chatisimo",
		"MASTER_KEY":    masterKey,
		"CHATISIMO_ENV": "local",
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	var stdout, stderr bytes.Buffer

	started := time.Now()
	code := run(ctx, []string{"run"}, strings.NewReader(""), &stdout, &stderr, env.lookup)
	elapsed := time.Since(started)

	// Ending the context aborts the connection retries, which otherwise last 30 s.
	if elapsed > maxStartupAbort {
		t.Errorf("run took %s to give up after its context ended, want under %s", elapsed, maxStartupAbort)
	}

	got := outcome{code: code, stdout: stdout.String(), stderr: stderr.String()}
	if got.code != 1 || got.stdout != "" || !strings.Contains(got.stderr, "chatisimo: ") {
		t.Errorf("got %+v, want exit 1, empty stdout and a chatisimo: line", got)
	}

	assertNoneOf(t, got, password)
}
