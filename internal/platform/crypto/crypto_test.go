package crypto_test

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/HectorVeraO/chatisimo/internal/platform/crypto"
)

func newKeyText(t *testing.T) string {
	t.Helper()

	text, err := crypto.GenerateMasterKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate master key: %v", err)
	}

	return text
}

func mustParse(t *testing.T) (crypto.MasterKey, string) {
	t.Helper()

	text := newKeyText(t)

	key, err := crypto.ParseMasterKey(text)
	if err != nil {
		t.Fatalf("parse generated key: %v", err)
	}

	return key, text
}

func encodeBytes(raw []byte) string {
	return base64.StdEncoding.EncodeToString(raw)
}

func TestParseMasterKeyRejectsBadInput(t *testing.T) {
	valid := newKeyText(t)
	// The last character of a canonical 32-byte key has two spare bits; "B" sets the low one.
	nonCanonical := strings.Repeat("Q", 42) + "R="

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "rejects an empty key", input: "", want: "empty"},
		{name: "rejects 43 characters", input: valid[:43], want: "not standard padded base64"},
		{name: "rejects 45 characters", input: valid + "A", want: "not standard padded base64"},
		{name: "rejects the URL-safe alphabet with dash", input: strings.Repeat("-", 43) + "=", want: "not standard padded base64"},
		{name: "rejects the URL-safe alphabet with under", input: strings.Repeat("_", 43) + "=", want: "not standard padded base64"},
		{name: "rejects unpadded base64", input: strings.TrimRight(valid, "="), want: "not standard padded base64"},
		{name: "rejects a key that decodes to 31 bytes", input: encodeBytes(bytes.Repeat([]byte{7}, 31)), want: "bytes, want 32"},
		{name: "rejects a key that decodes to 33 bytes", input: encodeBytes(bytes.Repeat([]byte{7}, 33)), want: "bytes, want 32"},
		{name: "rejects a newline inside", input: valid[:20] + "\n" + valid[20:], want: "not standard padded base64"},
		{name: "rejects a trailing newline", input: valid + "\n", want: "not standard padded base64"},
		{name: "rejects a trailing carriage return", input: valid + "\r", want: "not standard padded base64"},
		{name: "rejects non-canonical trailing bits", input: nonCanonical, want: "not standard padded base64"},
		{name: "rejects an all-zero key", input: encodeBytes(make([]byte, 32)), want: "all zero"},
		{name: "rejects surrounding spaces", input: " " + valid + " ", want: "not standard padded base64"},
		{name: "rejects unusual unicode", input: string([]rune{0x202e, 0x200b}) + strings.Repeat(string([]rune{'e', 0x301}), 20), want: "not standard padded base64"},
		{name: "rejects very long input", input: strings.Repeat("A", 1<<16), want: "bytes, want 32"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := crypto.ParseMasterKey(tc.input)
			if err == nil {
				t.Fatalf("expected an error")
			}

			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not name the cause %q", err.Error(), tc.want)
			}

			isInputLeaked := tc.input != "" && strings.Contains(err.Error(), tc.input)
			if isInputLeaked {
				t.Errorf("error text contains the input")
			}
		})
	}
}

func TestParseMasterKeyAcceptsGeneratedKey(t *testing.T) {
	text := newKeyText(t)

	_, err := crypto.ParseMasterKey(text)
	if err != nil {
		t.Fatalf("parse valid key: %v", err)
	}

	if len(text) != 44 {
		t.Errorf("generated key has %d characters, want 44", len(text))
	}
}

func TestGenerateMasterKeyFailsOnShortRead(t *testing.T) {
	short := io.MultiReader(bytes.NewReader(make([]byte, 31)), iotest.ErrReader(io.EOF))

	text, err := crypto.GenerateMasterKey(short)
	if err == nil {
		t.Fatalf("expected an error")
	}

	if text != "" {
		t.Errorf("expected no key text on failure")
	}
}

func TestGenerateMasterKeyRejectsNilReader(t *testing.T) {
	_, err := crypto.GenerateMasterKey(nil)
	if err == nil {
		t.Fatalf("expected an error")
	}
}

func TestGenerateMasterKeyProducesDistinctKeys(t *testing.T) {
	first := newKeyText(t)
	second := newKeyText(t)

	if first == second {
		t.Errorf("two generated keys are equal")
	}
}

func TestGenerateMasterKeyEncodesStandardPaddedBase64(t *testing.T) {
	// Bytes 0xfb 0xff 0xfe encode to "+//+" in the standard alphabet and "-__-" in the URL one.
	source := bytes.Repeat([]byte{0xfb, 0xff, 0xfe}, 10)
	source = append(source, 0xfb, 0xff)

	text, err := crypto.GenerateMasterKey(bytes.NewReader(source))
	if err != nil {
		t.Fatalf("generate master key: %v", err)
	}

	want := strings.Repeat("+//+", 10) + "+/8="
	if text != want {
		t.Errorf("encoded key = %q, want %q", text, want)
	}
}

func TestParseMasterKeyReportsEveryProblem(t *testing.T) {
	// 31 zero bytes: wrong length and all zero at once.
	text := base64.StdEncoding.EncodeToString(make([]byte, 31))

	_, err := crypto.ParseMasterKey(text)
	if err == nil {
		t.Fatalf("expected an error")
	}

	msg := err.Error()
	if !strings.Contains(msg, "31 bytes") || !strings.Contains(msg, "all zero") {
		t.Errorf("error does not name both problems: %v", err)
	}
}

func TestMasterKeyNeverRendersItsBytes(t *testing.T) {
	key, text := mustParse(t)

	raw, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	forbidden := []string{text, hex.EncodeToString(raw), strings.ToUpper(hex.EncodeToString(raw))}

	var jsonBuf, textBuf bytes.Buffer
	slog.New(slog.NewJSONHandler(&jsonBuf, nil)).Info("event", "key", key)
	slog.New(slog.NewTextHandler(&textBuf, nil)).Info("event", "key", key)

	outputs := map[string]string{
		"%v":        fmt.Sprintf("%v", key),
		"%+v":       fmt.Sprintf("%+v", key),
		"%#v":       fmt.Sprintf("%#v", key),
		"%s":        fmt.Sprintf("%s", key),
		"%q":        fmt.Sprintf("%q", key),
		"%x":        fmt.Sprintf("%x", key),
		"%d":        fmt.Sprintf("%d", key),
		"String":    key.String(),
		"GoString":  key.GoString(),
		"pointer":   fmt.Sprintf("%v", &key),
		"slog json": jsonBuf.String(),
		"slog text": textBuf.String(),
	}

	for name, out := range outputs {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(out, "[redacted]") {
				t.Errorf("output does not contain [redacted]")
			}

			for _, secret := range forbidden {
				if strings.Contains(out, secret) {
					t.Errorf("output contains key material")
				}
			}
		})
	}
}
