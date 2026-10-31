package main

import (
	"strings"
	"testing"
)

func TestConfigSetStoresASecretWithoutEchoingIt(t *testing.T) {
	database := newClone(t)
	env := envFor(database, "local")
	token := syntheticToken(t, 64)

	set := invoke(t, env, strings.NewReader(token+"\n"), "config", "set", "discord.token")
	get := invoke(t, env, strings.NewReader(""), "config", "get", "discord.token")
	list := invoke(t, env, strings.NewReader(""), "config", "list")

	if set.code != 0 || set.stdout != "discord.token set\n" {
		t.Errorf("set = %+v, want exit 0 and %q", set, "discord.token set\n")
	}

	if get.code != 0 || get.stdout != "[redacted]\n" {
		t.Errorf("get = %+v, want [redacted]", get)
	}

	wantLine := "discord.token\tsecret\t[redacted]\tDiscord bot token"
	if !strings.Contains(list.stdout, wantLine+"\n") {
		t.Errorf("list stdout = %q, want line %q", list.stdout, wantLine)
	}

	for name, got := range map[string]outcome{"set": set, "get": get, "list": list} {
		for _, secret := range []string{token, masterKey, database.URL().Reveal()} {
			if strings.Contains(got.all(), secret) {
				t.Errorf("%s output contains a secret", name)
			}
		}
	}
}

func TestConfigSetStripsOneTrailingNewline(t *testing.T) {
	env := envFor(newClone(t), "local")

	for _, ending := range []string{"", "\n", "\r\n"} {
		got := invoke(t, env,
			strings.NewReader("http://127.0.0.1:10010/api/v10"+ending), "config", "set", "discord.api_base_url")

		if got.code != 0 {
			t.Errorf("ending %q: got %+v, want exit 0", ending, got)
		}
	}

	get := invoke(t, env, strings.NewReader(""), "config", "get", "discord.api_base_url")
	if get.stdout != "http://127.0.0.1:10010/api/v10\n" {
		t.Errorf("get stdout = %q, want the stored URL without a line ending", get.stdout)
	}
}

func TestConfigSetRejectsBadInput(t *testing.T) {
	env := envFor(shared, "local")
	token := syntheticToken(t, 32)

	tests := []struct {
		name  string
		key   string
		stdin string
	}{
		{name: "empty", key: "discord.token", stdin: ""},
		{name: "only a newline", key: "discord.token", stdin: "\n"},
		{name: "control character", key: "discord.token", stdin: token + "\x07"},
		{name: "white space in a secret", key: "discord.token", stdin: token + " " + token},
		{name: "invalid UTF-8", key: "discord.token", stdin: token + "\xff\xfe"},
		{name: "non-ASCII in a token", key: "discord.token", stdin: token + "é"},
		{name: "right-to-left override", key: "discord.token", stdin: token + string(rune(0x202E))},
		{name: "zero-width space", key: "discord.token", stdin: token + string(rune(0x200B))},
		{name: "emoji", key: "discord.token", stdin: token + "\U0001F600"},
		{name: "257 bytes", key: "discord.token", stdin: strings.Repeat("a", 257)},
		{name: "tab in a plain value", key: "discord.api_base_url", stdin: "http://127.0.0.1\t:1"},
		{name: "two lines", key: "discord.api_base_url", stdin: "http://127.0.0.1\nhttp://127.0.0.2"},
		{name: "two trailing newlines", key: "discord.api_base_url", stdin: "http://127.0.0.1:10010/api/v10\n\n"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := invoke(t, env, strings.NewReader(tc.stdin), "config", "set", tc.key)

			if got.code != 1 || got.stdout != "" {
				t.Errorf("got %+v, want exit 1 and no stdout", got)
			}

			if !strings.HasPrefix(got.stderr, "chatisimo: ") {
				t.Errorf("stderr = %q, want a chatisimo: line", got.stderr)
			}

			hasInput := strings.TrimSpace(tc.stdin) != ""
			if strings.Contains(got.all(), token) || (hasInput && strings.Contains(got.all(), tc.stdin)) {
				t.Errorf("output echoes the input: %q", got.all())
			}
		})
	}
}

func TestConfigSetSizeLimit(t *testing.T) {
	env := envFor(shared, "local")

	over := invoke(t, env, strings.NewReader(strings.Repeat("a", 64<<10+1)), "config", "set", "discord.token")
	exact := invoke(t, env, strings.NewReader(strings.Repeat("a", 64<<10)), "config", "set", "discord.token")

	if over.code != 1 || exact.code != 1 {
		t.Fatalf("over = %+v, exact = %+v, want both exit 1", over, exact)
	}

	if !strings.Contains(over.stderr, "longer than") {
		t.Errorf("over stderr = %q, want the size error", over.stderr)
	}

	if strings.Contains(exact.stderr, "longer than") {
		t.Errorf("exact stderr = %q, want the validator's error, not the size error", exact.stderr)
	}

	if over.stderr == exact.stderr {
		t.Error("the size error and the validator error are the same")
	}
}

func TestConfigSetTokenLength(t *testing.T) {
	env := envFor(shared, "local")

	accepted := invoke(t, env, strings.NewReader(syntheticToken(t, 256)), "config", "set", "discord.token")
	one := invoke(t, env, strings.NewReader("a"), "config", "set", "discord.token")

	if accepted.code != 0 || one.code != 0 {
		t.Errorf("256 bytes = %+v, 1 byte = %+v, want both exit 0", accepted, one)
	}
}

func TestConfigSetBaseURLValidation(t *testing.T) {
	const localURL = "http://127.0.0.1:10010/api/v10"

	tests := []struct {
		name       string
		value      string
		appEnv     string
		wantAccept bool
	}{
		{name: "default in local", value: defaultDiscordAPIBaseURL, appEnv: "local", wantAccept: true},
		{name: "default in production", value: defaultDiscordAPIBaseURL, appEnv: "production", wantAccept: true},
		{name: "local URL in local", value: localURL, appEnv: "local", wantAccept: true},
		{name: "https URL in local", value: "https://example.com/api/v10", appEnv: "local", wantAccept: true},
		{name: "local URL in production", value: localURL, appEnv: "production"},
		{name: "query", value: "http://127.0.0.1/api?x=1", appEnv: "local"},
		{name: "empty query marker", value: "http://127.0.0.1/api?", appEnv: "local"},
		{name: "fragment", value: "http://127.0.0.1/api#frag", appEnv: "local"},
		{name: "user info", value: "http://user@127.0.0.1/api", appEnv: "local"},
		{name: "trailing slash", value: "http://127.0.0.1/api/", appEnv: "local"},
		{name: "ftp scheme", value: "ftp://127.0.0.1/api", appEnv: "local"},
		{name: "relative URL", value: "/api/v10", appEnv: "local"},
		{name: "no host", value: "http:///api", appEnv: "local"},
		{name: "space in path", value: "http://127.0.0.1/a b", appEnv: "local"},
		{name: "unparsable", value: "http://[::1", appEnv: "local"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := envFor(shared, tc.appEnv)

			got := invoke(t, env, strings.NewReader(tc.value), "config", "set", "discord.api_base_url")

			if tc.wantAccept && got.code != 0 {
				t.Errorf("got %+v, want accepted", got)
			}

			if tc.wantAccept && got.code == 0 {
				stored := invoke(t, env, strings.NewReader(""), "config", "get", "discord.api_base_url")

				want := tc.value + "\n"

				if stored.code != 0 || stored.stdout != want {
					t.Errorf("stored = %+v, want %q", stored, want)
				}
			}

			if !tc.wantAccept && (got.code != 1 || got.stdout != "") {
				t.Errorf("got %+v, want rejected with exit 1", got)
			}

			if !tc.wantAccept && strings.Contains(got.all(), tc.value) {
				t.Errorf("output echoes the value: %q", got.all())
			}
		})
	}
}

func TestConfigSetUnknownKeyReadsNoStdin(t *testing.T) {
	env := envFor(shared, "local")

	got := invoke(t, env, forbiddenReader{t: t}, "config", "set", "discord.nope")

	if got.code != 2 || got.stdout != "" {
		t.Errorf("got %+v, want exit 2", got)
	}
}

func TestConfigGetUnsetKeys(t *testing.T) {
	env := envFor(newClone(t), "local")

	plain := invoke(t, env, strings.NewReader(""), "config", "get", "discord.api_base_url")
	secret := invoke(t, env, strings.NewReader(""), "config", "get", "discord.token")

	if plain.code != 0 || plain.stdout != defaultDiscordAPIBaseURL+"\n" {
		t.Errorf("plain = %+v, want the default", plain)
	}

	if secret.code != 1 || secret.stdout != "" || !strings.Contains(secret.stderr, "discord.token is not set") {
		t.Errorf("secret = %+v, want exit 1 and a not-set line", secret)
	}
}

func TestConfigListFormat(t *testing.T) {
	env := envFor(newClone(t), "local")

	before := invoke(t, env, strings.NewReader(""), "config", "list")

	ucTail := "uc.api_base_url\tplain\t" + defaultUCAPIBaseURL + " (default)\tESPN soccer API base URL\n" +
		"uc.channel_id\tplain\t(unset)\tDiscord channel snowflake for UC alerts\n" +
		"uc.friday_alert_time\tplain\t" + defaultUCFridayAlertTime + " (default)\tFriday UC weekend summary time (HH:MM Santiago)\n" +
		"uc.friend_user_id\tplain\t(unset)\tDiscord user snowflake of the UC fan friend\n" +
		"uc.matchday_alert_time\tplain\t" + defaultUCMatchdayAlertTime + " (default)\tMatchday UC morning alert time (HH:MM Santiago)\n" +
		"uc.slang_cache_ttl\tplain\t" + defaultUCSlangCacheTTL + " (default)\tUC Chilean banter cache TTL (0s disables LLM)\n"

	wantBefore := "discord.api_base_url\tplain\t" + defaultDiscordAPIBaseURL + " (default)\tDiscord REST base URL\n" +
		"discord.token\tsecret\t(unset)\tDiscord bot token\n" +
		"llm.api_key\tsecret\t(unset)\tVertex AI API key\n" +
		"llm.base_url\tplain\t" + defaultLLMBaseURL + " (default)\tVertex AI publisher models base URL\n" +
		"llm.model\tplain\t" + defaultLLMModel + " (default)\tVertex AI model\n" +
		"llm.prompt\tplain\t" + defaultLLMPrompt + " (default)\tVertex AI question rephrase system prompt\n" +
		ucTail
	if before.code != 0 || before.stdout != wantBefore {
		t.Errorf("before = %+v, want stdout %q", before, wantBefore)
	}

	set := invoke(t, env, strings.NewReader("http://127.0.0.1:10010/api/v10\n"), "config", "set", "discord.api_base_url")
	if set.code != 0 {
		t.Fatalf("set = %+v", set)
	}

	after := invoke(t, env, strings.NewReader(""), "config", "list")

	wantAfter := "discord.api_base_url\tplain\thttp://127.0.0.1:10010/api/v10\tDiscord REST base URL\n" +
		"discord.token\tsecret\t(unset)\tDiscord bot token\n" +
		"llm.api_key\tsecret\t(unset)\tVertex AI API key\n" +
		"llm.base_url\tplain\t" + defaultLLMBaseURL + " (default)\tVertex AI publisher models base URL\n" +
		"llm.model\tplain\t" + defaultLLMModel + " (default)\tVertex AI model\n" +
		"llm.prompt\tplain\t" + defaultLLMPrompt + " (default)\tVertex AI question rephrase system prompt\n" +
		ucTail
	if after.stdout != wantAfter {
		t.Errorf("after stdout = %q, want %q", after.stdout, wantAfter)
	}
}

func TestConfigUCSettings(t *testing.T) {
	database := newClone(t)
	local := envFor(database, "local")
	prod := envFor(database, "production")

	for _, unsetKey := range []string{"uc.friend_user_id", "uc.channel_id"} {
		unsetGet := invoke(t, local, strings.NewReader(""), "config", "get", unsetKey)
		if unsetGet.code != 1 || unsetGet.stdout != "" {
			t.Errorf("unset get %s = %+v, want exit 1", unsetKey, unsetGet)
		}
	}

	validPairs := []struct {
		key   string
		value string
	}{
		{key: "uc.friend_user_id", value: "100000000000000042"},
		{key: "uc.channel_id", value: "200000000000000099"},
		{key: "uc.friday_alert_time", value: "18:30"},
		{key: "uc.matchday_alert_time", value: "08:45"},
		{key: "uc.slang_cache_ttl", value: "0s"},
		{key: "uc.slang_cache_ttl", value: "6h"},
	}
	for _, pair := range validPairs {
		setRes := invoke(t, local, strings.NewReader(pair.value+"\n"), "config", "set", pair.key)
		getRes := invoke(t, local, strings.NewReader(""), "config", "get", pair.key)
		if setRes.code != 0 || getRes.code != 0 || getRes.stdout != pair.value+"\n" {
			t.Errorf("%s set/get = %+v / %+v, want %q", pair.key, setRes, getRes, pair.value)
		}
	}

	invalidPairs := []struct {
		key   string
		value string
	}{
		{key: "uc.friend_user_id", value: "012345"},
		{key: "uc.friend_user_id", value: "not-a-snowflake-marker"},
		{key: "uc.channel_id", value: "0"},
		{key: "uc.friday_alert_time", value: "24:00"},
		{key: "uc.matchday_alert_time", value: "9:00"},
		{key: "uc.slang_cache_ttl", value: "30s"},
		{key: "uc.slang_cache_ttl", value: "200h"},
		{key: "uc.slang_cache_ttl", value: "1500ms"},
	}
	for _, pair := range invalidPairs {
		got := invoke(t, local, strings.NewReader(pair.value+"\n"), "config", "set", pair.key)
		if got.code != 1 || got.stdout != "" {
			t.Errorf("bad %s=%q accepted: %+v", pair.key, pair.value, got)
		}
		if len(pair.value) >= 4 && strings.Contains(got.all(), pair.value) {
			t.Errorf("error output echoed rejected value for %s: %q", pair.key, got.all())
		}
	}

	const localUCURL = "http://127.0.0.1:10010/apis/site/v2/sports/soccer"
	if got := invoke(t, prod, strings.NewReader(localUCURL+"\n"), "config", "set", "uc.api_base_url"); got.code != 1 {
		t.Errorf("uc.api_base_url override in production = %+v, want exit 1", got)
	}
	if got := invoke(t, local, strings.NewReader(localUCURL+"\n"), "config", "set", "uc.api_base_url"); got.code != 0 {
		t.Errorf("uc.api_base_url override in local = %+v, want exit 0", got)
	}
}

func TestConfigLLMSettings(t *testing.T) {
	database := newClone(t)
	local := envFor(database, "local")
	prod := envFor(database, "production")
	apiKey := syntheticToken(t, 48)

	setKey := invoke(t, local, strings.NewReader(apiKey+"\n"), "config", "set", "llm.api_key")
	getKey := invoke(t, local, strings.NewReader(""), "config", "get", "llm.api_key")
	if setKey.code != 0 || setKey.stdout != "llm.api_key set\n" {
		t.Errorf("set llm.api_key = %+v, want exit 0", setKey)
	}
	if getKey.code != 0 || getKey.stdout != "[redacted]\n" {
		t.Errorf("get llm.api_key = %+v, want [redacted]", getKey)
	}
	if strings.Contains(setKey.all()+getKey.all(), apiKey) {
		t.Error("llm.api_key output leaked the secret")
	}

	setModel := invoke(t, local, strings.NewReader("gemini-3.5-flash\n"), "config", "set", "llm.model")
	getModel := invoke(t, local, strings.NewReader(""), "config", "get", "llm.model")
	if setModel.code != 0 || getModel.stdout != "gemini-3.5-flash\n" {
		t.Errorf("llm.model set/get = %+v / %+v", setModel, getModel)
	}

	for _, badModel := range []string{"", "bad/model", "bad?model", "bad:model", "bad model", strings.Repeat("m", 129)} {
		got := invoke(t, local, strings.NewReader(badModel+"\n"), "config", "set", "llm.model")
		if got.code != 1 {
			t.Errorf("bad llm.model %q accepted: %+v", badModel, got)
		}
	}

	const localLLMURL = "http://127.0.0.1:10010/v1/publishers/google/models"
	if got := invoke(t, prod, strings.NewReader(localLLMURL+"\n"), "config", "set", "llm.base_url"); got.code != 1 {
		t.Errorf("llm.base_url override in production = %+v, want exit 1", got)
	}
	if got := invoke(t, local, strings.NewReader(localLLMURL+"\n"), "config", "set", "llm.base_url"); got.code != 0 {
		t.Errorf("llm.base_url override in local = %+v, want exit 0", got)
	}
}

func TestConfigSetMultilineLLMPrompt(t *testing.T) {
	database := newClone(t)
	env := envFor(database, "local")

	defaultGet := invoke(t, env, strings.NewReader(""), "config", "get", "llm.prompt")
	if defaultGet.code != 0 || defaultGet.stdout != defaultLLMPrompt+"\n" {
		t.Fatalf("default get llm.prompt = %+v, want default prompt", defaultGet)
	}

	const multilinePrompt = "Rule 1: Keep 2nd person singular.\nRule 2: Use neutral Spanish tuteo.\nRule 3: Drop vocative slang."

	for _, rawInput := range []string{
		multilinePrompt + "\n",
		strings.ReplaceAll(multilinePrompt, "\n", "\r\n") + "\r\n",
	} {
		set := invoke(t, env, strings.NewReader(rawInput), "config", "set", "llm.prompt")
		if set.code != 0 || set.stdout != "llm.prompt set\n" {
			t.Fatalf("set llm.prompt = %+v, want exit 0", set)
		}

		get := invoke(t, env, strings.NewReader(""), "config", "get", "llm.prompt")
		if get.code != 0 || get.stdout != multilinePrompt+"\n" {
			t.Errorf("get llm.prompt = %q, want %q", get.stdout, multilinePrompt+"\n")
		}

		list := invoke(t, env, strings.NewReader(""), "config", "list")
		if list.code != 0 {
			t.Fatalf("list = %+v, want exit 0", list)
		}

		rows := strings.Split(strings.TrimSuffix(list.stdout, "\n"), "\n")
		if len(rows) != len(settingKeys()) {
			t.Errorf("config list returned %d rows, want strictly %d (one per setting): %q", len(rows), len(settingKeys()), list.stdout)
		}

		wantRow := "llm.prompt\tplain\t" + strings.ReplaceAll(multilinePrompt, "\n", `\n`) + "\tVertex AI question rephrase system prompt"
		if !strings.Contains(list.stdout, wantRow+"\n") {
			t.Errorf("list stdout = %q, want row %q", list.stdout, wantRow)
		}
	}

	for _, badPrompt := range []string{
		"",
		"\n\n",
		"   \r\n  ",
		"prompt-with-nul\x00marker\n",
		"prompt-with-lone-cr\rmarker\n",
		"prompt-with-tab\tmarker\n",
		strings.Repeat("p", maxLLMPromptBytes+1) + "\n",
	} {
		got := invoke(t, env, strings.NewReader(badPrompt), "config", "set", "llm.prompt")
		if got.code != 1 || got.stdout != "" {
			t.Errorf("bad llm.prompt accepted: %+v", got)
		}

		if strings.Contains(got.all(), "marker") {
			t.Errorf("error output echoed prompt input: %q", got.all())
		}
	}
}

func TestConfigSetRejectsAIzaLLMKeyInProduction(t *testing.T) {
	const (
		aizaKey   = "AIza-synthetic-test-key"
		vertexKey = "vertex-synthetic-test-key"
	)

	database := newClone(t)
	local := envFor(database, "local")
	prod := envFor(database, "production")

	rejectProd := invoke(t, prod, strings.NewReader(aizaKey+"\n"), "config", "set", "llm.api_key")
	if rejectProd.code != 1 || rejectProd.stdout != "" {
		t.Errorf("set AIza key in production = %+v, want exit 1 and empty stdout", rejectProd)
	}

	if !strings.Contains(rejectProd.stderr, "llm.api_key") {
		t.Errorf("stderr = %q, want mention of llm.api_key", rejectProd.stderr)
	}

	for _, secret := range []string{aizaKey, "synthetic-test-key", masterKey, database.URL().Reveal()} {
		if strings.Contains(rejectProd.all(), secret) {
			t.Errorf("production rejection output leaked secret fragment %q: %q", secret, rejectProd.all())
		}
	}

	acceptProd := invoke(t, prod, strings.NewReader(vertexKey+"\n"), "config", "set", "llm.api_key")
	if acceptProd.code != 0 || acceptProd.stdout != "llm.api_key set\n" {
		t.Errorf("set Vertex key in production = %+v, want exit 0 and %q", acceptProd, "llm.api_key set\n")
	}

	if strings.Contains(acceptProd.all(), vertexKey) {
		t.Error("production set output leaked vertexKey")
	}

	acceptLocal := invoke(t, local, strings.NewReader(aizaKey+"\n"), "config", "set", "llm.api_key")
	if acceptLocal.code != 0 || acceptLocal.stdout != "llm.api_key set\n" {
		t.Errorf("set AIza key in local = %+v, want exit 0 and %q", acceptLocal, "llm.api_key set\n")
	}

	if strings.Contains(acceptLocal.all(), aizaKey) {
		t.Error("local set output leaked aizaKey")
	}
}

func TestConfigRefusesAnotherMasterKey(t *testing.T) {
	database := newClone(t)
	env := envFor(database, "local")

	set := invoke(t, env, strings.NewReader(syntheticToken(t, 32)), "config", "set", "discord.token")
	if set.code != 0 {
		t.Fatalf("set = %+v", set)
	}

	otherKey := strings.TrimSuffix(invoke(t, environment{}, strings.NewReader(""), "keygen").stdout, "\n")
	other := envFor(database, "local")
	other["MASTER_KEY"] = otherKey

	got := invoke(t, other, strings.NewReader(""), "config", "list")

	if got.code != 1 || got.stdout != "" || !strings.Contains(got.stderr, "MASTER_KEY") {
		t.Errorf("got %+v, want exit 1 naming MASTER_KEY", got)
	}

	if strings.Contains(got.all(), otherKey) {
		t.Error("output contains the master key")
	}
}

func TestNoCommandPrintsSecrets(t *testing.T) {
	database := newClone(t)
	env := envFor(database, "local")
	token := syntheticToken(t, 48)
	secrets := []string{token, masterKey, database.URL().Reveal()}

	runs := []struct {
		stdin string
		args  []string
	}{
		{token, []string{"config", "set", "discord.token"}},
		{token + " x", []string{"config", "set", "discord.token"}},
		{"", []string{"config", "get", "discord.token"}},
		{"", []string{"config", "list"}},
		{"", []string{"migrate"}},
		{"", []string{"version"}},
		{"", []string{"config", "get", "nope"}},
	}

	for _, r := range runs {
		got := invoke(t, env, strings.NewReader(r.stdin), r.args...)

		for _, secret := range secrets {
			if strings.Contains(got.all(), secret) {
				t.Errorf("%v printed a secret", r.args)
			}
		}
	}
}
