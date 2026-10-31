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
	Choices                  []choiceJSON      `json:"choices"`
	Options                  []optionJSON      `json:"options"`
}

type commandJSON struct {
	Name                     string            `json:"name"`
	Description              string            `json:"description"`
	DescriptionLocalizations map[string]string `json:"description_localizations"`
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
