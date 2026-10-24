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

	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%d"} {
		got := fmt.Sprintf(verb, secret) + fmt.Sprintf(verb, holder{Token: secret}) + fmt.Sprintf(verb, &secret)
		if strings.Contains(got, plain) {
			t.Errorf("%s leaks the value: %s", verb, got)
		}

		if !strings.Contains(got, "[redacted]") {
			t.Errorf("%s = %q, want [redacted]", verb, got)
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
