package fakediscord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"unicode/utf8"
)

const (
	componentActionRow  = 1
	componentButton     = 2
	componentStringMenu = 3
	componentTextInput  = 4
	componentLabel      = 18

	// maxComponentDepth bounds the walk over a message's component tree.
	maxComponentDepth = 8
	// maxTextInputLength is Discord's limit on a text input's value.
	maxTextInputLength = 4000
)

// Slash describes a slash command invocation.
type Slash struct {
	// Guild is where the command is invoked, or nil for the bot's DM with the user.
	Guild *Guild
	// Path names the command and its subcommands, like "/ge/join".
	Path string
	// Options are the option values by name: strings, whole numbers, floats, bools, or
	// user IDs (a User also works) for user options.
	Options map[string]any
	// BypassValidation sends the values as given even when the registered definition would
	// refuse them, to exercise the bot's own checks.
	BypassValidation bool
}

// ErrUnknownUser is returned for a user the fake does not know.
var ErrUnknownUser = errors.New("fakediscord: unknown user")

// ErrUnknownGuild is returned for a guild the fake does not know.
var ErrUnknownGuild = errors.New("fakediscord: unknown guild")

// reject wraps problems in ErrRejected, or returns nil when there are none.
func reject(problems []error) error {
	if len(problems) == 0 {
		return nil
	}

	return fmt.Errorf("%w: %w", ErrRejected, errors.Join(problems...))
}

// SetDMsOpen sets whether the bot can write to the user's DMs. A closed DM answers the
// bot's message with 403 and code 50007. It returns ErrUnknownUser for a user the fake does
// not know.
func (s *Server) SetDMsOpen(u User, open bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, found := s.w.users[u.ID]
	if !found {
		return fmt.Errorf("set DMs open for %q: %w", u.ID, ErrUnknownUser)
	}
	if open {
		delete(s.w.dmsClosed, u.ID)
	} else {
		s.w.dmsClosed[u.ID] = true
	}
	s.publishWorldChangedLocked()

	return nil
}

// SetUserLocale sets u's Discord locale tag (e.g. "en-US", "es-419"). It returns
// ErrUnknownUser for a user the fake does not know.
func (s *Server) SetUserLocale(u User, locale string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, found := s.w.users[u.ID]
	if !found {
		return fmt.Errorf("set locale for user %q: %w", u.ID, ErrUnknownUser)
	}
	if locale == "" {
		locale = "en-US"
	}
	existing.Locale = locale
	s.w.users[u.ID] = existing
	s.publishWorldChangedLocked()

	return nil
}

// SetGuildLocale sets g's preferred Discord locale tag (e.g. "en-US", "es-419"). It returns
// ErrUnknownGuild for a guild the fake does not know.
func (s *Server) SetGuildLocale(g Guild, locale string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, found := s.w.guilds[g.ID]
	if !found {
		return fmt.Errorf("set locale for guild %q: %w", g.ID, ErrUnknownGuild)
	}
	if locale == "" {
		locale = "en-US"
	}
	existing.Locale = locale
	s.publishWorldChangedLocked()

	return nil
}

// Slash sends a slash command interaction as the user and waits for the bot's first callback
// or the deadline. A Result with StatusTimeout means the deadline passed.
func (s *Server) Slash(ctx context.Context, as User, in Slash) (Result, error) {
	return s.act(ctx, func() (*plan, error) {
		return s.planSlash(as, in, kindSlash, "")
	})
}

// Autocomplete sends an autocomplete interaction for the focused option, whose partial text
// is in.Options[focused], and returns the choices the bot answered with.
func (s *Server) Autocomplete(ctx context.Context, as User, in Slash, focused string) ([]Choice, error) {
	result, err := s.act(ctx, func() (*plan, error) {
		return s.planSlash(as, in, kindAutocomplete, focused)
	})
	if err != nil {
		return nil, err
	}
	if result.Status == StatusTimeout {
		return nil, ErrNoCallback
	}

	return result.Choices, nil
}

// Click presses a button of the message as the user. It refuses a custom ID the message does
// not carry; ClickForged sends it anyway.
func (s *Server) Click(ctx context.Context, as User, msg Message, customID string) (Result, error) {
	return s.act(ctx, func() (*plan, error) {
		return s.planComponent(as, msg, customID, kindButton, nil, false)
	})
}

// ClickForged presses a button without checking that the message carries it.
func (s *Server) ClickForged(ctx context.Context, as User, msg Message, customID string) (Result, error) {
	return s.act(ctx, func() (*plan, error) {
		return s.planComponent(as, msg, customID, kindButton, nil, true)
	})
}

// Select picks values in a string select menu of the message as the user. It refuses a menu
// or values the message does not offer; SelectForged sends them anyway.
func (s *Server) Select(ctx context.Context, as User, msg Message, customID string, values ...string) (Result, error) {
	return s.act(ctx, func() (*plan, error) {
		return s.planComponent(as, msg, customID, kindSelect, values, false)
	})
}

// SelectForged picks values without checking that the message offers them.
func (s *Server) SelectForged(ctx context.Context, as User, msg Message, customID string, values ...string) (Result, error) {
	return s.act(ctx, func() (*plan, error) {
		return s.planComponent(as, msg, customID, kindSelect, values, true)
	})
}

// SubmitModal submits the modal the bot opened for the user. fields maps each text input's
// custom ID to its value. It refuses a modal that is not the pending one and fields the
// modal does not allow; SubmitModalForged sends them anyway.
func (s *Server) SubmitModal(ctx context.Context, as User, m Modal, fields map[string]string) (Result, error) {
	return s.act(ctx, func() (*plan, error) {
		return s.planModal(as, m, fields, false)
	})
}

// SubmitModalForged submits a modal without checking it against the pending one.
func (s *Server) SubmitModalForged(ctx context.Context, as User, m Modal, fields map[string]string) (Result, error) {
	return s.act(ctx, func() (*plan, error) {
		return s.planModal(as, m, fields, true)
	})
}

// WaitDM returns the first message in the user's DM channel that match accepts, whether it
// already exists or arrives later.
func (s *Server) WaitDM(ctx context.Context, u User, match func(Message) bool) (Message, error) {
	if match == nil {
		return Message{}, errors.New("fakediscord: wait for a DM: nil match function")
	}

	for {
		s.mu.Lock()
		isClosed := s.closed
		changed := s.changed
		messages := s.dmMessagesLocked(u.ID)
		s.mu.Unlock()

		if isClosed {
			return Message{}, ErrClosed
		}
		for _, message := range messages {
			if match(message) {
				return message, nil
			}
		}

		select {
		case <-ctx.Done():
			return Message{}, fmt.Errorf("wait for a DM: %w", ctx.Err())
		case <-changed:
		}
	}
}

func (s *Server) dmMessagesLocked(userID string) []Message {
	channelID, found := s.w.dmByUser[userID]
	if !found {
		return nil
	}

	return s.w.visibleMessages(userID, channelID)
}

func (s *Server) newUserOptionInput(in Slash, kind, focused string, guildID string) *optionInput {
	return &optionInput{
		values:       in.Options,
		bypass:       in.BypassValidation,
		autocomplete: kind == kindAutocomplete,
		focused:      focused,
		userByID: func(id string) (User, bool) {
			user, found := s.w.users[id]
			return user, found
		},
		isGuildMember: func(id string) bool {
			guild, found := s.w.guilds[guildID]
			return found && slices.Contains(guild.members, id)
		},
		resolvedUsers:  map[string]userJSON{},
		resolvedMember: map[string]map[string]any{},
	}
}

// planSlash validates a slash or autocomplete invocation and builds its interaction.
func (s *Server) planSlash(as User, in Slash, kind, focused string) (*plan, error) {
	actor, found := s.w.users[as.ID]
	if !found {
		return nil, reject([]error{fmt.Errorf("user %q is not known to the fake", as.ID)})
	}

	var problems []error
	guildID, guildErr := s.invocationGuildLocked(actor, in.Guild)
	if guildErr != nil {
		problems = append(problems, guildErr)
	}
	path, pathErr := resolvePath(s.w.commands, guildID, in.Path)
	if pathErr != nil {
		problems = append(problems, pathErr)
	}
	if len(problems) > 0 {
		return nil, reject(problems)
	}

	input := s.newUserOptionInput(in, kind, focused, guildID)
	leaves, optionProblems := buildOptions(path.leaves, input)
	if len(optionProblems) > 0 {
		return nil, reject(optionProblems)
	}

	p := &plan{kind: kind, actorID: actor.ID}
	p.data = templateData{
		User:        userToJSON(actor),
		Bot:         s.botUser(),
		Locale:      userLocaleOf(actor),
		GuildLocale: s.guildLocaleLocked(guildID),
		CommandID:   path.command.ID,
		CommandName: path.command.Name,
		Options:     nestOptions(path, leaves),
	}
	if len(input.resolvedUsers) > 0 {
		resolved := map[string]any{"users": input.resolvedUsers}
		if len(input.resolvedMember) > 0 {
			resolved["members"] = input.resolvedMember
		}
		p.data.Resolved = resolved
	}

	s.placeInChannelLocked(p, guildID, actor.ID)
	isAutocomplete := kind == kindAutocomplete
	switch {
	case guildID != "" && isAutocomplete:
		p.template = tplAutocompleteGuild
	case isAutocomplete:
		p.template = tplAutocompleteDM
	case guildID != "":
		p.template = tplSlashGuild
	default:
		p.template = tplSlashDM
	}

	return p, nil
}

// invocationGuildLocked checks that the user can invoke a command in the guild and returns
// its ID, or empty for a DM.
func (s *Server) invocationGuildLocked(actor User, guild *Guild) (string, error) {
	if guild == nil {
		return "", nil
	}

	stored, found := s.w.guilds[guild.ID]
	if !found {
		return "", fmt.Errorf("guild %q is not known to the fake", guild.ID)
	}
	if !slices.Contains(stored.members, actor.ID) {
		return "", fmt.Errorf("user %q is not a member of guild %q", actor.ID, guild.ID)
	}

	return stored.ID, nil
}

func userLocaleOf(u User) string {
	if u.Locale == "" {
		return "en-US"
	}
	return u.Locale
}

func (s *Server) guildLocaleLocked(guildID string) string {
	if guildID == "" {
		return ""
	}
	guild, found := s.w.guilds[guildID]
	if !found || guild.Locale == "" {
		return "en-US"
	}
	return guild.Locale
}

// placeInChannelLocked sets the plan's channel and the guild fields of its template data: the
// guild's channel, or the user's DM channel with the bot.
func (s *Server) placeInChannelLocked(p *plan, guildID, actorID string) {
	if guildID == "" {
		channel := s.w.dmChannelFor(actorID, s.ids)
		p.channelID = channel.id
		p.data.ChannelID = channel.id

		return
	}

	guild := s.w.guilds[guildID]
	p.channelID = guild.ChannelID
	p.data.ChannelID = guild.ChannelID
	p.data.GuildID = guild.ID
	p.data.GuildLocale = s.guildLocaleLocked(guild.ID)
}

// componentInfo is the part of a message or modal component the fake reads.
type componentInfo struct {
	Type       int               `json:"type"`
	CustomID   string            `json:"custom_id"`
	Label      string            `json:"label"`
	Disabled   bool              `json:"disabled"`
	Required   *bool             `json:"required"`
	MinLength  *int              `json:"min_length"`
	MaxLength  *int              `json:"max_length"`
	MinValues  *int              `json:"min_values"`
	MaxValues  *int              `json:"max_values"`
	Options    []menuOption      `json:"options"`
	Components []json.RawMessage `json:"components"`
	Component  json.RawMessage   `json:"component"`
}

type menuOption struct {
	Value string `json:"value"`
}

// walkComponents calls visit for every component in the tree, to a bounded depth.
func walkComponents(raws []json.RawMessage, depth int, visit func(componentInfo)) {
	if depth > maxComponentDepth {
		return
	}

	for _, raw := range raws {
		var info componentInfo
		err := json.Unmarshal(raw, &info)
		if err != nil {
			continue
		}
		visit(info)
		walkComponents(info.Components, depth+1, visit)
		if len(info.Component) > 0 {
			walkComponents([]json.RawMessage{info.Component}, depth+1, visit)
		}
	}
}

// findComponent returns the component with the custom ID and type.
func findComponent(raws []json.RawMessage, customID string, componentType int) (componentInfo, bool) {
	var match componentInfo
	var isFound bool
	walkComponents(raws, 0, func(info componentInfo) {
		isMatch := !isFound && info.CustomID == customID && info.Type == componentType
		if isMatch {
			match = info
			isFound = true
		}
	})

	return match, isFound
}

// planComponent validates a button click or menu selection and builds its interaction.
func (s *Server) planComponent(as User, msg Message, customID, kind string, values []string, forged bool) (*plan, error) {
	actor, found := s.w.users[as.ID]
	if !found {
		return nil, reject([]error{fmt.Errorf("user %q is not known to the fake", as.ID)})
	}

	target := msg
	index := s.w.messageIndex(msg.ID)
	if index >= 0 {
		target = s.w.messages[index]
	}
	channel, hasChannel := s.w.channels[target.ChannelID]
	if !hasChannel {
		return nil, reject([]error{fmt.Errorf("message %q is in no channel the fake knows", msg.ID)})
	}

	if !forged {
		problems := s.checkComponentLocked(actor, target, index >= 0, customID, kind, values)
		if len(problems) > 0 {
			return nil, reject(problems)
		}
	}

	p := &plan{kind: kind, actorID: actor.ID, channelID: channel.id, sourceMessageID: target.ID}
	p.data = templateData{
		User:        userToJSON(actor),
		Bot:         s.botUser(),
		Locale:      userLocaleOf(actor),
		ChannelID:   channel.id,
		GuildID:     channel.guildID,
		GuildLocale: s.guildLocaleLocked(channel.guildID),
		Message:     messageToJSON(cloneMessage(target), s.botUser()),
		CustomID:    customID,
		Values:      nonNilStrings(slices.Clone(values)),
	}
	isGuild := channel.guildID != ""
	switch {
	case kind == kindButton && isGuild:
		p.template = tplButtonGuild
	case kind == kindButton:
		p.template = tplButtonDM
	case isGuild:
		p.template = tplSelectGuild
	default:
		p.template = tplSelectDM
	}

	return p, nil
}

// checkComponentLocked lists every reason a Discord client could not have sent the click.
func (s *Server) checkComponentLocked(actor User, target Message, isStored bool, customID, kind string, values []string) []error {
	if !isStored {
		return []error{fmt.Errorf("message %q does not exist", target.ID)}
	}
	if !s.w.isMessageVisible(actor.ID, target) {
		return []error{fmt.Errorf("user %q cannot see message %q", actor.ID, target.ID)}
	}

	componentType := componentButton
	if kind == kindSelect {
		componentType = componentStringMenu
	}
	component, carried := findComponent(target.Components, customID, componentType)
	if !carried {
		return []error{fmt.Errorf("message %q carries no %s with custom id %q", target.ID, kind, customID)}
	}

	var problems []error
	if component.Disabled {
		problems = append(problems, fmt.Errorf("%s %q is disabled", kind, customID))
	}
	if kind == kindSelect {
		problems = append(problems, checkMenuValues(component, values)...)
	}

	return problems
}

func checkMenuValues(menu componentInfo, values []string) []error {
	minValues, maxValues := 1, 1
	if menu.MinValues != nil {
		minValues = *menu.MinValues
	}
	if menu.MaxValues != nil {
		maxValues = *menu.MaxValues
	}

	var problems []error
	if len(values) < minValues || len(values) > maxValues {
		problems = append(problems, fmt.Errorf("menu %q takes %d to %d values, got %d", menu.CustomID, minValues, maxValues, len(values)))
	}
	offered := make([]string, 0, len(menu.Options))
	for _, option := range menu.Options {
		offered = append(offered, option.Value)
	}
	for _, value := range values {
		if !slices.Contains(offered, value) {
			problems = append(problems, fmt.Errorf("menu %q does not offer value %q", menu.CustomID, value))
		}
	}

	return problems
}

// planModal validates a modal submission and builds its interaction.
func (s *Server) planModal(as User, m Modal, fields map[string]string, forged bool) (*plan, error) {
	actor, found := s.w.users[as.ID]
	if !found {
		return nil, reject([]error{fmt.Errorf("user %q is not known to the fake", as.ID)})
	}

	pending := s.w.modals[actor.ID]
	definition := m
	if pending != nil {
		definition = pending.Modal
	}
	if !forged {
		problems := checkModal(pending, m, fields)
		if len(problems) > 0 {
			return nil, reject(problems)
		}
	}

	p := &plan{kind: kindModal, actorID: actor.ID}
	p.data = templateData{
		User:            userToJSON(actor),
		Bot:             s.botUser(),
		Locale:          userLocaleOf(actor),
		CustomID:        m.CustomID,
		ModalComponents: modalSubmitComponents(definition, fields),
	}
	if pending != nil {
		p.sourceMessageID = pending.sourceMessageID
		s.placeModalLocked(p, pending)
	} else {
		s.placeInChannelLocked(p, "", actor.ID)
		p.template = tplModalDM
	}

	return p, nil
}

// placeModalLocked puts the submission in the channel the modal was opened in.
func (s *Server) placeModalLocked(p *plan, pending *pendingModal) {
	p.consumeModalFor = pending.UserID
	channelID := pending.channelID
	channel, found := s.w.channels[channelID]
	if !found {
		s.placeInChannelLocked(p, "", pending.UserID)
		p.template = tplModalDM

		return
	}

	p.channelID = channel.id
	p.data.ChannelID = channel.id
	p.data.GuildID = channel.guildID
	p.data.GuildLocale = s.guildLocaleLocked(channel.guildID)
	p.template = tplModalDM
	if channel.guildID != "" {
		p.template = tplModalGuild
	}
	if p.sourceMessageID != "" {
		index := s.w.messageIndex(p.sourceMessageID)
		if index >= 0 {
			p.data.Message = messageToJSON(cloneMessage(s.w.messages[index]), s.botUser())
		}
	}
}

// checkModal lists every reason a Discord client could not have sent the submission.
func checkModal(pending *pendingModal, m Modal, fields map[string]string) []error {
	if pending == nil {
		return []error{errors.New("user has no modal open")}
	}
	if pending.CustomID != m.CustomID {
		return []error{fmt.Errorf("the open modal is %q, not %q", pending.CustomID, m.CustomID)}
	}

	var problems []error
	inputs := modalInputs(pending.Modal)
	byID := map[string]componentInfo{}
	for _, input := range inputs {
		byID[input.CustomID] = input
	}
	for _, name := range slices.Sorted(maps.Keys(fields)) {
		_, isInput := byID[name]
		if !isInput {
			problems = append(problems, fmt.Errorf("modal %q has no text input %q", m.CustomID, name))
		}
	}
	for _, input := range inputs {
		value, isGiven := fields[input.CustomID]
		isRequired := input.Required == nil || *input.Required
		if isRequired && (!isGiven || value == "") {
			problems = append(problems, fmt.Errorf("text input %q: required", input.CustomID))
			continue
		}
		problems = append(problems, checkTextLength(input, value)...)
	}

	return problems
}

func checkTextLength(input componentInfo, value string) []error {
	var problems []error
	length := utf8.RuneCountInString(value)
	maxLength := maxTextInputLength
	if input.MaxLength != nil {
		maxLength = *input.MaxLength
	}
	if length > maxLength {
		problems = append(problems, fmt.Errorf("text input %q: longer than max_length %d", input.CustomID, maxLength))
	}
	if input.MinLength != nil && length < *input.MinLength && length > 0 {
		problems = append(problems, fmt.Errorf("text input %q: shorter than min_length %d", input.CustomID, *input.MinLength))
	}

	return problems
}

// modalInputs lists the text inputs of a modal in order, with each input's label resolved
// from its own label field or an enclosing Label component.
func modalInputs(m Modal) []componentInfo {
	var inputs []componentInfo
	walkModalInputs(m.Components, "", 0, func(info componentInfo) {
		inputs = append(inputs, info)
	})

	return inputs
}

func walkModalInputs(raws []json.RawMessage, parentLabel string, depth int, visit func(componentInfo)) {
	if depth > maxComponentDepth {
		return
	}

	for _, raw := range raws {
		var info componentInfo
		err := json.Unmarshal(raw, &info)
		if err != nil {
			continue
		}
		if info.Type == componentTextInput {
			if info.Label == "" {
				info.Label = parentLabel
			}
			visit(info)
		}
		innerLabel := parentLabel
		if info.Type == componentLabel {
			innerLabel = info.Label
		}
		walkModalInputs(info.Components, innerLabel, depth+1, visit)
		if len(info.Component) > 0 {
			walkModalInputs([]json.RawMessage{info.Component}, innerLabel, depth+1, visit)
		}
	}
}

// modalSubmitComponents builds the components of a modal submission: one action row per
// field, the modal's inputs first and then any extra field, which only a forged submission
// has.
func modalSubmitComponents(m Modal, fields map[string]string) []map[string]any {
	rows := []map[string]any{}
	seen := map[string]bool{}
	add := func(customID, value string) {
		rows = append(rows, map[string]any{
			"type": componentActionRow,
			"components": []map[string]any{
				{"type": componentTextInput, "custom_id": customID, "value": value},
			},
		})
	}

	for _, input := range modalInputs(m) {
		value, isGiven := fields[input.CustomID]
		if isGiven {
			add(input.CustomID, value)
			seen[input.CustomID] = true
		}
	}
	for _, name := range slices.Sorted(maps.Keys(fields)) {
		if !seen[name] {
			add(name, fields[name])
		}
	}

	return rows
}
