//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
)

const (
	textAlreadyIn = "You are already in this exchange."
	textNotFound  = "That exchange does not exist here."
	textChoose    = "You are in several exchanges."
	textJoined    = "You joined"
	textLeft      = "You left"
	textForbidden = "You are not allowed to do that in this exchange."

	titleRecipient = "Your recipient"
)

// commandOption is the part of a registered option the trees are compared on.
type commandOption struct {
	Type         int             `json:"type"`
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	Required     bool            `json:"required"`
	Autocomplete bool            `json:"autocomplete"`
	MaxLength    *int            `json:"max_length"`
	Options      []commandOption `json:"options"`
}

type commandTree struct {
	commandOption
	Contexts []int `json:"contexts"`
}

func registeredTree(t *testing.T, name string) commandTree {
	t.Helper()

	for _, command := range shared.fake.Commands() {
		if command.Name != name || command.GuildID != "" {
			continue
		}

		var tree commandTree
		err := json.Unmarshal(command.Definition, &tree)
		if err != nil {
			t.Fatalf("decode /%s: %v", name, err)
		}

		return tree
	}

	t.Fatalf("command /%s is not registered", name)

	return commandTree{}
}

func optionNames(options []commandOption) []string {
	names := make([]string, 0, len(options))
	for _, option := range options {
		names = append(names, option.Name)
	}

	return names
}

func findOption(t *testing.T, options []commandOption, name string) commandOption {
	t.Helper()

	for _, option := range options {
		if option.Name == name {
			return option
		}
	}
	t.Fatalf("no option %q among %v", name, optionNames(options))

	return commandOption{}
}

func TestCommandTrees(t *testing.T) {
	t.Parallel()

	short := registeredTree(t, "ge")
	long := registeredTree(t, "chatisimo")

	if len(long.Options) != 1 || long.Options[0].Name != "giftexchange" {
		t.Fatalf("/chatisimo options = %v, want the giftexchange group", optionNames(long.Options))
	}
	grouped := long.Options[0].Options

	if !reflect.DeepEqual(short.Options, grouped) {
		t.Errorf("/ge and /chatisimo giftexchange differ:\n%+v\n%+v", short.Options, grouped)
	}

	wantVerbs := []string{
		"create", "list", "join", "leave", "remove", "rules", "exclude", "unexclude", "rename",
		"draw", "recipient", "giver", "ask", "questions", "reveal", "cancel", "help",
	}
	if got := optionNames(short.Options); !slices.Equal(got, wantVerbs) {
		t.Errorf("verbs = %v, want %v", got, wantVerbs)
	}

	wantContexts := []int{0, 1}
	for name, tree := range map[string]commandTree{"ge": short, "chatisimo": long} {
		if !slices.Equal(tree.Contexts, wantContexts) {
			t.Errorf("/%s contexts = %v, want guild and bot DM %v", name, tree.Contexts, wantContexts)
		}
	}

	create := findOption(t, short.Options, "create")
	if got, want := optionNames(create.Options), []string{"name", "draw-at", "reveal-at"}; !slices.Equal(got, want) {
		t.Errorf("create options = %v, want %v", got, want)
	}
	name := findOption(t, create.Options, "name")
	if name.MaxLength == nil || *name.MaxLength != 100 || !name.Required {
		t.Errorf("create name = %+v, want required with max_length 100", name)
	}

	exchange := findOption(t, findOption(t, short.Options, "join").Options, "exchange")
	if !exchange.Autocomplete || exchange.MaxLength == nil || *exchange.MaxLength != 100 {
		t.Errorf("join exchange = %+v, want autocomplete with max_length 100", exchange)
	}

	question := findOption(t, findOption(t, short.Options, "ask").Options, "question")
	if question.MaxLength == nil || *question.MaxLength != 1000 {
		t.Errorf("ask question = %+v, want max_length 1000", question)
	}
}

func TestCreateCard(t *testing.T) {
	t.Parallel()

	s := newGuild(t, 3)
	organizer, other := s.members[0], s.members[1]
	name := s.exchangeName("Winter exchange")

	cardMessage, hint := s.createExchange(t, organizer, name)

	buttons := buttonIDs(t, cardMessage.message)
	want := []string{"/gx/join/" + cardMessage.id, "/gx/leave/" + cardMessage.id, "/gx/rules/" + cardMessage.id}
	if !slices.Equal(buttons, want) {
		t.Errorf("card buttons = %v, want %v", buttons, want)
	}

	if hint.message.EphemeralFor != organizer.ID {
		t.Errorf("hint is visible to %q, want only the organizer %q", hint.message.EphemeralFor, organizer.ID)
	}
	if !strings.Contains(hint.text, name) {
		t.Errorf("hint = %q, want it to name the exchange", hint.text)
	}

	for _, member := range s.members[1:] {
		isCardSeen := false
		for _, message := range s.fake.Visible(member, s.guild.ChannelID) {
			if message.ID == hint.message.ID {
				t.Errorf("%s sees the organizer's hint", member.Name)
			}
			if message.ID == cardMessage.message.ID {
				isCardSeen = true
			}
		}
		if !isCardSeen {
			t.Errorf("%s does not see the public card", member.Name)
		}
	}
	if other.ID == organizer.ID {
		t.Fatal("test setup: the members are the same user")
	}
}

func participantCount(t *testing.T, s *scene, asUser int, name string) string {
	t.Helper()

	listed := s.slash(t, s.members[asUser], "/ge/list", nil)
	for line := range strings.SplitSeq(listed.text, "\n") {
		if strings.Contains(line, name) {
			return line
		}
	}
	t.Fatalf("list %q does not show %q", listed.text, name)

	return ""
}

func TestJoinLeave(t *testing.T) {
	t.Parallel()

	s := newGuild(t, 3)
	organizer, bruno, carla := s.members[0], s.members[1], s.members[2]
	name := s.exchangeName("Winter exchange")
	exchange, _ := s.createExchange(t, organizer, name)

	first := s.join(t, bruno, exchange.id)
	if !strings.Contains(first.text, textJoined) || first.message.EphemeralFor != bruno.ID {
		t.Errorf("slash join = %q to %q, want a confirmation to Bruno", first.text, first.message.EphemeralFor)
	}

	byButton := s.click(t, carla, exchange.message, joinPrefix+exchange.id)
	if !strings.Contains(byButton.text, textJoined) || byButton.message.EphemeralFor != carla.ID {
		t.Errorf("button join = %q to %q, want a confirmation to Carla", byButton.text, byButton.message.EphemeralFor)
	}

	again := s.join(t, bruno, exchange.id)
	if !strings.Contains(again.text, textAlreadyIn) {
		t.Errorf("second join = %q, want %q", again.text, textAlreadyIn)
	}

	if line := participantCount(t, s, 0, name); !strings.Contains(line, "participants: 2") {
		t.Errorf("list line = %q, want 2 participants", line)
	}

	left := s.slash(t, bruno, "/ge/leave", map[string]any{"exchange": exchange.id})
	if !strings.Contains(left.text, textLeft) {
		t.Errorf("leave = %q, want %q", left.text, textLeft)
	}
	if line := participantCount(t, s, 0, name); !strings.Contains(line, "participants: 1") {
		t.Errorf("list line after the leave = %q, want 1 participant", line)
	}
}

func TestJoinWithAnExchangeOfAnotherGuildIsNotFound(t *testing.T) {
	t.Parallel()

	home := newGuild(t, 2)
	away := newGuild(t, 2)
	name := home.exchangeName("Winter exchange")
	exchange, _ := home.createExchange(t, home.members[0], name)

	outsider := away.members[1]
	bySlash := away.join(t, outsider, exchange.id)
	ownMessage := away.slash(t, outsider, "/ge/help", nil)
	forged, err := away.fake.ClickForged(t.Context(), outsider, ownMessage.message, joinPrefix+exchange.id)
	if err != nil {
		t.Fatalf("forged click: %v", err)
	}
	byButton := away.replyTo(t, outsider, forged)

	for kind, got := range map[string]reply{"slash": bySlash, "button": byButton} {
		if !strings.Contains(got.text, textNotFound) {
			t.Errorf("%s join from another guild = %q, want %q", kind, got.text, textNotFound)
		}
	}
	if line := participantCount(t, home, 0, name); !strings.Contains(line, "participants: 0") {
		t.Errorf("list line = %q, want the exchange untouched", line)
	}
}

func TestAutocompleteAndAutofill(t *testing.T) {
	t.Parallel()

	t.Run("join candidates exclude the exchanges the caller is in", func(t *testing.T) {
		t.Parallel()

		s := newGuild(t, 3)
		organizer, bruno := s.members[0], s.members[1]
		winter := s.exchangeName("Winter exchange")
		summer := s.exchangeName("Summer exchange")
		first, _ := s.createExchange(t, organizer, winter)
		s.createExchange(t, organizer, summer)
		s.join(t, bruno, first.id)

		guild := s.guild
		brunoChoices := choiceNames(s.complete(t, bruno, &guild, "join", ""))
		carlaChoices := choiceNames(s.complete(t, s.members[2], &guild, "join", ""))

		if !slices.Equal(brunoChoices, []string{summer}) {
			t.Errorf("Bruno's choices = %v, want only %q", brunoChoices, summer)
		}
		if !slices.Equal(carlaChoices, []string{summer, winter}) {
			t.Errorf("Carla's choices = %v, want both, sorted by name", carlaChoices)
		}
	})

	t.Run("join without the option fills in the only exchange", func(t *testing.T) {
		t.Parallel()

		s := newGuild(t, 2)
		name := s.exchangeName("Winter exchange")
		s.createExchange(t, s.members[0], name)

		got := s.slash(t, s.members[1], "/ge/join", nil)

		if !strings.Contains(got.text, textJoined) || !strings.Contains(got.text, name) {
			t.Errorf("join = %q, want a confirmation naming %q", got.text, name)
		}
	})

	t.Run("join without the option lists the choices when there are several", func(t *testing.T) {
		t.Parallel()

		s := newGuild(t, 2)
		winter := s.exchangeName("Winter exchange")
		summer := s.exchangeName("Summer exchange")
		s.createExchange(t, s.members[0], winter)
		s.createExchange(t, s.members[0], summer)

		got := s.slash(t, s.members[1], "/ge/join", nil)

		isChoice := strings.Contains(got.text, textChoose) &&
			strings.Contains(got.text, winter) && strings.Contains(got.text, summer)
		if !isChoice {
			t.Errorf("join = %q, want the list of both exchanges", got.text)
		}
		for _, name := range []string{winter, summer} {
			if line := participantCount(t, s, 0, name); !strings.Contains(line, "participants: 0") {
				t.Errorf("list line = %q, want nobody joined", line)
			}
		}
	})

	t.Run("choices in the bot DM carry the guild name", func(t *testing.T) {
		t.Parallel()

		s := newGuild(t, 3)
		organizer := s.members[0]
		name := s.exchangeName("Winter exchange")
		exchange, _ := s.createExchange(t, organizer, name)
		for _, member := range s.members {
			s.join(t, member, exchange.id)
		}
		s.draw(t, organizer, exchange.id)

		choices := choiceNames(s.complete(t, s.members[1], nil, "recipient", ""))

		want := name + " - " + s.guild.Name
		if !slices.Equal(choices, []string{want}) {
			t.Errorf("DM choices = %v, want [%q]", choices, want)
		}
	})
}

func TestSubprocessEnvironment(t *testing.T) {
	t.Parallel()

	env := subprocessEnv("db-url", "key", "cover")

	keys := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, found := strings.Cut(entry, "=")
		if !found {
			t.Fatalf("environment entry %q has no value", entry)
		}
		keys = append(keys, key)
	}
	slices.Sort(keys)

	want := []string{"CHATISIMO_ENV", "DATABASE_URL", "GOCOVERDIR", "MASTER_KEY", "PATH"}
	if runtime.GOOS == "windows" {
		want = append(want, "SYSTEMROOT")
		slices.Sort(want)
	}
	if !slices.Equal(keys, want) {
		t.Errorf("environment variables = %v, want exactly %v", keys, want)
	}
	if !slices.Contains(env, "CHATISIMO_ENV=local") {
		t.Errorf("CHATISIMO_ENV is not local: %v", env)
	}
}

func TestLogAuditFlagsPlantedValues(t *testing.T) {
	t.Parallel()

	var planted registry
	planted.add("planted name", "needle-in-the-log")
	planted.add("ignored empty value", "")
	path := filepath.Join(t.TempDir(), "chatisimo.log")

	clean := "level=INFO msg=ready\n"
	err := os.WriteFile(path, []byte(clean), 0o600)
	if err != nil {
		t.Fatalf("write log: %v", err)
	}
	if auditErr := auditLog(path, &planted); auditErr != nil {
		t.Errorf("clean log failed the audit: %v", auditErr)
	}

	dirty := clean + "level=INFO msg=leak value=needle-in-the-log\n"
	err = os.WriteFile(path, []byte(dirty), 0o600)
	if err != nil {
		t.Fatalf("write log: %v", err)
	}
	auditErr := auditLog(path, &planted)
	if auditErr == nil || !strings.Contains(auditErr.Error(), "planted name") {
		t.Errorf("planted value gave %v, want an audit failure naming it", auditErr)
	}
	if auditErr != nil && strings.Contains(auditErr.Error(), "needle-in-the-log") {
		t.Errorf("the audit error prints the value: %v", auditErr)
	}
}

const drawConfirmPrefix = "/gx/draw-confirm/"

var mentionPattern = regexp.MustCompile(`<@(\d+)>`)

// draw has the organizer run the draw of an exchange, confirming the prompt.
func (s *scene) draw(t *testing.T, organizer fakediscord.User, exchangeID string) {
	t.Helper()

	prompt := s.slash(t, organizer, "/ge/draw", map[string]any{"exchange": exchangeID})
	confirm := ""
	for _, customID := range buttonIDs(t, prompt.message) {
		if strings.HasPrefix(customID, drawConfirmPrefix) {
			confirm = customID
		}
	}
	if confirm == "" {
		t.Fatalf("the draw prompt has no confirm button: %v", buttonIDs(t, prompt.message))
	}
	s.click(t, organizer, prompt.message, confirm)
}

// recipientOf waits for the member's draw notice and returns the ID of the recipient it names.
func (s *scene) recipientOf(t *testing.T, giver fakediscord.User) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), waitDeadline)
	defer cancel()

	notice, err := s.fake.WaitDM(ctx, giver, func(m fakediscord.Message) bool {
		return strings.Contains(textOf(t, m), titleRecipient)
	})
	if err != nil {
		t.Fatalf("%s got no draw notice: %v", giver.Name, err)
	}

	match := mentionPattern.FindStringSubmatch(textOf(t, notice))
	if match == nil {
		t.Fatalf("the draw notice of %s names no recipient", giver.Name)
	}

	return match[1]
}

func TestOnlyTheOrganizerDraws(t *testing.T) {
	t.Parallel()

	s := newGuild(t, 3)
	organizer, bruno := s.members[0], s.members[1]
	name := s.exchangeName("Winter exchange")
	exchange, _ := s.createExchange(t, organizer, name)
	for _, member := range s.members {
		s.join(t, member, exchange.id)
	}

	denied := s.slash(t, bruno, "/ge/draw", map[string]any{"exchange": exchange.id})

	if !strings.Contains(denied.text, textForbidden) {
		t.Errorf("draw by a member = %q, want %q", denied.text, textForbidden)
	}
	if len(buttonIDs(t, denied.message)) != 0 {
		t.Errorf("draw by a member offers buttons: %v", buttonIDs(t, denied.message))
	}
	for _, member := range s.members {
		if dms := s.fake.DMs(member); len(dms) != 0 {
			t.Errorf("%s got %d DM(s) from a refused draw, want none", member.Name, len(dms))
		}
	}
}

func TestDrawKeepsAssignmentsPrivate(t *testing.T) {
	t.Parallel()

	s := newGuild(t, 3)
	organizer := s.members[0]
	name := s.exchangeName("Winter exchange")
	exchange, _ := s.createExchange(t, organizer, name)
	for _, member := range s.members {
		s.join(t, member, exchange.id)
	}

	s.draw(t, organizer, exchange.id)

	receivers := map[string]bool{}
	for _, giver := range s.members {
		recipient := s.recipientOf(t, giver)
		if recipient == giver.ID {
			t.Errorf("%s drew themselves", giver.Name)
		}
		receivers[recipient] = true
		created.addPair("giver and recipient of a draw", giver.ID, recipient)
	}
	if len(receivers) != len(s.members) {
		t.Errorf("the draw gave %d distinct recipients to %d givers", len(receivers), len(s.members))
	}

	recipients := map[string]string{}
	for _, giver := range s.members {
		recipients[giver.ID] = s.recipientOf(t, giver)
	}

	for _, viewer := range s.members {
		for _, message := range s.fake.Visible(viewer, s.guild.ChannelID) {
			if strings.Contains(textOf(t, message), titleRecipient) {
				t.Errorf("the guild channel shows a recipient notice to %s: %q", viewer.Name, textOf(t, message))
			}
		}

		for _, message := range s.fake.DMs(viewer) {
			for _, match := range mentionPattern.FindAllStringSubmatch(textOf(t, message), -1) {
				if match[1] != recipients[viewer.ID] {
					t.Errorf("a DM to %s mentions a user other than their recipient", viewer.Name)
				}
			}
		}
	}

	for _, record := range s.fake.Outbound() {
		if !outboundHoldsRecipientNotice(record) {
			continue
		}

		if len(record.VisibleTo) != 1 || record.ChannelID == s.guild.ChannelID {
			t.Errorf("a recipient notice reached %d viewer(s) in channel %s", len(record.VisibleTo), record.ChannelID)
		}
	}
}

// outboundHoldsRecipientNotice reports whether the record carries a draw notice.
func outboundHoldsRecipientNotice(record fakediscord.Record) bool {
	if strings.Contains(record.Content, titleRecipient) {
		return true
	}

	for _, embed := range record.Embeds {
		if strings.Contains(string(embed), titleRecipient) {
			return true
		}
	}

	return false
}
