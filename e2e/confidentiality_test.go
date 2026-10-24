//go:build e2e

package e2e

import (
	"context"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
)

// Texts of the organizer's replies, copied from the adapter's texts so a change to them is seen
// here.
const (
	organizerDrawn    = "Draw done for **%s**."
	organizerRevealed = "**%s** is revealed."
	organizerReopened = "Removed. Too few participants or no valid draw remained, so the draw was undone and **%s** is open again."
)

// markersOf lists the pair markers (embed titles) that a record carries.
func markersOf(t *testing.T, record fakediscord.Record) []string {
	t.Helper()

	var markers []string
	for _, doc := range embedsOf(t, record.Embeds) {
		for _, title := range []string{titleRecipient, titleNewRecipient, titleGiver} {
			if doc.Title == title {
				markers = append(markers, title)
			}
		}
	}

	return markers
}

// latestPair returns the recipient that the newest recipient DM of the giver names.
func (s *scene) latestPair(t *testing.T, giver fakediscord.User) string {
	t.Helper()

	messages := s.fake.DMs(giver)
	for i := len(messages) - 1; i >= 0; i-- {
		isPair := hasTitle(t, messages[i], titleRecipient) || hasTitle(t, messages[i], titleNewRecipient)
		if !isPair {
			continue
		}
		match := mentionPattern.FindStringSubmatch(textOf(t, messages[i]))
		if match != nil {
			return match[1]
		}
	}
	t.Fatalf("%s has no recipient DM", giver.Name)

	return ""
}

func (s *scene) pairDMCount(t *testing.T, user fakediscord.User) int {
	t.Helper()

	return len(s.dmsWithTitle(t, user, titleRecipient)) + len(s.dmsWithTitle(t, user, titleNewRecipient))
}

// drawText has the organizer draw and returns the organizer's final reply.
func (s *scene) drawText(t *testing.T, organizer fakediscord.User, exchangeID string) string {
	t.Helper()

	prompt, confirm := s.confirmPrompt(t, organizer, "/ge/draw", map[string]any{"exchange": exchangeID}, drawConfirmPrefix)

	return s.click(t, organizer, prompt.message, confirm).text
}

func TestConfidentialityOutbound(t *testing.T) {
	t.Parallel()

	s := newGuild(t, 6)
	givers, organizer := s.members[:5], s.members[5]
	outsider := s.addMember("Gina")
	everyone := append([]fakediscord.User{}, givers...)
	exchange, name := s.setup(t, organizer, givers)

	var replies []struct{ got, want string }
	expect := func(got, want string) {
		replies = append(replies, struct{ got, want string }{got, want})
	}

	expect(s.drawText(t, organizer, exchange.id), strings.Replace(organizerDrawn, "%s", name, 1))
	round := s.pairsAfterDraw(t, givers)
	assertDerangement(t, round)

	// 5 -> 4: a patch, unless the leaver and their giver drew each other, which no patch can fix.
	var leaver fakediscord.User
	for _, giver := range givers {
		if round[round[giver.ID]] != giver.ID {
			leaver = giver
		}
	}
	var remaining []fakediscord.User
	for _, giver := range givers {
		if giver.ID != leaver.ID {
			remaining = append(remaining, giver)
		}
	}
	expect(s.remove(t, organizer, leaver, exchange.id).text, textOutcomePatched)
	waitFor(t, "the patch DM", func() bool {
		count := 0
		for _, giver := range remaining {
			count += len(s.dmsWithTitle(t, giver, titleNewRecipient))
		}

		return count == 1
	})

	// 4 -> 3: a redraw.
	before := map[string]int{}
	for _, giver := range remaining[:3] {
		before[giver.ID] = s.pairDMCount(t, giver)
	}
	expect(s.remove(t, organizer, remaining[3], exchange.id).text, textOutcomeRedrawn)
	final := map[string]string{}
	for _, giver := range remaining[:3] {
		waitFor(t, "the redraw DM of "+giver.Name, func() bool { return s.pairDMCount(t, giver) > before[giver.ID] })
		final[giver.ID] = s.latestPair(t, giver)
	}
	assertDerangement(t, final)

	expect(s.reveal(t, organizer, exchange.id).text, strings.Replace(organizerRevealed, "%s", name, 1))
	for _, giver := range remaining[:3] {
		recipient := final[giver.ID]
		inGuild := s.slash(t, giver, "/ge/recipient", map[string]any{"exchange": exchange.id})
		if !strings.Contains(inGuild.text, "<@"+recipient+">") {
			t.Errorf("/ge recipient of %s disagrees with the DM", giver.Name)
		}
		for _, other := range remaining[:3] {
			if other.ID != recipient {
				continue
			}
			named := s.awaitTitled(t, other, titleGiver, 1)
			if named != giver.ID {
				t.Errorf("the giver DM of a recipient names the wrong giver")
			}
			reply := s.slash(t, other, "/ge/giver", map[string]any{"exchange": exchange.id})
			if !strings.Contains(reply.text, "<@"+giver.ID+">") {
				t.Errorf("/ge giver disagrees with the revealed DM")
			}
		}
	}

	// A second exchange ends in a void.
	small := s.exchangeName("Spring exchange")
	voidCard, _ := s.createExchange(t, organizer, small)
	for _, giver := range givers[:3] {
		s.join(t, giver, voidCard.id)
	}
	expect(s.drawText(t, organizer, voidCard.id), strings.Replace(organizerDrawn, "%s", small, 1))
	expect(s.remove(t, organizer, givers[0], voidCard.id).text, strings.Replace(organizerReopened, "%s", small, 1))

	for _, pair := range replies {
		if pair.got != pair.want {
			t.Errorf("organizer reply = %q, want %q", pair.got, pair.want)
		}
	}

	everyone = append(everyone, organizer, outsider)
	idsInScene := map[string]bool{}
	for _, user := range everyone {
		idsInScene[user.ID] = true
	}
	counterparts := counterpartsOf(t, s, everyone)
	for _, record := range shared.fake.Outbound() {
		markers := markersOf(t, record)
		if len(markers) == 0 {
			assertNoPlainPair(t, record, counterparts)

			continue
		}

		if len(record.VisibleTo) != 1 {
			t.Errorf("a record with a pair marker is visible to %d users", len(record.VisibleTo))

			continue
		}
		isPublic := record.GuildID != nil && !record.Ephemeral
		if isPublic {
			t.Error("a public record carries a pair marker")
		}
		viewer := record.VisibleTo[0]
		if viewer == organizer.ID || viewer == outsider.ID {
			t.Errorf("a pair marker reached the non-participant %s", map[bool]string{true: "organizer", false: "outsider"}[viewer == organizer.ID])
		}
		named := mentionPattern.FindStringSubmatch(contentOf(record))
		if named == nil {
			continue
		}
		if named[1] == viewer {
			t.Error("a pair marker names its own viewer")
		}
		isGiverMarker := len(markers) == 1 && markers[0] == titleGiver
		if isGiverMarker && !counterparts.recipientsOf[named[1]][viewer] {
			t.Error("a giver marker reached someone who is not the recipient of the giver it names")
		}
	}
}

// pairCounterparts holds, from the pair DMs, who each user was told is their recipient and
// everyone each user is paired with in either direction.
type pairCounterparts struct {
	recipientsOf map[string]map[string]bool
	all          map[string]map[string]bool
}

// counterpartsOf reads the pair DMs of every user.
func counterpartsOf(t *testing.T, s *scene, users []fakediscord.User) pairCounterparts {
	t.Helper()

	found := pairCounterparts{recipientsOf: map[string]map[string]bool{}, all: map[string]map[string]bool{}}
	link := func(from, to string) {
		if found.all[from] == nil {
			found.all[from] = map[string]bool{}
		}
		found.all[from][to] = true
	}
	for _, user := range users {
		found.recipientsOf[user.ID] = map[string]bool{}
		messages := append(s.dmsWithTitle(t, user, titleRecipient), s.dmsWithTitle(t, user, titleNewRecipient)...)
		for _, message := range messages {
			match := mentionPattern.FindStringSubmatch(textOf(t, message))
			if match == nil {
				continue
			}
			found.recipientsOf[user.ID][match[1]] = true
			link(user.ID, match[1])
			link(match[1], user.ID)
		}
	}

	return found
}

func contentOf(record fakediscord.Record) string {
	content := record.Content
	for _, embed := range record.Embeds {
		content += string(embed)
	}

	return content
}

// assertNoPlainPair fails when a record without a pair marker, visible to one user, mentions
// someone that user is paired with.
func assertNoPlainPair(t *testing.T, record fakediscord.Record, found pairCounterparts) {
	t.Helper()

	if len(record.VisibleTo) != 1 {
		return
	}
	viewer := record.VisibleTo[0]
	for _, match := range mentionPattern.FindAllStringSubmatch(contentOf(record), -1) {
		if found.all[viewer][match[1]] {
			t.Errorf("a private record without a pair marker names the counterpart of its viewer: %s", record.Source)
		}
	}
}

var (
	uuidPattern      = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	timestampPattern = regexp.MustCompile(`<t:\d+(:[a-zA-Z])?>`)
)

// viewOf is everything the user can see about one guild after normalizing what differs between
// two exchanges that only differ by their draw: IDs, names and times.
func viewOf(t *testing.T, s *scene, user fakediscord.User, name string) []string {
	t.Helper()

	messages := s.fake.Visible(user, s.guild.ChannelID)
	messages = append(messages, s.fake.DMs(user)...)
	view := make([]string, 0, len(messages))
	for _, message := range messages {
		raw := message.Content
		for _, embed := range message.Embeds {
			raw += "|" + string(embed)
		}
		for _, component := range message.Components {
			raw += "|" + string(component)
		}
		raw += "|" + message.EphemeralFor
		raw = strings.ReplaceAll(raw, name, "<exchange>")
		raw = strings.ReplaceAll(raw, s.guild.Name, "<guild>")
		raw = uuidPattern.ReplaceAllString(raw, "<id>")
		raw = timestampPattern.ReplaceAllString(raw, "<time>")
		view = append(view, raw)
	}

	return view
}

// runDrawScript has the organizer and the outsider do the same things in a guild, and returns
// the pairs of the draw with what each of them saw.
func runDrawScript(
	t *testing.T, s *scene, organizer, outsider fakediscord.User, givers []fakediscord.User,
) (map[string]string, []string, []string) {
	t.Helper()

	exchange, name := s.setup(t, organizer, givers)
	s.rulesView(t, outsider, exchange.id)
	s.slash(t, outsider, "/ge/list", nil)
	s.draw(t, organizer, exchange.id)
	pairs := s.pairsAfterDraw(t, givers)
	s.slash(t, organizer, "/ge/list", nil)
	s.rulesView(t, organizer, exchange.id)
	s.rulesView(t, outsider, exchange.id)
	s.slash(t, outsider, "/ge/recipient", map[string]any{"exchange": exchange.id})
	s.slash(t, organizer, "/ge/recipient", map[string]any{"exchange": exchange.id})
	s.slash(t, outsider, "/ge/giver", map[string]any{"exchange": exchange.id})

	return pairs, viewOf(t, s, organizer, name), viewOf(t, s, outsider, name)
}

func TestViewIndependentOfDraw(t *testing.T) {
	t.Parallel()

	first := newGuild(t, 6)
	givers := first.members
	organizer := first.addMember("Gael")
	outsider := first.addMember("Hana")
	wantPairs, wantOrganizer, wantOutsider := runDrawScript(t, first, organizer, outsider, givers)

	everyone := append(append([]fakediscord.User{}, givers...), organizer, outsider)
	for try := 1; try <= 5; try++ {
		other := newGuildFor(everyone)
		pairs, seenByOrganizer, seenByOutsider := runDrawScript(t, other, organizer, outsider, givers)
		if reflect.DeepEqual(pairs, wantPairs) {
			continue
		}

		if !reflect.DeepEqual(seenByOrganizer, wantOrganizer) {
			t.Errorf("the organizer's view depends on the draw:\n%q\n%q", seenByOrganizer, wantOrganizer)
		}
		if !reflect.DeepEqual(seenByOutsider, wantOutsider) {
			t.Errorf("the outsider's view depends on the draw:\n%q\n%q", seenByOutsider, wantOutsider)
		}
		if len(wantOrganizer) == 0 || len(wantOutsider) == 0 {
			t.Error("the compared views are empty")
		}

		return
	}
	t.Fatal("five draws gave the same pairs; the comparison proves nothing")
}

func TestMentionsPingNobody(t *testing.T) {
	t.Parallel()

	s := newGuild(t, 4)
	givers, victim := s.members[:3], s.members[3]
	name := s.exchangeName("@everyone @here <@" + victim.ID + ">")
	exchange, _ := s.createExchange(t, givers[0], name)
	for _, giver := range givers {
		s.join(t, giver, exchange.id)
	}
	s.slash(t, givers[0], "/ge/list", nil)
	s.rulesView(t, givers[1], exchange.id)
	renamed := s.exchangeName("@here <@" + victim.ID + "> @everyone")
	s.slash(t, givers[0], "/ge/rename", map[string]any{"name": renamed, "exchange": exchange.id})
	s.draw(t, givers[0], exchange.id)
	pairs := s.pairsAfterDraw(t, givers)

	question := "@everyone @here <@" + victim.ID + "> Do you like tea?"
	created.add("question text", question)
	s.slash(t, givers[1], "/ge/ask", map[string]any{"question": question, "exchange": exchange.id})
	recipient, found := userIn(givers, pairs[givers[1].ID])
	if !found {
		t.Fatal("the recipient is not a participant")
	}
	received := s.awaitQuestion(t, recipient)
	result := s.clickResult(t, recipient, received, idWithPrefix(t, received, "/gx/answer/"))
	answer := "marker @everyone <@" + victim.ID + ">"
	s.submit(t, recipient, result.Modal, map[string]string{"answer": answer})
	ctx, cancel := context.WithTimeout(t.Context(), waitDeadline)
	defer cancel()
	_, err := s.fake.WaitDM(ctx, givers[1], func(m fakediscord.Message) bool {
		return strings.Contains(textOf(t, m), "marker")
	})
	if err != nil {
		t.Fatalf("the giver's DM never carried the answer: %v", err)
	}
	s.slash(t, givers[1], "/ge/questions", map[string]any{"exchange": exchange.id})

	assertNoPings(t)
}

// recordTextOf returns the text of the first message the bot sent for the interaction.
func (s *scene) recordTextOf(t *testing.T, result fakediscord.Result) string {
	t.Helper()

	var text string
	waitFor(t, "the bot's answer to the interaction", func() bool {
		for _, record := range s.fake.Outbound() {
			isOwn := record.InteractionID != nil && *record.InteractionID == result.InteractionID
			if !isOwn {
				continue
			}
			parts := []string{record.Content}
			for _, doc := range embedsOf(t, record.Embeds) {
				parts = append(parts, doc.Title, doc.Description)
			}
			text = strings.Join(parts, "\n")

			return true
		}

		return false
	})

	return text
}

// awaitQuestion waits for the anonymous question DM of the user.
func (s *scene) awaitQuestion(t *testing.T, user fakediscord.User) fakediscord.Message {
	t.Helper()

	var found fakediscord.Message
	waitFor(t, "the question DM", func() bool {
		for _, message := range s.fake.DMs(user) {
			if strings.Contains(textOf(t, message), "Anonymous question") {
				found = message

				return true
			}
		}

		return false
	})

	return found
}

// assertNoPings checks every record the fake holds: the bot sent explicit empty allowed
// mentions and nobody was pinged.
func assertNoPings(t *testing.T) {
	t.Helper()

	records := shared.fake.Outbound()
	if len(records) == 0 {
		t.Fatal("the fake holds no outbound record")
	}
	for _, record := range records {
		sent := record.AllowedMentionsSent
		if sent == nil {
			t.Errorf("record %d (%s %s) sent no allowed_mentions", record.Seq, record.Source, record.Route)

			continue
		}
		isEmpty := sent.Parse != nil && sent.Roles != nil && sent.Users != nil &&
			len(sent.Parse) == 0 && len(sent.Roles) == 0 && len(sent.Users) == 0 && !sent.RepliedUser
		if !isEmpty {
			t.Errorf("record %d (%s %s) allows mentions: %+v", record.Seq, record.Source, record.Route, *sent)
		}
		if len(record.Pings) != 0 {
			t.Errorf("record %d (%s %s) pinged %d", record.Seq, record.Source, record.Route, len(record.Pings))
		}
	}
}

func TestForgedInteractions(t *testing.T) {
	t.Parallel()

	home := newGuild(t, 4)
	away := newGuild(t, 2)
	organizer, givers := home.members[0], home.members[1:]
	exchange, _ := home.setup(t, organizer, givers)
	awayCard, _ := away.setup(t, away.members[0], nil)
	help := home.slash(t, givers[0], "/ge/help", nil)
	orgHelp := home.slash(t, organizer, "/ge/help", nil)

	dmTotal := func() int {
		total := 0
		for _, user := range append(append([]fakediscord.User{}, home.members...), away.members...) {
			total += len(home.fake.DMs(user))
		}

		return total
	}
	baseline := dmTotal()

	for _, action := range []string{"draw-confirm", "reveal-confirm", "cancel-confirm"} {
		got := home.clickForged(t, givers[0], help.message, "/gx/"+action+"/"+exchange.id)
		if !strings.Contains(got.text, textForbidden) {
			t.Errorf("a participant on %s = %q, want %q", action, got.text, textForbidden)
		}
	}
	option := home.pickForged(t, givers[0], help.message, "/gx/rule-option/"+exchange.id+"/dropout_policy", "redraw")
	if !strings.Contains(option.text, textForbidden) {
		t.Errorf("a participant setting a rule = %q, want %q", option.text, textForbidden)
	}

	foreign := []string{"draw-confirm", "cancel-confirm", "rules", "join"}
	for _, action := range foreign {
		got := home.clickForged(t, organizer, orgHelp.message, "/gx/"+action+"/"+awayCard.id)
		if !strings.Contains(got.text, textNotFound) {
			t.Errorf("%s of another guild's exchange = %q, want %q", action, got.text, textNotFound)
		}
	}
	for _, id := range []string{"/gx/draw-confirm/not-a-uuid", "/gx/cancel-confirm/" + strings.Repeat("a", 60)} {
		got := home.clickForged(t, organizer, orgHelp.message, id)
		if !strings.Contains(got.text, textNotFound) {
			t.Errorf("malformed %q = %q, want %q", id, got.text, textNotFound)
		}
	}
	if view := away.rulesView(t, away.members[0], awayCard.id); !strings.Contains(view.text, "State: open") {
		t.Errorf("the other guild's exchange changed: %q", view.text)
	}
	if view := home.rulesView(t, organizer, exchange.id); !strings.Contains(view.text, "State: open") {
		t.Errorf("the forged confirms changed the exchange: %q", view.text)
	}
	if got := dmTotal(); got != baseline {
		t.Errorf("forged interactions produced %d DM(s)", got-baseline)
	}

	home.draw(t, organizer, exchange.id)
	pairs := home.pairsAfterDraw(t, givers)
	asker := givers[0]
	recipient, found := userIn(givers, pairs[asker.ID])
	if !found {
		t.Fatal("the recipient is not a participant")
	}
	var third fakediscord.User
	for _, giver := range givers {
		if giver.ID != asker.ID && giver.ID != recipient.ID {
			third = giver
		}
	}
	home.slash(t, asker, "/ge/ask", map[string]any{"question": "Do you like tea?", "exchange": exchange.id})
	question := home.awaitQuestion(t, recipient)
	baseline = dmTotal()

	opened := home.clickForgedResult(t, third, question, idWithPrefix(t, question, "/gx/answer/"))
	if opened.Modal == nil {
		t.Fatal("the answer button opened no modal")
	}
	submitted, err := home.fake.SubmitModal(t.Context(), third, *opened.Modal, map[string]string{"answer": "Not mine"})
	if err != nil {
		t.Fatalf("submit the forged answer: %v", err)
	}
	stolen := home.recordTextOf(t, submitted)
	isRefusal := strings.Contains(stolen, textForbidden) || strings.Contains(stolen, textNotFound)
	if strings.Contains(stolen, "Your answer:") || !isRefusal {
		t.Errorf("someone else's answer = %q, want a refusal", stolen)
	}
	if got := dmTotal(); got != baseline {
		t.Errorf("a forged answer produced %d DM(s)", got-baseline)
	}
	listed := home.slash(t, asker, "/ge/questions", map[string]any{"exchange": exchange.id})
	if got := fieldValue(t, listed.message, "Questions you asked"); strings.Contains(got, "Not mine") {
		t.Errorf("the forged answer was stored: %q", got)
	}
}
