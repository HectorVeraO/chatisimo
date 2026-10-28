package fakediscord

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

// User is a synthetic Discord user.
type User struct {
	ID     string
	Name   string
	Locale string
}

// Guild is a synthetic guild with one text channel.
type Guild struct {
	ID        string
	Name      string
	ChannelID string
	Locale    string
}

// Message is a message the bot sent.
type Message struct {
	ID         string
	ChannelID  string
	AuthorID   string
	Content    string
	Embeds     []json.RawMessage
	Components []json.RawMessage
	Flags      int
	// EphemeralFor is the only user who can see the message, or empty for a public one.
	EphemeralFor string
	CreatedAt    time.Time
	// EditedAt is zero until the message is edited.
	EditedAt time.Time
	// InteractionID is the interaction the message answers, or empty for a message the bot
	// sent straight to a channel.
	InteractionID string
}

// Modal is a modal the bot opened for a user in answer to an interaction.
type Modal struct {
	UserID     string
	CustomID   string
	Title      string
	Components []json.RawMessage
}

// Command is an application command the bot registered.
type Command struct {
	ID            string
	ApplicationID string
	// GuildID is empty for a global command.
	GuildID string
	Version string
	Name    string
	Type    int
	// Definition is the command as the fake returned it to the bot.
	Definition json.RawMessage
}

type guildState struct {
	Guild
	members []string
}

type channelState struct {
	id          string
	guildID     string
	recipientID string
}

// pendingModal is a modal waiting for its user to submit it.
type pendingModal struct {
	Modal
	// channelID is the channel the modal was opened in.
	channelID string
	// sourceMessageID is the message whose component opened the modal, or empty when a
	// slash command did.
	sourceMessageID string
}

// nonceEntry remembers the message a nonce created.
type nonceEntry struct {
	messageID string
	at        time.Time
}

// world is the fake's state. Callers hold Server.mu.
type world struct {
	users      map[string]User
	guilds     map[string]*guildState
	guildOrder []string
	channels   map[string]*channelState
	dmByUser   map[string]string
	// dmsClosed holds the users whose DMs the bot cannot write to. Users default to open.
	dmsClosed    map[string]bool
	messages     []Message
	records      []Record
	commands     []Command
	modals       map[string]*pendingModal
	interactions map[string]*interaction
	nonces       map[string]nonceEntry
	// globalSet is true once the bot has registered a global command set.
	globalSet  bool
	intents    int
	intentsSet bool
}

func newWorld() *world {
	return &world{
		users:        map[string]User{},
		guilds:       map[string]*guildState{},
		channels:     map[string]*channelState{},
		dmByUser:     map[string]string{},
		dmsClosed:    map[string]bool{},
		modals:       map[string]*pendingModal{},
		interactions: map[string]*interaction{},
		nonces:       map[string]nonceEntry{},
	}
}

// reset clears everything a test arranged or the bot produced, and keeps what belongs to
// the bot's session: its registered commands and the Identify data.
func (w *world) reset() {
	fresh := newWorld()
	fresh.commands = w.commands
	fresh.globalSet = w.globalSet
	fresh.intents = w.intents
	fresh.intentsSet = w.intentsSet
	*w = *fresh
}

// upsertUser stores u, generating an ID when it has none, and returns the stored user.
func (w *world) upsertUser(u User, ids *snowflakes) User {
	if u.ID == "" {
		u.ID = ids.next()
	}
	known, isKnown := w.users[u.ID]
	if u.Name == "" {
		if isKnown {
			u.Name = known.Name
		} else {
			u.Name = "user-" + u.ID
		}
	}
	if u.Locale == "" {
		if isKnown && known.Locale != "" {
			u.Locale = known.Locale
		} else {
			u.Locale = "en-US"
		}
	}
	w.users[u.ID] = u

	return u
}

// upsertGuild creates the guild and its channel, or renames an existing one, and returns it.
// isNew tells the caller whether the Gateway must announce it.
func (w *world) upsertGuild(g Guild, ids *snowflakes) (stored Guild, isNew bool) {
	if g.ID == "" {
		g.ID = ids.next()
	}
	existing, found := w.guilds[g.ID]
	if found {
		if g.Name != "" {
			existing.Name = g.Name
		}
		if g.Locale != "" {
			existing.Locale = g.Locale
		}
		return existing.Guild, false
	}

	if g.Locale == "" {
		g.Locale = "en-US"
	}
	g.ChannelID = ids.next()
	w.guilds[g.ID] = &guildState{Guild: g}
	w.guildOrder = append(w.guildOrder, g.ID)
	w.channels[g.ChannelID] = &channelState{id: g.ChannelID, guildID: g.ID}

	return g, true
}

func (w *world) addMember(guildID string, u User, ids *snowflakes) error {
	guild, found := w.guilds[guildID]
	if !found {
		return fmt.Errorf("add member: unknown guild %q", guildID)
	}

	stored := w.upsertUser(u, ids)
	if !slices.Contains(guild.members, stored.ID) {
		guild.members = append(guild.members, stored.ID)
	}

	return nil
}

// dmChannelFor returns the DM channel between the bot and the user, creating it once.
func (w *world) dmChannelFor(userID string, ids *snowflakes) *channelState {
	channelID, found := w.dmByUser[userID]
	if found {
		return w.channels[channelID]
	}

	channel := &channelState{id: ids.next(), recipientID: userID}
	w.channels[channel.id] = channel
	w.dmByUser[userID] = channel.id

	return channel
}

// audience lists who can see a public message in the channel right now.
func (w *world) audience(channel *channelState) []string {
	if channel.guildID == "" {
		return []string{channel.recipientID}
	}

	return slices.Clone(w.guilds[channel.guildID].members)
}

// canSee reports whether the user belongs to the channel's audience.
func (w *world) canSee(userID string, channel *channelState) bool {
	if channel.guildID == "" {
		return channel.recipientID == userID
	}

	return slices.Contains(w.guilds[channel.guildID].members, userID)
}

// isMessageVisible reports whether the user can see the message right now.
func (w *world) isMessageVisible(userID string, m Message) bool {
	channel, found := w.channels[m.ChannelID]
	if !found || !w.canSee(userID, channel) {
		return false
	}

	return m.EphemeralFor == "" || m.EphemeralFor == userID
}

// visibleMessages returns the messages of a channel that the user can see.
func (w *world) visibleMessages(userID, channelID string) []Message {
	var visible []Message
	for _, msg := range w.messages {
		isInChannel := msg.ChannelID == channelID
		if isInChannel && w.isMessageVisible(userID, msg) {
			visible = append(visible, cloneMessage(msg))
		}
	}

	return visible
}

// visibleTo lists who can see the message right now: its one user when ephemeral, else
// the whole channel.
func (w *world) visibleTo(m Message) []string {
	if m.EphemeralFor != "" {
		return []string{m.EphemeralFor}
	}
	channel, found := w.channels[m.ChannelID]
	if !found {
		return []string{}
	}

	return w.audience(channel)
}

// messageIndex returns the position of the message in the world, or -1.
func (w *world) messageIndex(id string) int {
	return slices.IndexFunc(w.messages, func(m Message) bool {
		return m.ID == id
	})
}

func cloneMessage(m Message) Message {
	m.Embeds = slices.Clone(m.Embeds)
	m.Components = slices.Clone(m.Components)

	return m
}

func cloneRecord(r Record) Record {
	r.VisibleTo = slices.Clone(r.VisibleTo)
	r.Embeds = slices.Clone(r.Embeds)
	r.Components = slices.Clone(r.Components)
	r.MentionsRendered = slices.Clone(r.MentionsRendered)
	r.Pings = slices.Clone(r.Pings)

	return r
}
