package uccmd_test

import (
	"encoding/json"
	"reflect"
	"regexp"
	"testing"
	"unicode/utf8"

	"github.com/HectorVeraO/chatisimo/internal/discord/internal/uccmd"
	"github.com/HectorVeraO/chatisimo/internal/uccalendar"
)

const (
	typeSubCommand = 1
	typeString     = 3
	typeUser       = 6
	typeChannel    = 7
	maxNameLength  = 32
)

var namePattern = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

type choiceJSON struct {
	Name              string            `json:"name"`
	NameLocalizations map[string]string `json:"name_localizations"`
	Value             string            `json:"value"`
}

type optionJSON struct {
	Type                     int               `json:"type"`
	Name                     string            `json:"name"`
	NameLocalizations        map[string]string `json:"name_localizations"`
	Description              string            `json:"description"`
	DescriptionLocalizations map[string]string `json:"description_localizations"`
	Required                 bool              `json:"required"`
	MaxLength                *int              `json:"max_length"`
	ChannelTypes             []int             `json:"channel_types"`
	Choices                  []choiceJSON      `json:"choices"`
	Options                  []optionJSON      `json:"options"`
}

type commandJSON struct {
	Name                     string            `json:"name"`
	Description              string            `json:"description"`
	DescriptionLocalizations map[string]string `json:"description_localizations"`
	DefaultMemberPermissions string            `json:"default_member_permissions"`
	Contexts                 []int             `json:"contexts"`
	IntegrationTypes         []int             `json:"integration_types"`
	Options                  []optionJSON      `json:"options"`
}

func marshalTopCommand(t *testing.T) commandJSON {
	t.Helper()

	raw, err := json.Marshal(uccmd.Command())
	if err != nil {
		t.Fatalf("marshal Command: %v", err)
	}

	var cmd commandJSON
	err = json.Unmarshal(raw, &cmd)
	if err != nil {
		t.Fatalf("unmarshal Command: %v", err)
	}

	return cmd
}

func marshalSubCommand(t *testing.T) optionJSON {
	t.Helper()

	raw, err := json.Marshal(uccmd.SubCommand())
	if err != nil {
		t.Fatalf("marshal SubCommand: %v", err)
	}

	var sub optionJSON
	err = json.Unmarshal(raw, &sub)
	if err != nil {
		t.Fatalf("unmarshal SubCommand: %v", err)
	}

	return sub
}

func TestTopLevelCommandAndSubCommandShareIdenticalOptionsAndMetadata(t *testing.T) {
	t.Parallel()

	top := marshalTopCommand(t)
	sub := marshalSubCommand(t)

	if top.Name != "uc" || sub.Name != "uc" {
		t.Fatalf("names = %q and %q, want uc", top.Name, sub.Name)
	}
	if sub.Type != typeSubCommand {
		t.Errorf("SubCommand type = %d, want %d", sub.Type, typeSubCommand)
	}
	if !reflect.DeepEqual(top.Contexts, []int{0, 1}) {
		t.Errorf("/uc contexts = %v, want [0 1]", top.Contexts)
	}
	if !reflect.DeepEqual(top.IntegrationTypes, []int{0}) {
		t.Errorf("/uc integration types = %v, want [0]", top.IntegrationTypes)
	}
	if top.Description != sub.Description {
		t.Errorf("descriptions differ: %q vs %q", top.Description, sub.Description)
	}
	if !reflect.DeepEqual(top.DescriptionLocalizations, sub.DescriptionLocalizations) {
		t.Errorf("description localizations differ: %v vs %v", top.DescriptionLocalizations, sub.DescriptionLocalizations)
	}
	if !reflect.DeepEqual(top.Options, sub.Options) {
		t.Errorf("/uc options differ from /chatisimo uc options:\ntop=%+v\nsub=%+v", top.Options, sub.Options)
	}
}

func TestCommandOptionsChoicesAndLocalizationsFitDiscordLimits(t *testing.T) {
	t.Parallel()

	top := marshalTopCommand(t)
	checkCommandName(t, "/uc", top.Name)
	checkLocalizedText(t, "/uc.description", top.Description, top.DescriptionLocalizations, 1, 100)

	if len(top.Options) != 2 {
		t.Fatalf("/uc has %d options, want 2 (window and date)", len(top.Options))
	}

	windowOpt := top.Options[0]
	if windowOpt.Name != "window" || windowOpt.Type != typeString || windowOpt.Required {
		t.Fatalf("window option = %+v, want optional string named window", windowOpt)
	}
	checkOptionNames(t, "/uc.window", windowOpt.Name, windowOpt.NameLocalizations, "ventana")
	checkLocalizedText(t, "/uc.window.description", windowOpt.Description, windowOpt.DescriptionLocalizations, 1, 100)

	wantChoices := []string{
		string(uccalendar.WindowToday),
		string(uccalendar.WindowTomorrow),
		string(uccalendar.WindowThisWeekend),
		string(uccalendar.WindowNextWeekend),
		string(uccalendar.WindowThisMonth),
	}
	if len(windowOpt.Choices) != len(wantChoices) {
		t.Fatalf("window has %d choices, want %d", len(windowOpt.Choices), len(wantChoices))
	}
	for i, ch := range windowOpt.Choices {
		if ch.Value != wantChoices[i] {
			t.Errorf("choice[%d].Value = %q, want %q", i, ch.Value, wantChoices[i])
		}
		checkLocalizedText(t, "/uc.window.choice."+ch.Value, ch.Name, ch.NameLocalizations, 1, 100)
	}

	dateOpt := top.Options[1]
	if dateOpt.Name != "date" || dateOpt.Type != typeString || dateOpt.Required {
		t.Fatalf("date option = %+v, want optional string named date", dateOpt)
	}
	if dateOpt.MaxLength == nil || *dateOpt.MaxLength != 10 {
		t.Errorf("date max_length = %v, want 10", dateOpt.MaxLength)
	}
	checkOptionNames(t, "/uc.date", dateOpt.Name, dateOpt.NameLocalizations, "fecha")
	checkLocalizedText(t, "/uc.date.description", dateOpt.Description, dateOpt.DescriptionLocalizations, 1, 100)
}

func TestConfigCommandIsGuildOnlyManageGuildAndValidatesSubcommands(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(uccmd.ConfigCommand())
	if err != nil {
		t.Fatalf("marshal ConfigCommand: %v", err)
	}

	var cfg commandJSON
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("unmarshal ConfigCommand: %v", err)
	}

	if cfg.Name != "uc-config" {
		t.Fatalf("name = %q, want uc-config", cfg.Name)
	}
	checkCommandName(t, "/uc-config", cfg.Name)
	checkLocalizedText(t, "/uc-config.description", cfg.Description, cfg.DescriptionLocalizations, 1, 100)

	if cfg.DefaultMemberPermissions != "32" {
		t.Errorf("default_member_permissions = %q, want 32 (ManageGuild)", cfg.DefaultMemberPermissions)
	}
	if !reflect.DeepEqual(cfg.Contexts, []int{0}) {
		t.Errorf("/uc-config contexts = %v, want [0] (guild only)", cfg.Contexts)
	}
	if !reflect.DeepEqual(cfg.IntegrationTypes, []int{0}) {
		t.Errorf("/uc-config integration_types = %v, want [0]", cfg.IntegrationTypes)
	}
	if len(cfg.Options) != 3 {
		t.Fatalf("/uc-config subcommands = %d, want 3 (set, show, clear)", len(cfg.Options))
	}

	setSub := cfg.Options[0]
	if setSub.Name != "set" || setSub.Type != typeSubCommand {
		t.Fatalf("subcommand[0] = %+v, want subcommand set", setSub)
	}
	checkOptionNames(t, "/uc-config.set", setSub.Name, setSub.NameLocalizations, "configurar")
	checkLocalizedText(t, "/uc-config.set.description", setSub.Description, setSub.DescriptionLocalizations, 1, 100)
	if len(setSub.Options) != 3 {
		t.Fatalf("/uc-config set options = %d, want 3 (friend, channel, locale)", len(setSub.Options))
	}
	friendOpt := setSub.Options[0]
	if friendOpt.Name != "friend" || friendOpt.Type != typeUser || friendOpt.Required {
		t.Fatalf("friend option = %+v, want optional user option", friendOpt)
	}
	checkOptionNames(t, "/uc-config.set.friend", friendOpt.Name, friendOpt.NameLocalizations, "amigo")
	checkLocalizedText(t, "/uc-config.set.friend.description", friendOpt.Description, friendOpt.DescriptionLocalizations, 1, 100)

	channelOpt := setSub.Options[1]
	if channelOpt.Name != "channel" || channelOpt.Type != typeChannel || channelOpt.Required {
		t.Fatalf("channel option = %+v, want optional channel option", channelOpt)
	}
	if !reflect.DeepEqual(channelOpt.ChannelTypes, []int{0, 5}) {
		t.Errorf("channel_types = %v, want [0 5]", channelOpt.ChannelTypes)
	}
	checkOptionNames(t, "/uc-config.set.channel", channelOpt.Name, channelOpt.NameLocalizations, "canal")
	checkLocalizedText(t, "/uc-config.set.channel.description", channelOpt.Description, channelOpt.DescriptionLocalizations, 1, 100)

	localeOpt := setSub.Options[2]
	if localeOpt.Name != "locale" || localeOpt.Type != typeString || localeOpt.Required {
		t.Fatalf("locale option = %+v, want optional string option", localeOpt)
	}
	checkOptionNames(t, "/uc-config.set.locale", localeOpt.Name, localeOpt.NameLocalizations, "idioma")
	checkLocalizedText(t, "/uc-config.set.locale.description", localeOpt.Description, localeOpt.DescriptionLocalizations, 1, 100)
	wantLocales := []string{uccalendar.LocaleESCL, uccalendar.LocaleENUS}
	if len(localeOpt.Choices) != len(wantLocales) {
		t.Fatalf("locale choices = %d, want %d", len(localeOpt.Choices), len(wantLocales))
	}
	for i, ch := range localeOpt.Choices {
		if ch.Value != wantLocales[i] {
			t.Errorf("locale choice[%d] = %q, want %q", i, ch.Value, wantLocales[i])
		}
		checkLocalizedText(t, "/uc-config.set.locale."+ch.Value, ch.Name, ch.NameLocalizations, 1, 100)
	}

	showSub := cfg.Options[1]
	if showSub.Name != "show" || showSub.Type != typeSubCommand || len(showSub.Options) != 0 {
		t.Fatalf("subcommand[1] = %+v, want subcommand show without options", showSub)
	}
	checkOptionNames(t, "/uc-config.show", showSub.Name, showSub.NameLocalizations, "ver")
	checkLocalizedText(t, "/uc-config.show.description", showSub.Description, showSub.DescriptionLocalizations, 1, 100)

	clearSub := cfg.Options[2]
	if clearSub.Name != "clear" || clearSub.Type != typeSubCommand || len(clearSub.Options) != 1 {
		t.Fatalf("subcommand[2] = %+v, want subcommand clear with target option", clearSub)
	}
	checkOptionNames(t, "/uc-config.clear", clearSub.Name, clearSub.NameLocalizations, "limpiar")
	checkLocalizedText(t, "/uc-config.clear.description", clearSub.Description, clearSub.DescriptionLocalizations, 1, 100)

	targetOpt := clearSub.Options[0]
	if targetOpt.Name != "target" || targetOpt.Type != typeString || targetOpt.Required {
		t.Fatalf("target option = %+v, want optional string option", targetOpt)
	}
	checkOptionNames(t, "/uc-config.clear.target", targetOpt.Name, targetOpt.NameLocalizations, "objetivo")
	checkLocalizedText(t, "/uc-config.clear.target.description", targetOpt.Description, targetOpt.DescriptionLocalizations, 1, 100)
	wantTargets := []string{
		string(uccalendar.ClearTargetAll),
		string(uccalendar.ClearTargetFriend),
		string(uccalendar.ClearTargetChannel),
		string(uccalendar.ClearTargetLocale),
	}
	if len(targetOpt.Choices) != len(wantTargets) {
		t.Fatalf("target choices = %d, want %d", len(targetOpt.Choices), len(wantTargets))
	}
	for i, ch := range targetOpt.Choices {
		if ch.Value != wantTargets[i] {
			t.Errorf("target choice[%d] = %q, want %q", i, ch.Value, wantTargets[i])
		}
		checkLocalizedText(t, "/uc-config.clear.target."+ch.Value, ch.Name, ch.NameLocalizations, 1, 100)
	}
}

func checkCommandName(t *testing.T, where, name string) {
	t.Helper()

	if !namePattern.MatchString(name) || len(name) > maxNameLength {
		t.Errorf("%s: name %q breaks ^[a-z0-9-]{1,32}$", where, name)
	}
}

func checkOptionNames(t *testing.T, where, name string, locs map[string]string, wantSpanish string) {
	t.Helper()

	checkCommandName(t, where, name)
	checkLocalizedText(t, where+".name", name, locs, 1, maxNameLength)
	for localeKey, localized := range locs {
		checkCommandName(t, where+"."+localeKey, localized)
	}
	if locs["es-419"] != wantSpanish || locs["es-ES"] != wantSpanish {
		t.Errorf("%s: Spanish name localizations = %v, want %q", where, locs, wantSpanish)
	}
}

func checkLocalizedText(t *testing.T, where, primary string, locs map[string]string, minRunes, maxRunes int) {
	t.Helper()

	primaryRunes := utf8.RuneCountInString(primary)
	if primaryRunes < minRunes || primaryRunes > maxRunes {
		t.Errorf("%s: %q has %d runes, want %d..%d", where, primary, primaryRunes, minRunes, maxRunes)
	}
	if len(locs) == 0 {
		t.Fatalf("%s: missing localizations", where)
	}
	if locs["en-US"] != primary {
		t.Errorf("%s: en-US %q != primary %q", where, locs["en-US"], primary)
	}
	for _, key := range []string{"es-419", "es-ES"} {
		val := locs[key]
		count := utf8.RuneCountInString(val)
		if count < minRunes || count > maxRunes {
			t.Errorf("%s: %s %q has %d runes, want %d..%d", where, key, val, count, minRunes, maxRunes)
		}
		if val == primary {
			t.Errorf("%s: %s %q is identical to English", where, key, val)
		}
	}
	if locs["es-419"] != locs["es-ES"] {
		t.Errorf("%s: es-419 %q != es-ES %q", where, locs["es-419"], locs["es-ES"])
	}
}
