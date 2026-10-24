package main

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"

	"github.com/HectorVeraO/chatisimo/internal/platform/config"
)

const (
	settingDiscordToken      = "discord.token"
	settingDiscordAPIBaseURL = "discord.api_base_url"

	defaultDiscordAPIBaseURL = "https://discord.com/api/v10"

	maxDiscordTokenBytes = 256
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

// validateDiscordAPIBaseURL accepts an absolute http or https URL with a host, no user info,
// query, fragment or trailing slash. Production accepts only the default. Its errors never carry
// the value.
func validateDiscordAPIBaseURL(value string, env config.Environment) error {
	var problems []error

	isProduction := env == config.EnvProduction
	if isProduction && value != defaultDiscordAPIBaseURL {
		problems = append(problems, fmt.Errorf("must be %s in production", defaultDiscordAPIBaseURL))
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
