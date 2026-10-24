package main

import (
	"net/url"
	"strings"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/platform/config"
)

func TestValidateDiscordAPIBaseURLReportsEveryProblem(t *testing.T) {
	err := validateDiscordAPIBaseURL("ftp://example.com?a=b/", config.EnvLocal)
	if err == nil {
		t.Fatal("accepted a bad scheme with a query and a trailing slash")
	}

	for _, want := range []string{"absolute http or https URL", "query or fragment", "end with a slash"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func FuzzValidateDiscordAPIBaseURL(f *testing.F) {
	seeds := []string{
		"",
		defaultDiscordAPIBaseURL,
		"http://127.0.0.1:8080",
		"https://example.com/",
		"https://user:pass@example.com",
		"https://example.com?a=b#c",
		"ftp://example.com",
		"https://exa mple.com",
		"https://مثال.example.com",
		"https://e\u0301xample.com\u200b",
		"https://example.com/\U0001F600",
		"%zz",
		"http://[::1",
	}
	for _, seed := range seeds {
		f.Add(seed, true)
		f.Add(seed, false)
	}

	f.Fuzz(func(t *testing.T, value string, isProduction bool) {
		env := config.EnvLocal
		if isProduction {
			env = config.EnvProduction
		}

		err := validateDiscordAPIBaseURL(value, env)
		if err != nil {
			return
		}

		parsed, parseErr := url.Parse(value)
		if parseErr != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			t.Errorf("accepted %q, which is not an absolute http or https URL", value)
		}
	})
}
