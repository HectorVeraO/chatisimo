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

// ParseInput turns the raw stdin bytes of config set into a single-line value: it strips one
// trailing "\n" or "\r\n", rejects an empty value, invalid UTF-8 and any control character, and
// for a secret also any white space. The caller reads at most MaxInputBytes+1 bytes and rejects
// more. Errors wrap ErrInvalidValue and never include the input.
func ParseInput(raw []byte, secret bool) (string, error) {
	return ParseKeyInput(raw, secret, false)
}

// ParseMultilineInput turns the raw stdin bytes of config set into a multiline plain value: it
// strips one trailing "\n" or "\r\n", normalizes remaining "\r\n" to "\n", and rejects an empty
// or blank value, invalid UTF-8, values longer than MaxInputBytes, and any control character
// other than "\n". Errors wrap ErrInvalidValue and never include the input.
func ParseMultilineInput(raw []byte) (string, error) {
	return ParseKeyInput(raw, false, true)
}

// ParseKeyInput turns the raw stdin bytes of config set into a value. It strips one trailing "\n"
// or "\r\n", and when multiline is true on a non-secret key it normalizes remaining "\r\n" to "\n"
// and allows interior "\n" runes. It rejects an empty or blank value, invalid UTF-8, values longer
// than MaxInputBytes, any other control character, and for a secret any white space. Errors wrap
// ErrInvalidValue and never include the input.
func ParseKeyInput(raw []byte, secret, multiline bool) (string, error) {
	text := string(raw)

	if strings.HasSuffix(text, "\r\n") {
		text = strings.TrimSuffix(text, "\r\n")
	} else {
		text = strings.TrimSuffix(text, "\n")
	}

	allowNewline := multiline && !secret
	if allowNewline {
		text = strings.ReplaceAll(text, "\r\n", "\n")
	}

	var problems []error

	isEmpty := text == "" || (allowNewline && strings.TrimSpace(text) == "")
	if isEmpty {
		problems = append(problems, errors.New("input is empty"))
	}

	if len(text) > MaxInputBytes {
		problems = append(problems, fmt.Errorf("input is longer than %d bytes", MaxInputBytes))
	}

	if !utf8.ValidString(text) {
		problems = append(problems, errors.New("input is not valid UTF-8"))
	}

	hasControl, hasSpace := scanRunes(text, allowNewline)

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

// scanRunes reports whether text holds a disallowed control character and whether it holds white
// space.
func scanRunes(text string, allowNewline bool) (hasControl, hasSpace bool) {
	for _, r := range text {
		isAllowedNewline := allowNewline && r == '\n'
		if !isAllowedNewline && unicode.IsControl(r) {
			hasControl = true
		}

		if unicode.IsSpace(r) {
			hasSpace = true
		}
	}

	return hasControl, hasSpace
}
