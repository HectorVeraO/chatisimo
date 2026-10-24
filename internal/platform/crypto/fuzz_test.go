package crypto_test

import (
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/platform/crypto"
)

func FuzzParseMasterKey(f *testing.F) {
	valid, err := crypto.GenerateMasterKey(rand.Reader)
	if err != nil {
		f.Fatalf("generate seed: %v", err)
	}

	f.Add(valid)
	f.Add("")
	f.Add(valid[:43])
	f.Add(valid + "A")
	f.Add(valid + "\n")
	f.Add(valid[:20] + "\n" + valid[20:])
	f.Add(strings.TrimRight(valid, "="))
	f.Add(strings.Repeat("-", 43) + "=")
	f.Add(strings.Repeat("_", 43) + "=")
	f.Add(base64.StdEncoding.EncodeToString(make([]byte, 31)))
	f.Add(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	f.Add(base64.StdEncoding.EncodeToString(make([]byte, 33)))
	f.Add(strings.Repeat("A", 42) + "B=")
	f.Add(string([]rune{0x202e, 0x200b, 'e', 0x301}))
	f.Add(strings.Repeat("🔑", 11))

	f.Fuzz(func(t *testing.T, text string) {
		key, err := crypto.ParseMasterKey(text)
		if err != nil {
			return
		}

		if len(text) != 44 {
			t.Fatalf("accepted a key of %d characters", len(text))
		}

		decoded, err := base64.StdEncoding.DecodeString(text)
		if err != nil {
			t.Fatalf("accepted text the standard decoder rejects: %v", err)
		}

		reencoded := base64.StdEncoding.EncodeToString(decoded)
		if reencoded != text {
			t.Fatalf("accepted a non-canonical encoding")
		}

		sealer, err := key.NewSealer("chatisimo/fuzz/v1")
		if err != nil {
			t.Fatalf("accepted key cannot build a sealer: %v", err)
		}

		sealed, err := sealer.Seal([]byte("value"), nil)
		if err != nil {
			t.Fatalf("seal: %v", err)
		}

		opened, err := sealer.Open(sealed, nil)
		if err != nil || string(opened) != "value" {
			t.Fatalf("accepted key does not round trip")
		}
	})
}
