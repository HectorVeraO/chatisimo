package config

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxInputBytes bounds a config set value.
const MaxInputBytes = 64 << 10

// ParseInput turns the raw stdin bytes of config set into a value: it strips one trailing "\n"
// or "\r\n", rejects an empty value, invalid UTF-8 and any control character, and for a secret
// also any white space. The caller reads at most MaxInputBytes+1 bytes and rejects more. Errors
// wrap ErrInvalidValue and never include the input.
func ParseInput(raw []byte, secret bool) (string, error) {
	text := string(raw)

	if strings.HasSuffix(text, "\r\n") {
		text = strings.TrimSuffix(text, "\r\n")
	} else {
		text = strings.TrimSuffix(text, "\n")
	}

	var problems []error

	if text == "" {
		problems = append(problems, errors.New("input is empty"))
	}

	if len(text) > MaxInputBytes {
		problems = append(problems, fmt.Errorf("input is longer than %d bytes", MaxInputBytes))
	}

	if !utf8.ValidString(text) {
		problems = append(problems, errors.New("input is not valid UTF-8"))
	}

	hasControl, hasSpace := scanRunes(text)

	if hasControl {
		problems = append(problems, errors.New("input contains a control character"))
	}

	if secret && hasSpace {
		problems = append(problems, errors.New("input contains white space"))
	}

	if len(problems) > 0 {
		return "", fmt.Errorf("%w: %w", ErrInvalidValue, errors.Join(problems...))
	}

	return text, nil
}

// scanRunes reports whether text holds a control character and whether it holds white space.
func scanRunes(text string) (hasControl, hasSpace bool) {
	for _, r := range text {
		if unicode.IsControl(r) {
			hasControl = true
		}

		if unicode.IsSpace(r) {
			hasSpace = true
		}
	}

	return hasControl, hasSpace
}
