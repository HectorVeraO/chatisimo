package main

import (
	"net/url"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

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

func TestValidateLLMKey(t *testing.T) {
	const (
		aizaKey   = "AIza-synthetic-test-key"
		vertexKey = "vertex-synthetic-test-key"
	)

	tests := []struct {
		name       string
		value      string
		env        config.Environment
		wantErr    bool
		wantSubstr []string
	}{
		{
			name:    "accepts non-AIza key in production",
			value:   vertexKey,
			env:     config.EnvProduction,
			wantErr: false,
		},
		{
			name:    "accepts non-AIza key in local",
			value:   vertexKey,
			env:     config.EnvLocal,
			wantErr: false,
		},
		{
			name:    "accepts AIza key in local",
			value:   aizaKey,
			env:     config.EnvLocal,
			wantErr: false,
		},
		{
			name:       "rejects AIza key in production",
			value:      aizaKey,
			env:        config.EnvProduction,
			wantErr:    true,
			wantSubstr: []string{"AIza"},
		},
		{
			name:       "rejects empty key in local",
			value:      "",
			env:        config.EnvLocal,
			wantErr:    true,
			wantSubstr: []string{"1-256 bytes"},
		},
		{
			name:       "rejects overlong key in local",
			value:      strings.Repeat("k", maxDiscordTokenBytes+1),
			env:        config.EnvLocal,
			wantErr:    true,
			wantSubstr: []string{"1-256 bytes"},
		},
		{
			name:       "rejects whitespace in key in local",
			value:      "vertex key",
			env:        config.EnvLocal,
			wantErr:    true,
			wantSubstr: []string{"printable ASCII without white space"},
		},
		{
			name:       "aggregates length, printable ASCII and AIza prefix errors in production",
			value:      "AIza " + strings.Repeat("k", maxDiscordTokenBytes),
			env:        config.EnvProduction,
			wantErr:    true,
			wantSubstr: []string{"1-256 bytes", "printable ASCII without white space", "AIza"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateLLMKey(tc.value, tc.env)
			if tc.wantErr && err == nil {
				t.Fatalf("validateLLMKey(%q, %q) = nil, want error", tc.value, tc.env)
			}

			if !tc.wantErr && err != nil {
				t.Fatalf("validateLLMKey(%q, %q) = %v, want nil", tc.value, tc.env, err)
			}

			if err != nil {
				msg := err.Error()
				for _, want := range tc.wantSubstr {
					if !strings.Contains(msg, want) {
						t.Errorf("error %q does not contain %q", msg, want)
					}
				}

				for _, secret := range []string{aizaKey, vertexKey, "synthetic-test-key"} {
					if strings.Contains(msg, secret) {
						t.Errorf("error %q leaked secret fragment %q", msg, secret)
					}
				}
			}
		})
	}
}

func FuzzValidateLLMKey(f *testing.F) {
	seeds := []string{
		"",
		"a",
		"AIza-synthetic-test-key",
		"vertex-synthetic-test-key",
		strings.Repeat("a", maxDiscordTokenBytes),
		strings.Repeat("a", maxDiscordTokenBytes+1),
		"AIza " + strings.Repeat("a", maxDiscordTokenBytes),
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
		f.Add(seed, true)
		f.Add(seed, false)
	}

	f.Fuzz(func(t *testing.T, value string, isProduction bool) {
		env := config.EnvLocal
		if isProduction {
			env = config.EnvProduction
		}

		err := validateLLMKey(value, env)
		if err != nil {
			msg := err.Error()
			if len(value) >= 4 && !strings.Contains(msg, value) {
				return
			}

			// Allow only substrings that naturally match fixed words in the error text.
			fixedMsg := validateLLMKey("AIza "+strings.Repeat("a", maxDiscordTokenBytes), config.EnvProduction).Error()
			if len(value) >= 4 && !strings.Contains(fixedMsg, value) && strings.Contains(msg, value) {
				t.Fatalf("error %q echoed input value %q", msg, value)
			}

			return
		}

		if len(value) < 1 || len(value) > maxDiscordTokenBytes {
			t.Errorf("accepted a key of %d bytes", len(value))
		}

		for i := range len(value) {
			if value[i] < 0x21 || value[i] > 0x7E {
				t.Fatalf("accepted byte 0x%02x at offset %d", value[i], i)
			}
		}

		if isProduction && strings.HasPrefix(value, "AIza") {
			t.Errorf("accepted AIza-prefixed key in production")
		}
	})
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

func FuzzValidateLLMModel(f *testing.F) {
	seeds := []string{
		"",
		defaultLLMModel,
		"gemini-3.5-flash",
		strings.Repeat("a", maxLLMModelBytes),
		strings.Repeat("a", maxLLMModelBytes+1),
		"bad/model",
		"bad\\model",
		"bad?model",
		"bad#model",
		"bad:model",
		"bad model",
		"model\n",
		"m\u00e9del",
		"model\x00",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, value string) {
		err := validateLLMModel(value, config.EnvLocal)
		if err != nil {
			return
		}

		if len(value) < 1 || len(value) > maxLLMModelBytes {
			t.Errorf("accepted a model of %d bytes", len(value))
		}

		if strings.ContainsAny(value, "/\\?#: \t\r\n") {
			t.Errorf("accepted model %q with forbidden delimiter", value)
		}
	})
}

func TestValidateLLMPrompt(t *testing.T) {
	const secretFragment = "synthetic-prompt-marker"

	tests := []struct {
		name       string
		value      string
		wantErr    bool
		wantSubstr []string
	}{
		{
			name:    "accepts default prompt",
			value:   defaultLLMPrompt,
			wantErr: false,
		},
		{
			name:    "accepts multiline prompt with LF",
			value:   "Rule 1: Keep 2nd person singular.\nRule 2: Use neutral tuteo.",
			wantErr: false,
		},
		{
			name:       "rejects empty prompt",
			value:      "",
			wantErr:    true,
			wantSubstr: []string{"1-16384 bytes", "must not be blank"},
		},
		{
			name:       "rejects whitespace and newlines only",
			value:      "   \n\n  ",
			wantErr:    true,
			wantSubstr: []string{"must not be blank"},
		},
		{
			name:       "rejects overlong prompt",
			value:      strings.Repeat("p", maxLLMPromptBytes+1),
			wantErr:    true,
			wantSubstr: []string{"1-16384 bytes"},
		},
		{
			name:       "rejects control characters other than newline without echoing value",
			value:      secretFragment + "\x00\r\t",
			wantErr:    true,
			wantSubstr: []string{"control characters other than newline"},
		},
		{
			name:       "rejects invalid UTF-8 without echoing value",
			value:      secretFragment + "\xff\xfe",
			wantErr:    true,
			wantSubstr: []string{"valid UTF-8"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateLLMPrompt(tc.value, config.EnvLocal)
			if tc.wantErr && err == nil {
				t.Fatalf("validateLLMPrompt(%q) = nil, want error", tc.value)
			}

			if !tc.wantErr && err != nil {
				t.Fatalf("validateLLMPrompt(%q) = %v, want nil", tc.value, err)
			}

			if err != nil {
				msg := err.Error()
				for _, want := range tc.wantSubstr {
					if !strings.Contains(msg, want) {
						t.Errorf("error %q does not contain %q", msg, want)
					}
				}

				if strings.Contains(msg, secretFragment) {
					t.Errorf("error %q leaked value fragment %q", msg, secretFragment)
				}
			}
		})
	}
}

func FuzzValidateLLMPrompt(f *testing.F) {
	seeds := []string{
		"",
		"   \n  ",
		defaultLLMPrompt,
		"Line 1\nLine 2\nLine 3",
		strings.Repeat("a", maxLLMPromptBytes),
		strings.Repeat("a", maxLLMPromptBytes+1),
		"prompt\rwith cr",
		"prompt\twith tab",
		"prompt\x00with nul",
		"prompt\xff\xfebad utf8",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, value string) {
		err := validateLLMPrompt(value, config.EnvLocal)
		if err != nil {
			return
		}

		if len(value) < 1 || len(value) > maxLLMPromptBytes {
			t.Errorf("accepted prompt of %d bytes", len(value))
		}

		if strings.TrimSpace(value) == "" {
			t.Error("accepted blank prompt")
		}

		if !utf8.ValidString(value) {
			t.Error("accepted invalid UTF-8 prompt")
		}

		for _, r := range value {
			if r != '\n' && unicode.IsControl(r) {
				t.Fatalf("accepted control rune %U", r)
			}
		}
	})
}

func settingAbuseSeeds() []string {
	return []string{
		strings.Repeat("9", 4096),
		"a\u0301\u0308123",
		"\u202e17:00",
		"09:\u200b00",
		"\U0001F60024h",
		"<@100000000000000001>",
		"@everyone",
	}
}

func FuzzValidateUCAPIBaseURL(f *testing.F) {
	seeds := append([]string{
		"",
		defaultUCAPIBaseURL,
		"http://127.0.0.1:8080",
		"https://example.com/",
		"https://user:pass@example.com",
		"https://example.com?a=b#c",
		"ftp://example.com",
		"https://exa mple.com",
		"https://e\u0301xample.com\u200b",
		"https://example.com/\U0001F600",
		"%zz",
		"http://[::1",
	}, settingAbuseSeeds()...)
	for _, seed := range seeds {
		f.Add(seed, true)
		f.Add(seed, false)
	}

	fixedErr := validateUCAPIBaseURL("ftp://user:pass@exa mple.com?a=b/", config.EnvProduction).Error()

	f.Fuzz(func(t *testing.T, value string, isProduction bool) {
		env := config.EnvLocal
		if isProduction {
			env = config.EnvProduction
		}

		err := validateUCAPIBaseURL(value, env)
		if err != nil {
			msg := err.Error()
			if len(value) >= 4 && !strings.Contains(fixedErr, value) && strings.Contains(msg, value) {
				t.Fatalf("error %q echoed input %q", msg, value)
			}

			return
		}

		parsed, parseErr := url.Parse(value)
		if parseErr != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			t.Errorf("accepted %q, which is not an absolute http or https URL", value)
		}

		if isProduction && value != defaultUCAPIBaseURL {
			t.Errorf("accepted non-default URL %q in production", value)
		}
	})
}

func FuzzValidateUCAlertClock(f *testing.F) {
	seeds := append([]string{
		"",
		"00:00",
		"09:00",
		"17:00",
		"23:59",
		"24:00",
		"12:60",
		"9:00",
		"17-00",
		"bad-clock-marker",
	}, settingAbuseSeeds()...)
	for _, seed := range seeds {
		f.Add(seed)
	}

	fixedErr := validateUCAlertClock("bad-clock-marker", config.EnvLocal).Error()

	f.Fuzz(func(t *testing.T, value string) {
		err := validateUCAlertClock(value, config.EnvLocal)
		if err != nil {
			msg := err.Error()
			if len(value) >= 4 && !strings.Contains(fixedErr, value) && strings.Contains(msg, value) {
				t.Fatalf("error %q echoed input %q", msg, value)
			}

			return
		}

		if len(value) != 5 || value[2] != ':' {
			t.Fatalf("accepted malformed clock %q", value)
		}
	})
}

func FuzzValidateUCSlangCacheTTL(f *testing.F) {
	seeds := append([]string{
		"",
		"0s",
		"1m",
		"24h",
		"168h",
		"30s",
		"169h",
		"1500ms",
		"-5m",
		"+1h",
		"bad-ttl-marker",
	}, settingAbuseSeeds()...)
	for _, seed := range seeds {
		f.Add(seed)
	}

	fixedErr := validateUCSlangCacheTTL("bad-ttl-marker", config.EnvLocal).Error()

	f.Fuzz(func(t *testing.T, value string) {
		err := validateUCSlangCacheTTL(value, config.EnvLocal)
		if err != nil {
			msg := err.Error()
			if len(value) >= 4 && !strings.Contains(fixedErr, value) && strings.Contains(msg, value) {
				t.Fatalf("error %q echoed input %q", msg, value)
			}

			return
		}

		if value == "" || value[0] < '0' || value[0] > '9' {
			t.Fatalf("accepted invalid duration %q", value)
		}
	})
}
