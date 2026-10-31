package uccalendar

import (
	"errors"
	"strconv"
	"time"
)

const (
	minAllowedYear   = 2000
	maxAllowedYear   = 2100
	maxSnowflakeLen  = 20
	clockFormatBytes = 5
	ddmmBytes        = 5
	yyyymmddBytes    = 10
)

// ParseDate parses raw as either strict 5-byte DD/MM (resolved in currentYear) or strict 10-byte
// YYYY-MM-DD for years in [2000, 2100], verifying calendar validity.
func ParseDate(raw string, currentYear int) (LocalDate, error) {
	if raw == "" {
		return LocalDate{}, &InputError{Field: "date", Code: InputCodeEmpty}
	}

	switch len(raw) {
	case ddmmBytes:
		return parseDDMM(raw, currentYear)
	case yyyymmddBytes:
		return parseYYYYMMDD(raw)
	default:
		return LocalDate{}, &InputError{Field: "date", Code: InputCodeInvalidDate}
	}
}

func parseDDMM(raw string, currentYear int) (LocalDate, error) {
	var problems []error

	yearInRange := currentYear >= minAllowedYear && currentYear <= maxAllowedYear
	if !yearInRange {
		problems = append(problems, &InputError{Field: "date", Code: InputCodeYearOutOfRange})
	}

	hasSlash := raw[2] == '/'
	day, okDay := parseASCIIUint(raw[0:2])
	monthNum, okMonth := parseASCIIUint(raw[3:5])
	isValidFormat := hasSlash && okDay && okMonth
	if !isValidFormat || !isValidCalendarDate(currentYear, monthNum, day) {
		problems = append(problems, &InputError{Field: "date", Code: InputCodeInvalidDate})
	}

	if len(problems) > 0 {
		return LocalDate{}, errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
	}

	parsed := LocalDate{
		Year:  currentYear,
		Month: time.Month(monthNum),
		Day:   day,
	}

	return parsed, nil
}

func parseYYYYMMDD(raw string) (LocalDate, error) {
	if raw[4] != '-' || raw[7] != '-' {
		return LocalDate{}, &InputError{Field: "date", Code: InputCodeInvalidDate}
	}

	year, okYear := parseASCIIUint(raw[0:4])
	monthNum, okMonth := parseASCIIUint(raw[5:7])
	day, okDay := parseASCIIUint(raw[8:10])
	if !okYear || !okMonth || !okDay {
		return LocalDate{}, &InputError{Field: "date", Code: InputCodeInvalidDate}
	}

	var problems []error

	yearInRange := year >= minAllowedYear && year <= maxAllowedYear
	if !yearInRange {
		problems = append(problems, &InputError{Field: "date", Code: InputCodeYearOutOfRange})
	}

	validCalendar := isValidCalendarDate(year, monthNum, day)
	if !validCalendar {
		problems = append(problems, &InputError{Field: "date", Code: InputCodeInvalidDate})
	}

	if len(problems) > 0 {
		return LocalDate{}, errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
	}

	parsed := LocalDate{
		Year:  year,
		Month: time.Month(monthNum),
		Day:   day,
	}

	return parsed, nil
}

func isValidCalendarDate(year, monthNum, day int) bool {
	if monthNum < 1 || monthNum > 12 || day < 1 || day > 31 {
		return false
	}

	month := time.Month(monthNum)
	check := time.Date(year, month, day, 12, 0, 0, 0, time.UTC)
	matches := check.Year() == year && check.Month() == month && check.Day() == day

	return matches
}

// ParseClockHHMM parses raw as a strict 5-byte 24-hour clock value HH:MM in 00:00..23:59.
func ParseClockHHMM(raw string) (int, int, error) {
	if raw == "" {
		return 0, 0, &InputError{Field: "clock", Code: InputCodeEmpty}
	}

	if len(raw) != clockFormatBytes || raw[2] != ':' {
		return 0, 0, &InputError{Field: "clock", Code: InputCodeInvalidClock}
	}

	hour, okHour := parseASCIIUint(raw[0:2])
	minute, okMinute := parseASCIIUint(raw[3:5])
	if !okHour || !okMinute {
		return 0, 0, &InputError{Field: "clock", Code: InputCodeInvalidClock}
	}

	if hour > 23 || minute > 59 {
		return 0, 0, &InputError{Field: "clock", Code: InputCodeInvalidClock}
	}

	return hour, minute, nil
}

// ParseSlangCacheTTL parses raw as a banter cache TTL. "0" and "0s" return 0 (disabling LLM
// banter); non-zero values must parse via time.ParseDuration as whole seconds within [1m, 168h].
func ParseSlangCacheTTL(raw string) (time.Duration, error) {
	if raw == "" {
		return 0, &InputError{Field: "slang_cache_ttl", Code: InputCodeEmpty}
	}

	if raw == "0" || raw == "0s" {
		return 0, nil
	}

	startsWithDigit := raw[0] >= '0' && raw[0] <= '9'
	if !startsWithDigit {
		return 0, &InputError{Field: "slang_cache_ttl", Code: InputCodeInvalidDuration}
	}

	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return 0, &InputError{Field: "slang_cache_ttl", Code: InputCodeInvalidDuration}
	}

	if parsed == 0 {
		return 0, nil
	}

	isWholeSecond := parsed%time.Second == 0
	if !isWholeSecond {
		return 0, &InputError{Field: "slang_cache_ttl", Code: InputCodeInvalidDuration}
	}

	inRange := parsed >= MinSlangCacheTTL && parsed <= MaxSlangCacheTTL
	if !inRange {
		return 0, &InputError{Field: "slang_cache_ttl", Code: InputCodeDurationOutOfRange}
	}

	return parsed, nil
}

// ValidateSnowflake verifies that raw is a valid Discord snowflake: 1 to 20 ASCII decimal digits,
// starting with '1'..'9', fitting in a non-zero uint64.
func ValidateSnowflake(raw string) error {
	if raw == "" {
		return &InputError{Field: "snowflake", Code: InputCodeEmpty}
	}

	if len(raw) > maxSnowflakeLen {
		return &InputError{Field: "snowflake", Code: InputCodeInvalidSnowflake}
	}

	startsNonZero := raw[0] >= '1' && raw[0] <= '9'
	if !startsNonZero {
		return &InputError{Field: "snowflake", Code: InputCodeInvalidSnowflake}
	}

	for i := range len(raw) {
		isDigit := raw[i] >= '0' && raw[i] <= '9'
		if !isDigit {
			return &InputError{Field: "snowflake", Code: InputCodeInvalidSnowflake}
		}
	}

	parsed, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || parsed == 0 {
		return &InputError{Field: "snowflake", Code: InputCodeInvalidSnowflake}
	}

	return nil
}

func parseASCIIUint(s string) (int, bool) {
	if s == "" {
		return 0, false
	}

	val := 0
	for i := range len(s) {
		ch := s[i]
		isDigit := ch >= '0' && ch <= '9'
		if !isDigit {
			return 0, false
		}

		val = val*10 + int(ch-'0')
	}

	return val, true
}
