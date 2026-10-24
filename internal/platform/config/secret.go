package config

import (
	"fmt"
	"io"
	"log/slog"
)

const redacted = "[redacted]"

// Secret holds a sensitive string. String, GoString, Format, LogValue and MarshalJSON render
// "[redacted]".
type Secret struct{ value string }

// NewSecret wraps value.
func NewSecret(value string) Secret {
	return Secret{value: value}
}

// Reveal returns the plain value; call it only at the point of use.
func (s Secret) Reveal() string {
	return s.value
}

// String renders "[redacted]".
func (Secret) String() string {
	return redacted
}

// GoString renders "[redacted]".
func (Secret) GoString() string {
	return redacted
}

// Format writes "[redacted]" for every verb.
func (Secret) Format(f fmt.State, _ rune) {
	// A failed write to the formatter's destination cannot be reported from Format.
	_, _ = io.WriteString(f, redacted)
}

// LogValue renders "[redacted]" in structured logs.
func (Secret) LogValue() slog.Value {
	return slog.StringValue(redacted)
}

// MarshalJSON renders the JSON string "[redacted]", since encoding/json ignores fmt.Formatter.
func (Secret) MarshalJSON() ([]byte, error) {
	return []byte(`"` + redacted + `"`), nil
}
