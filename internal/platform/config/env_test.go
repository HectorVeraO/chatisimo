package config_test

import (
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/platform/config"
	"github.com/HectorVeraO/chatisimo/internal/platform/crypto"
)

const (
	urlMarker = "pw-marker-7f3a"
	validURL  = "postgres://app:" + urlMarker + "@localhost:10000/app"
)

func newKeyText(t *testing.T) string {
	t.Helper()

	text, err := crypto.GenerateMasterKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate master key: %v", err)
	}

	return text
}

func lookupFrom(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, found := values[name]

		return value, found
	}
}

func envErrors(t *testing.T, err error) map[string]string {
	t.Helper()

	var joined interface{ Unwrap() []error }
	if !errors.As(err, &joined) {
		t.Fatalf("error %v holds no joined errors", err)
	}

	reasons := make(map[string]string)

	for _, inner := range joined.Unwrap() {
		var envErr *config.EnvError
		if !errors.As(inner, &envErr) {
			t.Fatalf("%v is not an *EnvError", inner)
		}

		reasons[envErr.Name] = envErr.Reason
	}

	return reasons
}

func TestLoadEnvAcceptsValidValues(t *testing.T) {
	t.Parallel()

	env, err := config.LoadEnv(lookupFrom(map[string]string{
		"DATABASE_URL":  validURL,
		"MASTER_KEY":    newKeyText(t),
		"CHATISIMO_ENV": "production",
	}))
	if err != nil {
		t.Fatalf("LoadEnv: %v", err)
	}

	if env.DatabaseURL.Reveal() != validURL {
		t.Error("DatabaseURL did not round trip")
	}

	if env.Environment != config.EnvProduction {
		t.Errorf("Environment = %q, want production", env.Environment)
	}

	_, err = env.MasterKey.NewSealer(config.PurposeSettings)
	if err != nil {
		t.Errorf("master key is not usable: %v", err)
	}
}

func TestLoadEnvReportsEachBadVariable(t *testing.T) {
	t.Parallel()

	good := func() map[string]string {
		return map[string]string{
			"DATABASE_URL":  validURL,
			"MASTER_KEY":    newKeyText(t),
			"CHATISIMO_ENV": "local",
		}
	}

	badURL := "postgres://app:" + urlMarker + "@localhost:notaport/app"

	cases := []struct {
		name     string
		variable string
		set      bool
		value    string
		reason   string
	}{
		{"database url unset", "DATABASE_URL", false, "", "is not set"},
		{"database url empty", "DATABASE_URL", true, "", "is empty"},
		{"database url invalid", "DATABASE_URL", true, badURL, "is not a valid Postgres connection string"},
		{"master key unset", "MASTER_KEY", false, "", "is not set"},
		{"master key empty", "MASTER_KEY", true, "", "is empty"},
		{"master key short", "MASTER_KEY", true, "c2hvcnQ=", "decodes to 5 bytes"},
		{"environment unset", "CHATISIMO_ENV", false, "", "is not set"},
		{"environment empty", "CHATISIMO_ENV", true, "", "is empty"},
		{"environment unknown", "CHATISIMO_ENV", true, "staging", "must be local or production"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			values := good()
			if tc.set {
				values[tc.variable] = tc.value
			} else {
				delete(values, tc.variable)
			}

			_, err := config.LoadEnv(lookupFrom(values))
			if err == nil {
				t.Fatal("LoadEnv accepted a bad variable")
			}

			reasons := envErrors(t, err)
			if len(reasons) != 1 {
				t.Fatalf("got %d errors, want 1: %v", len(reasons), err)
			}

			if !strings.Contains(reasons[tc.variable], tc.reason) {
				t.Errorf("reason for %s = %q, want it to contain %q", tc.variable, reasons[tc.variable], tc.reason)
			}

			for _, leaked := range []string{tc.value, urlMarker} {
				if leaked != "" && strings.Contains(err.Error(), leaked) {
					t.Errorf("error leaks an input value: %v", err)
				}
			}
		})
	}
}

func TestLoadEnvReportsAllThreeAtOnce(t *testing.T) {
	t.Parallel()

	badKey := "short-key-value-marker"

	_, err := config.LoadEnv(lookupFrom(map[string]string{
		"DATABASE_URL":  "postgres://app:" + urlMarker + "@localhost:notaport/app",
		"MASTER_KEY":    badKey,
		"CHATISIMO_ENV": "staging",
	}))
	if err == nil {
		t.Fatal("LoadEnv accepted three bad variables")
	}

	reasons := envErrors(t, err)
	if len(reasons) != 3 {
		t.Fatalf("got %d errors, want 3: %v", len(reasons), err)
	}

	for _, leaked := range []string{urlMarker, badKey, "staging"} {
		if strings.Contains(err.Error(), leaked) {
			t.Errorf("error leaks %q: %v", leaked, err)
		}
	}
}

func TestLoadEnvRejectsNilLookup(t *testing.T) {
	t.Parallel()

	_, err := config.LoadEnv(nil)
	if err == nil {
		t.Fatal("LoadEnv accepted a nil lookup")
	}
}

func TestEnvErrorTextIsNameAndReason(t *testing.T) {
	t.Parallel()

	got := (&config.EnvError{Name: "MASTER_KEY", Reason: "is empty"}).Error()
	if got != "MASTER_KEY is empty" {
		t.Errorf("Error() = %q", got)
	}
}

func TestEnvRendersRedacted(t *testing.T) {
	t.Parallel()

	env, err := config.LoadEnv(lookupFrom(map[string]string{
		"DATABASE_URL":  validURL,
		"MASTER_KEY":    newKeyText(t),
		"CHATISIMO_ENV": "local",
	}))
	if err != nil {
		t.Fatalf("LoadEnv: %v", err)
	}

	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		got := fmt.Sprintf(verb, env)
		if strings.Contains(got, urlMarker) || strings.Contains(got, "localhost") {
			t.Errorf("%s leaks the connection string: %s", verb, got)
		}

		if !strings.Contains(got, "[redacted]") {
			t.Errorf("%s = %q, want [redacted]", verb, got)
		}
	}
}
