package giftexchange_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

const (
	validID      = "0190a1b2-c3d4-7e5f-8a6b-7c8d9e0f1a2b"
	validIDUpper = "0190A1B2-C3D4-7E5F-8A6B-7C8D9E0F1A2B"
	zeroID       = "00000000-0000-0000-0000-000000000000"
)

var badIDs = map[string]string{
	"empty":            "",
	"zero uuid":        zeroID,
	"35 characters":    validID[:35],
	"37 characters":    validID + "0",
	"braces":           "{" + validID + "}",
	"urn prefix":       "urn:uuid:" + validID,
	"32 hex digits":    strings.ReplaceAll(validID, "-", ""),
	"non hex letter":   "0190a1b2-c3d4-7e5f-8a6b-7c8d9e0f1a2g",
	"misplaced hyphen": "0190a1b2c-3d4-7e5f-8a6b-7c8d9e0f1a2b",
	"padded":           " " + validID[:35],
	"multibyte":        strings.Repeat("é", 18),
}

type idParser struct {
	name  string
	parse func(string) (string, error)
}

func idParsers() []idParser {
	return []idParser{
		{"exchange", func(s string) (string, error) {
			id, err := giftexchange.ParseExchangeID(s)
			return id.String(), err
		}},
		{"question", func(s string) (string, error) {
			id, err := giftexchange.ParseQuestionID(s)
			return id.String(), err
		}},
		{"person", func(s string) (string, error) {
			id, err := giftexchange.ParsePersonID(s)
			return id.String(), err
		}},
	}
}

func TestParseIDAcceptsCanonicalForm(t *testing.T) {
	for _, p := range idParsers() {
		t.Run(p.name+" lower case round trips", func(t *testing.T) {
			got, err := p.parse(validID)
			if err != nil || got != validID {
				t.Fatalf("got %q, %v", got, err)
			}
		})
		t.Run(p.name+" upper case is normalized to lower case", func(t *testing.T) {
			got, err := p.parse(validIDUpper)
			if err != nil || got != validID {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
}

func TestParseIDRejectsOtherForms(t *testing.T) {
	for _, p := range idParsers() {
		for name, raw := range badIDs {
			t.Run(p.name+" rejects "+name, func(t *testing.T) {
				_, err := p.parse(raw)
				if !errors.Is(err, giftexchange.ErrInvalidInput) {
					t.Fatalf("err = %v, want ErrInvalidInput", err)
				}
				var inputErr *giftexchange.InputError
				if !errors.As(err, &inputErr) {
					t.Fatalf("err = %v, want *InputError", err)
				}
				wantCode := giftexchange.InputCodeNotCanonicalUUID
				if raw == zeroID {
					wantCode = giftexchange.InputCodeZeroUUID
				}
				if inputErr.Code != wantCode {
					t.Errorf("Code = %q, want %q", inputErr.Code, wantCode)
				}
				if raw != "" && strings.Contains(err.Error(), raw) {
					t.Fatalf("error text %q contains the input", err.Error())
				}
			})
		}
	}
}

func TestIDZeroValue(t *testing.T) {
	var id giftexchange.ExchangeID
	if !id.IsZero() {
		t.Fatal("zero value must report IsZero")
	}
	parsed, err := giftexchange.ParseExchangeID(validID)
	if err != nil || parsed.IsZero() {
		t.Fatalf("parsed = %v, %v", parsed, err)
	}
	if got := id.String(); got != zeroID {
		t.Fatalf("zero String = %q", got)
	}
}

func TestGroupIDValues(t *testing.T) {
	var zero giftexchange.GroupID
	if !zero.IsZero() {
		t.Fatal("zero value must report IsZero")
	}
	if got := zero.String(); got != zeroID {
		t.Fatalf("zero String = %q", got)
	}
	set := giftexchange.GroupID{15: 1}
	if set.IsZero() {
		t.Fatal("non-zero value must not report IsZero")
	}
	if got, want := set.String(), "00000000-0000-0000-0000-000000000001"; got != want {
		t.Fatalf("String = %q, want %q", got, want)
	}
}

func TestParseTextReportsEveryProblem(t *testing.T) {
	_, err := giftexchange.ParseExchangeName("Win\xff\x07ter")
	if !errors.Is(err, giftexchange.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	for _, want := range []string{"(invalid_utf8)", "(control_chars)"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %q", err.Error(), want)
		}
	}
}

const (
	zeroWidth    = string(rune(0x200B))
	combiningAcc = string(rune(0x0301))
)

type textCase struct {
	name string
	in   string
	want string
	ok   bool
}

func sharedTextCases() []textCase {
	return []textCase{
		{"valid", "Winter exchange", "Winter exchange", true},
		{"one rune", "A", "A", true},
		{"empty", "", "", false},
		{"only spaces", "  \t ", "", false},
		{"surrounding white space is trimmed", "  Winter exchange \t", "Winter exchange", true},
		{"C0 control", "Win\x07ter", "", false},
		{"NUL", "Win\x00ter", "", false},
		{"DEL", "Win\x7fter", "", false},
		{"C1 control", "Win\u0085ter", "", false},
		{"line separator", "Win" + string(rune(0x2028)) + "ter", "", false},
		{"paragraph separator", "Win" + string(rune(0x2029)) + "ter", "", false},
		{"lone carriage return", "Win\rter", "", false},
		{"bidi embedding LRE", "Win" + string(rune(0x202A)) + "ter", "", false},
		{"bidi override RLO", "Win" + string(rune(0x202E)) + "ter", "", false},
		{"bidi isolate LRI", "Win" + string(rune(0x2066)) + "ter", "", false},
		{"bidi isolate PDI", "Win" + string(rune(0x2069)) + "ter", "", false},
		{"zero width only", zeroWidth + zeroWidth, "", false},
		{"combining marks only", combiningAcc + combiningAcc, "", false},
		{"emoji", "Navidad \U0001F381\U0001F384", "Navidad \U0001F381\U0001F384", true},
		{"emoji only", "\U0001F381", "\U0001F381", true},
		{"right to left text", "هدايا", "هدايا", true},
		{"everyone mention stays text", "@everyone", "@everyone", true},
		{"user mention stays text", "<@100000000000000001>", "<@100000000000000001>", true},
		{"invalid UTF-8", "Win\xffter", "", false},
		{"truncated UTF-8", "Win\xe2\x82", "", false},
		{"punctuation only", "...", "...", true},
	}
}

func runTextCases(t *testing.T, parse func(string) (string, error), cases []textCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parse(tc.in)
			if tc.ok {
				if err != nil || got != tc.want {
					t.Fatalf("got %q, %v; want %q", got, err, tc.want)
				}
				return
			}
			if !errors.Is(err, giftexchange.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
			if got != "" {
				t.Fatalf("got %q on error, want empty", got)
			}
			if trimmed := strings.TrimSpace(tc.in); trimmed != "" && strings.Contains(err.Error(), trimmed) {
				t.Fatalf("error text %q contains the input", err.Error())
			}
		})
	}
}

func TestParseExchangeName(t *testing.T) {
	cases := append(sharedTextCases(),
		textCase{"line feed", "Win\nter", "", false},
		textCase{"CRLF", "Win\r\nter", "", false},
		textCase{"max runes", strings.Repeat("a", giftexchange.MaxNameRunes), strings.Repeat("a", giftexchange.MaxNameRunes), true},
		textCase{"max runes multibyte", strings.Repeat("\U0001F381", giftexchange.MaxNameRunes), strings.Repeat("\U0001F381", giftexchange.MaxNameRunes), true},
		textCase{"max runes plus one", strings.Repeat("a", giftexchange.MaxNameRunes+1), "", false},
		textCase{"10k runes", strings.Repeat("a", 10000), "", false},
		textCase{"white space does not count toward the length", " " + strings.Repeat("a", giftexchange.MaxNameRunes) + " ", strings.Repeat("a", giftexchange.MaxNameRunes), true},
	)
	runTextCases(t, giftexchange.ParseExchangeName, cases)
}

func TestParseQuestionText(t *testing.T) {
	cases := append(sharedTextCases(),
		textCase{"line feed allowed", "Line one\nLine two", "Line one\nLine two", true},
		textCase{"CRLF becomes LF", "Line one\r\nLine two", "Line one\nLine two", true},
		textCase{"max runes", strings.Repeat("a", giftexchange.MaxQuestionRunes), strings.Repeat("a", giftexchange.MaxQuestionRunes), true},
		textCase{"max runes plus one", strings.Repeat("a", giftexchange.MaxQuestionRunes+1), "", false},
		textCase{"10k runes", strings.Repeat("a", 10000), "", false},
		textCase{"only line feeds", "\n\n", "", false},
	)
	runTextCases(t, giftexchange.ParseQuestionText, cases)
}

func TestParseTextReportsEveryProblemTogether(t *testing.T) {
	_, err := giftexchange.ParseExchangeName("a" + string(rune(0x202E)) + "\nb" + strings.Repeat("c", giftexchange.MaxNameRunes))
	var inputErr *giftexchange.InputError
	if !errors.As(err, &inputErr) {
		t.Fatalf("err = %v, want an *InputError", err)
	}
	text := err.Error()
	for _, want := range []string{"(too_long)", "(line_breaks)", "(bidi_chars)"} {
		if !strings.Contains(text, want) {
			t.Fatalf("error %q does not mention %q", text, want)
		}
	}
}

func TestParseTextSetsErrorCodeAndParameters(t *testing.T) {
	_, nameEmptyErr := giftexchange.ParseExchangeName("   ")
	_, nameUTF8Err := giftexchange.ParseExchangeName("Win\xffter")
	_, nameLongErr := giftexchange.ParseExchangeName(strings.Repeat("a", giftexchange.MaxNameRunes+1))
	_, questionLongErr := giftexchange.ParseQuestionText(strings.Repeat("a", giftexchange.MaxQuestionRunes+1))
	_, prefsLongErr := giftexchange.ParsePreferences(giftexchange.Preferences{
		Likes: strings.Repeat("a", giftexchange.MaxPreferenceRunes+1),
	})
	_, nameCtrlErr := giftexchange.ParseExchangeName("Win\x07ter")
	_, nameBreakErr := giftexchange.ParseExchangeName("Win\nter")
	_, nameBidiErr := giftexchange.ParseExchangeName("Win" + string(rune(0x202E)) + "ter")
	_, nameNoVisibleErr := giftexchange.ParseExchangeName(zeroWidth)

	cases := []struct {
		name     string
		err      error
		wantCode giftexchange.InputErrorCode
		wantMax  int
	}{
		{"empty name", nameEmptyErr, giftexchange.InputCodeEmpty, 0},
		{"invalid UTF-8", nameUTF8Err, giftexchange.InputCodeInvalidUTF8, 0},
		{"too long name sets MaxNameRunes", nameLongErr, giftexchange.InputCodeTooLong, giftexchange.MaxNameRunes},
		{"too long question sets MaxQuestionRunes", questionLongErr, giftexchange.InputCodeTooLong, giftexchange.MaxQuestionRunes},
		{"too long preference sets MaxPreferenceRunes", prefsLongErr, giftexchange.InputCodeTooLong, giftexchange.MaxPreferenceRunes},
		{"control characters", nameCtrlErr, giftexchange.InputCodeControlChars, 0},
		{"line breaks", nameBreakErr, giftexchange.InputCodeLineBreaks, 0},
		{"bidi control characters", nameBidiErr, giftexchange.InputCodeBidiChars, 0},
		{"no visible character", nameNoVisibleErr, giftexchange.InputCodeNoVisibleChar, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var inputErr *giftexchange.InputError
			if !errors.As(tc.err, &inputErr) {
				t.Fatalf("err = %v, want *InputError", tc.err)
			}
			if inputErr.Code != tc.wantCode {
				t.Errorf("Code = %q, want %q", inputErr.Code, tc.wantCode)
			}
			if inputErr.Max != tc.wantMax {
				t.Errorf("Max = %d, want %d", inputErr.Max, tc.wantMax)
			}
		})
	}
}
