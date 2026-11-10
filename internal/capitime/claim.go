package capitime

import (
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const maxInspectMessageBytes = 1024

var santiagoLocation = sync.OnceValue(func() *time.Location {
	loc, err := time.LoadLocation(Timezone)
	if err != nil {
		panic("capitime: failed to load America/Santiago location: " + err.Error())
	}

	return loc
})

// Santiago returns the America/Santiago time.Location used across the capitime module.
func Santiago() *time.Location {
	return santiagoLocation()
}

// ArrivalClaim describes an arrival promise read from a message.
type ArrivalClaim struct {
	Detected     bool
	Immediate    bool
	ClaimedAt    time.Time
	ClaimSummary string
}

func dateAtClock(day time.Time, hour, minute int) time.Time {
	loc := day.Location()
	return time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, loc)
}

// resolveNextClockInstant returns the next occurrence of hour:minute seen from ref. A time that
// passed at most 15 minutes ago still counts as today, so a slow clock does not push it a day.
func resolveNextClockInstant(ref time.Time, hour, minute int) time.Time {
	candidate := dateAtClock(ref, hour, minute)
	if candidate.Before(ref.Add(-15 * time.Minute)) {
		candidate = dateAtClock(ref.AddDate(0, 0, 1), hour, minute)
	}

	return candidate
}

// sanitizeInputText returns raw cut to maxInspectMessageBytes, with every control character
// replaced by a space and the ends trimmed. It returns "" for text that is not valid UTF-8.
func sanitizeInputText(raw string) string {
	if raw == "" || !utf8.ValidString(raw) {
		return ""
	}
	if len(raw) > maxInspectMessageBytes {
		raw = raw[:maxInspectMessageBytes]
		for !utf8.ValidString(raw) && len(raw) > 0 {
			raw = raw[:len(raw)-1]
		}
	}

	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range raw {
		if unicode.IsControl(r) {
			b.WriteByte(' ')
			continue
		}
		b.WriteRune(r)
	}

	return strings.TrimSpace(b.String())
}
