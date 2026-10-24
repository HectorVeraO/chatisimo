package logging_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/platform/config"
	"github.com/HectorVeraO/chatisimo/internal/platform/crypto"
	"github.com/HectorVeraO/chatisimo/internal/platform/logging"
)

const plain = "log-secret-value-5d2"

func newEnv(t *testing.T) (config.Env, string) {
	t.Helper()

	keyText, err := crypto.GenerateMasterKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	values := map[string]string{
		"DATABASE_URL":  "postgres://app:" + plain + "@localhost:10000/app",
		"MASTER_KEY":    keyText,
		"CHATISIMO_ENV": "local",
	}

	env, err := config.LoadEnv(func(name string) (string, bool) {
		value, found := values[name]

		return value, found
	})
	if err != nil {
		t.Fatalf("LoadEnv: %v", err)
	}

	return env, keyText
}

func TestSecretsAreRedactedInBothFormats(t *testing.T) {
	t.Parallel()

	env, keyText := newEnv(t)

	type holder struct {
		Token config.Secret
		Key   crypto.MasterKey
		Env   config.Env
	}

	for _, environment := range []config.Environment{config.EnvLocal, config.EnvProduction} {
		t.Run(string(environment), func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer

			logger := logging.New(&out, environment)
			logger.Info("record",
				slog.Any("secret", config.NewSecret(plain)),
				slog.Any("key", env.MasterKey),
				slog.Any("env", env),
				slog.Any("holder", holder{Token: config.NewSecret(plain), Key: env.MasterKey, Env: env}),
			)

			got := out.String()
			for _, leaked := range []string{plain, keyText, "localhost"} {
				if strings.Contains(got, leaked) {
					t.Errorf("log leaks %q: %s", leaked, got)
				}
			}

			if !strings.Contains(got, "[redacted]") {
				t.Errorf("log has no [redacted]: %s", got)
			}
		})
	}
}

func TestProductionLogsJSONAtInfo(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	logger := logging.New(&out, config.EnvProduction)
	logger.Debug("hidden")
	logger.Info("shown")

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("got %d records, want only the Info one: %q", len(lines), out.String())
	}

	var record map[string]any

	err := json.Unmarshal([]byte(lines[0]), &record)
	if err != nil {
		t.Fatalf("record is not JSON: %v", err)
	}

	if record["msg"] != "shown" {
		t.Errorf("msg = %v", record["msg"])
	}
}

func TestLocalLogsTextAtDebug(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	logger := logging.New(&out, config.EnvLocal)
	logger.Debug("visible")

	got := out.String()
	if !strings.Contains(got, "msg=visible") {
		t.Errorf("no text Debug record: %q", got)
	}

	if json.Valid([]byte(strings.TrimSpace(got))) {
		t.Error("local output is JSON, want text")
	}
}

func TestUnknownEnvironmentGetsProductionLogger(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	logger := logging.New(&out, config.Environment("staging"))

	enabled := logger.Enabled(context.Background(), slog.LevelDebug)
	if enabled {
		t.Error("Debug enabled for an unknown environment")
	}
}

func TestNewPanicsOnNilWriter(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Error("New accepted a nil writer")
		}
	}()

	logging.New(nil, config.EnvLocal)
}
