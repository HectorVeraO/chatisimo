package capitime_test

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/HectorVeraO/chatisimo/internal/capitime"
)

func FuzzSettingParsers(f *testing.F) {
	seeds := []string{
		"",
		"5",
		"0",
		"100",
		"101",
		"-1",
		"45",
		"180",
		"1440",
		"1441",
		"2m",
		"0s",
		"24h",
		"25h",
		"-2m",
		"+2m",
		"1500ms",
		"bad\x00prompt",
		"bad\xff\xfeutf8",
		strings.Repeat("a", 5000),
	}

	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		if pct, err := capitime.ParseEasterEggPercent(raw); err == nil {
			if pct < 0 || pct > 100 {
				t.Fatalf("ParseEasterEggPercent accepted out-of-range %d", pct)
			}
		}
		if mins, err := capitime.ParseDelayMinutes(raw); err == nil {
			if mins < 1 || mins > 1440 {
				t.Fatalf("ParseDelayMinutes accepted out-of-range %d", mins)
			}
		}
		if dur, err := capitime.ParseDebounceWindow(raw); err == nil {
			if dur < 0 || dur > 24*time.Hour || dur%time.Second != 0 {
				t.Fatalf("ParseDebounceWindow accepted invalid duration %v", dur)
			}
		}
		if count, err := capitime.ParseContextMessages(raw); err == nil {
			if count < 0 || count > 10 {
				t.Fatalf("ParseContextMessages accepted out-of-range %d", count)
			}
		}
		if err := capitime.ValidatePrompt(raw); err == nil {
			if strings.TrimSpace(raw) == "" || !utf8.ValidString(raw) {
				t.Fatalf("ValidatePrompt accepted invalid prompt %q", raw)
			}
		}
	})
}

func FuzzParseContextMessages(f *testing.F) {
	seeds := []string{
		"",
		"0",
		"1",
		"4",
		"10",
		"11",
		"-1",
		"+4",
		" 4 ",
		"4.0",
		"1e1",
		"0x4",
		"04",
		"9223372036854775808",
		"four",
		"4\x00",
		"4\n5",
		// Arabic-Indic and fullwidth digits.
		"\xd9\xa4",
		"\xef\xbc\x94",
		// A digit wrapped in a right-to-left override and a zero-width space.
		"\xe2\x80\xae4\xe2\x80\x8b",
		"4\xcc\x81",
		"<@100000000000000001>",
		"\U0001F600",
		"bad\xff\xfeutf8",
		strings.Repeat("1", 65),
		strings.Repeat("a", 5000),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		count, err := capitime.ParseContextMessages(raw)
		if err != nil {
			if count != 0 {
				t.Fatalf("ParseContextMessages(%q) = %d with an error, want 0", raw, count)
			}

			hasLongDistinctInput := len(raw) >= 6 && !strings.Contains(contextMessagesError, raw)
			if hasLongDistinctInput && strings.Contains(err.Error(), raw) {
				t.Fatalf("error %q echoed input %q", err.Error(), raw)
			}

			return
		}

		if count < 0 || count > 10 {
			t.Fatalf("ParseContextMessages(%q) accepted out-of-range %d", raw, count)
		}
	})
}
