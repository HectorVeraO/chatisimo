package giftexchangecmd_test

import (
	"encoding/json"
	"reflect"
	"regexp"
	"testing"
	"unicode/utf8"

	"github.com/HectorVeraO/chatisimo/internal/discord/internal/giftexchangecmd"
	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

// Discord's option types.
const (
	typeSubCommand      = 1
	typeSubCommandGroup = 2
	typeString          = 3

	maxSubcommands = 25
	maxNameLength  = 32
)

var namePattern = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

type option struct {
	Type         int      `json:"type"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Required     bool     `json:"required"`
	Autocomplete bool     `json:"autocomplete"`
	MaxLength    *int     `json:"max_length"`
	MinLength    *int     `json:"min_length"`
	Options      []option `json:"options"`
}

type command struct {
	Name             string   `json:"name"`
	Description      string   `json:"description"`
	Contexts         []int    `json:"contexts"`
	IntegrationTypes []int    `json:"integration_types"`
	Options          []option `json:"options"`
}

func builtTrees(t *testing.T) []command {
	t.Helper()

	raw, err := json.Marshal(giftexchangecmd.Commands())
	if err != nil {
		t.Fatalf("marshal commands: %v", err)
	}

	var commands []command
	err = json.Unmarshal(raw, &commands)
	if err != nil {
		t.Fatalf("unmarshal commands: %v", err)
	}

	return commands
}

// verbsOf returns the subcommands of each tree: /chatisimo through its group, /ge directly.
func verbsOf(t *testing.T, commands []command) map[string][]option {
	t.Helper()

	trees := map[string][]option{}
	for _, c := range commands {
		if c.Name == "chatisimo" {
			if len(c.Options) != 1 || c.Options[0].Type != typeSubCommandGroup || c.Options[0].Name != "giftexchange" {
				t.Fatalf("/chatisimo options = %+v, want the giftexchange group only", c.Options)
			}
			trees[c.Name] = c.Options[0].Options
			continue
		}
		trees[c.Name] = c.Options
	}

	return trees
}

func TestTheTwoTreesHoldTheSameVerbs(t *testing.T) {
	t.Parallel()

	trees := verbsOf(t, builtTrees(t))

	if len(trees) != 2 || trees["chatisimo"] == nil || trees["ge"] == nil {
		t.Fatalf("trees = %v, want chatisimo and ge", trees)
	}
	if !reflect.DeepEqual(trees["chatisimo"], trees["ge"]) {
		t.Error("the /chatisimo giftexchange verbs differ from the /ge verbs")
	}
	if got := len(trees["ge"]); got != 17 {
		t.Errorf("%d verbs, want 17", got)
	}
}

func TestBothTreesWorkInServersAndTheBotDMForGuildInstalls(t *testing.T) {
	t.Parallel()

	for _, c := range builtTrees(t) {
		if !reflect.DeepEqual(c.Contexts, []int{0, 1}) {
			t.Errorf("/%s contexts = %v, want [0 1]", c.Name, c.Contexts)
		}
		if !reflect.DeepEqual(c.IntegrationTypes, []int{0}) {
			t.Errorf("/%s integration types = %v, want [0]", c.Name, c.IntegrationTypes)
		}
		if n := utf8.RuneCountInString(c.Description); n < 1 || n > 100 {
			t.Errorf("/%s description has %d characters", c.Name, n)
		}
	}
}

func TestVerbsKeepToDiscordsLimits(t *testing.T) {
	t.Parallel()

	for tree, verbs := range verbsOf(t, builtTrees(t)) {
		if len(verbs) > maxSubcommands {
			t.Errorf("%s has %d subcommands, limit %d", tree, len(verbs), maxSubcommands)
		}

		for _, verb := range verbs {
			if verb.Type != typeSubCommand {
				t.Errorf("%s/%s has type %d, want a subcommand", tree, verb.Name, verb.Type)
			}
			checkNameAndDescription(t, tree+"/"+verb.Name, verb.Name, verb.Description)
			checkOptions(t, tree+"/"+verb.Name, verb.Options)
		}
	}
}

func checkNameAndDescription(t *testing.T, where, name, description string) {
	t.Helper()

	if !namePattern.MatchString(name) || len(name) > maxNameLength {
		t.Errorf("%s: name %q breaks ^[a-z0-9-]{1,32}$", where, name)
	}
	if n := utf8.RuneCountInString(description); n < 1 || n > 100 {
		t.Errorf("%s: description has %d characters, want 1-100", where, n)
	}
}

func checkOptions(t *testing.T, where string, options []option) {
	t.Helper()

	seenOptional := false
	for _, o := range options {
		checkNameAndDescription(t, where+"."+o.Name, o.Name, o.Description)

		if o.Required && seenOptional {
			t.Errorf("%s: required option %s follows an optional one", where, o.Name)
		}
		if !o.Required {
			seenOptional = true
		}
	}

	exchange := -1
	for i, o := range options {
		if o.Name == "exchange" {
			exchange = i
		}
	}
	if exchange >= 0 {
		checkExchangeOption(t, where, options, exchange)
	}
}

func checkExchangeOption(t *testing.T, where string, options []option, i int) {
	t.Helper()

	o := options[i]
	if i != len(options)-1 {
		t.Errorf("%s: exchange is not the last option", where)
	}
	if o.Required || !o.Autocomplete || o.Type != typeString {
		t.Errorf("%s: exchange = %+v, want an optional string with autocomplete", where, o)
	}
	if o.MaxLength == nil || *o.MaxLength != 100 {
		t.Errorf("%s: exchange max_length = %v, want 100", where, o.MaxLength)
	}
}

func TestTextOptionsUseTheModulesLimits(t *testing.T) {
	t.Parallel()

	want := map[string]struct{ min, max int }{
		"create/name":      {1, giftexchange.MaxNameRunes},
		"rename/name":      {1, giftexchange.MaxNameRunes},
		"ask/question":     {1, giftexchange.MaxQuestionRunes},
		"create/draw-at":   {0, 64},
		"create/reveal-at": {0, 64},
	}

	for _, verb := range verbsOf(t, builtTrees(t))["ge"] {
		for _, o := range verb.Options {
			limits, listed := want[verb.Name+"/"+o.Name]
			if !listed {
				continue
			}

			delete(want, verb.Name+"/"+o.Name)
			if o.MaxLength == nil || *o.MaxLength != limits.max {
				t.Errorf("%s/%s max_length = %v, want %d", verb.Name, o.Name, o.MaxLength, limits.max)
			}
			gotMin := 0
			if o.MinLength != nil {
				gotMin = *o.MinLength
			}
			if gotMin != limits.min {
				t.Errorf("%s/%s min_length = %d, want %d", verb.Name, o.Name, gotMin, limits.min)
			}
		}
	}

	for missing := range want {
		t.Errorf("option %s is not in the tree", missing)
	}
}
