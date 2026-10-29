//go:build !race

// These tests open a real disgo Gateway client; see gateway_harness_test.go for why they are
// left out of the race run.

package discord_test

import (
	cryptorand "crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"

	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

const (
	otherGuild = "Other server"

	textNoCandidates     = "You have no exchange where you can do this."
	textNoMatch          = "No exchange matches that name. Pick one from the list."
	textNotFound         = "That exchange does not exist here."
	textAlreadyIn        = "You are already in this exchange."
	textChooseLead       = "You are in several exchanges. Choose one with the exchange option:"
	textJoinedPrefix     = "You joined "
	textNotPossibleDrawn = "Not possible while the exchange is drawn."
)

// scene is a harness with its own guild and four users, so that tests sharing the database
// neither see each other's exchanges nor the ones the DM autocomplete lists across guilds.
// a, b, c and d are the users' fake snowflakes; a organizes in most tests.
type scene struct {
	*harness
	a, b, c, d string
	guild      fakediscord.Guild
	group      giftexchange.GroupID
	people     map[string]person
}

func uniqueSnowflake(t *testing.T) string {
	t.Helper()

	var raw [8]byte
	if _, err := cryptorand.Read(raw[:]); err != nil {
		t.Fatalf("read randomness: %v", err)
	}

	return fmt.Sprintf("3%018d", binary.BigEndian.Uint64(raw[:])%1_000_000_000_000_000_000)
}

func newScene(t *testing.T) *scene {
	t.Helper()

	h := newHarness(t)
	s := &scene{harness: h, people: map[string]person{}}

	var members []fakediscord.User
	var ids []string
	for i := range 4 {
		user := h.fake.AddUser(fakediscord.User{ID: uniqueSnowflake(t), Name: fmt.Sprintf("scene-%d", i)})
		h.users[user.ID] = user
		members = append(members, user)
		ids = append(ids, user.ID)
	}
	s.a, s.b, s.c, s.d = ids[0], ids[1], ids[2], ids[3]
	s.guild = h.fake.AddGuild(fakediscord.Guild{ID: uniqueSnowflake(t), Name: guildName}, members...)

	group, err := h.svc.ResolveGroup(t.Context(), giftexchange.GroupRef{
		Provider: giftexchange.ProviderDiscord, Subject: s.guild.ID,
	})
	if err != nil {
		t.Fatalf("ResolveGroup: %v", err)
	}
	s.group = group

	for _, id := range ids {
		actor, actorErr := h.svc.ResolveActor(t.Context(), giftexchange.IdentityRef{
			Provider: giftexchange.ProviderDiscord, Subject: id,
		})
		if actorErr != nil {
			t.Fatalf("ResolveActor: %v", actorErr)
		}
		s.people[id] = person{actor: actor, id: actor.Person()}
	}

	return s
}

// otherGuildWith adds a second guild with the given members.
func (s *scene) otherGuildWith(t *testing.T, members ...string) fakediscord.Guild {
	t.Helper()

	var users []fakediscord.User
	for _, id := range members {
		users = append(users, s.users[id])
	}

	return s.fake.AddGuild(fakediscord.Guild{ID: uniqueSnowflake(t), Name: otherGuild}, users...)
}

func (s *scene) slashAt(path string, inGuild bool, options map[string]any) fakediscord.Slash {
	in := fakediscord.Slash{Path: path, Options: options}
	if inGuild {
		guild := s.guild
		in.Guild = &guild
	}

	return in
}

func (s *scene) ge(verb string, inGuild bool, options map[string]any) fakediscord.Slash {
	return s.slashAt("/ge/"+verb, inGuild, options)
}

// reply is the last thing the bot said to an interaction.
type reply struct {
	content     string
	description string
	ephemeralTo string
}

// replyTo returns the reply of a result: the direct message, or the edit that followed a
// deferred response.
func replyTo(t *testing.T, s *harness, result fakediscord.Result) reply {
	t.Helper()

	if result.Message != nil {
		return reply{content: result.Message.Content, ephemeralTo: result.Message.EphemeralFor}
	}

	var found fakediscord.Record
	waitFor(t, "the edit of the deferred reply", func() bool {
		for _, record := range s.fake.Outbound() {
			isEdit := record.Source == "edit" && record.InteractionID != nil && *record.InteractionID == result.InteractionID
			if isEdit {
				found = record
				return true
			}
		}

		return false
	})

	r := reply{content: found.Content}
	if found.Ephemeral && len(found.VisibleTo) == 1 {
		r.ephemeralTo = found.VisibleTo[0]
	}
	for _, raw := range found.Embeds {
		var embed discord.Embed
		if err := json.Unmarshal(raw, &embed); err != nil {
			t.Fatalf("decode embed: %v", err)
		}
		r.description += embed.Description
	}

	return r
}

func (s *scene) createExchange(t *testing.T, organizer, name string) giftexchange.ExchangeID {
	t.Helper()

	s.clock.Advance(time.Second)
	view, err := s.svc.CreateExchange(t.Context(), s.people[organizer].actor, giftexchange.CreateExchangeRequest{
		Group: s.group,
		Name:  name,
	})
	if err != nil {
		t.Fatalf("CreateExchange %q: %v", name, err)
	}

	return view.ID
}

func (s *scene) joinAs(t *testing.T, user string, id giftexchange.ExchangeID) {
	t.Helper()

	err := s.svc.Join(t.Context(), s.people[user].actor, s.group, id)
	if err != nil {
		t.Fatalf("Join as %s: %v", user, err)
	}
}

// drawnExchange creates an exchange named name organized by A, joined by A, B and C, and drawn.
func (s *scene) drawnExchange(t *testing.T, name string) giftexchange.ExchangeID {
	t.Helper()

	id := s.createExchange(t, s.a, name)
	for _, user := range []string{s.a, s.b, s.c} {
		s.joinAs(t, user, id)
	}
	err := s.svc.Draw(t.Context(), s.people[s.a].actor, s.group, id)
	if err != nil {
		t.Fatalf("Draw: %v", err)
	}

	return id
}

func (s *scene) summaries(t *testing.T, user string) []giftexchange.ExchangeSummary {
	t.Helper()

	listed, err := s.svc.ExchangesFor(t.Context(), s.people[user].actor, &s.group)
	if err != nil {
		t.Fatalf("ExchangesFor: %v", err)
	}

	return listed
}

func (s *scene) complete(t *testing.T, user string, inGuild bool, verb, typed string) []fakediscord.Choice {
	t.Helper()

	in := s.ge(verb, inGuild, map[string]any{"exchange": typed})
	choices, err := s.fake.Autocomplete(t.Context(), s.users[user], in, "exchange")
	if err != nil {
		t.Fatalf("Autocomplete %s: %v", verb, err)
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

func TestListShowsOneLinePerExchangeWithTheCallersRole(t *testing.T) {
	s := newScene(t)
	winter := s.createExchange(t, s.a, "Winter")
	s.createExchange(t, s.b, "Summer")
	s.joinAs(t, s.b, winter)

	for _, path := range []string{"/ge/list", "/chatisimo/giftexchange/list"} {
		result := slashOrFail(t, s.harness, s.b, s.slashAt(path, true, nil))
		got := replyTo(t, s.harness, result)

		lines := strings.Split(got.description, "\n")
		if len(lines) != 2 {
			t.Fatalf("%s: %d lines, want 2: %q", path, len(lines), got.description)
		}
		if !strings.Contains(lines[0], "Summer") || !strings.Contains(lines[0], "the organizer") ||
			!strings.Contains(lines[0], "participants: 0") {
			t.Errorf("%s: first line = %q", path, lines[0])
		}
		if !strings.Contains(lines[1], "Winter") || !strings.Contains(lines[1], "a participant") ||
			!strings.Contains(lines[1], "participants: 1") || !strings.Contains(lines[1], "open") {
			t.Errorf("%s: second line = %q", path, lines[1])
		}
		if got.ephemeralTo != s.b {
			t.Errorf("%s: reply is visible to %q, want only the caller", path, got.ephemeralTo)
		}
	}
}

func TestListInTheBotDMAnswersGuildOnly(t *testing.T) {
	s := newScene(t)

	result := slashOrFail(t, s.harness, s.a, s.ge("list", false, nil))

	if got := replyTo(t, s.harness, result); got.content != guildOnlyText {
		t.Errorf("reply = %q, want %q", got.content, guildOnlyText)
	}
}

func TestListDoesNotShowTheExchangesOfAnotherGuild(t *testing.T) {
	s := newScene(t)
	s.createExchange(t, s.a, "Winter")
	other := s.otherGuildWith(t, s.c)

	result := slashOrFail(t, s.harness, s.c, fakediscord.Slash{Path: "/ge/list", Guild: &other})

	got := replyTo(t, s.harness, result)
	if strings.Contains(got.description, "Winter") {
		t.Errorf("the list of another guild shows %q", got.description)
	}
}

func TestJoinWithoutTheOptionAutoFillsTheOnlyExchange(t *testing.T) {
	s := newScene(t)
	id := s.createExchange(t, s.a, "Winter")

	result := slashOrFail(t, s.harness, s.b, s.ge("join", true, nil))

	got := replyTo(t, s.harness, result)
	if got.content != "You joined **Winter**." {
		t.Errorf("reply = %q", got.content)
	}
	isListed := false
	for _, summary := range s.summaries(t, s.b) {
		if summary.ID != id {
			continue
		}
		isListed = true
		if !summary.CallerIsParticipant {
			t.Error("the caller is not a participant after joining")
		}
	}
	if !isListed {
		t.Error("the exchange is missing from the caller's list")
	}
}

func TestJoinByNameIgnoresCase(t *testing.T) {
	s := newScene(t)
	s.createExchange(t, s.a, "Winter")
	s.createExchange(t, s.a, "Summer")

	result := slashOrFail(t, s.harness, s.b, s.ge("join", true, map[string]any{"exchange": "sUMMER"}))

	if got := replyTo(t, s.harness, result); got.content != "You joined **Summer**." {
		t.Errorf("reply = %q", got.content)
	}
}

func TestJoinWithSeveralExchangesAsksToChoose(t *testing.T) {
	s := newScene(t)
	s.createExchange(t, s.a, "Winter")
	s.createExchange(t, s.a, "Summer")

	result := slashOrFail(t, s.harness, s.b, s.ge("join", true, nil))

	got := replyTo(t, s.harness, result)
	if !strings.HasPrefix(got.content, textChooseLead) ||
		!strings.Contains(got.content, "Winter") || !strings.Contains(got.content, "Summer") {
		t.Errorf("reply = %q, want the lead and both names", got.content)
	}
	for _, summary := range s.summaries(t, s.b) {
		if summary.CallerIsParticipant {
			t.Error("the caller joined without choosing")
		}
	}
}

func TestJoinWithNothingToJoinAnswersNoCandidates(t *testing.T) {
	s := newScene(t)

	result := slashOrFail(t, s.harness, s.b, s.ge("join", true, nil))

	if got := replyTo(t, s.harness, result); got.content != textNoCandidates {
		t.Errorf("reply = %q, want %q", got.content, textNoCandidates)
	}
}

func TestJoinWithAnUnknownNameAnswersNoMatch(t *testing.T) {
	s := newScene(t)
	s.createExchange(t, s.a, "Winter")

	for _, typed := range []string{"Wint", "Spring", "\u202eretniW", "<@" + s.a + ">"} {
		result := slashOrFail(t, s.harness, s.b, s.ge("join", true, map[string]any{"exchange": typed}))

		if got := replyTo(t, s.harness, result); got.content != textNoMatch {
			t.Errorf("join %q: reply = %q, want %q", typed, got.content, textNoMatch)
		}
	}
}

func TestJoiningTwiceAnswersAlreadyParticipant(t *testing.T) {
	s := newScene(t)
	id := s.createExchange(t, s.a, "Winter")
	option := map[string]any{"exchange": id.String()}

	first := replyTo(t, s.harness, slashOrFail(t, s.harness, s.b, s.ge("join", true, option)))
	second := replyTo(t, s.harness, slashOrFail(t, s.harness, s.b, s.ge("join", true, option)))

	if !strings.HasPrefix(first.content, textJoinedPrefix) {
		t.Errorf("first reply = %q", first.content)
	}
	if second.content != textAlreadyIn {
		t.Errorf("second reply = %q, want %q", second.content, textAlreadyIn)
	}
	isListed := false
	for _, summary := range s.summaries(t, s.b) {
		if summary.ID != id {
			continue
		}
		isListed = true
		if summary.ParticipantCount != 1 {
			t.Errorf("participant count = %d, want 1", summary.ParticipantCount)
		}
	}
	if !isListed {
		t.Error("the exchange is missing from the caller's list")
	}
}

func TestJoinAfterTheDrawAnswersInvalidState(t *testing.T) {
	s := newScene(t)
	id := s.drawnExchange(t, "Winter")

	result := slashOrFail(t, s.harness, s.d, s.ge("join", true, map[string]any{"exchange": id.String()}))

	if got := replyTo(t, s.harness, result); got.content != textNotPossibleDrawn {
		t.Errorf("reply = %q, want %q", got.content, textNotPossibleDrawn)
	}
}

func TestJoinByTheCardButtonJoinsAndLeavesTheCardAlone(t *testing.T) {
	s := newScene(t)
	id := s.createExchange(t, s.a, "Winter")
	card := slashOrFail(t, s.harness, s.b, s.ge("help", true, nil))
	before := len(s.fake.Outbound())

	result, err := s.fake.ClickForged(t.Context(), s.users[s.b], *card.Message, "/gx/join/"+id.String())
	if err != nil {
		t.Fatalf("ClickForged: %v", err)
	}

	if result.CallbackType != 5 {
		t.Errorf("callback type = %d, want 5 (deferred message)", result.CallbackType)
	}
	got := replyTo(t, s.harness, result)
	if got.content != "You joined **Winter**." || got.ephemeralTo != s.b {
		t.Errorf("reply = %+v", got)
	}
	for _, record := range s.fake.Outbound()[before:] {
		if record.Route == "PATCH /channels/{channel}/messages/{id}" {
			t.Errorf("the card was edited: %+v", record)
		}
	}
}

func TestJoinByButtonRefusesForgedCustomIDs(t *testing.T) {
	s := newScene(t)
	id := s.createExchange(t, s.a, "Winter")
	card := slashOrFail(t, s.harness, s.b, s.ge("help", true, nil))
	before := s.summaries(t, s.b)

	forged := []string{
		"/gx/join/not-a-uuid",
		"/gx/join/" + id.String() + "/extra",
		"/gx/join/" + strings.ToUpper(id.String()),
		"/gx/join/100000000000000001",
		"/gx/join/" + strings.Repeat("a", 120),
	}
	for _, customID := range forged {
		result, err := s.fake.ClickForged(t.Context(), s.users[s.b], *card.Message, customID)
		if err != nil {
			t.Fatalf("ClickForged %q: %v", customID, err)
		}

		if got := replyTo(t, s.harness, result); got.content != textNotFound {
			t.Errorf("custom ID %q: reply = %q, want %q", customID, got.content, textNotFound)
		}
	}
	if after := s.summaries(t, s.b); !slices.Equal(before, after) {
		t.Errorf("the exchanges changed: %+v -> %+v", before, after)
	}
}

func TestAnOutsiderCannotJoinAnotherGuildsExchange(t *testing.T) {
	s := newScene(t)
	id := s.createExchange(t, s.a, "Winter")
	other := s.otherGuildWith(t, s.c)
	listBefore := s.summaries(t, s.a)
	dmsBefore := len(s.fake.DMs(s.users[s.c])) + len(s.fake.DMs(s.users[s.a]))
	card := slashOrFail(t, s.harness, s.c, fakediscord.Slash{Path: "/ge/help", Guild: &other})

	bySlash := slashOrFail(t, s.harness, s.c, fakediscord.Slash{
		Path: "/ge/join", Guild: &other, Options: map[string]any{"exchange": id.String()},
	})
	byButton, err := s.fake.ClickForged(t.Context(), s.users[s.c], *card.Message, "/gx/join/"+id.String())
	if err != nil {
		t.Fatalf("ClickForged: %v", err)
	}

	for name, result := range map[string]fakediscord.Result{"slash": bySlash, "button": byButton} {
		if got := replyTo(t, s.harness, result); got.content != textNotFound {
			t.Errorf("%s: reply = %q, want %q", name, got.content, textNotFound)
		}
	}
	if listAfter := s.summaries(t, s.a); !slices.Equal(listBefore, listAfter) {
		t.Errorf("the exchanges changed: %+v -> %+v", listBefore, listAfter)
	}
	if dmsAfter := len(s.fake.DMs(s.users[s.c])) + len(s.fake.DMs(s.users[s.a])); dmsAfter != dmsBefore {
		t.Errorf("DMs went from %d to %d, want no new DM", dmsBefore, dmsAfter)
	}
}

// Only join registers its autocomplete in this change; the filters of the other verbs are
// covered where the table is, and each verb's handlers register their own autocomplete.
func TestAutocompleteForJoinOffersOnlyOpenExchangesTheCallerIsNotIn(t *testing.T) {
	s := newScene(t)
	open := s.createExchange(t, s.a, "Open one")
	s.joinAs(t, s.b, open)
	s.createExchange(t, s.a, "Open two")
	s.drawnExchange(t, "Drawn one")

	cases := map[string]struct {
		user string
		want []string
	}{
		"organizer who did not join": {s.a, []string{"Open two", "Open one"}},
		"participant of one":         {s.b, []string{"Open two"}},
		"participant of the drawn":   {s.c, []string{"Open two", "Open one"}},
		"outsider":                   {s.d, []string{"Open two", "Open one"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := choiceNames(s.complete(t, tc.user, true, "join", ""))

			if !slices.Equal(got, tc.want) {
				t.Errorf("choices = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAutocompleteFiltersByTypedTextAndSortsByName(t *testing.T) {
	s := newScene(t)
	for _, name := range []string{"Zeta party", "alpha party", "Beta", "PARTY three"} {
		s.createExchange(t, s.a, name)
	}

	got := s.complete(t, s.b, true, "join", "  PaRtY ")

	want := []string{"PARTY three", "alpha party", "Zeta party"}
	if names := choiceNames(got); !slices.Equal(names, want) {
		t.Errorf("choices = %v, want %v", names, want)
	}
	for _, choice := range got {
		if _, err := giftexchange.ParseExchangeID(fmt.Sprint(choice.Value)); err != nil {
			t.Errorf("choice value %v is not an exchange ID", choice.Value)
		}
	}
}

func TestAutocompleteCapsTheChoicesAtTwentyFive(t *testing.T) {
	s := newScene(t)
	for i := range 30 {
		s.createExchange(t, s.a, fmt.Sprintf("Round %02d", i))
	}

	got := choiceNames(s.complete(t, s.b, true, "join", ""))

	if len(got) != 25 {
		t.Fatalf("%d choices, want 25", len(got))
	}
	if got[0] != "Round 29" || got[24] != "Round 05" {
		t.Errorf("choices = %v, want Round 29 down to Round 05", got)
	}
}

func TestAutocompleteKeepsUnusualNamesAsPlainText(t *testing.T) {
	s := newScene(t)
	names := []string{"\u05e9\u05dc\u05d5\u05dd \u05d7\u05d2", "\U0001F381 Gifts", "Cafe\u0301 *bold* _it_", "caf\u00e9"}
	for _, name := range names {
		s.createExchange(t, s.a, name)
	}

	got := choiceNames(s.complete(t, s.b, true, "join", ""))

	for _, name := range names {
		if !slices.Contains(got, name) {
			t.Errorf("choices %q lack the plain name %q", got, name)
		}
	}
	if found := choiceNames(s.complete(t, s.b, true, "join", "gifts")); len(found) != 1 {
		t.Errorf("typing gifts found %v, want the emoji exchange", found)
	}
}

func TestAutocompleteOfAGuildVerbInADMOffersNothing(t *testing.T) {
	s := newScene(t)
	s.createExchange(t, s.a, "Winter")

	got := s.complete(t, s.b, false, "join", "")

	if len(got) != 0 {
		t.Errorf("choices = %v, want none", got)
	}
}

func TestAutocompleteInAGuildLeavesOutTheExchangesOfOtherGuilds(t *testing.T) {
	s := newScene(t)
	s.drawnExchange(t, "Winter")
	other := s.otherGuildWith(t, s.b)

	in := fakediscord.Slash{Path: "/ge/recipient", Guild: &other, Options: map[string]any{"exchange": ""}}
	choices, err := s.fake.Autocomplete(t.Context(), s.users[s.b], in, "exchange")
	if err != nil {
		t.Fatalf("Autocomplete: %v", err)
	}

	if len(choices) != 0 {
		t.Errorf("choices = %v, want none from another guild", choices)
	}
}

func TestTypedTextAndNamesStayOutOfTheLogs(t *testing.T) {
	s := newScene(t)
	s.createExchange(t, s.a, "Winter")
	typed := "needle-typed-text"

	s.complete(t, s.b, true, "join", typed)
	slashOrFail(t, s.harness, s.b, s.ge("join", true, map[string]any{"exchange": typed}))
	slashOrFail(t, s.harness, s.b, s.ge("list", true, nil))
	s.waitInteractionLogs(t, 3)

	logs := s.logs.String()
	for _, secret := range []string{typed, "Winter", guildName, s.a, s.b, guildID} {
		if strings.Contains(logs, secret) {
			t.Errorf("logs hold %q", secret)
		}
	}
}

func TestActiveExchangesRankAheadOfRevealedAndRecipientAutoResolvesToTheOnlyDrawn(t *testing.T) {
	s := newScene(t)
	autumn := s.drawnExchange(t, "Last Autumn")
	if err := s.svc.Reveal(t.Context(), s.people[s.a].actor, s.group, autumn); err != nil {
		t.Fatalf("Reveal: %v", err)
	}
	spring := s.drawnExchange(t, "Spring Swap")

	listReply := replyTo(t, s.harness, slashOrFail(t, s.harness, s.b, s.ge("list", true, nil)))
	lines := strings.Split(listReply.description, "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "Spring Swap") || !strings.Contains(lines[1], "Last Autumn") {
		t.Errorf("list lines = %q, want Spring Swap before Last Autumn", listReply.description)
	}

	wantMulti := []string{"Spring Swap (drawn)", "Last Autumn (revealed)"}
	for _, verb := range []string{"recipient", "rules"} {
		if got := choiceNames(s.complete(t, s.b, true, verb, "")); !slices.Equal(got, wantMulti) {
			t.Errorf("autocomplete %s = %v, want %v", verb, got, wantMulti)
		}
	}
	wantDrawnOnly := []string{"Spring Swap (drawn)"}
	if got := choiceNames(s.complete(t, s.a, true, "cancel", "Spring Swap (drawn)")); !slices.Equal(got, wantDrawnOnly) {
		t.Errorf("autocomplete cancel with state suffix = %v, want %v", got, wantDrawnOnly)
	}

	wantDM := []string{"Spring Swap (drawn) - " + guildName, "Last Autumn (revealed) - " + guildName}
	if got := choiceNames(s.complete(t, s.b, false, "recipient", "")); !slices.Equal(got, wantDM) {
		t.Errorf("DM autocomplete recipient = %v, want %v", got, wantDM)
	}

	if err := s.fake.SetUserLocale(s.users[s.b], "es-CL"); err != nil {
		t.Fatalf("SetUserLocale: %v", err)
	}
	wantSpanish := []string{"Spring Swap (sorteado)", "Last Autumn (revelado)"}
	if got := choiceNames(s.complete(t, s.b, true, "recipient", "")); !slices.Equal(got, wantSpanish) {
		t.Errorf("Spanish autocomplete recipient = %v, want %v", got, wantSpanish)
	}
	if err := s.fake.SetUserLocale(s.users[s.b], "en-US"); err != nil {
		t.Fatalf("SetUserLocale: %v", err)
	}

	assignment, err := s.svc.MyAssignment(t.Context(), s.people[s.b].actor, spring)
	if err != nil {
		t.Fatalf("MyAssignment: %v", err)
	}
	wantRecipient := "<@" + assignment.Recipient.Identities[0].Subject + ">"
	recipReply := replyTo(t, s.harness, slashOrFail(t, s.harness, s.b, s.ge("recipient", true, nil)))
	if recipReply.description != wantRecipient {
		t.Errorf("recipient description = %q, want %q for Spring Swap", recipReply.description, wantRecipient)
	}

	summer := s.drawnExchange(t, "Summer Swap")
	twoDrawn := replyTo(t, s.harness, slashOrFail(t, s.harness, s.b, s.ge("recipient", true, nil)))
	if !strings.HasPrefix(twoDrawn.content, textChooseLead) {
		t.Errorf("recipient with two drawn exchanges = %q, want %q", twoDrawn.content, textChooseLead)
	}

	for _, id := range []giftexchange.ExchangeID{spring, summer} {
		if revealErr := s.svc.Reveal(t.Context(), s.people[s.a].actor, s.group, id); revealErr != nil {
			t.Fatalf("Reveal %s: %v", id, revealErr)
		}
	}
	zeroDrawn := replyTo(t, s.harness, slashOrFail(t, s.harness, s.b, s.ge("recipient", true, nil)))
	if !strings.HasPrefix(zeroDrawn.content, textChooseLead) {
		t.Errorf("recipient with zero drawn and three revealed exchanges = %q, want %q", zeroDrawn.content, textChooseLead)
	}
}

func TestActiveExchangeRanksFirstAheadOfMoreThanTwentyFiveRevealedExchanges(t *testing.T) {
	s := newScene(t)
	for i := 1; i <= 30; i++ {
		id := s.drawnExchange(t, fmt.Sprintf("Archive %02d", i))
		if err := s.svc.Reveal(t.Context(), s.people[s.a].actor, s.group, id); err != nil {
			t.Fatalf("Reveal Archive %02d: %v", i, err)
		}
	}

	zulu := s.createExchange(t, s.a, "Zulu Swap")
	for _, user := range []string{s.a, s.b, s.c} {
		s.joinAs(t, user, zulu)
	}

	rulesOpen := choiceNames(s.complete(t, s.b, true, "rules", ""))
	if len(rulesOpen) != 25 || rulesOpen[0] != "Zulu Swap (open)" {
		t.Errorf("rules autocomplete while open = %v, want 25 choices with Zulu Swap (open) first", rulesOpen)
	}

	if err := s.svc.Draw(t.Context(), s.people[s.a].actor, s.group, zulu); err != nil {
		t.Fatalf("Draw Zulu Swap: %v", err)
	}

	for _, verb := range []string{"rules", "recipient"} {
		got := choiceNames(s.complete(t, s.b, true, verb, ""))
		if len(got) != 25 || got[0] != "Zulu Swap (drawn)" || got[1] != "Archive 30 (revealed)" {
			t.Errorf("%s autocomplete = %v, want Zulu Swap (drawn) then Archive 30 (revealed)", verb, got)
		}
	}
}
