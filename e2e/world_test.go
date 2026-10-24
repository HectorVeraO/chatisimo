//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
)

const (
	waitDeadline = 30 * time.Second
	pollInterval = 25 * time.Millisecond

	statusTimeout = "timeout"
)

var personNames = []string{"Alice", "Bruno", "Carla", "Diego", "Elena", "Felipe"}

// uniqueCounter makes the guild and exchange names of parallel tests distinct.
var uniqueCounter atomic.Int64

// secretEntry is a value that must never reach the binary's logs.
type secretEntry struct {
	label string
	value string
	// partner, when set, makes the entry a pair: it matches only a log line holding both values.
	partner string
}

// registry remembers every snowflake, name and text the tests create, for the log audit.
type registry struct {
	mu      sync.Mutex
	entries []secretEntry
}

// add records a value. An empty value is ignored: it would match every log.
func (r *registry) add(label, value string) {
	if value == "" {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.entries = append(r.entries, secretEntry{label: label, value: value})
}

// addPair records two values that must never appear together on one log line, such as the
// giver and the recipient of a draw.
func (r *registry) addPair(label, value, partner string) {
	if value == "" || partner == "" {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.entries = append(r.entries, secretEntry{label: label, value: value, partner: partner})
}

// findIn returns the label of every registered value that text contains.
func (r *registry) findIn(text string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	var found []string
	for _, entry := range r.entries {
		if entry.matches(text) {
			found = append(found, entry.label)
		}
	}

	return found
}

func (e secretEntry) matches(text string) bool {
	if e.partner == "" {
		return strings.Contains(text, e.value)
	}

	for line := range strings.SplitSeq(text, "\n") {
		if strings.Contains(line, e.value) && strings.Contains(line, e.partner) {
			return true
		}
	}

	return false
}

// created holds what every test of the run created.
var created registry

// waitFor polls condition until it holds or the deadline passes.
func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), waitDeadline)
	defer cancel()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		if condition() {
			return
		}

		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %s", what)
		case <-ticker.C:
		}
	}
}

// scene is one guild with its own users on the shared fake.
type scene struct {
	fake    *fakediscord.Server
	guild   fakediscord.Guild
	members []fakediscord.User
	// serial distinguishes the names this scene creates.
	serial int64
}

// newGuild creates a guild with participants users named Alice, Bruno, and so on. The first
// one organizes in most tests.
func newGuild(t *testing.T, participants int) *scene {
	t.Helper()

	return newGuildOn(t, shared.fake, participants)
}

func newGuildOn(t *testing.T, fake *fakediscord.Server, participants int) *scene {
	t.Helper()

	if participants < 1 || participants > len(personNames) {
		t.Fatalf("a guild has 1 to %d participants, got %d", len(personNames), participants)
	}

	serial := uniqueCounter.Add(1)
	members := make([]fakediscord.User, 0, participants)
	for _, name := range personNames[:participants] {
		user := fake.AddUser(fakediscord.User{Name: name})
		created.add("user snowflake", user.ID)
		members = append(members, user)
	}

	guildName := fmt.Sprintf("Hearth %d", serial)
	guild := fake.AddGuild(fakediscord.Guild{Name: guildName}, members...)
	created.add("guild snowflake", guild.ID)
	created.add("guild name", guildName)

	return &scene{fake: fake, guild: guild, members: members, serial: serial}
}

// exchangeName returns a name no other exchange of the run has, and registers it.
func (s *scene) exchangeName(base string) string {
	name := fmt.Sprintf("%s %d.%d", base, s.serial, uniqueCounter.Add(1))
	created.add("exchange name", name)

	return name
}

// reply is what the bot answered to an interaction, as the caller sees it.
type reply struct {
	message fakediscord.Message
	// text is the content and the embed titles and descriptions, joined by newlines.
	text string
}

type embedText struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

func textOf(t *testing.T, message fakediscord.Message) string {
	t.Helper()

	parts := []string{message.Content}
	for _, raw := range message.Embeds {
		var embed embedText
		err := json.Unmarshal(raw, &embed)
		if err != nil {
			t.Fatalf("decode embed: %v", err)
		}
		parts = append(parts, embed.Title, embed.Description)
	}

	return strings.Join(parts, "\n")
}

// replyTo returns the answer to result: the message of the callback, or the message the
// deferred callback was edited into.
func (s *scene) replyTo(t *testing.T, as fakediscord.User, result fakediscord.Result) reply {
	t.Helper()

	if result.Status == statusTimeout {
		t.Fatalf("the bot did not answer interaction %s in time", result.InteractionID)
	}

	if result.Message != nil {
		return reply{message: *result.Message, text: textOf(t, *result.Message)}
	}

	var record fakediscord.Record
	waitFor(t, "the edit of the deferred reply", func() bool {
		for _, candidate := range s.fake.Outbound() {
			isEdit := candidate.Source == "edit" && candidate.InteractionID != nil &&
				*candidate.InteractionID == result.InteractionID
			if isEdit {
				record = candidate

				return true
			}
		}

		return false
	})

	var found fakediscord.Message
	waitFor(t, "the edited reply to be visible", func() bool {
		for _, message := range s.fake.Visible(as, record.ChannelID) {
			if message.ID == record.MessageID {
				found = message

				return true
			}
		}

		return false
	})

	return reply{message: found, text: textOf(t, found)}
}

// slash runs a slash command in the guild as a user and returns the bot's answer.
func (s *scene) slash(t *testing.T, as fakediscord.User, path string, options map[string]any) reply {
	t.Helper()

	guild := s.guild
	result, err := s.fake.Slash(t.Context(), as, fakediscord.Slash{Guild: &guild, Path: path, Options: options})
	if err != nil {
		t.Fatalf("slash %s: %v", path, err)
	}

	return s.replyTo(t, as, result)
}

// click presses a button of a message as a user and returns the bot's answer.
func (s *scene) click(t *testing.T, as fakediscord.User, message fakediscord.Message, customID string) reply {
	t.Helper()

	result, err := s.fake.Click(t.Context(), as, message, customID)
	if err != nil {
		t.Fatalf("click %s: %v", customID, err)
	}

	return s.replyTo(t, as, result)
}

// complete asks for the autocomplete choices of the exchange option of a verb. A nil guild
// asks in the bot's DM.
func (s *scene) complete(
	t *testing.T, as fakediscord.User, guild *fakediscord.Guild, verb, typed string,
) []fakediscord.Choice {
	t.Helper()

	in := fakediscord.Slash{Guild: guild, Path: "/ge/" + verb, Options: map[string]any{"exchange": typed}}
	choices, err := s.fake.Autocomplete(t.Context(), as, in, "exchange")
	if err != nil {
		t.Fatalf("autocomplete %s: %v", verb, err)
	}

	return choices
}

func choiceNames(choices []fakediscord.Choice) []string {
	names := make([]string, 0, len(choices))
	for _, choice := range choices {
		names = append(names, choice.Name)
	}

	return names
}

type buttonRow struct {
	Components []struct {
		CustomID string `json:"custom_id"`
	} `json:"components"`
}

// buttonIDs lists the custom IDs of the buttons of a message, in order.
func buttonIDs(t *testing.T, message fakediscord.Message) []string {
	t.Helper()

	var ids []string
	for _, raw := range message.Components {
		var row buttonRow
		err := json.Unmarshal(raw, &row)
		if err != nil {
			t.Fatalf("decode component: %v", err)
		}
		for _, component := range row.Components {
			ids = append(ids, component.CustomID)
		}
	}

	return ids
}

// card is the public message of an exchange.
type card struct {
	message fakediscord.Message
	id      string
}

const joinPrefix = "/gx/join/"

// createExchange has the organizer create an exchange and returns its public card as seen by
// the other members, together with the organizer's own reply.
func (s *scene) createExchange(t *testing.T, organizer fakediscord.User, name string) (card, reply) {
	t.Helper()

	answer := s.slash(t, organizer, "/ge/create", map[string]any{"name": name})

	var found fakediscord.Message
	waitFor(t, "the public card", func() bool {
		for _, message := range s.fake.Visible(organizer, s.guild.ChannelID) {
			isCard := message.EphemeralFor == "" && strings.Contains(textOf(t, message), name)
			if isCard {
				found = message

				return true
			}
		}

		return false
	})

	var id string
	for _, customID := range buttonIDs(t, found) {
		if strings.HasPrefix(customID, joinPrefix) {
			id = strings.TrimPrefix(customID, joinPrefix)
		}
	}
	if id == "" {
		t.Fatalf("the card of %q has no join button: %v", name, buttonIDs(t, found))
	}

	return card{message: found, id: id}, answer
}

// join has a member join an exchange by slash command.
func (s *scene) join(t *testing.T, as fakediscord.User, exchangeID string) reply {
	t.Helper()

	return s.slash(t, as, "/ge/join", map[string]any{"exchange": exchangeID})
}
