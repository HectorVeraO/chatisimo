package capitime

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	maxInspectMessageBytes = 1024
	llmClassifyTimeout     = 3 * time.Second
)

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

// ArrivalClaim describes a parsed arrival promise extracted from a message.
type ArrivalClaim struct {
	Detected     bool
	Immediate    bool
	ClaimedAt    time.Time
	ClaimSummary string
}

var (
	clock24Re = regexp.MustCompile(`\b([01]?\d|2[0-3]):([0-5]\d)\b`)
	clock12Re = regexp.MustCompile(`(?i)\b(?:a\s+las\s+|tipo\s+|hasta\s+las\s+)(1[0-2]|0?[1-9])(?::([0-5]\d))?\s*(am|pm|hrs?)?\b`)
	inMinsRe  = regexp.MustCompile(`(?i)\b(?:en\s+|unos?\s+)?(\d{1,3})\s*(?:min(?:utos?)?|m)\b`)

	pastTenseRe = regexp.MustCompile(
		`(?i)\b(ayer|anoche|anteayer|pasad[oa]s?|hace\s+rato|ya\s+llegu[eé]|llegu[eé]|me\s+conect[eé]|fui\s+a|estuve\s+en|cuando\s+llegu[eé]|hab[ií]a\s+llegado|llegaron|llegamos\s+ayer)\b`,
	)

	thirdPersonOtherRe = regexp.MustCompile(
		`(?i)\b(mi\s+(?:hermano|hermana|mam[aá]|pap[aá]|viejo|vieja|polola|pololo|se[nñ]ora|t[ií]o|t[ií]a|primo|prima|jefe|amigo)|el\s+(?:uber|didi|cabify|bus|metro|tren|avi[oó]n|pedido|repartidor|delivery|partido|profe)|la\s+(?:micro|pizza|comida| encomienda)|ellos\s+llegan|ellas\s+llegan|(?:juan|pedro|diego|felipe|mat[ií]as|nicol[aá]s|andres|andr[eé]s|carlos|javier)\s+(?:llega|dijo|viene))\b`,
	)

	selfNicknameRe = regexp.MustCompile(`(?i)\b(capi|capis|braulio|braulitros)\b`)

	firstPersonArrivalVerbRe = regexp.MustCompile(
		`(?i)\b(llego|llegando|lleganding|voy|vengo|salgo|saliendo|caigo|entro|me\s+conecto|conectando|me\s+meto|estar[eé]|aparezco|prendo|juego|jugamos|toy\s+ready|estoy\s+ready|ready|en\s+camino|in\s+pc|en\s+el\s+pc|al\s+pc|hasta\s+las|a\s+las|tipo)\b`,
	)

	thirdPersonSingularVerbRe = regexp.MustCompile(`(?i)\b(llega|viene|va\s+saliendo|est[aá]\s+llegando|est[aá]\s+ready)\b`)
)

type immediateToken struct {
	pattern *regexp.Regexp
	summary string
}

var immediateTokens = []immediateToken{
	{pattern: regexp.MustCompile(`(?i)\bvoy\s+al\s*tiro\b`), summary: "voy altiro"},
	{pattern: regexp.MustCompile(`(?i)\bvoy\s+voy\b`), summary: "voy voy"},
	{pattern: regexp.MustCompile(`(?i)\b(?:toy|estoy)\s+ready\b`), summary: "toy ready"},
	{pattern: regexp.MustCompile(`(?i)\blleganding\b`), summary: "lleganding"},
	{pattern: regexp.MustCompile(`(?i)\bllegando\b`), summary: "llegando"},
	{pattern: regexp.MustCompile(`(?i)\bvoy\s+saliendo\b`), summary: "voy saliendo"},
	{pattern: regexp.MustCompile(`(?i)\bsaliendo\b`), summary: "saliendo"},
	{pattern: regexp.MustCompile(`(?i)\ben\s+camino\b`), summary: "en camino"},
	{pattern: regexp.MustCompile(`(?i)\bme\s+conecto\s+altiro\b`), summary: "me conecto altiro"},
	{pattern: regexp.MustCompile(`(?i)\bentro\s+altiro\b`), summary: "entro altiro"},
	{pattern: regexp.MustCompile(`(?i)\bah[ií]\s+voy\b`), summary: "ahi voy"},
	{pattern: regexp.MustCompile(`(?i)\bya\s+voy\b`), summary: "ya voy"},
	{pattern: regexp.MustCompile(`(?i)\bready\b`), summary: "ready"},
}

type weekdayRule struct {
	pattern *regexp.Regexp
	weekday time.Weekday
	summary string
}

var weekdayRules = []weekdayRule{
	{pattern: regexp.MustCompile(`(?i)\b(?:el\s+)?lunes\b`), weekday: time.Monday, summary: "el lunes"},
	{pattern: regexp.MustCompile(`(?i)\b(?:el\s+)?martes\b`), weekday: time.Tuesday, summary: "el martes"},
	{pattern: regexp.MustCompile(`(?i)\b(?:el\s+)?mi[eé]rcoles\b`), weekday: time.Wednesday, summary: "el miercoles"},
	{pattern: regexp.MustCompile(`(?i)\b(?:el\s+)?jueves\b`), weekday: time.Thursday, summary: "el jueves"},
	{pattern: regexp.MustCompile(`(?i)\b(?:el\s+)?viernes\b`), weekday: time.Friday, summary: "el viernes"},
	{pattern: regexp.MustCompile(`(?i)\b(?:el\s+)?s[aá]bado\b`), weekday: time.Saturday, summary: "el sabado"},
	{pattern: regexp.MustCompile(`(?i)\b(?:el\s+)?domingo\b`), weekday: time.Sunday, summary: "el domingo"},
}

var (
	todayNightRe        = regexp.MustCompile(`(?i)\b(?:hoy\s+(?:en\s+la\s+|por\s+la\s+|a\s+la\s+|al\s+)?(?:noche|night)|(?:esta|en\s+la|a\s+la)\s+noche|al\s+night)\b`)
	todayAfternoonRe    = regexp.MustCompile(`(?i)\b(?:hoy\s+)?(?:en\s+la\s+|por\s+la\s+)tarde\b`)
	laterTodayRe        = regexp.MustCompile(`(?i)\b(?:m[aá]s\s+rato|m[aá]s\s+tarde|en\s+un\s+rato)\b`)
	tomorrowMorningRe   = regexp.MustCompile(`(?i)\bma[nñ]ana\s+(?:en\s+la\s+|por\s+la\s+)?ma[nñ]ana\b`)
	tomorrowAfternoonRe = regexp.MustCompile(`(?i)\bma[nñ]ana\s+(?:en\s+la\s+|por\s+la\s+)?tarde\b`)
	tomorrowNightRe     = regexp.MustCompile(`(?i)\bma[nñ]ana\s+(?:en\s+la\s+|por\s+la\s+)?noche\b`)
	tomorrowBareRe      = regexp.MustCompile(`(?i)\bma[nñ]ana\b`)
)

// ParseArrivalMessage evaluates raw message text at reference instant now using Stage 1 regex and
// token heuristics in America/Santiago time.
func ParseArrivalMessage(raw string, now time.Time) ArrivalClaim {
	claim, _ := parseStage1(raw, now, defaultCompiledRegex)
	return claim
}

func parseStage1(raw string, now time.Time, customRegex *regexp.Regexp) (ArrivalClaim, bool) {
	cleaned := sanitizeInputText(raw)
	if cleaned == "" {
		return ArrivalClaim{}, true
	}

	hasSelfNickname := selfNicknameRe.MatchString(cleaned)
	if pastTenseRe.MatchString(cleaned) {
		return ArrivalClaim{}, true
	}
	if thirdPersonOtherRe.MatchString(cleaned) && !hasSelfNickname {
		return ArrivalClaim{}, true
	}

	hasFirstPerson := firstPersonArrivalVerbRe.MatchString(cleaned)
	hasThirdPersonVerb := thirdPersonSingularVerbRe.MatchString(cleaned)
	if hasThirdPersonVerb && !hasSelfNickname && !hasFirstPerson {
		return ArrivalClaim{}, true
	}

	loc := Santiago()
	ref := now.In(loc)

	// Check tomorrow / weekday expressions with optional explicit clock time.
	if dayClaim, ok := parseDayOrWeekdayClaim(cleaned, ref, hasFirstPerson || hasSelfNickname); ok {
		return dayClaim, true
	}

	// Check explicit 24-hour clock time (e.g. "22:30 llego", "00:00", "hasta las 23:40").
	if clockClaim, ok := parseExplicitClockClaim(cleaned, ref, hasFirstPerson || hasSelfNickname); ok {
		return clockClaim, true
	}

	// Check relative minute offset (e.g. "en 10 min llego", "voy en 15 minutos").
	if minsClaim, ok := parseRelativeMinutesClaim(cleaned, ref, hasFirstPerson || hasSelfNickname); ok {
		return minsClaim, true
	}

	// Check immediate Chilean arrival slang tokens ("voy altiro", "voy voy", "toy ready", "llegando").
	for _, tok := range immediateTokens {
		if tok.pattern.MatchString(cleaned) {
			return ArrivalClaim{
				Detected:     true,
				Immediate:    true,
				ClaimedAt:    ref,
				ClaimSummary: tok.summary,
			}, true
		}
	}

	// If a custom regex pattern was configured and matched where standard tokens did not, treat as
	// immediate arrival when accompanied by first-person or nickname context.
	if customRegex != nil && customRegex != defaultCompiledRegex && customRegex.MatchString(cleaned) {
		return ArrivalClaim{
			Detected:     true,
			Immediate:    true,
			ClaimedAt:    ref,
			ClaimSummary: "ahora",
		}, true
	}

	return ArrivalClaim{}, false
}

func parseDayOrWeekdayClaim(cleaned string, ref time.Time, hasIntentContext bool) (ArrivalClaim, bool) {
	if !hasIntentContext {
		return ArrivalClaim{}, false
	}

	clockHour, clockMin, hasClock := extractClockHHMM(cleaned)

	if todayNightRe.MatchString(cleaned) && !tomorrowNightRe.MatchString(cleaned) {
		hour, minute := 22, 0
		if hasClock {
			hour, minute = clockHour, clockMin
		}
		target := resolveNextClockInstant(ref, hour, minute)
		return ArrivalClaim{
			Detected:     true,
			Immediate:    false,
			ClaimedAt:    target,
			ClaimSummary: "hoy en la noche",
		}, true
	}

	if todayAfternoonRe.MatchString(cleaned) && !tomorrowAfternoonRe.MatchString(cleaned) {
		hour, minute := 18, 0
		if hasClock {
			hour, minute = clockHour, clockMin
		}
		target := resolveNextClockInstant(ref, hour, minute)
		return ArrivalClaim{
			Detected:     true,
			Immediate:    false,
			ClaimedAt:    target,
			ClaimSummary: "hoy en la tarde",
		}, true
	}

	if laterTodayRe.MatchString(cleaned) {
		target := ref.Add(30 * time.Minute)
		if hasClock {
			target = resolveNextClockInstant(ref, clockHour, clockMin)
		}
		return ArrivalClaim{
			Detected:     true,
			Immediate:    false,
			ClaimedAt:    target,
			ClaimSummary: "mas rato",
		}, true
	}

	if tomorrowMorningRe.MatchString(cleaned) {
		hour, minute := 10, 0
		if hasClock {
			hour, minute = clockHour, clockMin
		}
		target := dateAtClock(ref.AddDate(0, 0, 1), hour, minute)
		return ArrivalClaim{
			Detected:     true,
			Immediate:    false,
			ClaimedAt:    target,
			ClaimSummary: "manana en la manana",
		}, true
	}

	if tomorrowAfternoonRe.MatchString(cleaned) {
		hour, minute := 16, 0
		if hasClock {
			hour, minute = clockHour, clockMin
		}
		target := dateAtClock(ref.AddDate(0, 0, 1), hour, minute)
		return ArrivalClaim{
			Detected:     true,
			Immediate:    false,
			ClaimedAt:    target,
			ClaimSummary: "manana en la tarde",
		}, true
	}

	if tomorrowNightRe.MatchString(cleaned) {
		hour, minute := 21, 0
		if hasClock {
			hour, minute = clockHour, clockMin
		}
		target := dateAtClock(ref.AddDate(0, 0, 1), hour, minute)
		return ArrivalClaim{
			Detected:     true,
			Immediate:    false,
			ClaimedAt:    target,
			ClaimSummary: "manana en la noche",
		}, true
	}

	if tomorrowBareRe.MatchString(cleaned) {
		hour, minute := 12, 0
		if hasClock {
			hour, minute = clockHour, clockMin
		}
		target := dateAtClock(ref.AddDate(0, 0, 1), hour, minute)
		return ArrivalClaim{
			Detected:     true,
			Immediate:    false,
			ClaimedAt:    target,
			ClaimSummary: "manana",
		}, true
	}

	for _, rule := range weekdayRules {
		if rule.pattern.MatchString(cleaned) {
			daysAhead := (int(rule.weekday) - int(ref.Weekday()) + 7) % 7
			if daysAhead == 0 {
				daysAhead = 7
			}
			hour, minute := 20, 0
			if hasClock {
				hour, minute = clockHour, clockMin
			}
			target := dateAtClock(ref.AddDate(0, 0, daysAhead), hour, minute)
			return ArrivalClaim{
				Detected:     true,
				Immediate:    false,
				ClaimedAt:    target,
				ClaimSummary: rule.summary,
			}, true
		}
	}

	return ArrivalClaim{}, false
}

func parseExplicitClockClaim(cleaned string, ref time.Time, hasIntentContext bool) (ArrivalClaim, bool) {
	hour, minute, hasClock := extractClockHHMM(cleaned)
	if !hasClock {
		return ArrivalClaim{}, false
	}

	// Standalone clock reply (e.g. "00:00", "22:30") or clock accompanied by arrival/nickname intent.
	trimmed := strings.TrimSpace(cleaned)
	isBareClock := clock24Re.MatchString(trimmed) && len(trimmed) <= 12
	if !isBareClock && !hasIntentContext {
		return ArrivalClaim{}, false
	}

	target := resolveNextClockInstant(ref, hour, minute)
	summary := fmt.Sprintf("%02d:%02d", hour, minute)

	return ArrivalClaim{
		Detected:     true,
		Immediate:    false,
		ClaimedAt:    target,
		ClaimSummary: summary,
	}, true
}

func parseRelativeMinutesClaim(cleaned string, ref time.Time, hasIntentContext bool) (ArrivalClaim, bool) {
	if !hasIntentContext {
		return ArrivalClaim{}, false
	}

	sub := inMinsRe.FindStringSubmatch(cleaned)
	if len(sub) < 2 {
		return ArrivalClaim{}, false
	}

	mins, err := strconv.Atoi(sub[1])
	if err != nil || mins <= 0 || mins > 720 {
		return ArrivalClaim{}, false
	}

	target := ref.Add(time.Duration(mins) * time.Minute)
	summary := fmt.Sprintf("en %d min", mins)

	return ArrivalClaim{
		Detected:     true,
		Immediate:    false,
		ClaimedAt:    target,
		ClaimSummary: summary,
	}, true
}

func extractClockHHMM(cleaned string) (int, int, bool) {
	if sub := clock24Re.FindStringSubmatch(cleaned); len(sub) == 3 {
		hour, errH := strconv.Atoi(sub[1])
		minute, errM := strconv.Atoi(sub[2])
		if errH == nil && errM == nil && hour >= 0 && hour <= 23 && minute >= 0 && minute <= 59 {
			return hour, minute, true
		}
	}

	if sub := clock12Re.FindStringSubmatch(cleaned); len(sub) >= 2 {
		hour, errH := strconv.Atoi(sub[1])
		if errH != nil || hour < 1 || hour > 12 {
			return 0, 0, false
		}
		minute := 0
		if len(sub) >= 3 && sub[2] != "" {
			parsedMin, errM := strconv.Atoi(sub[2])
			if errM != nil || parsedMin < 0 || parsedMin > 59 {
				return 0, 0, false
			}
			minute = parsedMin
		}
		meridiem := ""
		if len(sub) >= 4 {
			meridiem = strings.ToLower(sub[3])
		}
		switch {
		case meridiem == "pm" && hour < 12:
			hour += 12
		case meridiem == "am" && hour == 12:
			hour = 0
		case meridiem == "" && hour <= 11:
			hour += 12
		}

		return hour, minute, true
	}

	return 0, 0, false
}

func dateAtClock(day time.Time, hour, minute int) time.Time {
	loc := day.Location()
	return time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, loc)
}

func resolveNextClockInstant(ref time.Time, hour, minute int) time.Time {
	candidate := dateAtClock(ref, hour, minute)
	if candidate.Before(ref.Add(-15 * time.Minute)) {
		candidate = dateAtClock(ref.AddDate(0, 0, 1), hour, minute)
	}

	return candidate
}

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

const stage2SystemInstruction = "" +
	"Clasifica si un mensaje corto de Discord en espanol chileno expresa una promesa o intencion " +
	"en primera persona (o apodandose Capi/Braulio/Braulitros/Capis) de llegar, conectarse o estar " +
	"en camino en el presente o futuro cercano. " +
	"Rechaza relatos en pasado (llegue, ayer) o sobre terceros ajenos. " +
	"Responde UNICAMENTE un objeto JSON valido en una linea con el formato: " +
	`{"arrival":true,"immediate":true,"offset_minutes":0,"clock":""}` +
	" o " +
	`{"arrival":false}`

type stage2Response struct {
	Arrival       bool   `json:"arrival"`
	Immediate     bool   `json:"immediate"`
	OffsetMinutes int    `json:"offset_minutes"`
	Clock         string `json:"clock"`
}

func classifyStage2WithLLM(ctx context.Context, gen LLMGenerator, raw string, now time.Time) ArrivalClaim {
	if gen == nil || ctx == nil {
		return ArrivalClaim{}
	}

	cleaned := sanitizeInputText(raw)
	if cleaned == "" {
		return ArrivalClaim{}
	}

	callCtx, cancel := context.WithTimeout(ctx, llmClassifyTimeout)
	defer cancel()

	out, err := gen.GenerateWithPrompt(callCtx, stage2SystemInstruction, cleaned)
	if err != nil {
		return ArrivalClaim{}
	}

	trimmed := strings.TrimSpace(out)
	trimmed = strings.TrimPrefix(trimmed, "```json")
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimSuffix(trimmed, "```")
	trimmed = strings.TrimSpace(trimmed)

	var parsed stage2Response
	if unmarshalErr := json.Unmarshal([]byte(trimmed), &parsed); unmarshalErr != nil || !parsed.Arrival {
		return ArrivalClaim{}
	}

	ref := now.In(Santiago())
	if parsed.Clock != "" {
		if hour, minute, ok := extractClockHHMM(parsed.Clock); ok {
			target := resolveNextClockInstant(ref, hour, minute)
			return ArrivalClaim{
				Detected:     true,
				Immediate:    false,
				ClaimedAt:    target,
				ClaimSummary: fmt.Sprintf("%02d:%02d", hour, minute),
			}
		}
	}
	if parsed.OffsetMinutes > 0 && parsed.OffsetMinutes <= 720 {
		target := ref.Add(time.Duration(parsed.OffsetMinutes) * time.Minute)
		return ArrivalClaim{
			Detected:     true,
			Immediate:    false,
			ClaimedAt:    target,
			ClaimSummary: fmt.Sprintf("en %d min", parsed.OffsetMinutes),
		}
	}

	return ArrivalClaim{
		Detected:     true,
		Immediate:    true,
		ClaimedAt:    ref,
		ClaimSummary: "ahora",
	}
}
