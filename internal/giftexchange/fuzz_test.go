package giftexchange_test

import (
	"errors"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

func textSeeds() []string {
	return []string{
		"", "A", "Winter exchange", "  padded  ", "Win\nter", "Win\r\nter", "Win\x07ter",
		"Win\u0085ter", "Win" + string(rune(0x202E)) + "ter", "Win" + string(rune(0x2066)) + "ter", string(rune(0x200B)) + string(rune(0x200B)), string(rune(0x0301)) + string(rune(0x0301)),
		"\U0001F381", "هدايا", "@everyone", "<@100000000000000001>",
		string([]byte{0xff, 0xfe}), string([]byte{'a', 0xe2, 0x82}), strings.Repeat("a", 10000),
	}
}

func idSeeds() []string {
	seeds := []string{validID, validIDUpper, zeroID, "", "{" + validID + "}", "urn:uuid:" + validID}
	seeds = append(seeds, strings.ReplaceAll(validID, "-", ""), validID[:35], validID+"0")
	return seeds
}

func isForbiddenRune(r rune, allowNewline bool) bool {
	if r == '\n' && allowNewline {
		return false
	}
	isBidi := (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069)
	isBreak := r == '\n' || r == '\r' || r == 0x2028 || r == 0x2029
	return unicode.IsControl(r) || isBidi || isBreak
}

func checkValidText(t *testing.T, got string, maxRunes int, allowNewline bool, parse func(string) (string, error)) {
	t.Helper()
	count := utf8.RuneCountInString(got)
	if !utf8.ValidString(got) || count < 1 || count > maxRunes {
		t.Fatalf("valid output %q has %d runes", got, count)
	}
	for _, r := range got {
		if isForbiddenRune(r, allowNewline) {
			t.Fatalf("valid output %q contains forbidden rune %U", got, r)
		}
	}
	again, err := parse(got)
	if err != nil || again != got {
		t.Fatalf("valid output %q does not parse to itself: %q, %v", got, again, err)
	}
}

func checkInvalid(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, giftexchange.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func FuzzParseExchangeID(f *testing.F) {
	for _, s := range idSeeds() {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		id, err := giftexchange.ParseExchangeID(raw)
		if err != nil {
			checkInvalid(t, err)
			return
		}
		if id.IsZero() || id.String() != strings.ToLower(raw) {
			t.Fatalf("valid id %q renders %q", raw, id.String())
		}
	})
}

func FuzzParseQuestionID(f *testing.F) {
	for _, s := range idSeeds() {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		id, err := giftexchange.ParseQuestionID(raw)
		if err != nil {
			checkInvalid(t, err)
			return
		}
		if id.IsZero() || id.String() != strings.ToLower(raw) {
			t.Fatalf("valid id %q renders %q", raw, id.String())
		}
	})
}

func FuzzParsePersonID(f *testing.F) {
	for _, s := range idSeeds() {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		id, err := giftexchange.ParsePersonID(raw)
		if err != nil {
			checkInvalid(t, err)
			return
		}
		if id.IsZero() || id.String() != strings.ToLower(raw) {
			t.Fatalf("valid id %q renders %q", raw, id.String())
		}
		again, err := giftexchange.ParsePersonID(id.String())
		if err != nil || again != id {
			t.Fatalf("round trip gave %v, %v", again, err)
		}
	})
}

func FuzzParseExchangeName(f *testing.F) {
	for _, s := range textSeeds() {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		got, err := giftexchange.ParseExchangeName(raw)
		if err != nil {
			checkInvalid(t, err)
			return
		}
		checkValidText(t, got, giftexchange.MaxNameRunes, false, giftexchange.ParseExchangeName)
	})
}

func FuzzParseQuestionText(f *testing.F) {
	for _, s := range textSeeds() {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		got, err := giftexchange.ParseQuestionText(raw)
		if err != nil {
			checkInvalid(t, err)
			return
		}
		checkValidText(t, got, giftexchange.MaxQuestionRunes, true, giftexchange.ParseQuestionText)
	})
}

func FuzzParseRuleName(f *testing.F) {
	for _, name := range allRuleNames {
		f.Add(string(name))
	}
	f.Add("")
	f.Add("DRAW_AT")
	f.Add(string([]byte{0xff}))
	f.Fuzz(func(t *testing.T, raw string) {
		name, err := giftexchange.ParseRuleName(raw)
		if err != nil {
			checkInvalid(t, err)
			if !errors.Is(err, giftexchange.ErrUnknownRule) {
				t.Fatalf("err = %v, want ErrUnknownRule", err)
			}
			return
		}
		if string(name) != raw {
			t.Fatalf("ParseRuleName(%q) = %q", raw, name)
		}
	})
}

func FuzzParseRuleValue(f *testing.F) {
	seeds := []string{
		"", "unset", "2026-12-24T23:00:00-03:00", "2026-12-25T02:00:00.5Z", "1999-12-31T23:59:59Z",
		"10000-01-01T00:00:00Z", "0", "3d", "1d12h", "36h", "48h", "14d", "14d1s", "-1h", "1.5s",
		"99999999999d", "patch", "redraw", "on", "off", "true", "false", "ON", string([]byte{0xff}),
	}
	for i := range allRuleNames {
		for _, s := range seeds {
			f.Add(i, s)
		}
	}
	f.Fuzz(func(t *testing.T, index int, raw string) {
		name := giftexchange.RuleName("unknown")
		if index >= 0 && index < len(allRuleNames) {
			name = allRuleNames[index]
		}
		value, err := giftexchange.ParseRuleValue(name, raw)
		if err != nil {
			isFixed := errors.Is(err, giftexchange.ErrRuleFixed)
			isInvalid := errors.Is(err, giftexchange.ErrInvalidInput)
			if !isFixed && !isInvalid {
				t.Fatalf("err = %v, want ErrInvalidInput or ErrRuleFixed", err)
			}
			return
		}
		again, err := giftexchange.ParseRuleValue(value.Rule(), value.String())
		if err != nil || again != value {
			t.Fatalf("round trip of %q gave %v, %v", value.String(), again, err)
		}
	})
}
