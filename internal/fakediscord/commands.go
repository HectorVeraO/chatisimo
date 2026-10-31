package fakediscord

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"unicode/utf8"
)

// Application command option types.
const (
	optionSubCommand      = 1
	optionSubCommandGroup = 2
	optionString          = 3
	optionInteger         = 4
	optionBoolean         = 5
	optionUser            = 6
	optionChannel         = 7
	optionRole            = 8
	optionMentionable     = 9
	optionNumber          = 10

	chatInputCommand = 1
	maxPathSegments  = 3
	maxPathLength    = 256
	// maxExactInteger is the largest integer a float64 holds exactly.
	maxExactInteger = 1 << 53
)

// problemList is a set of validation problems reported together.
type problemList []error

func (p problemList) Error() string {
	return errors.Join(p...).Error()
}

func (p problemList) Unwrap() []error {
	return p
}

// optionDef is one option of a registered command, as far as validation needs it.
type optionDef struct {
	Name         string            `json:"name"`
	Type         int               `json:"type"`
	Required     bool              `json:"required"`
	Autocomplete bool              `json:"autocomplete"`
	MinLength    *int              `json:"min_length"`
	MaxLength    *int              `json:"max_length"`
	MinValue     *float64          `json:"min_value"`
	MaxValue     *float64          `json:"max_value"`
	Choices      []json.RawMessage `json:"choices"`
	ChannelTypes []int             `json:"channel_types"`
	Options      []optionDef       `json:"options"`
}

// commandDef is a registered command's definition.
type commandDef struct {
	Name     string      `json:"name"`
	Type     int         `json:"type"`
	Contexts []int       `json:"contexts"`
	Options  []optionDef `json:"options"`
}

// parsedPath is a slash path resolved against a registered command.
type parsedPath struct {
	command  Command
	group    string
	sub      string
	leaves   []optionDef
	contexts []int
}

// resolvePath maps a path like /ge/join to the registered command, its subcommand group and
// subcommand. A guild's own commands win over global ones of the same name.
func resolvePath(commands []Command, guildID, path string) (parsedPath, error) {
	if len(path) > maxPathLength {
		return parsedPath{}, fmt.Errorf("path: longer than %d characters", maxPathLength)
	}
	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	isShapeValid := strings.HasPrefix(path, "/") && len(segments) <= maxPathSegments && !slices.Contains(segments, "")
	if !isShapeValid {
		return parsedPath{}, fmt.Errorf("path %q: must look like /command/subcommand", path)
	}

	command, found := findCommand(commands, guildID, segments[0])
	if !found {
		return parsedPath{}, fmt.Errorf("path %q: no registered command %q", path, segments[0])
	}
	var def commandDef
	err := json.Unmarshal(command.Definition, &def)
	if err != nil {
		return parsedPath{}, fmt.Errorf("path %q: the registered definition is unreadable", path)
	}

	resolved := parsedPath{command: command, leaves: def.Options, contexts: def.Contexts}
	for depth, segment := range segments[1:] {
		next, isFound := findSubcommand(resolved.leaves, segment)
		if !isFound {
			return parsedPath{}, fmt.Errorf("path %q: %q is not a subcommand here", path, segment)
		}
		isGroup := next.Type == optionSubCommandGroup
		if isGroup && depth == 0 {
			resolved.group = next.Name
		} else {
			resolved.sub = next.Name
		}
		resolved.leaves = next.Options
	}

	hasMore := slices.ContainsFunc(resolved.leaves, func(o optionDef) bool {
		return o.Type == optionSubCommand || o.Type == optionSubCommandGroup
	})
	if hasMore {
		return parsedPath{}, fmt.Errorf("path %q: the command needs a subcommand", path)
	}

	return resolved, nil
}

func findCommand(commands []Command, guildID, name string) (Command, bool) {
	scopes := []string{""}
	if guildID != "" {
		scopes = []string{guildID, ""}
	}
	for _, scope := range scopes {
		for _, command := range commands {
			isMatch := command.GuildID == scope && command.Name == name && command.Type == chatInputCommand
			if isMatch {
				return command, true
			}
		}
	}

	return Command{}, false
}

func findSubcommand(options []optionDef, name string) (optionDef, bool) {
	for _, option := range options {
		isSub := option.Type == optionSubCommand || option.Type == optionSubCommandGroup
		if isSub && option.Name == name {
			return option, true
		}
	}

	return optionDef{}, false
}

// optionInput is how option building sees the world.
type optionInput struct {
	values           map[string]any
	bypass           bool
	autocomplete     bool
	focused          string
	guildID          string
	userJSONByID     func(id string) (userJSON, bool)
	isGuildMember    func(id string) bool
	channelByID      func(id string) (*channelState, bool)
	resolvedUsers    map[string]userJSON
	resolvedMember   map[string]map[string]any
	resolvedChannels map[string]map[string]any
}

// buildOptions validates the values against the leaf definitions and returns the leaf
// options as Discord sends them, together with every problem found.
func buildOptions(leaves []optionDef, in *optionInput) ([]map[string]any, []error) {
	var problems []error
	built := []map[string]any{}

	known := map[string]bool{}
	for _, def := range leaves {
		known[def.Name] = true
		value, isSet := in.values[def.Name]
		if !isSet {
			mustHave := def.Required && !in.autocomplete && !in.bypass
			if mustHave {
				problems = append(problems, fmt.Errorf("option %q: required", def.Name))
			}
			continue
		}

		leaf, err := buildLeaf(def, value, in)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		built = append(built, leaf)
	}

	for _, name := range slices.Sorted(maps.Keys(in.values)) {
		if known[name] {
			continue
		}
		if !in.bypass {
			problems = append(problems, fmt.Errorf("option %q: not defined by the command", name))
			continue
		}
		leaf, err := buildLeaf(optionDef{Name: name, Type: inferredType(in.values[name])}, in.values[name], in)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		built = append(built, leaf)
	}

	if in.autocomplete {
		problems = append(problems, checkFocused(leaves, in)...)
	}

	return built, problems
}

// checkFocused makes sure an autocomplete targets an option that offers autocomplete.
func checkFocused(leaves []optionDef, in *optionInput) []error {
	for _, def := range leaves {
		if def.Name != in.focused {
			continue
		}
		_, isSet := in.values[def.Name]
		if !isSet {
			return []error{fmt.Errorf("option %q: the focused option needs a value, possibly empty", def.Name)}
		}
		if !def.Autocomplete && !in.bypass {
			return []error{fmt.Errorf("option %q: does not offer autocomplete", def.Name)}
		}

		return nil
	}
	if in.bypass {
		return nil
	}

	return []error{fmt.Errorf("option %q: the focused option is not defined by the command", in.focused)}
}

func buildLeaf(def optionDef, value any, in *optionInput) (map[string]any, error) {
	wireType := def.Type
	wire, err := coerce(def, value, in)
	if err != nil {
		if !in.bypass {
			return nil, err
		}
		// A bypassed value of the wrong kind is sent as the kind it is, so the bot's own
		// checks run instead of its option getters panicking on a mismatched type.
		wireType = inferredType(value)
		wire, err = coerce(optionDef{Name: def.Name, Type: wireType}, value, in)
		if err != nil {
			return nil, err
		}
	}

	leaf := map[string]any{"name": def.Name, "type": wireType, "value": wire}
	if in.autocomplete && def.Name == in.focused {
		leaf["focused"] = true
	}
	in.resolve(wireType, wire)

	return leaf, nil
}

// resolve records the user or channel an option points at, so the bot can resolve it.
func (in *optionInput) resolve(optionType int, wire any) {
	id, isString := wire.(string)
	if !isString {
		return
	}
	if optionType == optionChannel {
		ch, found := in.channelByID(id)
		isInGuild := found && in.guildID != "" && ch.guildID == in.guildID
		if !isInGuild {
			return
		}
		in.resolvedChannels[id] = map[string]any{
			"id":          ch.id,
			"name":        ch.name,
			"type":        ch.channelType,
			"permissions": "0",
		}

		return
	}
	isUser := optionType == optionUser || optionType == optionMentionable
	if !isUser {
		return
	}
	user, found := in.userJSONByID(id)
	if !found {
		return
	}

	in.resolvedUsers[id] = user
	if in.isGuildMember(id) {
		in.resolvedMember[id] = map[string]any{
			"roles":       []string{},
			"joined_at":   "2024-01-01T00:00:00.000000+00:00",
			"permissions": "0",
			"flags":       0,
		}
	}
}

// coerce checks the value against the option definition and returns it as JSON will carry
// it.
func coerce(def optionDef, value any, in *optionInput) (any, error) {
	switch def.Type {
	case optionString:
		return coerceString(def, value)
	case optionInteger:
		return coerceInteger(def, value)
	case optionNumber:
		return coerceNumber(def, value)
	case optionBoolean:
		flag, isBool := value.(bool)
		if !isBool {
			return nil, fmt.Errorf("option %q: must be a boolean", def.Name)
		}
		return flag, nil
	case optionUser, optionChannel, optionRole, optionMentionable:
		return coerceSnowflake(def, value, in)
	default:
		return nil, fmt.Errorf("option %q: type %d is not supported by the fake", def.Name, def.Type)
	}
}

func coerceString(def optionDef, value any) (any, error) {
	text, isString := value.(string)
	if !isString {
		return nil, fmt.Errorf("option %q: must be a string", def.Name)
	}

	var problems []error
	length := utf8.RuneCountInString(text)
	if def.MinLength != nil && length < *def.MinLength {
		problems = append(problems, fmt.Errorf("option %q: shorter than min_length %d", def.Name, *def.MinLength))
	}
	if def.MaxLength != nil && length > *def.MaxLength {
		problems = append(problems, fmt.Errorf("option %q: longer than max_length %d", def.Name, *def.MaxLength))
	}
	problems = append(problems, checkChoice(def, text)...)
	if len(problems) > 0 {
		return nil, problemList(problems)
	}

	return text, nil
}

func coerceInteger(def optionDef, value any) (any, error) {
	number, isNumber := asFloat(value)
	isWhole := isNumber && number == math.Trunc(number) && math.Abs(number) <= maxExactInteger
	if !isWhole {
		return nil, fmt.Errorf("option %q: must be an integer", def.Name)
	}

	whole := int64(number)
	problems := checkRange(def, number)
	problems = append(problems, checkChoice(def, whole)...)
	if len(problems) > 0 {
		return nil, problemList(problems)
	}

	return whole, nil
}

func coerceNumber(def optionDef, value any) (any, error) {
	number, isNumber := asFloat(value)
	if !isNumber || math.IsNaN(number) || math.IsInf(number, 0) {
		return nil, fmt.Errorf("option %q: must be a number", def.Name)
	}

	problems := checkRange(def, number)
	problems = append(problems, checkChoice(def, number)...)
	if len(problems) > 0 {
		return nil, problemList(problems)
	}

	return number, nil
}

func coerceSnowflake(def optionDef, value any, in *optionInput) (any, error) {
	var id string
	switch typed := value.(type) {
	case string:
		id = typed
	case User:
		id = typed.ID
	default:
		return nil, fmt.Errorf("option %q: must be an ID", def.Name)
	}
	if !isSnowflake(id) {
		return nil, fmt.Errorf("option %q: %q is not an ID", def.Name, id)
	}
	if !in.bypass {
		if def.Type == optionUser {
			_, found := in.userJSONByID(id)
			if !found {
				return nil, fmt.Errorf("option %q: unknown user", def.Name)
			}
		}
		if def.Type == optionChannel {
			ch, found := in.channelByID(id)
			isInGuild := found && in.guildID != "" && ch.guildID == in.guildID
			if !isInGuild {
				return nil, fmt.Errorf("option %q: unknown channel", def.Name)
			}
			hasRestrictedTypes := len(def.ChannelTypes) > 0
			if hasRestrictedTypes && !slices.Contains(def.ChannelTypes, ch.channelType) {
				return nil, fmt.Errorf("option %q: channel type %d is not allowed", def.Name, ch.channelType)
			}
		}
	}

	return id, nil
}

func checkRange(def optionDef, number float64) []error {
	var problems []error
	if def.MinValue != nil && number < *def.MinValue {
		problems = append(problems, fmt.Errorf("option %q: below min_value %v", def.Name, *def.MinValue))
	}
	if def.MaxValue != nil && number > *def.MaxValue {
		problems = append(problems, fmt.Errorf("option %q: above max_value %v", def.Name, *def.MaxValue))
	}

	return problems
}

// checkChoice requires the value to be one of the option's fixed choices, when it has any.
func checkChoice(def optionDef, value any) []error {
	if len(def.Choices) == 0 || def.Autocomplete {
		return nil
	}

	encoded, err := json.Marshal(value)
	if err != nil {
		return []error{fmt.Errorf("option %q: not encodable", def.Name)}
	}
	for _, raw := range def.Choices {
		var choice struct {
			Value json.RawMessage `json:"value"`
		}
		if json.Unmarshal(raw, &choice) != nil {
			continue
		}
		if bytes.Equal(bytes.TrimSpace(choice.Value), encoded) {
			return nil
		}
	}

	return []error{fmt.Errorf("option %q: not one of the offered choices", def.Name)}
}

// asFloat converts any Go number to float64.
func asFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int8:
		return float64(typed), true
	case int16:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case uint:
		return float64(typed), true
	case uint8:
		return float64(typed), true
	case uint16:
		return float64(typed), true
	case uint32:
		return float64(typed), true
	case uint64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

// inferredType is the option type a Go value naturally has.
func inferredType(value any) int {
	switch typed := value.(type) {
	case bool:
		return optionBoolean
	case User:
		return optionUser
	case string:
		return optionString
	default:
		number, isNumber := asFloat(typed)
		if isNumber && number == math.Trunc(number) && math.Abs(number) <= maxExactInteger {
			return optionInteger
		}
		if isNumber {
			return optionNumber
		}

		return optionString
	}
}

// nestOptions wraps the leaves in the group and subcommand the path named.
func nestOptions(path parsedPath, leaves []map[string]any) []map[string]any {
	if path.sub == "" {
		return leaves
	}

	nested := []map[string]any{{"name": path.sub, "type": optionSubCommand, "options": leaves}}
	if path.group != "" {
		nested = []map[string]any{{"name": path.group, "type": optionSubCommandGroup, "options": nested}}
	}

	return nested
}
