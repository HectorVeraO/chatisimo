package capitimecmd_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"

	"github.com/HectorVeraO/chatisimo/internal/capitime"
	"github.com/HectorVeraO/chatisimo/internal/discord/internal/capitimecmd"
)

func mustUnmarshalSlashData(t *testing.T, raw string) discord.SlashCommandInteractionData {
	t.Helper()

	var data discord.SlashCommandInteractionData
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		t.Fatalf("Unmarshal SlashCommandInteractionData: %v", err)
	}

	return data
}

func TestConfigCommandDefinitionAndCatalogs(t *testing.T) {
	cmd := capitimecmd.ConfigCommand()
	if cmd.Name != "capitime-config" {
		t.Fatalf("ConfigCommand.Name = %q, want capitime-config", cmd.Name)
	}
	if len(cmd.Options) != 3 {
		t.Fatalf("ConfigCommand options len = %d, want 3 (set, show, clear)", len(cmd.Options))
	}

	if got := capitimecmd.ResolveLocale("en-US"); got != capitimecmd.LocaleENUS {
		t.Errorf("ResolveLocale(en-US) = %q, want en-US", got)
	}
	if got := capitimecmd.ResolveLocale("", "es-419"); got != capitimecmd.LocaleESCL {
		t.Errorf("ResolveLocale('', es-419) = %q, want es-CL", got)
	}
	if got := capitimecmd.ResolveLocale("fr"); got != capitimecmd.LocaleESCL {
		t.Errorf("ResolveLocale(fr) = %q, want default es-CL", got)
	}
}

func TestRenderConfigViewsAndErrorMapping(t *testing.T) {
	cfg := capitime.GuildConfig{
		GuildID:           "200000000000000001",
		TargetUserID:      "100000000000000001",
		EnabledChannelIDs: []string{"300000000000000001", "300000000000000002"},
		Locale:            capitime.LocaleESCL,
		DebounceWindow:    2 * time.Minute,
	}

	showES := capitimecmd.RenderConfigShow(capitimecmd.LocaleESCL, cfg)
	if !strings.Contains(showES, "<@100000000000000001>") || !strings.Contains(showES, "<#300000000000000001>, <#300000000000000002>") {
		t.Errorf("showES = %q, missing target or channel mentions", showES)
	}

	savedEN := capitimecmd.RenderConfigSaved(capitimecmd.LocaleENUS, capitime.GuildConfig{
		GuildID:        "200000000000000001",
		DebounceWindow: 2 * time.Minute,
	})
	if !strings.Contains(savedEN, "Not configured") || !strings.Contains(savedEN, "All server channels") {
		t.Errorf("savedEN = %q, missing unset labels", savedEN)
	}

	clearedES := capitimecmd.RenderConfigCleared(capitimecmd.LocaleESCL, capitime.GuildConfig{
		GuildID:        "200000000000000001",
		DebounceWindow: 2 * time.Minute,
	})
	if !strings.Contains(clearedES, "restablecida") {
		t.Errorf("clearedES = %q, missing cleared heading", clearedES)
	}

	cases := []struct {
		err     error
		wantOut string
	}{
		{err: capitimecmd.ErrGuildOnly, wantOut: "rejected:guild_only"},
		{err: capitime.ErrForbidden, wantOut: "rejected:forbidden"},
		{err: capitimecmd.ErrInvalidTargetUser, wantOut: "rejected:invalid_input"},
		{err: capitimecmd.ErrInvalidChannel, wantOut: "rejected:invalid_input"},
		{err: &capitime.InputError{Code: capitime.ErrCodeEmptyUpdate}, wantOut: "rejected:invalid_input"},
		{err: &capitime.InputError{Code: capitime.ErrCodeSnowflake}, wantOut: "rejected:invalid_input"},
		{err: &capitime.InputError{Code: capitime.ErrCodeLocale}, wantOut: "rejected:invalid_input"},
		{err: &capitime.InputError{Code: capitime.ErrCodeClearTarget}, wantOut: "rejected:invalid_input"},
		{err: &capitime.InputError{Code: capitime.ErrCodeChannelCap}, wantOut: "rejected:invalid_input"},
		{err: capitime.ErrInvalidInput, wantOut: "rejected:invalid_input"},
	}

	for _, tc := range cases {
		rej, ok := capitimecmd.TryMapErrorIn(capitimecmd.LocaleENUS, "req-1", tc.err)
		if !ok || rej.Outcome != tc.wantOut || rej.Text == "" {
			t.Errorf("TryMapErrorIn(%v) = (%+v, %v), want outcome %q", tc.err, rej, ok, tc.wantOut)
		}
	}

	if _, ok := capitimecmd.TryMapError("req-1", errors.New("unrelated error")); ok {
		t.Error("TryMapError matched unrelated error")
	}
}

func TestParseConfigOptionsValidation(t *testing.T) {
	validData := mustUnmarshalSlashData(t, `{
		"id": "600000000000000001",
		"name": "capitime-config",
		"type": 1,
		"options": [
			{"name": "target", "type": 6, "value": "100000000000000001"},
			{"name": "channel", "type": 7, "value": "300000000000000001"},
			{"name": "locale", "type": 3, "value": "es-CL"}
		],
		"resolved": {
			"users": {
				"100000000000000001": {"id": "100000000000000001", "username": "capi", "discriminator": "0", "bot": false}
			},
			"members": {
				"100000000000000001": {"roles": []}
			},
			"channels": {
				"300000000000000001": {"id": "300000000000000001", "name": "general", "type": 0}
			}
		}
	}`)

	upd, err := capitimecmd.ParseConfigUpdateOptions(validData)
	if err != nil {
		t.Fatalf("ParseConfigUpdateOptions valid: %v", err)
	}
	if upd.TargetUserID == nil || *upd.TargetUserID != "100000000000000001" {
		t.Errorf("TargetUserID = %v", upd.TargetUserID)
	}
	if upd.ChannelID == nil || *upd.ChannelID != "300000000000000001" {
		t.Errorf("ChannelID = %v", upd.ChannelID)
	}
	if upd.Locale == nil || *upd.Locale != capitime.LocaleESCL {
		t.Errorf("Locale = %v", upd.Locale)
	}

	// Bot target user is rejected.
	botData := mustUnmarshalSlashData(t, `{
		"id": "600000000000000001",
		"name": "capitime-config",
		"type": 1,
		"options": [
			{"name": "target", "type": 6, "value": "100000000000000001"}
		],
		"resolved": {
			"users": {
				"100000000000000001": {"id": "100000000000000001", "username": "bot", "discriminator": "0", "bot": true}
			},
			"members": {
				"100000000000000001": {"roles": []}
			}
		}
	}`)
	if _, err = capitimecmd.ParseConfigUpdateOptions(botData); !errors.Is(err, capitimecmd.ErrInvalidTargetUser) {
		t.Errorf("bot target user = %v, want ErrInvalidTargetUser", err)
	}

	// Default clear target when option is omitted.
	clearTarget, err := capitimecmd.ParseClearTargetOption(discord.SlashCommandInteractionData{})
	if err != nil || clearTarget != capitime.ClearTargetAll {
		t.Errorf("ParseClearTargetOption empty = (%q, %v), want all", clearTarget, err)
	}

	// Explicit clear targets and invalid clear target.
	for _, targetVal := range []string{"all", "target", "channels", "locale"} {
		clearData := mustUnmarshalSlashData(t, `{
			"id": "600000000000000001",
			"name": "capitime-config",
			"type": 1,
			"options": [{"name": "target", "type": 3, "value": "`+targetVal+`"}]
		}`)
		gotTarget, parseErr := capitimecmd.ParseClearTargetOption(clearData)
		if parseErr != nil || string(gotTarget) != targetVal {
			t.Errorf("ParseClearTargetOption(%s) = (%q, %v)", targetVal, gotTarget, parseErr)
		}
	}

	badClearData := mustUnmarshalSlashData(t, `{
		"id": "600000000000000001",
		"name": "capitime-config",
		"type": 1,
		"options": [{"name": "target", "type": 3, "value": "unknown"}]
	}`)
	if _, err = capitimecmd.ParseClearTargetOption(badClearData); !errors.Is(err, capitime.ErrInvalidInput) {
		t.Errorf("ParseClearTargetOption(unknown) = %v, want ErrInvalidInput", err)
	}

	// Voice channel and unresolvable member/channel options are rejected.
	voiceChannelData := mustUnmarshalSlashData(t, `{
		"id": "600000000000000001",
		"name": "capitime-config",
		"type": 1,
		"options": [{"name": "channel", "type": 7, "value": "300000000000000009"}],
		"resolved": {
			"channels": {
				"300000000000000009": {"id": "300000000000000009", "name": "voice", "type": 2}
			}
		}
	}`)
	if _, err = capitimecmd.ParseConfigUpdateOptions(voiceChannelData); !errors.Is(err, capitimecmd.ErrInvalidChannel) {
		t.Errorf("voice channel = %v, want ErrInvalidChannel", err)
	}

	badLocaleData := mustUnmarshalSlashData(t, `{
		"id": "600000000000000001",
		"name": "capitime-config",
		"type": 1,
		"options": [{"name": "locale", "type": 3, "value": "pt-BR"}]
	}`)
	if _, err = capitimecmd.ParseConfigUpdateOptions(badLocaleData); !errors.Is(err, capitime.ErrInvalidInput) {
		t.Errorf("invalid locale = %v, want ErrInvalidInput", err)
	}

	if _, err = capitimecmd.New(nil); err == nil {
		t.Error("New(nil) succeeded, want error")
	}
}

func FuzzParseSlashOptions(f *testing.F) {
	seeds := []string{
		`{}`,
		`{"id":"600000000000000001","name":"capitime-config","type":1,"options":[{"name":"target","type":3,"value":"all"}]}`,
		`{"id":"600000000000000001","name":"capitime-config","type":1,"options":[{"name":"locale","type":3,"value":"es-CL"}]}`,
		`{"id":"600000000000000001","name":"capitime-config","type":1,"options":[{"name":"target","type":6,"value":"100000000000000001"}]}`,
		`{"id":"600000000000000001","name":"capitime-config","type":1,"options":[{"name":"channel","type":7,"value":"300000000000000001"}]}`,
		`{"id":"600000000000000001","name":"capitime-config","type":1,"options":[{"name":"target","type":3,"value":"\u202e@everyone"}]}`,
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(_ *testing.T, raw string) {
		var data discord.SlashCommandInteractionData
		if err := json.Unmarshal([]byte(raw), &data); err != nil {
			return
		}
		// Fuzzing verifies neither parser panics on arbitrary unmarshaled interaction payloads.
		_, _ = capitimecmd.ParseConfigUpdateOptions(data)
		_, _ = capitimecmd.ParseClearTargetOption(data)
	})
}
