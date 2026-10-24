package config_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/platform/config"
)

func TestSecretRendersRedactedEverywhere(t *testing.T) {
	t.Parallel()

	const plain = "secret-plain-value-91c"

	secret := config.NewSecret(plain)

	if secret.Reveal() != plain {
		t.Fatal("Reveal did not return the value")
	}

	type holder struct {
		Token config.Secret
	}

	renders := map[string]any{
		"value":   secret,
		"field":   holder{Token: secret},
		"pointer": &secret,
	}

	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%d"} {
		for form, arg := range renders {
			got := fmt.Sprintf(verb, arg)
			if strings.Contains(got, plain) {
				t.Errorf("%s of %s leaks the value: %s", verb, form, got)
			}

			if !strings.Contains(got, "[redacted]") {
				t.Errorf("%s of %s = %q, want [redacted]", verb, form, got)
			}
		}
	}

	encoded, err := json.Marshal(holder{Token: secret})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if string(encoded) != `{"Token":"[redacted]"}` {
		t.Errorf("json = %s", encoded)
	}
}
