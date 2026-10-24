// Package crypto holds the master key and the per-purpose sealers derived from it.
package crypto

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
)

const (
	masterKeySize = 32
	redacted      = "[redacted]"
)

// MasterKey is the 32-byte root secret from MASTER_KEY. String, GoString, Format and LogValue
// render "[redacted]".
type MasterKey struct{ key [masterKeySize]byte }

// String renders "[redacted]".
func (MasterKey) String() string {
	return redacted
}

// GoString renders "[redacted]".
func (MasterKey) GoString() string {
	return redacted
}

// Format writes "[redacted]" for every verb.
func (MasterKey) Format(f fmt.State, _ rune) {
	// A failed write to the formatter's destination cannot be reported from Format.
	_, _ = io.WriteString(f, redacted)
}

// LogValue renders "[redacted]" in structured logs.
func (MasterKey) LogValue() slog.Value {
	return slog.StringValue(redacted)
}

// ParseMasterKey decodes standard padded base64 (44 characters) into exactly 32 bytes. Errors
// name the problem ("not base64", "decodes to 31 bytes, want 32"), never the input.
func ParseMasterKey(text string) (MasterKey, error) {
	var zero MasterKey

	if text == "" {
		return zero, errors.New("crypto: MASTER_KEY is empty")
	}

	// The base64 decoder silently skips line breaks, which would let two texts name one key.
	if strings.ContainsAny(text, "\r\n") {
		return zero, errors.New("crypto: MASTER_KEY is not standard padded base64")
	}

	decoded, err := base64.StdEncoding.Strict().DecodeString(text)
	if err != nil {
		return zero, errors.New("crypto: MASTER_KEY is not standard padded base64")
	}

	if len(decoded) != masterKeySize {
		return zero, fmt.Errorf("crypto: MASTER_KEY decodes to %d bytes, want %d", len(decoded), masterKeySize)
	}

	var key MasterKey
	copy(key.key[:], decoded)

	if key.isZero() {
		return zero, errors.New("crypto: MASTER_KEY is all zero bytes")
	}

	return key, nil
}

// GenerateMasterKey reads 32 bytes from r (crypto/rand.Reader in production) and returns them as
// standard base64.
func GenerateMasterKey(r io.Reader) (string, error) {
	if r == nil {
		return "", errors.New("crypto: random source is nil")
	}

	raw := make([]byte, masterKeySize)

	_, err := io.ReadFull(r, raw)
	if err != nil {
		return "", fmt.Errorf("crypto: read random bytes: %w", err)
	}

	encoded := base64.StdEncoding.EncodeToString(raw)

	return encoded, nil
}

// isZero reports whether the key is the unset zero value.
func (k MasterKey) isZero() bool {
	return k.key == [masterKeySize]byte{}
}
