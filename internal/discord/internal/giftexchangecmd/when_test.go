package giftexchangecmd

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func santiago(tb testing.TB) *time.Location {
	tb.Helper()

	loc, err := time.LoadLocation("America/Santiago")
	if err != nil {
		tb.Fatalf("LoadLocation: %v", err)
	}

	return loc
}

var whenNow = time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)

func TestParseWhenReadsEveryAcceptedForm(t *testing.T) {
	loc := santiago(t)
	cases := map[string]time.Time{
		"in 3d":                     whenNow.Add(72 * time.Hour),
		"in 2h30m":                  whenNow.Add(150 * time.Minute),
		"in 1d12h":                  whenNow.Add(36 * time.Hour),
		"in 90m":                    whenNow.Add(90 * time.Minute),
		"en 3d":                     whenNow.Add(72 * time.Hour),
		"en 2h30m":                  whenNow.Add(150 * time.Minute),
		"en 1d12h":                  whenNow.Add(36 * time.Hour),
		"en 90m":                    whenNow.Add(90 * time.Minute),
		"EN 45m":                    whenNow.Add(45 * time.Minute),
		"EN 2h":                     whenNow.Add(2 * time.Hour),
		"En 3d":                     whenNow.Add(72 * time.Hour),
		"  en   3D  ":               whenNow.Add(72 * time.Hour),
		"en 730d":                   whenNow.Add(730 * 24 * time.Hour),
		"45s":                       whenNow.Add(45 * time.Second),
		"3d":                        whenNow.Add(72 * time.Hour),
		"  in   3D  ":               whenNow.Add(72 * time.Hour),
		"in 730d":                   whenNow.Add(730 * 24 * time.Hour),
		"in 17520h":                 whenNow.Add(730 * 24 * time.Hour),
		"2026-12-20 18:00":          time.Date(2026, time.December, 20, 21, 0, 0, 0, time.UTC),
		"2026-12-20 18:00:30":       time.Date(2026, time.December, 20, 21, 0, 30, 0, time.UTC),
		"2026-07-01 08:00":          time.Date(2026, time.July, 1, 12, 0, 0, 0, time.UTC),
		"2026-12-20T18:00:00Z":      time.Date(2026, time.December, 20, 18, 0, 0, 0, time.UTC),
		"2026-12-20T18:00:00-03:00": time.Date(2026, time.December, 20, 21, 0, 0, 0, time.UTC),
	}

	for raw, want := range cases {
		t.Run(raw, func(t *testing.T) {
			got, empty, err := parseWhen(raw, whenNow, loc)
			if err != nil || empty {
				t.Fatalf("parseWhen(%q) = %v, %v, %v", raw, got, empty, err)
			}
			if !got.Equal(want) {
				t.Errorf("parseWhen(%q) = %v, want %v", raw, got, want)
			}
			if got.Location() != time.UTC {
				t.Errorf("parseWhen(%q) is in %v, want UTC", raw, got.Location())
			}
		})
	}
}

func TestParseWhenReportsEmptyInput(t *testing.T) {
	for _, raw := range []string{"", "   ", "\t\n"} {
		got, empty, err := parseWhen(raw, whenNow, santiago(t))
		if err != nil || !empty || !got.IsZero() {
			t.Errorf("parseWhen(%q) = %v, %v, %v; want empty", raw, got, empty, err)
		}
	}
}

func TestParseWhenRefusesOtherInput(t *testing.T) {
	cases := map[string]string{
		"words":                    "tomorrow",
		"only the prefix":          "in",
		"prefix and space":         "in ",
		"prefix without space":     "in3d",
		"spanish only prefix":      "en",
		"spanish prefix and space": "en ",
		"spanish without space":    "en3d",
		"double prefix":            "en in 3d",
		"unit without number":      "d",
		"number without unit":      "30",
		"unknown unit":             "3w",
		"zero":                     "0s",
		"zero days":                "in 0d",
		"spanish zero minutes":     "en 0m",
		"negative":                 "in -3d",
		"spanish negative":         "en -1d",
		"decimal":                  "in 1.5h",
		"spaces inside":            "in 2h 30m",
		"spanish spaces inside":    "en 2h 30m",
		"over 730 days":            "in 731d",
		"spanish over 730 days":    "en 731d",
		"over 730 days in hours":   "in 17521h",
		"overflow digits":          "in 99999999999999999999d",
		"nine digits of days":      "in 999999999d",
		"date without time":        "2026-12-20",
		"time without date":        "18:00",
		"day first":                "20-12-2026 18:00",
		"impossible day":           "2026-02-30 10:00",
		"impossible hour":          "2026-12-20 25:00",
		"rfc3339 without zone":     "2026-12-20T18:00:00",
		"unicode digits":           "in \u0663d",
		"right to left":            "\u202ein 3d",
		"spanish right to left":    "\u202een 3d",
		"emoji":                    "\U0001F600",
		"long":                     strings.Repeat("1d", 5000),
	}

	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			got, empty, err := parseWhen(raw, whenNow, santiago(t))
			if !errors.Is(err, errBadTime) || empty || !got.IsZero() {
				t.Errorf("parseWhen(%q) = %v, %v, %v; want errBadTime", raw, got, empty, err)
			}
		})
	}
}

func TestParseWhenResolvesADaylightSavingGapLikeTimeDate(t *testing.T) {
	loc := santiago(t)
	// 2026-09-06 00:30 does not exist in Santiago: clocks jump from 00:00 to 01:00.
	want := time.Date(2026, time.September, 6, 0, 30, 0, 0, loc).UTC()

	got, _, err := parseWhen("2026-09-06 00:30", whenNow, loc)
	if err != nil {
		t.Fatalf("parseWhen: %v", err)
	}
	if !got.Equal(want) {
		t.Errorf("parseWhen = %v, want %v", got, want)
	}
}

func FuzzParseWhen(f *testing.F) {
	loc, err := time.LoadLocation("America/Santiago")
	if err != nil {
		f.Fatalf("LoadLocation: %v", err)
	}

	seeds := []string{
		"", " ", "in 3d", "in 2h30m", "en 3d", "en 1d12h", "en 90m", "en ", "en 999999d",
		"45s", "in 730d", "in 731d", "in 99999999999999999999d",
		"2026-12-20 18:00", "2026-12-20 18:00:30", "2026-12-20T18:00:00Z", "2026-02-30 10:00",
		"in \u0663d", "\u202ein 3d", "\U0001F600", strings.Repeat("9d", 100), "in 0s", "in -1d",
		"en 0m", "en -1d", "in 999999999d999999999h",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		got, empty, err := parseWhen(raw, whenNow, loc)
		if err != nil {
			if !errors.Is(err, errBadTime) || empty || !got.IsZero() {
				t.Fatalf("parseWhen(%q) = %v, %v, %v", raw, got, empty, err)
			}
			return
		}
		if empty {
			if !got.IsZero() || strings.TrimSpace(raw) != "" {
				t.Fatalf("parseWhen(%q) = %v, empty", raw, got)
			}
			return
		}

		if got.IsZero() || got.Location() != time.UTC {
			t.Fatalf("parseWhen(%q) = %v, want a UTC time", raw, got)
		}

		lower := strings.ToLower(strings.TrimSpace(raw))
		isRelative := strings.HasPrefix(lower, "in ") || strings.HasPrefix(lower, "en ")
		if isRelative {
			if !got.After(whenNow) || got.Sub(whenNow) > 730*24*time.Hour {
				t.Fatalf("parseWhen(%q) = %v, want within 730 days after now", raw, got)
			}
		}
	})
}
