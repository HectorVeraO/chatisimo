package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
)

const (
	versionByte byte = 0x01
	// Version byte, 12-byte nonce and 16-byte tag.
	minSealedSize = 1 + 12 + 16
)

// ErrOpen is returned by Open for a wrong key, wrong aad, unknown version or any tampering.
var ErrOpen = errors.New("crypto: cannot open sealed value")

var purposePattern = regexp.MustCompile(`^chatisimo/[a-z0-9][a-z0-9-]*/v[1-9][0-9]*$`)

// Purpose is the HKDF info string of one use, "chatisimo/<use>/v<n>". Each owner declares its own.
type Purpose string

// Sealer encrypts and authenticates values for one purpose. Safe for concurrent use.
type Sealer struct {
	aead cipher.AEAD
}

// NewSealer derives the purpose key with hkdf.Key(sha256.New, master, nil, string(p), 32) and
// wraps it in cipher.NewGCMWithRandomNonce over AES-256.
func (k MasterKey) NewSealer(p Purpose) (*Sealer, error) {
	var problems []error

	if k.isZero() {
		problems = append(problems, errors.New("crypto: master key is not set"))
	}

	if !purposePattern.MatchString(string(p)) {
		problems = append(problems, fmt.Errorf("crypto: purpose %q must match %s", string(p), purposePattern.String()))
	}

	if len(problems) > 0 {
		return nil, fmt.Errorf("crypto: invalid sealer parameters: %w", errors.Join(problems...))
	}

	derived, err := hkdf.Key(sha256.New, k.key[:], nil, string(p), masterKeySize)
	if err != nil {
		return nil, fmt.Errorf("crypto: derive purpose key: %w", err)
	}

	block, err := aes.NewCipher(derived)
	if err != nil {
		return nil, fmt.Errorf("crypto: create cipher: %w", err)
	}

	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: create gcm: %w", err)
	}

	return &Sealer{aead: aead}, nil
}

// Seal returns 0x01 || nonce || ciphertext || tag. aad binds the value to its context.
func (s *Sealer) Seal(plaintext, aad []byte) ([]byte, error) {
	if s == nil || s.aead == nil {
		return nil, errors.New("crypto: sealer is not initialized")
	}

	out := make([]byte, 1, 1+len(plaintext)+s.aead.Overhead())
	out[0] = versionByte

	//nolint:gosec // G407: the AEAD from NewGCMWithRandomNonce generates the nonce itself.
	sealed := s.aead.Seal(out, nil, plaintext, aad)

	return sealed, nil
}

// Open reverses Seal. A wrong key, wrong aad, unknown version byte or any tampering returns ErrOpen.
func (s *Sealer) Open(sealed, aad []byte) ([]byte, error) {
	if s == nil || s.aead == nil {
		return nil, errors.New("crypto: sealer is not initialized")
	}

	isTooShort := len(sealed) < minSealedSize
	if isTooShort || sealed[0] != versionByte {
		return nil, ErrOpen
	}

	plaintext, err := s.aead.Open(nil, nil, sealed[1:], aad)
	if err != nil {
		return nil, ErrOpen
	}

	return plaintext, nil
}
