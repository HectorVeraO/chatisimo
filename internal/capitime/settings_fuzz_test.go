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
		if err := capitime.ValidatePrompt(raw); err == nil {
			if strings.TrimSpace(raw) == "" || !utf8.ValidString(raw) {
				t.Fatalf("ValidatePrompt accepted invalid prompt %q", raw)
			}
		}
	})
}
