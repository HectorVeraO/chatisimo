package fakediscord

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

// User is a synthetic Discord user.
type User struct {
	ID   string
	Name string
}

// Guild is a synthetic guild with one text channel.
type Guild struct {
	ID        string
	Name      string
	ChannelID string
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

// world is the fake's state. Callers hold Server.mu.
type world struct {
	users      map[string]User
	guilds     map[string]*guildState
	guildOrder []string
	channels   map[string]*channelState
	dmByUser   map[string]string
	messages   []Message
	records    []Record
	commands   []Command
	// globalSet is true once the bot has registered a global command set.
	globalSet  bool
	intents    int
	intentsSet bool
}

func newWorld() *world {
	return &world{
		users:    map[string]User{},
		guilds:   map[string]*guildState{},
		channels: map[string]*channelState{},
		dmByUser: map[string]string{},
	}
}

// upsertUser stores u, generating an ID when it has none, and returns the stored user.
func (w *world) upsertUser(u User, ids *snowflakes) User {
	if u.ID == "" {
		u.ID = ids.next()
	}
	if u.Name == "" {
		known, isKnown := w.users[u.ID]
		if isKnown {
			u.Name = known.Name
		} else {
			u.Name = "user-" + u.ID
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
		return existing.Guild, false
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

// visibleMessages returns the messages of a channel that the user can see.
func (w *world) visibleMessages(userID, channelID string) []Message {
	channel, found := w.channels[channelID]
	if !found || !w.canSee(userID, channel) {
		return nil
	}

	var visible []Message
	for _, msg := range w.messages {
		isInChannel := msg.ChannelID == channelID
		isForUser := msg.EphemeralFor == "" || msg.EphemeralFor == userID
		if isInChannel && isForUser {
			visible = append(visible, cloneMessage(msg))
		}
	}

	return visible
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
