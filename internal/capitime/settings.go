package capitime

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/HectorVeraO/chatisimo/internal/platform/config"
)

// Runtime setting keys in platform.setting for Capi Time arrival detection and banter.
const (
	SettingBanterPrompt     = "capitime.banter_prompt"
	SettingEasterEggPrompt  = "capitime.easter_egg_prompt"
	SettingEasterEggPercent = "capitime.easter_egg_percent"
	SettingDelayMinMinutes  = "capitime.delay_min_minutes"
	SettingDelayMaxMinutes  = "capitime.delay_max_minutes"
	SettingDebounceWindow   = "capitime.debounce_window"
	SettingRegexPattern     = "capitime.regex_pattern"
)

// Default runtime setting values used when the corresponding key in platform.setting is unset.
const (
	DefaultEasterEggPercent = "5"
	DefaultDelayMinMinutes  = "45"
	DefaultDelayMaxMinutes  = "180"
	DefaultDebounceWindow   = "2m"
	DefaultRegexPattern     = `(?i)(\b([01]?\d|2[0-3]):[0-5]\d\b|\b(voy\s+al\s*tiro|voy\s+altiro|voy\s+voy|toy\s+ready|estoy\s+ready|ready|llegando|lleganding|saliendo|en\s+camino|voy\s+saliendo|ya\s+voy|ah[ií]\s+voy|me\s+conecto\s+altiro|entro\s+altiro|llego|llega)\b)`

	DefaultBanterPrompt = "" +
		"Genera UNA sola frase corta (maximo 140 caracteres, una sola linea) de talla chilena de grupo de amigos " +
		"para traducir una promesa de llegada a 'Capi Time' (hora real con atraso exagerado). " +
		"Usa espanol chileno con tuteo (tienes, vas, llegas) y jerga natural como 'mi king', 'mi rey', 'la bendi' o 'quedo pana'. " +
		"Prohibido usar voseo argentino (tenes, vas a llegar vos, podes, sos). " +
		"No incluyas etiquetas <t:...> ni saltos de linea; responde solo la frase de talla."

	DefaultEasterEggPrompt = "" +
		"Genera UNA sola frase corta (maximo 140 caracteres, una sola linea) de sorpresa humoristica chilena " +
		"porque ocurrio el milagro del siglo: el Capi va a llegar exactamente a la hora que dijo, sin atraso. " +
		"Usa espanol chileno con tuteo y jerga como 'mi king', 'mi rey', 'la bendi' o 'quedo pana'. " +
		"Prohibido usar voseo argentino (tenes, podes, sos). " +
		"No incluyas etiquetas <t:...> ni saltos de linea; responde solo la frase."
)

const (
	defaultEasterEggPct   = 5
	defaultDelayMinMins   = 45
	defaultDelayMaxMins   = 180
	defaultDebounceDur    = 2 * time.Minute
	minDelayMinutes       = 1
	maxDelayMinutes       = 1440
	maxDebounceWindow     = 24 * time.Hour
	maxPromptBytes        = 16 << 10
	maxRegexPatternBytes  = 4096
	maxSettingStringBytes = 64
)

// SettingReader reads non-secret runtime settings by key, returning config.ErrNotSet when a key has
// no stored row.
type SettingReader interface {
	Get(ctx context.Context, name string) (string, error)
}

type runtimeSettings struct {
	banterPrompt     string
	easterEggPrompt  string
	easterEggPercent int
	delayMinMinutes  int
	delayMaxMinutes  int
	debounceWindow   time.Duration
	regexPattern     string
	compiledRegex    *regexp.Regexp
}

var defaultCompiledRegex = regexp.MustCompile(DefaultRegexPattern)

func defaultRuntimeSettings() runtimeSettings {
	return runtimeSettings{
		banterPrompt:     DefaultBanterPrompt,
		easterEggPrompt:  DefaultEasterEggPrompt,
		easterEggPercent: defaultEasterEggPct,
		delayMinMinutes:  defaultDelayMinMins,
		delayMaxMinutes:  defaultDelayMaxMins,
		debounceWindow:   defaultDebounceDur,
		regexPattern:     DefaultRegexPattern,
		compiledRegex:    defaultCompiledRegex,
	}
}

// ValidatePrompt validates a Capi Time banter or easter-egg prompt without echoing its contents.
func ValidatePrompt(raw string) error {
	var problems []error

	if len(raw) < 1 || len(raw) > maxPromptBytes {
		problems = append(problems, fmt.Errorf("prompt must be 1-%d bytes", maxPromptBytes))
	}
	if strings.TrimSpace(raw) == "" {
		problems = append(problems, errors.New("prompt must not be blank"))
	}
	if !utf8.ValidString(raw) {
		problems = append(problems, errors.New("prompt must be valid UTF-8"))
	} else {
		for _, r := range raw {
			isBadControl := r != '\n' && unicode.IsControl(r)
			if isBadControl {
				problems = append(problems, errors.New("prompt must not contain control characters other than newline"))
				break
			}
		}
	}

	return errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
}

// ParseEasterEggPercent parses an integer percentage in the inclusive range 0..100.
func ParseEasterEggPercent(raw string) (int, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || len(trimmed) > maxSettingStringBytes {
		return 0, errors.New("easter egg percent must be an integer between 0 and 100")
	}

	pct, err := strconv.Atoi(trimmed)
	if err != nil || pct < 0 || pct > 100 {
		return 0, errors.New("easter egg percent must be an integer between 0 and 100")
	}

	return pct, nil
}

// ParseDelayMinutes parses a positive delay bound in minutes in the inclusive range 1..1440.
func ParseDelayMinutes(raw string) (int, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || len(trimmed) > maxSettingStringBytes {
		return 0, fmt.Errorf("delay minutes must be an integer between %d and %d", minDelayMinutes, maxDelayMinutes)
	}

	minutes, err := strconv.Atoi(trimmed)
	if err != nil || minutes < minDelayMinutes || minutes > maxDelayMinutes {
		return 0, fmt.Errorf("delay minutes must be an integer between %d and %d", minDelayMinutes, maxDelayMinutes)
	}

	return minutes, nil
}

// ParseDebounceWindow parses a Go duration between 0s and 24h (whole seconds or minutes).
func ParseDebounceWindow(raw string) (time.Duration, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || len(trimmed) > maxSettingStringBytes || strings.HasPrefix(trimmed, "+") || strings.HasPrefix(trimmed, "-") {
		return 0, errors.New("debounce window must be a duration between 0s and 24h in whole seconds")
	}

	dur, err := time.ParseDuration(trimmed)
	if err != nil || dur < 0 || dur > maxDebounceWindow || dur%time.Second != 0 {
		return 0, errors.New("debounce window must be a duration between 0s and 24h in whole seconds")
	}

	return dur, nil
}

// ValidateRegexPattern validates that raw is non-empty valid UTF-8 without control characters and
// compiles as a Go regular expression.
func ValidateRegexPattern(raw string) error {
	_, err := compileRegexSetting(raw)
	return err
}

func compileRegexSetting(raw string) (*regexp.Regexp, error) {
	var problems []error

	if len(raw) < 1 || len(raw) > maxRegexPatternBytes {
		problems = append(problems, fmt.Errorf("regex pattern must be 1-%d bytes", maxRegexPatternBytes))
	}
	if strings.TrimSpace(raw) == "" {
		problems = append(problems, errors.New("regex pattern must not be blank"))
	}
	if !utf8.ValidString(raw) {
		problems = append(problems, errors.New("regex pattern must be valid UTF-8"))
	} else {
		for _, r := range raw {
			if unicode.IsControl(r) {
				problems = append(problems, errors.New("regex pattern must not contain control characters"))
				break
			}
		}
	}
	if len(problems) > 0 {
		return nil, errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
	}

	compiled, err := regexp.Compile(raw)
	if err != nil {
		return nil, errors.New("regex pattern is not a valid regular expression")
	}

	return compiled, nil
}

// ValidateSettings checks every capitime.* setting currently stored in reader and returns a joined
// error naming each invalid key without echoing raw setting values.
func ValidateSettings(ctx context.Context, reader SettingReader) error {
	if ctx == nil {
		return errors.New("capitime: context is nil")
	}
	if reader == nil {
		return nil
	}

	var problems []error

	check := func(key string, validate func(string) error) (string, bool) {
		raw, err := reader.Get(ctx, key)
		if err != nil {
			if errors.Is(err, config.ErrNotSet) {
				return "", false
			}
			problems = append(problems, fmt.Errorf("read %s: %w", key, err))
			return "", false
		}
		if valErr := validate(raw); valErr != nil {
			problems = append(
				problems,
				newInputError(ErrCodeSetting, fmt.Sprintf("%s: %s", key, valErr.Error()), valErr),
			)
			return "", false
		}

		return raw, true
	}

	check(SettingBanterPrompt, ValidatePrompt)
	check(SettingEasterEggPrompt, ValidatePrompt)
	check(SettingEasterEggPercent, func(v string) error {
		_, err := ParseEasterEggPercent(v)
		return err
	})
	minRaw, hasMin := check(SettingDelayMinMinutes, func(v string) error {
		_, err := ParseDelayMinutes(v)
		return err
	})
	maxRaw, hasMax := check(SettingDelayMaxMinutes, func(v string) error {
		_, err := ParseDelayMinutes(v)
		return err
	})
	if hasMin && hasMax {
		minVal, _ := ParseDelayMinutes(minRaw)
		maxVal, _ := ParseDelayMinutes(maxRaw)
		if minVal > maxVal {
			problems = append(
				problems,
				newInputError(
					ErrCodeSetting,
					fmt.Sprintf("%s must be less than or equal to %s", SettingDelayMinMinutes, SettingDelayMaxMinutes),
					nil,
				),
			)
		}
	}
	check(SettingDebounceWindow, func(v string) error {
		_, err := ParseDebounceWindow(v)
		return err
	})
	check(SettingRegexPattern, ValidateRegexPattern)

	return errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
}

func resolveRuntimeSettings(ctx context.Context, reader SettingReader) runtimeSettings {
	resolved := defaultRuntimeSettings()
	if reader == nil || ctx == nil {
		return resolved
	}

	if raw, ok := readOptionalSetting(ctx, reader, SettingBanterPrompt); ok {
		if ValidatePrompt(raw) == nil {
			resolved.banterPrompt = raw
		}
	}
	if raw, ok := readOptionalSetting(ctx, reader, SettingEasterEggPrompt); ok {
		if ValidatePrompt(raw) == nil {
			resolved.easterEggPrompt = raw
		}
	}
	if raw, ok := readOptionalSetting(ctx, reader, SettingEasterEggPercent); ok {
		if pct, err := ParseEasterEggPercent(raw); err == nil {
			resolved.easterEggPercent = pct
		}
	}
	if raw, ok := readOptionalSetting(ctx, reader, SettingDelayMinMinutes); ok {
		if mins, err := ParseDelayMinutes(raw); err == nil {
			resolved.delayMinMinutes = mins
		}
	}
	if raw, ok := readOptionalSetting(ctx, reader, SettingDelayMaxMinutes); ok {
		if mins, err := ParseDelayMinutes(raw); err == nil {
			resolved.delayMaxMinutes = mins
		}
	}
	if resolved.delayMinMinutes > resolved.delayMaxMinutes {
		resolved.delayMinMinutes, resolved.delayMaxMinutes = resolved.delayMaxMinutes, resolved.delayMinMinutes
	}
	if raw, ok := readOptionalSetting(ctx, reader, SettingDebounceWindow); ok {
		if dur, err := ParseDebounceWindow(raw); err == nil {
			resolved.debounceWindow = dur
		}
	}
	if raw, ok := readOptionalSetting(ctx, reader, SettingRegexPattern); ok {
		if compiled, err := compileRegexSetting(raw); err == nil {
			resolved.regexPattern = raw
			resolved.compiledRegex = compiled
		}
	}

	return resolved
}

func readOptionalSetting(ctx context.Context, reader SettingReader, key string) (string, bool) {
	raw, err := reader.Get(ctx, key)
	if err != nil {
		return "", false
	}

	return raw, true
}
