package giftexchange

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
	"uuid"
)

const canonicalIDLength = 36

// ParseExchangeID parses the canonical 36-character uuid form (any letter case). The zero uuid
// is rejected.
func ParseExchangeID(raw string) (ExchangeID, error) {
	parsed, err := parseCanonicalUUID("exchange_id", raw)
	return ExchangeID(parsed), err
}

// ParseQuestionID parses a question ID like ParseExchangeID.
func ParseQuestionID(raw string) (QuestionID, error) {
	parsed, err := parseCanonicalUUID("question_id", raw)
	return QuestionID(parsed), err
}

// ParsePersonID parses a person ID like ParseExchangeID.
func ParsePersonID(raw string) (PersonID, error) {
	parsed, err := parseCanonicalUUID("person_id", raw)
	return PersonID(parsed), err
}

// ParseExchangeName validates an exchange name: valid UTF-8; leading and trailing white
// space trimmed; 1 to MaxNameRunes runes after trimming; no line breaks; no C0 or C1 control
// characters; no bidi embedding, override or isolate characters (U+202A-U+202E, U+2066-U+2069);
// at least one rune that is a letter, number, symbol or punctuation (so names made only of
// zero-width characters, combining marks or spaces are rejected). It returns the trimmed name.
func ParseExchangeName(raw string) (string, error) {
	name, err := parseText("name", raw, MaxNameRunes, false)
	return name, err
}

// ParseQuestionText validates question and answer text with the ParseExchangeName rules, except
// that the length is 1 to MaxQuestionRunes runes and "\n" is allowed ("\r\n" becomes "\n").
// Mentions stay as plain text; clients send with mentions disabled.
func ParseQuestionText(raw string) (string, error) {
	text, err := parseText("text", raw, MaxQuestionRunes, true)
	return text, err
}

// parseCanonicalUUID accepts only the 8-4-4-4-12 hyphenated hexadecimal form, which is stricter
// than uuid.Parse, and rejects the zero uuid.
func parseCanonicalUUID(field, raw string) (uuid.UUID, error) {
	if !isCanonicalUUIDForm(raw) {
		return uuid.UUID{}, &InputError{Field: field, Code: InputCodeNotCanonicalUUID}
	}
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return uuid.UUID{}, &InputError{Field: field, Code: InputCodeNotCanonicalUUID}
	}
	if parsed == (uuid.UUID{}) {
		return uuid.UUID{}, &InputError{Field: field, Code: InputCodeZeroUUID}
	}
	return parsed, nil
}

func isCanonicalUUIDForm(raw string) bool {
	if len(raw) != canonicalIDLength {
		return false
	}
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		isHyphenSlot := i == 8 || i == 13 || i == 18 || i == 23
		if isHyphenSlot {
			if c != '-' {
				return false
			}
			continue
		}
		isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
		if !isHex {
			return false
		}
	}
	return true
}

// textFlaws records which rules a text broke, so each reason is reported once.
type textFlaws struct {
	hasControl bool
	hasBreak   bool
	hasBidi    bool
	hasVisible bool
}

func parseText(field, raw string, maxRunes int, allowNewline bool) (string, error) {
	text := raw
	if allowNewline {
		text = strings.ReplaceAll(text, "\r\n", "\n")
	}
	text = strings.TrimSpace(text)
	runeCount := utf8.RuneCountInString(text)
	if runeCount == 0 {
		return "", &InputError{Field: field, Code: InputCodeEmpty}
	}

	var problems []error
	if !utf8.ValidString(raw) {
		problems = append(problems, &InputError{Field: field, Code: InputCodeInvalidUTF8})
	}
	if runeCount > maxRunes {
		problems = append(problems, &InputError{Field: field, Code: InputCodeTooLong, Max: maxRunes})
	}
	flaws := scanText(text, allowNewline)
	if flaws.hasControl {
		problems = append(problems, &InputError{Field: field, Code: InputCodeControlChars})
	}
	if flaws.hasBreak {
		problems = append(problems, &InputError{Field: field, Code: InputCodeLineBreaks})
	}
	if flaws.hasBidi {
		problems = append(problems, &InputError{Field: field, Code: InputCodeBidiChars})
	}
	if !flaws.hasVisible {
		problems = append(problems, &InputError{Field: field, Code: InputCodeNoVisibleChar})
	}
	if len(problems) > 0 {
		return "", errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
	}
	return text, nil
}

func scanText(text string, allowNewline bool) textFlaws {
	var flaws textFlaws
	for _, r := range text {
		switch {
		case r == '\n' && allowNewline:
		case r == '\n' || r == '\r' || r == 0x2028 || r == 0x2029:
			flaws.hasBreak = true
		case unicode.IsControl(r):
			flaws.hasControl = true
		case isBidiControl(r):
			flaws.hasBidi = true
		case unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsSymbol(r) || unicode.IsPunct(r):
			flaws.hasVisible = true
		}
	}
	return flaws
}

func isBidiControl(r rune) bool {
	isEmbeddingOrOverride := r >= 0x202A && r <= 0x202E
	isIsolate := r >= 0x2066 && r <= 0x2069
	return isEmbeddingOrOverride || isIsolate
}
