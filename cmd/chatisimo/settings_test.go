package main

import (
	"net/url"
	"strings"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/platform/config"
)

func TestConfigSetReportsEveryBaseURLProblem(t *testing.T) {
	env := envFor(shared, "local")

	got := invoke(t, env, strings.NewReader("ftp://example.com?a=b/"), "config", "set", "discord.api_base_url")

	if got.code != 1 || got.stdout != "" {
		t.Fatalf("got %+v, want exit 1 and no stdout", got)
	}

	for _, want := range []string{"absolute http or https URL", "query or fragment", "end with a slash"} {
		if !strings.Contains(got.stderr, want) {
			t.Errorf("stderr %q does not mention %q", got.stderr, want)
		}
	}
}

func FuzzValidateDiscordToken(f *testing.F) {
	seeds := []string{
		"",
		"a",
		strings.Repeat("a", maxDiscordTokenBytes),
		strings.Repeat("a", maxDiscordTokenBytes+1),
		"tok en",
		"tok\ten",
		"token\n",
		"t\u00e9ken",
		"token\u200b",
		"token\u202e",
		"token\U0001F600",
		"token\xff\xfe",
		"token\x00",
		"token\x7f",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, value string) {
		err := validateDiscordToken(value, config.EnvLocal)
		if err != nil {
			return
		}

		if len(value) < 1 || len(value) > maxDiscordTokenBytes {
			t.Errorf("accepted a token of %d bytes", len(value))
		}

		for i := range len(value) {
			if value[i] < 0x21 || value[i] > 0x7E {
				t.Fatalf("accepted byte 0x%02x at offset %d", value[i], i)
			}
		}
	})
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
