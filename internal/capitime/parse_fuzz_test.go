package capitime_test

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/HectorVeraO/chatisimo/internal/capitime"
)

func FuzzParseArrivalMessage(f *testing.F) {
	seeds := []string{
		"",
		"   ",
		"22:30 llego",
		"00:00",
		"hasta las 23:40",
		"llego a las 9pm",
		"llego a las 12am",
		"voy altiro",
		"voy al tiro",
		"voy voy",
		"toy ready",
		"estoy ready",
		"ready",
		"llegando",
		"lleganding",
		"llego mañana en la mañana",
		"llego mañana en la tarde",
		"llego mañana en la noche",
		"llego mañana",
		"llego el miércoles",
		"llego el lunes a las 21:00",
		"en 10 min llego",
		"el Capi llega a las 22:30",
		"Braulitros llegando",
		"ayer llegué a las 22:30",
		"mi hermano llega a las 22:30",
		"el uber llega en 5 minutos",
		"99:99 llego",
		"24:00",
		"en 999999999999999999999 min llego",
		strings.Repeat("voy altiro ", 500),
		"llego\x00\r\n\t22:30",
		"\u202e22:30 llego\u200b",
		"llego \U0001F600 a las 22:30",
		"invalid utf8 \xff\xfe 22:30",
		"<@100000000000000001> @everyone voy altiro",
	}

	for _, seed := range seeds {
		f.Add(seed)
	}

	ref := time.Date(2026, 11, 1, 21, 0, 0, 0, capitime.Santiago())

	f.Fuzz(func(t *testing.T, raw string) {
		got := capitime.ParseArrivalMessage(raw, ref)
		if !got.Detected {
			return
		}

		if got.ClaimedAt.IsZero() {
			t.Fatalf("Detected=true with zero ClaimedAt for input %q", raw)
		}
		if got.ClaimSummary == "" || !utf8.ValidString(got.ClaimSummary) {
			t.Fatalf("invalid ClaimSummary %q for input %q", got.ClaimSummary, raw)
		}
		if strings.ContainsAny(got.ClaimSummary, "\n\r\t\x00@") {
			t.Fatalf("ClaimSummary %q contains forbidden characters for input %q", got.ClaimSummary, raw)
		}
	})
}

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
		capitime.DefaultRegexPattern,
		"[unclosed",
		"bad\x00regex",
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
		if err := capitime.ValidateRegexPattern(raw); err == nil {
			if strings.TrimSpace(raw) == "" || !utf8.ValidString(raw) {
				t.Fatalf("ValidateRegexPattern accepted invalid pattern %q", raw)
			}
		}
		if err := capitime.ValidatePrompt(raw); err == nil {
			if strings.TrimSpace(raw) == "" || !utf8.ValidString(raw) {
				t.Fatalf("ValidatePrompt accepted invalid prompt %q", raw)
			}
		}
	})
}
