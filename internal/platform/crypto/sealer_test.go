package crypto_test

import (
	"bytes"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/platform/crypto"
)

const testPurpose crypto.Purpose = "chatisimo/test/v1"

func newSealer(t *testing.T, key crypto.MasterKey, p crypto.Purpose) *crypto.Sealer {
	t.Helper()

	sealer, err := key.NewSealer(p)
	if err != nil {
		t.Fatalf("new sealer: %v", err)
	}

	return sealer
}

func TestSealOpenRoundTrip(t *testing.T) {
	key, _ := mustParse(t)
	sealer := newSealer(t, key, testPurpose)
	aad := []byte("context")

	big := make([]byte, 64*1024)

	_, err := rand.Read(big)
	if err != nil {
		t.Fatalf("random: %v", err)
	}

	cases := map[string][]byte{
		"an empty plaintext": {},
		"a short plaintext":  []byte("synthetic value"),
		"a 64 KiB plaintext": big,
	}

	for name, plaintext := range cases {
		t.Run(name, func(t *testing.T) {
			sealed, err := sealer.Seal(plaintext, aad)
			if err != nil {
				t.Fatalf("seal: %v", err)
			}

			if sealed[0] != 0x01 {
				t.Errorf("version byte = %#x, want 0x01", sealed[0])
			}

			got, err := sealer.Open(sealed, aad)
			if err != nil {
				t.Fatalf("open: %v", err)
			}

			if !bytes.Equal(got, plaintext) {
				t.Errorf("round trip changed the value")
			}
		})
	}
}

func TestSealOpenWithNilAAD(t *testing.T) {
	key, _ := mustParse(t)
	sealer := newSealer(t, key, testPurpose)

	sealed, err := sealer.Seal([]byte("v"), nil)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	got, err := sealer.Open(sealed, []byte{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	if string(got) != "v" {
		t.Errorf("round trip changed the value")
	}
}

func TestSealIsRandomized(t *testing.T) {
	key, _ := mustParse(t)
	sealer := newSealer(t, key, testPurpose)

	first, err := sealer.Seal([]byte("same"), nil)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	second, err := sealer.Seal([]byte("same"), nil)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	if bytes.Equal(first, second) {
		t.Errorf("two seals of the same plaintext are equal")
	}
}

func TestSealedValueDoesNotContainPlaintext(t *testing.T) {
	key, _ := mustParse(t)
	sealer := newSealer(t, key, testPurpose)
	plaintext := []byte("synthetic-plaintext-marker")

	sealed, err := sealer.Seal(plaintext, nil)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	if bytes.Contains(sealed, plaintext) {
		t.Errorf("sealed value contains the plaintext")
	}
}

func TestOpenRejectsTampering(t *testing.T) {
	key, _ := mustParse(t)
	sealer := newSealer(t, key, testPurpose)
	aad := []byte("context")

	sealed, err := sealer.Seal([]byte("synthetic value"), aad)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	// Layout: version (1) | nonce (12) | ciphertext | tag (16).
	regions := map[string]int{
		"a flipped version byte":    0,
		"a flipped nonce byte":      1,
		"a flipped ciphertext byte": 13,
		"a flipped tag byte":        len(sealed) - 1,
	}

	for name, index := range regions {
		t.Run("rejects "+name, func(t *testing.T) {
			tampered := bytes.Clone(sealed)
			tampered[index] ^= 0x01

			_, err := sealer.Open(tampered, aad)
			if !errors.Is(err, crypto.ErrOpen) {
				t.Errorf("err = %v, want ErrOpen", err)
			}
		})
	}

	for _, version := range []byte{0x00, 0x02, 0xff} {
		t.Run("rejects an unknown version byte", func(t *testing.T) {
			tampered := bytes.Clone(sealed)
			tampered[0] = version

			_, err := sealer.Open(tampered, aad)
			if !errors.Is(err, crypto.ErrOpen) {
				t.Errorf("version %#x: err = %v, want ErrOpen", version, err)
			}
		})
	}

	for _, size := range []int{0, 1, 28} {
		t.Run("rejects truncated input", func(t *testing.T) {
			_, err := sealer.Open(sealed[:size], aad)
			if !errors.Is(err, crypto.ErrOpen) {
				t.Errorf("size %d: err = %v, want ErrOpen", size, err)
			}
		})
	}

	t.Run("rejects nil input", func(t *testing.T) {
		_, err := sealer.Open(nil, aad)
		if !errors.Is(err, crypto.ErrOpen) {
			t.Errorf("err = %v, want ErrOpen", err)
		}
	})

	t.Run("rejects a missing last byte", func(t *testing.T) {
		_, err := sealer.Open(sealed[:len(sealed)-1], aad)
		if !errors.Is(err, crypto.ErrOpen) {
			t.Errorf("err = %v, want ErrOpen", err)
		}
	})

	t.Run("rejects wrong aad", func(t *testing.T) {
		_, err := sealer.Open(sealed, []byte("other context"))
		if !errors.Is(err, crypto.ErrOpen) {
			t.Errorf("err = %v, want ErrOpen", err)
		}
	})
}

func TestOpenRejectsOtherSealers(t *testing.T) {
	key, _ := mustParse(t)
	otherKey, _ := mustParse(t)
	sealer := newSealer(t, key, testPurpose)

	sealed, err := sealer.Seal([]byte("synthetic value"), nil)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	t.Run("rejects a sealer of another purpose", func(t *testing.T) {
		other := newSealer(t, key, "chatisimo/other/v1")

		_, err := other.Open(sealed, nil)
		if !errors.Is(err, crypto.ErrOpen) {
			t.Errorf("err = %v, want ErrOpen", err)
		}
	})

	t.Run("rejects a sealer of another version of the purpose", func(t *testing.T) {
		other := newSealer(t, key, "chatisimo/test/v2")

		_, err := other.Open(sealed, nil)
		if !errors.Is(err, crypto.ErrOpen) {
			t.Errorf("err = %v, want ErrOpen", err)
		}
	})

	t.Run("rejects a sealer of another key", func(t *testing.T) {
		other := newSealer(t, otherKey, testPurpose)

		_, err := other.Open(sealed, nil)
		if !errors.Is(err, crypto.ErrOpen) {
			t.Errorf("err = %v, want ErrOpen", err)
		}
	})

	t.Run("opens with a second sealer of the same key and purpose", func(t *testing.T) {
		same := newSealer(t, key, testPurpose)

		got, err := same.Open(sealed, nil)
		if err != nil {
			t.Fatalf("open: %v", err)
		}

		if string(got) != "synthetic value" {
			t.Errorf("round trip changed the value")
		}
	})
}

func TestNewSealerRejectsInvalidInput(t *testing.T) {
	key, _ := mustParse(t)

	purposes := map[string]crypto.Purpose{
		"rejects an empty purpose":          "",
		"rejects a purpose without version": "chatisimo/x",
		"rejects a foreign prefix":          "other/x/v1",
		"rejects version zero":              "chatisimo/x/v0",
		"rejects an uppercase use":          "chatisimo/X/v1",
		"rejects a trailing newline":        "chatisimo/x/v1\n",
		"rejects an empty use":              "chatisimo//v1",
	}

	for name, purpose := range purposes {
		t.Run(name, func(t *testing.T) {
			_, err := key.NewSealer(purpose)
			if err == nil {
				t.Errorf("expected an error")
			}
		})
	}

	t.Run("rejects a zero master key", func(t *testing.T) {
		var zero crypto.MasterKey

		_, err := zero.NewSealer(testPurpose)
		if err == nil {
			t.Errorf("expected an error")
		}
	})
}

func TestNilSealerReturnsErrors(t *testing.T) {
	var sealer *crypto.Sealer

	_, err := sealer.Seal([]byte("v"), nil)
	if err == nil {
		t.Errorf("Seal on a nil sealer: expected an error")
	}

	_, err = sealer.Open(make([]byte, 64), nil)
	if err == nil {
		t.Errorf("Open on a nil sealer: expected an error")
	}
}
