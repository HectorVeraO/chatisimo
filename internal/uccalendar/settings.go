package uccalendar

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/HectorVeraO/chatisimo/internal/platform/config"
)

type resolvedSettings struct {
	fridayHour     int
	fridayMinute   int
	matchdayHour   int
	matchdayMinute int
	slangCacheTTL  time.Duration
	apiBaseURL     string
	banterPrompt   string
	loadedAt       time.Time
}

func (s *Service) getSettings(ctx context.Context) (resolvedSettings, error) {
	now := s.clock.Now()

	s.mu.RLock()
	cached := s.cachedSettings
	hasCached := s.hasSettings
	s.mu.RUnlock()

	if hasCached {
		age := now.Sub(cached.loadedAt)
		isFresh := age >= 0 && age < s.settingsTTL
		if isFresh {
			return cached, nil
		}
	}

	loaded, err := s.loadSettingsFromReader(ctx, now)
	if err != nil {
		return resolvedSettings{}, err
	}

	s.mu.Lock()
	s.cachedSettings = loaded
	s.hasSettings = true
	s.mu.Unlock()

	return loaded, nil
}

func (s *Service) loadSettingsFromReader(ctx context.Context, loadedAt time.Time) (resolvedSettings, error) {
	var problems []error

	fridayRaw, err := readOptionalSetting(ctx, s.settings, SettingFridayAlertTime, DefaultFridayAlertTime)
	fridayHour := 0
	fridayMinute := 0
	if err != nil {
		problems = append(problems, err)
	} else {
		var clockErr error
		fridayHour, fridayMinute, clockErr = ParseClockHHMM(fridayRaw)
		if clockErr != nil {
			problems = append(problems, fmt.Errorf("setting %s: %w", SettingFridayAlertTime, clockErr))
		}
	}

	matchdayRaw, err := readOptionalSetting(ctx, s.settings, SettingMatchdayAlertTime, DefaultMatchdayAlertTime)
	matchdayHour := 0
	matchdayMinute := 0
	if err != nil {
		problems = append(problems, err)
	} else {
		var clockErr error
		matchdayHour, matchdayMinute, clockErr = ParseClockHHMM(matchdayRaw)
		if clockErr != nil {
			problems = append(problems, fmt.Errorf("setting %s: %w", SettingMatchdayAlertTime, clockErr))
		}
	}

	ttlRaw, err := readOptionalSetting(ctx, s.settings, SettingSlangCacheTTL, "24h")
	var slangTTL time.Duration
	if err != nil {
		problems = append(problems, err)
	} else {
		var ttlErr error
		slangTTL, ttlErr = ParseSlangCacheTTL(ttlRaw)
		if ttlErr != nil {
			problems = append(problems, fmt.Errorf("setting %s: %w", SettingSlangCacheTTL, ttlErr))
		}
	}

	apiBaseRaw, err := readOptionalSetting(ctx, s.settings, SettingAPIBaseURL, DefaultAPIBaseURL)
	apiBaseURL := ""
	if err != nil {
		problems = append(problems, err)
	} else {
		trimmedURL := strings.TrimRight(strings.TrimSpace(apiBaseRaw), "/")
		if urlErr := validateBaseURL(trimmedURL); urlErr != nil {
			problems = append(problems, fmt.Errorf("setting %s: %w", SettingAPIBaseURL, urlErr))
		} else {
			apiBaseURL = trimmedURL
		}
	}

	promptRaw, err := readOptionalSetting(ctx, s.settings, SettingBanterPrompt, DefaultBanterPrompt)
	banterPrompt := ""
	if err != nil {
		problems = append(problems, err)
	} else {
		var promptErr error
		banterPrompt, promptErr = ValidateBanterPrompt(promptRaw)
		if promptErr != nil {
			problems = append(problems, fmt.Errorf("setting %s: %w", SettingBanterPrompt, promptErr))
		}
	}

	if len(problems) > 0 {
		return resolvedSettings{}, errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
	}

	resolved := resolvedSettings{
		fridayHour:     fridayHour,
		fridayMinute:   fridayMinute,
		matchdayHour:   matchdayHour,
		matchdayMinute: matchdayMinute,
		slangCacheTTL:  slangTTL,
		apiBaseURL:     apiBaseURL,
		banterPrompt:   banterPrompt,
		loadedAt:       loadedAt,
	}

	return resolved, nil
}

func readOptionalSetting(ctx context.Context, reader SettingReader, name, fallback string) (string, error) {
	val, err := reader.Get(ctx, name)
	if err != nil {
		if errors.Is(err, config.ErrNotSet) {
			return fallback, nil
		}

		return "", fmt.Errorf("read setting %s: %w", name, err)
	}

	return val, nil
}

func validateBaseURL(raw string) error {
	if raw == "" {
		return &InputError{Field: "api_base_url", Code: InputCodeEmpty}
	}

	var problems []error

	hasSpace := strings.ContainsFunc(raw, unicode.IsSpace)
	if hasSpace {
		problems = append(problems, &InputError{Field: "api_base_url", Code: InputCodeInvalidURL})
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		problems = append(problems, &InputError{Field: "api_base_url", Code: InputCodeInvalidURL})
		return errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
	}

	isHTTP := parsed.Scheme == "http" || parsed.Scheme == "https"
	if !isHTTP || parsed.Host == "" {
		problems = append(problems, &InputError{Field: "api_base_url", Code: InputCodeInvalidURL})
	}

	hasDisallowedParts := parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != ""
	if hasDisallowedParts {
		problems = append(problems, &InputError{Field: "api_base_url", Code: InputCodeInvalidURL})
	}

	return errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
}
