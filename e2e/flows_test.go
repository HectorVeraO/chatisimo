//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

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

func TestOnlyTheOrganizerManagesTheRoster(t *testing.T) {
	t.Parallel()

	s := newGuild(t, 4)
	organizer, alice, bruno, carla := s.members[3], s.members[0], s.members[1], s.members[2]
	exchange, _ := s.setup(t, organizer, []fakediscord.User{alice, bruno, carla})
	s.slash(t, organizer, "/ge/exclude", map[string]any{"giver": alice, "recipient": bruno, "exchange": exchange.id})

	attempts := map[string]struct {
		path    string
		options map[string]any
	}{
		"remove":    {"/ge/remove", map[string]any{"participant": carla, "exchange": exchange.id}},
		"exclude":   {"/ge/exclude", map[string]any{"giver": carla, "recipient": alice, "exchange": exchange.id}},
		"unexclude": {"/ge/unexclude", map[string]any{"giver": alice, "recipient": bruno, "exchange": exchange.id}},
	}
	for name, attempt := range attempts {
		denied := s.slash(t, bruno, attempt.path, attempt.options)
		if !strings.Contains(denied.text, textForbidden) {
			t.Errorf("%s by a participant = %q, want %q", name, denied.text, textForbidden)
		}
		if len(buttonIDs(t, denied.message)) != 0 {
			t.Errorf("%s by a participant offers buttons: %v", name, buttonIDs(t, denied.message))
		}
	}

	organizerView := s.rulesView(t, organizer, exchange.id)
	lines := fieldValue(t, organizerView.message, "Exclusions")
	if !strings.Contains(lines, "<@"+alice.ID+"> -> <@"+bruno.ID+">") || strings.Count(lines, "->") != 1 {
		t.Errorf("exclusions after the refused attempts = %q, want only Alice to Bruno", lines)
	}
	left := s.slash(t, carla, "/ge/leave", map[string]any{"exchange": exchange.id})
	if !strings.Contains(left.text, textLeft) {
		t.Errorf("leave by Carla = %q, want %q: she must still be a participant", left.text, textLeft)
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

const (
	textInvalidStatePrefix = "Not possible while the exchange is"
	textNothingChanged     = "Nothing changed."
	textOwnRecipient       = "You can't remove your own recipient. They can leave, or you can cancel the exchange."
	textNotParticipant     = "You are not in this exchange."
	textRateLimited        = "You reached the limit of 20 questions per day for this exchange."
	textNotSaved           = "Not saved:"
	textOutcomePatched     = "Removed. One giver got a new recipient by DM."
	textOutcomeRedrawn     = "Removed. The draw was redone and every giver got a DM."
	textExclusionAdded     = "Exclusion added"
	textExclusionRemoved   = "Exclusion removed"

	dayAndAHalf = "36h"
)

var strandedPattern = regexp.MustCompile(`(\d+) givers have no possible`)

// rulesScene is a guild with an organizer and three participants, not yet drawn.
func rulesScene(t *testing.T) (*scene, fakediscord.User, fakediscord.User, card) {
	t.Helper()

	s := newGuild(t, 4)
	organizer, bruno, carla, diego := s.members[0], s.members[1], s.members[2], s.members[3]
	exchange, _ := s.setup(t, organizer, []fakediscord.User{bruno, carla, diego})

	return s, organizer, bruno, exchange
}

func TestRulesParticipantView(t *testing.T) {
	t.Parallel()

	s, _, bruno, exchange := rulesScene(t)

	participantView := s.rulesView(t, bruno, exchange.id)
	if ids := buttonIDs(t, participantView.message); len(ids) != 0 {
		t.Errorf("a participant sees components in the rules view: %v", ids)
	}
	if !strings.Contains(participantView.text, "Participants: 3") ||
		!strings.Contains(participantView.text, "You are a participant.") {
		t.Errorf("participant rules view = %q, want the count 3 and the role", participantView.text)
	}
}

func TestRulesForgedPickRefused(t *testing.T) {
	t.Parallel()

	s, organizer, bruno, exchange := rulesScene(t)

	forgedPick := s.pickForged(t, bruno, s.rulesView(t, organizer, exchange.id).message,
		"/gx/rule-pick/"+exchange.id, "draw_at")
	if !strings.Contains(forgedPick.text, textForbidden) {
		t.Errorf("a participant's forged rule pick = %q, want %q", forgedPick.text, textForbidden)
	}
}

func TestRulesOrganizerEditsEnum(t *testing.T) {
	t.Parallel()

	s, organizer, _, exchange := rulesScene(t)

	view := s.rulesView(t, organizer, exchange.id)
	if !strings.Contains(view.text, "You are the organizer.") {
		t.Errorf("organizer rules view = %q, want the organizer role", view.text)
	}
	picked := s.pickResult(t, organizer, view.message, idWithPrefix(t, view.message, "/gx/rule-pick/"), "dropout_policy")
	if picked.Message == nil {
		t.Fatal("picking an enum rule offered no options")
	}
	optionID := idWithPrefix(t, *picked.Message, "/gx/rule-option/")
	enumView := s.replyTo(t, organizer, s.pickResult(t, organizer, *picked.Message, optionID, "redraw"))
	if got := fieldValue(t, enumView.message, "Dropout policy"); !strings.Contains(got, "redraw") ||
		strings.Contains(got, "(default)") {
		t.Errorf("dropout policy field = %q, want redraw and no default mark", got)
	}
}

func TestRulesOrganizerEditsTimeAndDuration(t *testing.T) {
	t.Parallel()

	s, organizer, _, exchange := rulesScene(t)

	view := s.rulesView(t, organizer, exchange.id)
	timeView := s.editRule(t, organizer, view, "draw_at", "in 2h")
	if got := fieldValue(t, timeView.message, "Draw time"); !strings.Contains(got, "<t:") {
		t.Errorf("draw time field = %q, want a Discord timestamp", got)
	}
	durationView := s.editRule(t, organizer, timeView, "reminder_lead", dayAndAHalf)
	if got := fieldValue(t, durationView.message, "Reminder before each"); !strings.Contains(got, dayAndAHalf) {
		t.Errorf("reminder field = %q, want %s", got, dayAndAHalf)
	}
}

func TestRulesInvalidInputKeepsEarlierValue(t *testing.T) {
	t.Parallel()

	s, organizer, _, exchange := rulesScene(t)

	view := s.rulesView(t, organizer, exchange.id)
	timeView := s.editRule(t, organizer, view, "draw_at", "in 2h")
	badTime := s.editRule(t, organizer, timeView, "draw_at", "someday")
	badLead := s.editRule(t, organizer, badTime, "reminder_lead", "banana")
	for rule, got := range map[string]reply{"draw_at": badTime, "reminder_lead": badLead} {
		if !strings.Contains(got.text, textNotSaved) || !strings.Contains(got.text, strings.ReplaceAll(rule, "_", `\_`)) {
			t.Errorf("invalid %s = %q, want the Not saved line naming it", rule, got.text)
		}
	}
	if got := fieldValue(t, badLead.message, "Draw time"); !strings.Contains(got, "<t:") {
		t.Errorf("draw time after invalid input = %q, want the earlier value kept", got)
	}
}

func TestRulesLockAfterDraw(t *testing.T) {
	t.Parallel()

	s, organizer, _, exchange := rulesScene(t)

	view := s.rulesView(t, organizer, exchange.id)
	s.draw(t, organizer, exchange.id)
	locked := s.pickForged(t, organizer, view.message, idWithPrefix(t, view.message, "/gx/rule-pick/"), "draw_at")
	if !strings.Contains(locked.text, "This rule can't change while the exchange is drawn.") {
		t.Errorf("editing draw_at after the draw = %q, want the locked rule text", locked.text)
	}
	after := s.rulesView(t, organizer, exchange.id)
	if slices.Contains(selectValues(t, after.message), "draw_at") {
		t.Errorf("the rules view still offers draw_at after the draw: %v", selectValues(t, after.message))
	}
	if got := fieldValue(t, after.message, "Draw time"); !strings.Contains(got, "locked") {
		t.Errorf("draw time after the draw = %q, want it marked locked", got)
	}
}

func TestExclusions(t *testing.T) {
	t.Parallel()

	s := newGuild(t, 4)
	organizer, alice, bruno, carla := s.members[3], s.members[0], s.members[1], s.members[2]
	exchange, _ := s.setup(t, organizer, []fakediscord.User{alice, bruno, carla})

	single := s.slash(t, organizer, "/ge/exclude",
		map[string]any{"giver": alice, "recipient": bruno, "exchange": exchange.id})
	if !strings.Contains(single.text, textExclusionAdded) || strings.Contains(single.text, "reverse") {
		t.Errorf("exclude = %q, want a one-way exclusion", single.text)
	}
	mutual := s.slash(t, organizer, "/ge/exclude",
		map[string]any{"giver": carla, "recipient": alice, "both": true, "exchange": exchange.id})
	if !strings.Contains(mutual.text, "The reverse is excluded too.") {
		t.Errorf("mutual exclude = %q, want the reverse noted", mutual.text)
	}

	organizerView := s.rulesView(t, organizer, exchange.id)
	lines := fieldValue(t, organizerView.message, "Exclusions")
	pair := "<@" + alice.ID + "> -> <@" + bruno.ID + ">"
	if !strings.Contains(lines, pair) || strings.Count(lines, "->") != 3 {
		t.Errorf("organizer exclusions = %q, want 3 pairs including Alice to Bruno", lines)
	}
	participantView := s.rulesView(t, bruno, exchange.id)
	if got := fieldValue(t, participantView.message, "Exclusions"); got != "3" {
		t.Errorf("participant exclusions = %q, want only the count 3", got)
	}

	removed := s.slash(t, organizer, "/ge/unexclude",
		map[string]any{"giver": alice, "recipient": bruno, "exchange": exchange.id})
	if !strings.Contains(removed.text, textExclusionRemoved) {
		t.Errorf("unexclude = %q, want %q", removed.text, textExclusionRemoved)
	}
	if got := fieldValue(t, s.rulesView(t, bruno, exchange.id).message, "Exclusions"); got != "2" {
		t.Errorf("participant exclusions after the removal = %q, want 2", got)
	}

	prompt, confirm := s.confirmPrompt(t, organizer, "/ge/draw", map[string]any{"exchange": exchange.id}, drawConfirmPrefix)
	refused := s.click(t, organizer, prompt.message, confirm)
	match := strandedPattern.FindStringSubmatch(refused.text)
	if !strings.Contains(refused.text, "No valid draw exists") || match == nil {
		t.Fatalf("infeasible draw = %q, want the stranded count", refused.text)
	}
	if match[1] == "0" {
		t.Errorf("stranded count = %s, want at least 1", match[1])
	}
	if state := s.rulesView(t, organizer, exchange.id).text; !strings.Contains(state, "State: open") {
		t.Errorf("rules view after the refused draw = %q, want the exchange still open", state)
	}
}

func TestDrawManual(t *testing.T) {
	t.Parallel()

	s := newGuild(t, 4)
	organizer, givers := s.members[0], s.members[1:]
	exchange, name := s.setup(t, organizer, givers)

	prompt, _ := s.confirmPrompt(t, organizer, "/ge/draw", map[string]any{"exchange": exchange.id}, drawConfirmPrefix)
	if !strings.Contains(prompt.text, "with 3 participants") {
		t.Errorf("draw prompt = %q, want the participant count", prompt.text)
	}
	back := s.click(t, organizer, prompt.message, idWithPrefix(t, prompt.message, "/gx/dismiss/"))
	if !strings.Contains(back.text, textNothingChanged) {
		t.Errorf("Back = %q, want %q", back.text, textNothingChanged)
	}
	for _, giver := range givers {
		if dms := s.fake.DMs(giver); len(dms) != 0 {
			t.Errorf("%s got %d DM(s) after Back, want none", giver.Name, len(dms))
		}
	}

	prompt, confirm := s.confirmPrompt(t, organizer, "/ge/draw", map[string]any{"exchange": exchange.id}, drawConfirmPrefix)
	done := s.click(t, organizer, prompt.message, confirm)
	if !strings.Contains(done.text, "Draw done for") || !strings.Contains(done.text, name) {
		t.Errorf("confirm = %q, want the draw confirmation", done.text)
	}
	again := s.clickForged(t, organizer, prompt.message, confirm)
	if !strings.Contains(again.text, textInvalidStatePrefix) {
		t.Errorf("second confirm = %q, want an invalid state refusal", again.text)
	}

	pairs := s.pairsAfterDraw(t, givers)
	assertDerangement(t, pairs)
	for _, giver := range givers {
		if got := len(s.dmsWithTitle(t, giver, titleRecipient)); got != 1 {
			t.Errorf("%s got %d recipient DMs, want exactly 1", giver.Name, got)
		}
	}

	announcements := 0
	for _, message := range s.fake.Visible(organizer, s.guild.ChannelID) {
		isAnnouncement := message.EphemeralFor == "" && strings.Contains(textOf(t, message), "is done")
		if !isAnnouncement {
			continue
		}
		announcements++
		if mentionPattern.MatchString(textOf(t, message)) {
			t.Errorf("the public announcement names someone: %q", textOf(t, message))
		}
	}
	if announcements == 0 {
		t.Error("no public draw announcement was posted in the channel")
	}
}

func TestRecipientAndGiver(t *testing.T) {
	t.Parallel()

	s := newGuild(t, 5)
	organizer, outsider, givers := s.members[0], s.members[4], s.members[1:4]
	exchange, _ := s.setup(t, organizer, givers)
	s.draw(t, organizer, exchange.id)
	pairs := s.pairsAfterDraw(t, givers)
	bruno := givers[0]
	options := map[string]any{"exchange": exchange.id}

	inGuild := s.slash(t, bruno, "/ge/recipient", options)
	inDM := s.dmSlash(t, bruno, "/ge/recipient", options)
	for place, got := range map[string]reply{"guild": inGuild, "bot DM": inDM} {
		if got.message.EphemeralFor != bruno.ID || !hasTitle(t, got.message, titleRecipient) ||
			!strings.Contains(got.text, "<@"+pairs[bruno.ID]+">") {
			t.Errorf("recipient in the %s = %q to %q, want Bruno's own recipient only to Bruno",
				place, got.text, got.message.EphemeralFor)
		}
	}
	if inDM.message.ChannelID == s.guild.ChannelID {
		t.Error("the DM reply was sent to the guild channel")
	}

	refused := s.slash(t, outsider, "/ge/recipient", options)
	if hasTitle(t, refused.message, titleRecipient) || mentionPattern.MatchString(refused.text) {
		t.Errorf("a non-participant got %q", refused.text)
	}
	if !strings.Contains(refused.text, textNotParticipant) && !strings.Contains(refused.text, textNotFound) {
		t.Errorf("non-participant recipient = %q, want a refusal", refused.text)
	}

	early := s.slash(t, bruno, "/ge/giver", options)
	if hasTitle(t, early.message, titleGiver) || !strings.Contains(early.text, textInvalidStatePrefix) {
		t.Errorf("giver before the reveal = %q, want an invalid state refusal", early.text)
	}

	s.reveal(t, organizer, exchange.id)
	late := s.slash(t, bruno, "/ge/giver", options)
	var giverOfBruno string
	for giver, recipient := range pairs {
		if recipient == bruno.ID {
			giverOfBruno = giver
		}
	}
	if !hasTitle(t, late.message, titleGiver) || !strings.Contains(late.text, "<@"+giverOfBruno+">") {
		t.Errorf("giver after the reveal = %q, want Bruno's giver", late.text)
	}
}

func TestQuestions(t *testing.T) {
	t.Parallel()

	s := newGuild(t, 4)
	organizer, givers := s.members[0], s.members[1:]
	exchange, name := s.setup(t, organizer, givers)
	s.draw(t, organizer, exchange.id)
	pairs := s.pairsAfterDraw(t, givers)
	asker := givers[0]
	recipient, found := userIn(givers, pairs[asker.ID])
	if !found {
		t.Fatal("the recipient of the asker is not a participant")
	}

	question := "Do you like tea?"
	asked := s.slash(t, asker, "/ge/ask", map[string]any{"question": question, "exchange": exchange.id})
	if !strings.Contains(asked.text, "sent anonymously") {
		t.Errorf("ask = %q, want the anonymous confirmation", asked.text)
	}

	ctx, cancel := context.WithTimeout(t.Context(), waitDeadline)
	defer cancel()
	received, err := s.fake.WaitDM(ctx, recipient, func(m fakediscord.Message) bool {
		return strings.Contains(textOf(t, m), "Anonymous question")
	})
	if err != nil {
		t.Fatalf("the recipient got no question: %v", err)
	}
	body := textOf(t, received)
	if !strings.Contains(body, name) || !strings.Contains(body, question) {
		t.Errorf("question DM = %q, want the exchange and the question", body)
	}
	if strings.Contains(body, asker.ID) || strings.Contains(body, asker.Name) {
		t.Errorf("the question DM names the giver: %q", body)
	}

	result := s.clickResult(t, recipient, received, idWithPrefix(t, received, "/gx/answer/"))
	answered := s.submit(t, recipient, result.Modal, map[string]string{"answer": "Green tea"})
	if !strings.Contains(answered.text, "Your answer:") {
		t.Errorf("answer = %q, want the answer echoed", answered.text)
	}
	back, err := s.fake.WaitDM(ctx, asker, func(m fakediscord.Message) bool {
		return strings.Contains(textOf(t, m), "Green tea")
	})
	if err != nil {
		t.Fatalf("the giver got no answer: %v", err)
	}
	if !strings.Contains(textOf(t, back), question) {
		t.Errorf("answer DM = %q, want the question quoted", textOf(t, back))
	}

	options := map[string]any{"exchange": exchange.id}
	askedList := s.slash(t, asker, "/ge/questions", options)
	receivedList := s.slash(t, recipient, "/ge/questions", options)
	if got := fieldValue(t, askedList.message, "Questions you asked"); !strings.Contains(got, question) ||
		!strings.Contains(got, "Green tea") {
		t.Errorf("the giver's asked list = %q, want question and answer", got)
	}
	if got := fieldValue(t, receivedList.message, "Questions you received"); !strings.Contains(got, question) {
		t.Errorf("the recipient's received list = %q, want the question", got)
	}

	for i := 2; i <= 20; i++ {
		text := fmt.Sprintf("Question number %d?", i)
		got := s.slash(t, asker, "/ge/ask", map[string]any{"question": text, "exchange": exchange.id})
		if !strings.Contains(got.text, "sent anonymously") {
			t.Fatalf("question %d = %q, want it sent", i, got.text)
		}
	}
	limited := s.slash(t, asker, "/ge/ask", map[string]any{"question": "One too many?", "exchange": exchange.id})
	if !strings.Contains(limited.text, textRateLimited) {
		t.Errorf("21st question = %q, want %q", limited.text, textRateLimited)
	}
}

func TestQuestionInputIsDeliveredIntact(t *testing.T) {
	t.Parallel()

	s := newGuild(t, 4)
	organizer, givers := s.members[0], s.members[1:]
	exchange, _ := s.setup(t, organizer, givers)
	s.draw(t, organizer, exchange.id)
	pairs := s.pairsAfterDraw(t, givers)
	asker := givers[0]
	recipient, found := userIn(givers, pairs[asker.ID])
	if !found {
		t.Fatal("the recipient of the asker is not a participant")
	}

	questions := map[string]string{
		"long":    strings.Repeat("Is tea your favorite drink? ", 14),
		"unicode": "Cafe\u0301 \u05e9\u05dc\u05d5\u05dd \u0645\u0631\u062d\u0628\u0627 \U0001F375 a\u200bb?",
	}
	for name, question := range questions {
		asked := s.slash(t, asker, "/ge/ask", map[string]any{"question": question, "exchange": exchange.id})
		if !strings.Contains(asked.text, "sent anonymously") {
			t.Fatalf("%s question: ask = %q, want the anonymous confirmation", name, asked.text)
		}

		ctx, cancel := context.WithTimeout(t.Context(), waitDeadline)
		_, err := s.fake.WaitDM(ctx, recipient, func(m fakediscord.Message) bool {
			return strings.Contains(textOf(t, m), strings.TrimSpace(question))
		})
		cancel()
		if err != nil {
			t.Errorf("%s question: the recipient got no intact copy: %v", name, err)
		}
	}
}

func userIn(users []fakediscord.User, id string) (fakediscord.User, bool) {
	for _, user := range users {
		if user.ID == id {
			return user, true
		}
	}

	return fakediscord.User{}, false
}

// dropoutScene draws an exchange of participants members, organized by a non-participant.
func dropoutScene(t *testing.T, participants int) (*scene, fakediscord.User, []fakediscord.User, card, map[string]string) {
	t.Helper()

	s := newGuild(t, participants+1)
	organizer, givers := s.members[participants], s.members[:participants]
	exchange, _ := s.setup(t, organizer, givers)
	s.draw(t, organizer, exchange.id)

	return s, organizer, givers, exchange, s.pairsAfterDraw(t, givers)
}

func TestDropoutPatch(t *testing.T) {
	t.Parallel()

	s, organizer, givers, exchange, pairs := dropoutScene(t, 5)
	// A giver who drew their leaver's giver back cannot be patched; the module redraws instead.
	var leaver fakediscord.User
	for _, giver := range givers {
		if pairs[pairs[giver.ID]] != giver.ID {
			leaver = giver
		}
	}
	var affected fakediscord.User
	for _, giver := range givers {
		if pairs[giver.ID] == leaver.ID {
			affected = giver
		}
	}
	before := map[string]int{}
	for _, giver := range givers {
		before[giver.ID] = len(s.fake.DMs(giver))
	}

	outcome := s.remove(t, organizer, leaver, exchange.id)
	if outcome.text != textOutcomePatched {
		t.Errorf("organizer reply = %q, want %q", outcome.text, textOutcomePatched)
	}

	replacement := s.awaitTitled(t, affected, titleNewRecipient, 1)
	if replacement == leaver.ID || replacement == affected.ID {
		t.Error("the patched giver got the leaver or themselves")
	}
	for _, giver := range givers {
		if giver.ID == affected.ID {
			continue
		}
		if got := len(s.fake.DMs(giver)); got != before[giver.ID] {
			t.Errorf("%s got %d new DM(s) from a patch of another giver, want none", giver.Name, got-before[giver.ID])
		}
	}
}

func TestDropoutRedraw(t *testing.T) {
	t.Parallel()

	s, organizer, givers, exchange, _ := dropoutScene(t, 4)
	leaver, remaining := givers[0], givers[1:]

	outcome := s.remove(t, organizer, leaver, exchange.id)
	if outcome.text != textOutcomeRedrawn {
		t.Errorf("organizer reply = %q, want %q", outcome.text, textOutcomeRedrawn)
	}

	pairs := map[string]string{}
	for _, giver := range remaining {
		pairs[giver.ID] = s.awaitTitled(t, giver, titleNewRecipient, 1)
	}
	assertDerangement(t, pairs)
	if got := len(s.dmsWithTitle(t, leaver, titleNewRecipient)); got != 0 {
		t.Errorf("the leaver got %d new recipient DMs, want none", got)
	}
}

func TestDropoutVoid(t *testing.T) {
	t.Parallel()

	s, organizer, givers, exchange, _ := dropoutScene(t, 3)
	leaver, remaining := givers[0], givers[1:]

	outcome := s.remove(t, organizer, leaver, exchange.id)
	if !strings.Contains(outcome.text, "the draw was undone and") || !strings.Contains(outcome.text, "is open again.") {
		t.Errorf("organizer reply = %q, want the reopened text", outcome.text)
	}

	for _, user := range append([]fakediscord.User{organizer}, remaining...) {
		ctx, cancel := context.WithTimeout(t.Context(), waitDeadline)
		_, err := s.fake.WaitDM(ctx, user, func(m fakediscord.Message) bool {
			return strings.Contains(textOf(t, m), "was undone")
		})
		cancel()
		if err != nil {
			t.Errorf("%s got no voided notice: %v", user.Name, err)
		}
	}
	if view := s.rulesView(t, organizer, exchange.id); !strings.Contains(view.text, "State: open") {
		t.Errorf("rules view after the void = %q, want the exchange open", view.text)
	}
}

func TestOrganizerOwnRecipient(t *testing.T) {
	t.Parallel()

	s := newGuild(t, 4)
	organizer, givers := s.members[0], s.members
	exchange, _ := s.setup(t, organizer, givers)
	s.draw(t, organizer, exchange.id)
	pairs := s.pairsAfterDraw(t, givers)
	target, found := userIn(givers, pairs[organizer.ID])
	if !found {
		t.Fatal("the organizer's recipient is not a participant")
	}
	before := 0
	for _, giver := range givers {
		before += len(s.fake.DMs(giver))
	}

	refused := s.remove(t, organizer, target, exchange.id)

	if refused.text != textOwnRecipient {
		t.Errorf("removing the own recipient = %q, want %q", refused.text, textOwnRecipient)
	}
	after := 0
	for _, giver := range givers {
		after += len(s.fake.DMs(giver))
	}
	if after != before {
		t.Errorf("the refusal produced %d new DM(s)", after-before)
	}
	if view := s.rulesView(t, organizer, exchange.id); !strings.Contains(view.text, "Participants: 4") {
		t.Errorf("rules view = %q, want all 4 participants still in", view.text)
	}
}

func TestCancel(t *testing.T) {
	t.Parallel()

	s := newGuild(t, 4)
	organizer, bruno, carla, outsider := s.members[0], s.members[1], s.members[2], s.members[3]
	exchange, name := s.setup(t, organizer, []fakediscord.User{bruno, carla})

	prompt, confirm := s.confirmPrompt(t, organizer, "/ge/cancel", map[string]any{"exchange": exchange.id}, "/gx/cancel-confirm/")
	done := s.click(t, organizer, prompt.message, confirm)
	if !strings.Contains(done.text, "is cancelled.") || !strings.Contains(done.text, name) {
		t.Errorf("cancel = %q, want the confirmation", done.text)
	}

	for _, member := range []fakediscord.User{bruno, carla} {
		ctx, cancel := context.WithTimeout(t.Context(), waitDeadline)
		_, err := s.fake.WaitDM(ctx, member, func(m fakediscord.Message) bool {
			return strings.Contains(textOf(t, m), "was cancelled by the organizer")
		})
		cancel()
		if err != nil {
			t.Errorf("%s got no cancellation DM: %v", member.Name, err)
		}
	}

	joined := s.click(t, outsider, exchange.message, joinPrefix+exchange.id)
	left := s.click(t, bruno, exchange.message, "/gx/leave/"+exchange.id)
	for button, got := range map[string]reply{"join": joined, "leave": left} {
		if !strings.Contains(got.text, textInvalidStatePrefix) || !strings.Contains(got.text, "cancelled") {
			t.Errorf("%s on a cancelled exchange = %q, want an invalid state refusal", button, got.text)
		}
	}
}

const (
	timedLead      = 5 * time.Second
	timedDrawIn    = 15 * time.Second
	timedRevealIn  = 40 * time.Second
	timedTolerance = 20 * time.Second
	// timedSlack is how much earlier than its due time a notice may arrive: the bot parsed the
	// rule a moment before the test read the clock.
	timedSlack = 3 * time.Second
)

// awaitDM waits for a DM to the user that match accepts, until limit, and fails when the DM
// was created before earliest.
func awaitDM(
	t *testing.T, s *scene, user fakediscord.User, earliest, limit time.Time, what string,
	match func(fakediscord.Message) bool,
) {
	t.Helper()

	ctx, cancel := context.WithDeadline(t.Context(), limit)
	defer cancel()

	got, err := s.fake.WaitDM(ctx, user, match)
	if err != nil {
		t.Errorf("%s got no %s by %s: %v", user.Name, what, limit.Format(time.RFC3339), err)

		return
	}
	if got.CreatedAt.Before(earliest) {
		t.Errorf("%s got the %s at %s, before %s", user.Name, what,
			got.CreatedAt.Format(time.RFC3339), earliest.Format(time.RFC3339))
	}
}

func TestTimed(t *testing.T) {
	t.Parallel()

	s := newGuild(t, 4)
	organizer, givers := s.members[0], s.members[1:]
	exchange, _ := s.setup(t, organizer, givers)

	view := s.rulesView(t, organizer, exchange.id)
	view = s.editRule(t, organizer, view, "reminder_lead", "5s")
	drawDue := time.Now().Add(timedDrawIn)
	view = s.editRule(t, organizer, view, "draw_at", "in 15s")
	revealDue := time.Now().Add(timedRevealIn)
	s.editRule(t, organizer, view, "reveal_at", "in 40s")

	isDrawSoon := func(m fakediscord.Message) bool {
		return strings.Contains(textOf(t, m), "Reminder: the draw for")
	}
	isDrawn := func(m fakediscord.Message) bool {
		return hasTitle(t, m, titleRecipient) && strings.Contains(m.Content, "is done")
	}
	isRevealSoon := func(m fakediscord.Message) bool {
		return hasTitle(t, m, titleRecipient) && strings.Contains(m.Content, "Reminder:")
	}
	isRevealed := func(m fakediscord.Message) bool {
		return hasTitle(t, m, titleGiver)
	}

	everyone := append([]fakediscord.User{organizer}, givers...)
	drawReminderAt := drawDue.Add(-timedLead)
	revealReminderAt := revealDue.Add(-timedLead)
	for _, user := range everyone {
		awaitDM(t, s, user, drawReminderAt.Add(-timedSlack), drawReminderAt.Add(timedTolerance), "draw_soon", isDrawSoon)
	}
	for _, giver := range givers {
		awaitDM(t, s, giver, drawDue.Add(-timedSlack), drawDue.Add(timedTolerance), "drawn", isDrawn)
	}
	for _, giver := range givers {
		awaitDM(t, s, giver, revealReminderAt.Add(-timedSlack), revealReminderAt.Add(timedTolerance), "reveal_soon", isRevealSoon)
	}
	for _, giver := range givers {
		awaitDM(t, s, giver, revealDue.Add(-timedSlack), revealDue.Add(timedTolerance), "revealed", isRevealed)
	}
}
