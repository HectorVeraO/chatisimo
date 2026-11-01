package uccalendar_test

import (
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/uccalendar"
)

const ucInputErrorVocabulary = "uccalendar: invalid input: query window date clock slang_cache_ttl snowflake api_base_url locale banter_prompt " +
	"(required) (empty) (unknown) (mutually_exclusive) (invalid_date) (year_out_of_range) " +
	"(invalid_clock) (invalid_duration) (duration_out_of_range) (invalid_snowflake) (invalid_url) (invalid_locale) (invalid_prompt)"

func parserAbuseCorpus() []string {
	return []string{
		strings.Repeat("9", 4096),
		"a\u0301\u030831/10",
		"\u202e17:00",
		"09:\u200b00",
		"\U0001f60024h",
		"<@100000000000000001>",
		"@everyone",
	}
}

func assertNoErrorInputEcho(t *testing.T, err error, raw string) {
	t.Helper()

	if err == nil || len(raw) < 4 {
		return
	}

	msg := err.Error()
	if !strings.Contains(ucInputErrorVocabulary, raw) && strings.Contains(msg, raw) {
		t.Fatalf("error %q echoed rejected input %q", msg, raw)
	}
}

func TestLocalDateMethodsAndChileDST(t *testing.T) {
	t.Parallel()

	var zero uccalendar.LocalDate
	if !zero.IsZero() {
		t.Fatal("zero.IsZero() = false, want true")
	}

	if zero.String() != "" || zero.DDMM() != "" {
		t.Errorf("zero formatting = (%q, %q), want both empty", zero.String(), zero.DDMM())
	}

	loc := santiagoLocation(t)

	// Chile springs forward at 00:00 -> 01:00 on the first Sunday of September (2026-09-06).
	dstSunday := uccalendar.LocalDate{Year: 2026, Month: time.September, Day: 6}
	start := dstSunday.StartOfDay(loc)
	if start.Year() != 2026 || start.Month() != time.September || start.Day() != 6 {
		t.Errorf("StartOfDay on Chile DST Sunday = %v, want calendar date 2026-09-06", start)
	}

	if dstSunday.Weekday(loc) != time.Sunday {
		t.Errorf("Weekday on Chile DST Sunday = %v, want Sunday", dstSunday.Weekday(loc))
	}

	if dstSunday.String() != "2026-09-06" || dstSunday.DDMM() != "06/09" {
		t.Errorf("formatting = (%q, %q), want (2026-09-06, 06/09)", dstSunday.String(), dstSunday.DDMM())
	}

	utcStart := dstSunday.StartOfDay(nil)
	utcEnd := dstSunday.EndOfDay(nil)
	if utcStart.Location() != time.UTC || utcEnd.Location() != time.UTC {
		t.Errorf("nil location fallback = (%v, %v), want UTC", utcStart.Location(), utcEnd.Location())
	}

	if dstSunday.Weekday(nil) != time.Sunday {
		t.Errorf("Weekday(nil) = %v, want Sunday", dstSunday.Weekday(nil))
	}

	next := dstSunday.AddDays(1)
	prev := dstSunday.AddDays(-1)
	sameDay := uccalendar.LocalDate{Year: 2026, Month: time.September, Day: 6}
	if dstSunday.Compare(next) != -1 || dstSunday.Compare(prev) != 1 || dstSunday.Compare(sameDay) != 0 {
		t.Errorf("Compare day mismatch for %v vs %v / %v", dstSunday, prev, next)
	}

	nextMonth := uccalendar.LocalDate{Year: 2026, Month: time.October, Day: 6}
	prevMonth := uccalendar.LocalDate{Year: 2026, Month: time.August, Day: 6}
	if dstSunday.Compare(nextMonth) != -1 || dstSunday.Compare(prevMonth) != 1 {
		t.Error("Compare month mismatch")
	}

	nextYear := uccalendar.LocalDate{Year: 2027, Month: time.September, Day: 6}
	prevYear := uccalendar.LocalDate{Year: 2025, Month: time.September, Day: 6}
	if dstSunday.Compare(nextYear) != -1 || dstSunday.Compare(prevYear) != 1 {
		t.Error("Compare year mismatch")
	}

	// Chile falls back at midnight at the end of the first Saturday of April (2026-04-04 24:00 -03 -> 23:00 -04),
	// repeating 23:00..23:59:59 in -04. EndOfDay must cover the second (-04) 23:30 hour.
	fallBackSat := uccalendar.LocalDate{Year: 2026, Month: time.April, Day: 4}
	secondElevenThirty := time.Date(2026, time.April, 5, 3, 30, 0, 0, time.UTC).In(loc)
	satEnd := fallBackSat.EndOfDay(loc)
	if secondElevenThirty.After(satEnd) {
		t.Errorf("Chile fall-back Saturday EndOfDay = %v, excluded second 23:30 -04 instant %v", satEnd, secondElevenThirty)
	}
}

func TestParseDate(t *testing.T) {
	t.Parallel()

	validCases := []struct {
		name        string
		raw         string
		currentYear int
		want        uccalendar.LocalDate
	}{
		{
			name:        "DD/MM in currentYear",
			raw:         "31/10",
			currentYear: 2026,
			want:        uccalendar.LocalDate{Year: 2026, Month: time.October, Day: 31},
		},
		{
			name:        "leap day DD/MM in leap year",
			raw:         "29/02",
			currentYear: 2024,
			want:        uccalendar.LocalDate{Year: 2024, Month: time.February, Day: 29},
		},
		{
			name:        "YYYY-MM-DD lower year boundary",
			raw:         "2000-01-01",
			currentYear: 2026,
			want:        uccalendar.LocalDate{Year: 2000, Month: time.January, Day: 1},
		},
		{
			name:        "YYYY-MM-DD upper year boundary",
			raw:         "2100-12-31",
			currentYear: 2026,
			want:        uccalendar.LocalDate{Year: 2100, Month: time.December, Day: 31},
		},
		{
			name:        "YYYY-MM-DD leap day",
			raw:         "2028-02-29",
			currentYear: 2026,
			want:        uccalendar.LocalDate{Year: 2028, Month: time.February, Day: 29},
		},
	}

	for _, tc := range validCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := uccalendar.ParseDate(tc.raw, tc.currentYear)
			if err != nil {
				t.Fatalf("ParseDate(%q, %d) unexpected error: %v", tc.raw, tc.currentYear, err)
			}

			if got != tc.want {
				t.Errorf("ParseDate(%q, %d) = %+v, want %+v", tc.raw, tc.currentYear, got, tc.want)
			}
		})
	}

	invalidCases := []struct {
		name        string
		raw         string
		currentYear int
		wantCode    uccalendar.InputCode
	}{
		{name: "empty", raw: "", currentYear: 2026, wantCode: uccalendar.InputCodeEmpty},
		{name: "31/02 impossible date", raw: "31/02", currentYear: 2026, wantCode: uccalendar.InputCodeInvalidDate},
		{name: "29/02 non-leap year", raw: "29/02", currentYear: 2026, wantCode: uccalendar.InputCodeInvalidDate},
		{name: "31/11 impossible date", raw: "31/11", currentYear: 2026, wantCode: uccalendar.InputCodeInvalidDate},
		{name: "2100-02-29 century non-leap", raw: "2100-02-29", currentYear: 2026, wantCode: uccalendar.InputCodeInvalidDate},
		{name: "00/01 zero day", raw: "00/01", currentYear: 2026, wantCode: uccalendar.InputCodeInvalidDate},
		{name: "10/13 month 13", raw: "10/13", currentYear: 2026, wantCode: uccalendar.InputCodeInvalidDate},
		{name: "1999-12-31 below min year", raw: "1999-12-31", currentYear: 2026, wantCode: uccalendar.InputCodeYearOutOfRange},
		{name: "2101-01-01 above max year", raw: "2101-01-01", currentYear: 2026, wantCode: uccalendar.InputCodeYearOutOfRange},
		{name: "DD/MM with out of range currentYear", raw: "15/10", currentYear: 1999, wantCode: uccalendar.InputCodeYearOutOfRange},
		{name: "leading whitespace", raw: " 31/10", currentYear: 2026, wantCode: uccalendar.InputCodeInvalidDate},
		{name: "single digit day", raw: "1/10", currentYear: 2026, wantCode: uccalendar.InputCodeInvalidDate},
		{name: "wrong separator on DD-MM", raw: "31-10", currentYear: 2026, wantCode: uccalendar.InputCodeInvalidDate},
		{name: "wrong separator on YYYY/MM/DD", raw: "2026/10/31", currentYear: 2026, wantCode: uccalendar.InputCodeInvalidDate},
		{name: "control char nul", raw: "31\x0010", currentYear: 2026, wantCode: uccalendar.InputCodeInvalidDate},
		{name: "non-digit YYYY-MM-DD", raw: "202a-10-31", currentYear: 2026, wantCode: uccalendar.InputCodeInvalidDate},
		{name: "mention abuse", raw: "<@10000001>", currentYear: 2026, wantCode: uccalendar.InputCodeInvalidDate},
		{name: "everyone mention", raw: "@everyone", currentYear: 2026, wantCode: uccalendar.InputCodeInvalidDate},
		{name: "combining marks", raw: "3\u03011/10", currentYear: 2026, wantCode: uccalendar.InputCodeInvalidDate},
		{name: "zero width space", raw: "31/\u200b10", currentYear: 2026, wantCode: uccalendar.InputCodeInvalidDate},
		{name: "emoji input", raw: "\U0001f60031/10", currentYear: 2026, wantCode: uccalendar.InputCodeInvalidDate},
		{name: "bidi override", raw: "\u202e31/10", currentYear: 2026, wantCode: uccalendar.InputCodeInvalidDate},
		{name: "very long input", raw: strings.Repeat("9", 4096), currentYear: 2026, wantCode: uccalendar.InputCodeInvalidDate},
	}

	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := uccalendar.ParseDate(tc.raw, tc.currentYear)
			if !errors.Is(err, uccalendar.ErrInvalidInput) {
				t.Fatalf("ParseDate(%q) error = %v, want ErrInvalidInput", tc.raw, err)
			}

			var inputErr *uccalendar.InputError
			if !errors.As(err, &inputErr) {
				t.Fatalf("ParseDate(%q) error %T, want *InputError", tc.raw, err)
			}

			if inputErr.Code != tc.wantCode {
				t.Errorf("InputError.Code = %q, want %q", inputErr.Code, tc.wantCode)
			}

			if tc.raw != "" && len(tc.raw) >= 4 && strings.Contains(err.Error(), tc.raw) {
				t.Errorf("error %q leaked rejected input %q", err.Error(), tc.raw)
			}
		})
	}
}

func TestParseClockHHMM(t *testing.T) {
	t.Parallel()

	validCases := []struct {
		raw      string
		wantHour int
		wantMin  int
	}{
		{raw: "00:00", wantHour: 0, wantMin: 0},
		{raw: "09:00", wantHour: 9, wantMin: 0},
		{raw: "17:30", wantHour: 17, wantMin: 30},
		{raw: "23:59", wantHour: 23, wantMin: 59},
	}

	for _, tc := range validCases {
		t.Run(tc.raw, func(t *testing.T) {
			t.Parallel()

			h, m, err := uccalendar.ParseClockHHMM(tc.raw)
			if err != nil {
				t.Fatalf("ParseClockHHMM(%q): %v", tc.raw, err)
			}

			if h != tc.wantHour || m != tc.wantMin {
				t.Errorf("ParseClockHHMM(%q) = (%d, %d), want (%d, %d)", tc.raw, h, m, tc.wantHour, tc.wantMin)
			}
		})
	}

	invalid := append(
		[]string{"", "9:00", "09:0", "24:00", "23:60", "12-00", " 09:00", "09:00 ", "ab:cd", "09\x0000"},
		parserAbuseCorpus()...,
	)
	for _, raw := range invalid {
		t.Run("reject", func(t *testing.T) {
			t.Parallel()

			_, _, err := uccalendar.ParseClockHHMM(raw)
			if !errors.Is(err, uccalendar.ErrInvalidInput) {
				t.Fatalf("ParseClockHHMM(%q) = %v, want ErrInvalidInput", raw, err)
			}

			assertNoErrorInputEcho(t, err, raw)
		})
	}
}

func TestParseSlangCacheTTL(t *testing.T) {
	t.Parallel()

	valid := []struct {
		raw  string
		want time.Duration
	}{
		{raw: "0", want: 0},
		{raw: "0s", want: 0},
		{raw: "0m", want: 0},
		{raw: "1m", want: time.Minute},
		{raw: "24h", want: 24 * time.Hour},
		{raw: "1h30m", want: 90 * time.Minute},
		{raw: "168h", want: 168 * time.Hour},
	}

	for _, tc := range valid {
		t.Run(tc.raw, func(t *testing.T) {
			t.Parallel()

			got, err := uccalendar.ParseSlangCacheTTL(tc.raw)
			if err != nil {
				t.Fatalf("ParseSlangCacheTTL(%q): %v", tc.raw, err)
			}

			if got != tc.want {
				t.Errorf("ParseSlangCacheTTL(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}

	invalid := []struct {
		raw      string
		wantCode uccalendar.InputCode
	}{
		{raw: "", wantCode: uccalendar.InputCodeEmpty},
		{raw: "59s", wantCode: uccalendar.InputCodeDurationOutOfRange},
		{raw: "168h1s", wantCode: uccalendar.InputCodeDurationOutOfRange},
		{raw: "1m500ms", wantCode: uccalendar.InputCodeInvalidDuration},
		{raw: "-5m", wantCode: uccalendar.InputCodeInvalidDuration},
		{raw: "+5m", wantCode: uccalendar.InputCodeInvalidDuration},
		{raw: " 24h", wantCode: uccalendar.InputCodeInvalidDuration},
		{raw: "forever", wantCode: uccalendar.InputCodeInvalidDuration},
	}

	for _, abuse := range parserAbuseCorpus() {
		invalid = append(invalid, struct {
			raw      string
			wantCode uccalendar.InputCode
		}{raw: abuse, wantCode: uccalendar.InputCodeInvalidDuration})
	}

	for _, tc := range invalid {
		t.Run("reject", func(t *testing.T) {
			t.Parallel()

			_, err := uccalendar.ParseSlangCacheTTL(tc.raw)
			if !errors.Is(err, uccalendar.ErrInvalidInput) {
				t.Fatalf("ParseSlangCacheTTL(%q) = %v, want ErrInvalidInput", tc.raw, err)
			}

			var inputErr *uccalendar.InputError
			if !errors.As(err, &inputErr) || inputErr.Code != tc.wantCode {
				t.Errorf("ParseSlangCacheTTL(%q) code = %v, want %q", tc.raw, err, tc.wantCode)
			}

			assertNoErrorInputEcho(t, err, tc.raw)
		})
	}
}

func TestValidateSnowflake(t *testing.T) {
	t.Parallel()

	valid := []string{"1", fakeFriendID, "18446744073709551615"}
	for _, raw := range valid {
		if err := uccalendar.ValidateSnowflake(raw); err != nil {
			t.Errorf("ValidateSnowflake(%q) unexpected error: %v", raw, err)
		}
	}

	invalid := append([]string{
		"",
		"0",
		"0123456789",
		"-12345",
		"+12345",
		" 100000000000000001",
		"100000000000000001 ",
		"18446744073709551616",
		"99999999999999999999",
		"123456789012345678901",
		"<@100000000000000001>",
		"12345abcde",
	}, parserAbuseCorpus()...)
	for _, raw := range invalid {
		err := uccalendar.ValidateSnowflake(raw)
		if !errors.Is(err, uccalendar.ErrInvalidInput) {
			t.Errorf("ValidateSnowflake(%q) = %v, want ErrInvalidInput", raw, err)
		}

		assertNoErrorInputEcho(t, err, raw)
	}
}

func TestParseGuildLocale(t *testing.T) {
	t.Parallel()

	for _, valid := range []string{uccalendar.LocaleESCL, uccalendar.LocaleENUS} {
		got, err := uccalendar.ParseGuildLocale(valid)
		if err != nil || got != valid {
			t.Fatalf("ParseGuildLocale(%q) = (%q, %v), want (%q, nil)", valid, got, err, valid)
		}
	}

	invalid := append([]string{
		"",
		"es",
		"es_CL",
		"es-AR",
		"en-GB",
		"en_US",
		"ES-CL",
		" es-CL",
		"es-CL ",
	}, parserAbuseCorpus()...)
	for _, raw := range invalid {
		_, err := uccalendar.ParseGuildLocale(raw)
		if !errors.Is(err, uccalendar.ErrInvalidInput) {
			t.Errorf("ParseGuildLocale(%q) = %v, want ErrInvalidInput", raw, err)
		}

		assertNoErrorInputEcho(t, err, raw)
	}
}

func TestValidateBanterPrompt(t *testing.T) {
	t.Parallel()

	got, err := uccalendar.ValidateBanterPrompt("  Line one\r\nLine two for kingcito  ")
	if err != nil {
		t.Fatalf("ValidateBanterPrompt valid multiline: %v", err)
	}
	if got != "Line one\nLine two for kingcito" {
		t.Errorf("ValidateBanterPrompt normalized = %q, want trimmed LF multiline", got)
	}

	if _, err := uccalendar.ValidateBanterPrompt(uccalendar.DefaultBanterPrompt); err != nil {
		t.Fatalf("ValidateBanterPrompt(DefaultBanterPrompt): %v", err)
	}

	invalid := []string{
		"",
		"   \n\r\n   ",
		"prompt with\x00nul",
		"prompt with\ttab",
		"prompt with \u202e bidi",
		string([]byte{0xff, 0xfe, 'a', 'b'}),
		strings.Repeat("k", uccalendar.MaxBanterPromptBytes+1),
		strings.Repeat("k", uccalendar.MaxBanterPromptBytes*2+10),
		"   \x07   ",
	}
	for _, raw := range invalid {
		_, err := uccalendar.ValidateBanterPrompt(raw)
		if !errors.Is(err, uccalendar.ErrInvalidInput) {
			t.Errorf("ValidateBanterPrompt(%d bytes) = %v, want ErrInvalidInput", len(raw), err)
		}

		assertNoErrorInputEcho(t, err, raw)
	}
}

func FuzzParseGuildLocale(f *testing.F) {
	seeds := append([]string{
		uccalendar.LocaleESCL,
		uccalendar.LocaleENUS,
		"",
		"es",
		"es_CL",
		"en_US",
		"es-AR",
	}, parserAbuseCorpus()...)
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		got, err := uccalendar.ParseGuildLocale(raw)
		if err != nil {
			if !errors.Is(err, uccalendar.ErrInvalidInput) {
				t.Fatalf("ParseGuildLocale error %v does not match ErrInvalidInput", err)
			}

			assertNoErrorInputEcho(t, err, raw)

			return
		}

		if got != uccalendar.LocaleESCL && got != uccalendar.LocaleENUS {
			t.Fatalf("ParseGuildLocale(%q) accepted unsupported locale %q", raw, got)
		}
	})
}

func FuzzValidateBanterPrompt(f *testing.F) {
	seeds := append([]string{
		uccalendar.DefaultBanterPrompt,
		"Short prompt\r\nsecond line",
		"",
		"   \n  ",
		"bad\x00prompt",
		"bad\tprompt",
	}, parserAbuseCorpus()...)
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		got, err := uccalendar.ValidateBanterPrompt(raw)
		if err != nil {
			if !errors.Is(err, uccalendar.ErrInvalidInput) {
				t.Fatalf("ValidateBanterPrompt error %v does not match ErrInvalidInput", err)
			}

			assertNoErrorInputEcho(t, err, raw)

			return
		}

		if got == "" || len(got) > uccalendar.MaxBanterPromptBytes || strings.ContainsRune(got, '\r') {
			t.Fatalf("ValidateBanterPrompt accepted out-of-contract prompt len=%d", len(got))
		}
	})
}

func FuzzParseDate(f *testing.F) {
	seeds := append([]string{
		"31/10",
		"29/02",
		"31/02",
		"2026-10-31",
		"1999-01-01",
		"2101-12-31",
		"",
		" 31/10 ",
		"\u202e31/10\x00",
	}, parserAbuseCorpus()...)
	for _, s := range seeds {
		f.Add(s, 2026)
	}

	f.Fuzz(func(t *testing.T, raw string, year int) {
		got, err := uccalendar.ParseDate(raw, year)
		if err != nil {
			if !errors.Is(err, uccalendar.ErrInvalidInput) {
				t.Fatalf("ParseDate error %v does not match ErrInvalidInput", err)
			}

			assertNoErrorInputEcho(t, err, raw)

			return
		}

		if got.Year < 2000 || got.Year > 2100 {
			t.Fatalf("ParseDate(%q, %d) accepted out-of-range year %d", raw, year, got.Year)
		}

		check := time.Date(got.Year, got.Month, got.Day, 12, 0, 0, 0, time.UTC)
		if check.Year() != got.Year || check.Month() != got.Month || check.Day() != got.Day {
			t.Fatalf("ParseDate(%q, %d) returned non-existent calendar date %+v", raw, year, got)
		}
	})
}

func FuzzParseClockHHMM(f *testing.F) {
	seeds := append([]string{
		"00:00",
		"09:00",
		"17:00",
		"23:59",
		"24:00",
		"12:60",
		"9:00",
		"",
		"\x00\x00:\x00\x00",
	}, parserAbuseCorpus()...)
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		h, m, err := uccalendar.ParseClockHHMM(raw)
		if err != nil {
			if !errors.Is(err, uccalendar.ErrInvalidInput) {
				t.Fatalf("ParseClockHHMM error %v does not match ErrInvalidInput", err)
			}

			assertNoErrorInputEcho(t, err, raw)

			return
		}

		if len(raw) != 5 || raw[2] != ':' {
			t.Fatalf("ParseClockHHMM(%q) accepted non-5-byte HH:MM", raw)
		}

		if h < 0 || h > 23 || m < 0 || m > 59 {
			t.Fatalf("ParseClockHHMM(%q) returned out-of-range (%d, %d)", raw, h, m)
		}
	})
}

func FuzzParseSlangCacheTTL(f *testing.F) {
	seeds := append(
		[]string{"0", "0s", "1m", "24h", "168h", "59s", "169h", "-1m", "1m500ms", ""},
		parserAbuseCorpus()...,
	)
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		got, err := uccalendar.ParseSlangCacheTTL(raw)
		if err != nil {
			if !errors.Is(err, uccalendar.ErrInvalidInput) {
				t.Fatalf("ParseSlangCacheTTL error %v does not match ErrInvalidInput", err)
			}

			assertNoErrorInputEcho(t, err, raw)

			return
		}

		if got != 0 && (got < uccalendar.MinSlangCacheTTL || got > uccalendar.MaxSlangCacheTTL || got%time.Second != 0) {
			t.Fatalf("ParseSlangCacheTTL(%q) accepted invalid duration %v", raw, got)
		}
	})
}

func FuzzValidateSnowflake(f *testing.F) {
	seeds := append(
		[]string{"1", fakeFriendID, "0", "01", "18446744073709551615", "18446744073709551616", "<@1>"},
		parserAbuseCorpus()...,
	)
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		err := uccalendar.ValidateSnowflake(raw)
		if err != nil {
			if !errors.Is(err, uccalendar.ErrInvalidInput) {
				t.Fatalf("ValidateSnowflake error %v does not match ErrInvalidInput", err)
			}

			assertNoErrorInputEcho(t, err, raw)

			return
		}

		if len(raw) < 1 || len(raw) > 20 || raw[0] < '1' || raw[0] > '9' {
			t.Fatalf("ValidateSnowflake(%q) accepted invalid snowflake", raw)
		}
	})
}

func FuzzCheckQuery(f *testing.F) {
	svc, err := uccalendar.New(uccalendar.Deps{
		Pool:     shared.Pool(),
		Settings: newMapSettings(nil),
		Clock:    newFakeClock(time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)),
		Logger:   slog.New(slog.DiscardHandler),
	})
	if err != nil {
		f.Fatalf("uccalendar.New: %v", err)
	}
	f.Cleanup(func() {
		_ = svc.Close()
	})

	for _, win := range []string{"", "today", "tomorrow", "this_weekend", "next_weekend", "this_month", "bad_window"} {
		f.Add(win, "")
	}
	for _, date := range []string{"31/10", "2026-10-31", "99/99", "2026-02-29"} {
		f.Add("", date)
		f.Add("today", date)
	}
	for _, abuse := range parserAbuseCorpus() {
		f.Add(abuse, "")
		f.Add("", abuse)
		f.Add(abuse, abuse)
	}

	f.Fuzz(func(t *testing.T, rawWindow string, rawDate string) {
		_, checkErr := svc.Check(t.Context(), uccalendar.Query{
			Window: uccalendar.WindowKind(rawWindow),
			Date:   rawDate,
		})
		if checkErr == nil {
			t.Fatal("unsynced Service.Check returned nil error")
		}

		// Valid queries reach the unsynced schedule guard; all invalid queries must match ErrInvalidInput.
		if errors.Is(checkErr, uccalendar.ErrScheduleUnavailable) {
			if rawWindow != "" && rawDate != "" {
				t.Fatalf("Check(%q, %q) accepted mutually exclusive options", rawWindow, rawDate)
			}

			return
		}

		if !errors.Is(checkErr, uccalendar.ErrInvalidInput) {
			t.Fatalf("Check(%q, %q) error %v does not match ErrInvalidInput", rawWindow, rawDate, checkErr)
		}

		assertNoErrorInputEcho(t, checkErr, rawWindow)
		assertNoErrorInputEcho(t, checkErr, rawDate)
	})
}
