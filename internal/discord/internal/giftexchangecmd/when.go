package giftexchangecmd

import (
	"strings"
	"time"
)

const (
	relativePrefix = "in "
	// maxWhenAheadSeconds bounds a relative time to 730 days. It also keeps the arithmetic far
	// from overflow: nine digits of days is under 10^14 seconds.
	maxWhenAheadSeconds int64 = 730 * 24 * 60 * 60
	// maxDurationDigits bounds one number of a duration.
	maxDurationDigits = 9

	layoutMinute = "2006-01-02 15:04"
	layoutSecond = "2006-01-02 15:04:05"
)

// parseWhen reads the time a person typed. Empty input returns empty = true. A duration with
// an optional "in " prefix (in 3d, 2h30m, 45s) is added to now. A date and time without a
// zone is read in loc, and an RFC 3339 time carries its own zone. The result is in UTC. Any
// other input returns errBadTime. Whether the time lies in the future is the module's rule.
func parseWhen(raw string, now time.Time, loc *time.Location) (time.Time, bool, error) {
	if loc == nil {
		panic("giftexchangecmd: parseWhen without a location")
	}

	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}, true, nil
	}

	lower := strings.ToLower(trimmed)
	duration, isDuration := parseDuration(strings.TrimSpace(strings.TrimPrefix(lower, relativePrefix)))
	if isDuration {
		when := now.Add(duration).UTC()
		return when, false, nil
	}

	for _, layout := range []string{layoutMinute, layoutSecond} {
		parsed, err := time.ParseInLocation(layout, trimmed, loc)
		if err == nil {
			return parsed.UTC(), false, nil
		}
	}

	parsed, err := time.Parse(time.RFC3339, trimmed)
	if err == nil {
		return parsed.UTC(), false, nil
	}

	return time.Time{}, false, errBadTime
}

// parseDuration reads one or more <int><unit> pairs with units d, h, m and s. It accepts a
// total above zero and up to 730 days only.
func parseDuration(s string) (time.Duration, bool) {
	if s == "" {
		return 0, false
	}

	var totalSeconds int64
	rest := s
	for rest != "" {
		digits := 0
		var amount int64
		for digits < len(rest) && rest[digits] >= '0' && rest[digits] <= '9' {
			amount = amount*10 + int64(rest[digits]-'0')
			digits++
			if digits > maxDurationDigits {
				return 0, false
			}
		}

		hasUnit := digits > 0 && digits < len(rest)
		if !hasUnit {
			return 0, false
		}

		perUnit, known := unitSeconds(rest[digits])
		if !known {
			return 0, false
		}

		totalSeconds += amount * perUnit
		if totalSeconds > maxWhenAheadSeconds {
			return 0, false
		}
		rest = rest[digits+1:]
	}

	if totalSeconds <= 0 {
		return 0, false
	}

	total := time.Duration(totalSeconds) * time.Second
	return total, true
}

func unitSeconds(c byte) (int64, bool) {
	switch c {
	case 'd':
		return 24 * 60 * 60, true
	case 'h':
		return 60 * 60, true
	case 'm':
		return 60, true
	case 's':
		return 1, true
	default:
		return 0, false
	}
}
