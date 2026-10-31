package main

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/HectorVeraO/chatisimo/internal/platform/config"
	"github.com/HectorVeraO/chatisimo/internal/platform/llm"
	"github.com/HectorVeraO/chatisimo/internal/uccalendar"
)

const (
	settingDiscordToken        = "discord.token"
	settingDiscordAPIBaseURL   = "discord.api_base_url"
	settingLLMKey              = "llm.api_key"
	settingLLMModel            = "llm.model"
	settingLLMBaseURL          = "llm.base_url"
	settingLLMPrompt           = "llm.prompt"
	settingUCAPIBaseURL        = uccalendar.SettingAPIBaseURL
	settingUCFridayAlertTime   = uccalendar.SettingFridayAlertTime
	settingUCMatchdayAlertTime = uccalendar.SettingMatchdayAlertTime
	settingUCSlangCacheTTL     = uccalendar.SettingSlangCacheTTL

	defaultDiscordAPIBaseURL   = "https://discord.com/api/v10"
	defaultLLMModel            = llm.DefaultModel
	defaultLLMBaseURL          = llm.DefaultBaseURL
	defaultLLMPrompt           = llm.DefaultPrompt
	defaultUCAPIBaseURL        = uccalendar.DefaultAPIBaseURL
	defaultUCFridayAlertTime   = uccalendar.DefaultFridayAlertTime
	defaultUCMatchdayAlertTime = uccalendar.DefaultMatchdayAlertTime
	defaultUCSlangCacheTTL     = "24h"

	maxDiscordTokenBytes = 256
	maxLLMModelBytes     = 128
	maxLLMPromptBytes    = 16 << 10
)

// settingKeys declares every setting the process knows. Owners of a setting read it through
// config.Store; this list is the only place a name is registered.
func settingKeys() []config.Key {
	keys := []config.Key{
		{
			Name:     settingDiscordToken,
			Secret:   true,
			Validate: validateDiscordToken,
			Doc:      "Discord bot token",
		},
		{
			Name:     settingDiscordAPIBaseURL,
			Default:  defaultDiscordAPIBaseURL,
			Validate: validateDiscordAPIBaseURL,
			Doc:      "Discord REST base URL",
		},
		{
			Name:     settingLLMKey,
			Secret:   true,
			Validate: validateLLMKey,
			Doc:      "Vertex AI API key",
		},
		{
			Name:     settingLLMModel,
			Default:  defaultLLMModel,
			Validate: validateLLMModel,
			Doc:      "Vertex AI model",
		},
		{
			Name:     settingLLMBaseURL,
			Default:  defaultLLMBaseURL,
			Validate: validateLLMBaseURL,
			Doc:      "Vertex AI publisher models base URL",
		},
		{
			Name:      settingLLMPrompt,
			Multiline: true,
			Default:   defaultLLMPrompt,
			Validate:  validateLLMPrompt,
			Doc:       "Vertex AI question rephrase system prompt",
		},
		{
			Name:     settingUCAPIBaseURL,
			Default:  defaultUCAPIBaseURL,
			Validate: validateUCAPIBaseURL,
			Doc:      "ESPN soccer API base URL",
		},
		{
			Name:     settingUCFridayAlertTime,
			Default:  defaultUCFridayAlertTime,
			Validate: validateUCAlertClock,
			Doc:      "Friday UC weekend summary time (HH:MM Santiago)",
		},
		{
			Name:     settingUCMatchdayAlertTime,
			Default:  defaultUCMatchdayAlertTime,
			Validate: validateUCAlertClock,
			Doc:      "Matchday UC morning alert time (HH:MM Santiago)",
		},
		{
			Name:     settingUCSlangCacheTTL,
			Default:  defaultUCSlangCacheTTL,
			Validate: validateUCSlangCacheTTL,
			Doc:      "UC Chilean banter cache TTL (0s disables LLM)",
		},
	}

	return keys
}

// findSettingKey returns the registered key called name.
func findSettingKey(name string) (config.Key, bool) {
	for _, key := range settingKeys() {
		if key.Name == name {
			return key, true
		}
	}

	return config.Key{}, false
}

// validateDiscordToken accepts 1-256 printable ASCII bytes. Its errors never carry the value.
func validateDiscordToken(value string, _ config.Environment) error {
	var problems []error

	if len(value) < 1 || len(value) > maxDiscordTokenBytes {
		problems = append(problems, fmt.Errorf("must be 1-%d bytes", maxDiscordTokenBytes))
	}

	for i := range len(value) {
		isPrintable := value[i] >= 0x21 && value[i] <= 0x7E
		if !isPrintable {
			problems = append(problems, errors.New("must be printable ASCII without white space"))

			break
		}
	}

	return errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
}

// validateLLMKey accepts 1-256 printable ASCII bytes, and in production rejects AIza-prefixed
// keys. Its errors never carry the value.
func validateLLMKey(value string, env config.Environment) error {
	var problems []error

	if err := validateDiscordToken(value, env); err != nil {
		problems = append(problems, err)
	}

	isProduction := env == config.EnvProduction
	if isProduction && strings.HasPrefix(value, "AIza") {
		problems = append(problems, errors.New("must be a Vertex AI key in production, not an AIza key"))
	}

	return errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
}

// validateDiscordAPIBaseURL accepts an absolute http or https URL with a host, no user info,
// query, fragment or trailing slash. Production accepts only the default. Its errors never carry
// the value.
func validateDiscordAPIBaseURL(value string, env config.Environment) error {
	return validateAPIBaseURL(value, env, defaultDiscordAPIBaseURL)
}

// validateLLMBaseURL accepts an absolute http or https URL with a host, no user info, query,
// fragment or trailing slash. Production accepts only defaultLLMBaseURL. Its errors never carry
// the value.
func validateLLMBaseURL(value string, env config.Environment) error {
	return validateAPIBaseURL(value, env, defaultLLMBaseURL)
}

// validateUCAPIBaseURL accepts an absolute http or https URL with a host, no user info, query,
// fragment or trailing slash. Production accepts only defaultUCAPIBaseURL. Its errors never carry
// the value.
func validateUCAPIBaseURL(value string, env config.Environment) error {
	return validateAPIBaseURL(value, env, defaultUCAPIBaseURL)
}

// validateUCAlertClock accepts a strict 5-byte 24-hour clock value HH:MM in 00:00..23:59. Its
// errors never carry the value.
func validateUCAlertClock(value string, _ config.Environment) error {
	_, _, err := uccalendar.ParseClockHHMM(value)
	if err != nil {
		return errors.New("must be a 24-hour clock time in HH:MM format (00:00 to 23:59)")
	}

	return nil
}

// validateUCSlangCacheTTL accepts 0s (disabling LLM banter) or a whole-second duration between
// 1m and 168h. Its errors never carry the value.
func validateUCSlangCacheTTL(value string, _ config.Environment) error {
	_, err := uccalendar.ParseSlangCacheTTL(value)
	if err != nil {
		return errors.New("must be 0s or a whole-second duration from 1m to 168h")
	}

	return nil
}

// validateLLMModel accepts a 1-128 byte printable ASCII model identifier without URL path or
// query characters. Its errors never carry the value.
func validateLLMModel(value string, _ config.Environment) error {
	var problems []error

	if len(value) < 1 || len(value) > maxLLMModelBytes {
		problems = append(problems, fmt.Errorf("must be 1-%d bytes", maxLLMModelBytes))
	}

	for i := range len(value) {
		isPrintable := value[i] >= 0x21 && value[i] <= 0x7E
		if !isPrintable {
			problems = append(problems, errors.New("must be printable ASCII without white space"))

			break
		}
	}

	if strings.ContainsAny(value, "/\\?#:") {
		problems = append(problems, errors.New("must not contain URL path or query characters"))
	}

	return errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
}

// validateLLMPrompt accepts 1-16384 bytes of valid UTF-8 text with non-empty trimmed content and
// no control characters other than '\n'. Its errors never carry the value.
func validateLLMPrompt(value string, _ config.Environment) error {
	var problems []error

	if len(value) < 1 || len(value) > maxLLMPromptBytes {
		problems = append(problems, fmt.Errorf("must be 1-%d bytes", maxLLMPromptBytes))
	}

	if strings.TrimSpace(value) == "" {
		problems = append(problems, errors.New("must not be blank"))
	}

	if !utf8.ValidString(value) {
		problems = append(problems, errors.New("must be valid UTF-8"))
	}

	for _, r := range value {
		if r != '\n' && unicode.IsControl(r) {
			problems = append(problems, errors.New("must not contain control characters other than newline"))

			break
		}
	}

	return errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
}

func validateAPIBaseURL(value string, env config.Environment, prodDefault string) error {
	var problems []error

	isProduction := env == config.EnvProduction
	if isProduction && value != prodDefault {
		problems = append(problems, fmt.Errorf("must be %s in production", prodDefault))
	}

	hasSpace := strings.ContainsFunc(value, unicode.IsSpace)
	if hasSpace {
		problems = append(problems, errors.New("must not contain white space"))
	}

	parsed, err := url.Parse(value)
	isParsed := err == nil
	isHTTP := isParsed && (parsed.Scheme == "http" || parsed.Scheme == "https")
	if !isHTTP || parsed.Host == "" {
		problems = append(problems, errors.New("must be an absolute http or https URL"))
	}

	if isParsed && parsed.User != nil {
		problems = append(problems, errors.New("must not contain user info"))
	}

	if strings.ContainsAny(value, "?#") {
		problems = append(problems, errors.New("must not contain a query or fragment"))
	}

	if strings.HasSuffix(value, "/") {
		problems = append(problems, errors.New("must not end with a slash"))
	}

	return errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
}
