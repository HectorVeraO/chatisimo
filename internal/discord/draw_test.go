//go:build !race

// These tests open a real disgo Gateway client; see gateway_harness_test.go for why they are
// left out of the race run.

package discord_test

import (
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/disgoorg/disgo/discord"

	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

const (
	drawCallbackDeferredUpdate = 6

	drawTextPrompt        = "Draw **Winter exchange** now with 3 participants? This can't be undone."
	drawTextDrawn         = "Draw done for **Winter exchange**."
	drawTextDrawnPublic   = "The draw for **Winter exchange** is done. Each giver got a DM with their recipient, or can use /ge recipient."
	drawTextConfirmReveal = "Reveal **Winter exchange** now? Each participant will learn their giver."
	drawTextRevealed      = "**Winter exchange** is revealed."
	drawTextRevealedPub   = "**Winter exchange** is revealed. Each participant got a DM with their giver, or can use /ge giver."
	drawTextForbidden     = "You are not allowed to do that in this exchange."
	drawTextNotFound      = "That exchange does not exist here."
	drawTextNotMember     = "You are not in this exchange."
	drawTextTooFew        = "At least 3 participants are needed to draw."
	drawTextInfeasible    = "No valid draw exists with the current exclusions: 1 givers have no possible recipient. " +
		"Remove some exclusions and try again."
	drawTextStateOpen      = "Not possible while the exchange is open."
	drawTextStateDrawn     = "Not possible while the exchange is drawn."
	drawTextStateRevealed  = "Not possible while the exchange is revealed."
	drawTextStateCancelled = "Not possible while the exchange is cancelled."
)

// drawMember is a user added to the harness guild who is not one of the harness users.
type drawMember struct {
	user   fakediscord.User
	person person
}

// drawNewMember adds a user to guild and resolves them in the module.
func drawNewMember(t *testing.T, h *harness, guild fakediscord.Guild) drawMember {
	t.Helper()

	id := uniqueSnowflake(t)
	user := h.fake.AddUser(fakediscord.User{ID: id, Name: "drawer-" + id[len(id)-4:]})
	h.fake.AddMember(guild, user)
	h.users[id] = user
	h.secrets = append(h.secrets, id)

	actor, err := h.svc.ResolveActor(t.Context(), giftexchange.IdentityRef{
		Provider: giftexchange.ProviderDiscord, Subject: id,
	})
	if err != nil {
		t.Fatalf("ResolveActor: %v", err)
	}

	return drawMember{user: user, person: person{actor: actor, id: actor.Person()}}
}

// drawOpenExchange creates an exchange organized by organizer, who does not join, and joins
// the three harness users.
func drawOpenExchange(t *testing.T, h *harness, organizer drawMember) giftexchange.ExchangeID {
	t.Helper()

	view, err := h.svc.CreateExchange(t.Context(), organizer.person.actor, giftexchange.CreateExchangeRequest{
		Group: h.group,
		Name:  exchangeName,
	})
	if err != nil {
		t.Fatalf("CreateExchange: %v", err)
	}
	for _, subject := range []string{userA, userB, userC} {
		joinErr := h.svc.Join(t.Context(), h.people[subject].actor, h.group, view.ID)
		if joinErr != nil {
			t.Fatalf("Join: %v", joinErr)
		}
	}

	return view.ID
}

// drawDrawnExchange creates an exchange and draws it through the module.
func drawDrawnExchange(t *testing.T, h *harness, organizer drawMember) giftexchange.ExchangeID {
	t.Helper()

	id := drawOpenExchange(t, h, organizer)
	err := h.svc.Draw(t.Context(), organizer.person.actor, h.group, id)
	if err != nil {
		t.Fatalf("Draw: %v", err)
	}
	h.markDrawn(id)

	return id
}

func drawRevealedExchange(t *testing.T, h *harness, organizer drawMember) giftexchange.ExchangeID {
	t.Helper()

	id := drawDrawnExchange(t, h, organizer)
	err := h.svc.Reveal(t.Context(), organizer.person.actor, h.group, id)
	if err != nil {
		t.Fatalf("Reveal: %v", err)
	}

	return id
}

// drawWait waits for the record of the given source that answers the interaction.
func drawWait(t *testing.T, h *harness, result fakediscord.Result, source string) fakediscord.Record {
	t.Helper()

	var found fakediscord.Record
	waitFor(t, "the "+source+" of the interaction", func() bool {
		for _, record := range drawRecordsOf(h, result, source) {
			found = record
			return true
		}

		return false
	})

	return found
}

func drawRecordsOf(h *harness, result fakediscord.Result, source string) []fakediscord.Record {
	var records []fakediscord.Record
	for _, record := range h.fake.Outbound() {
		isOurs := record.Source == source && record.InteractionID != nil && *record.InteractionID == result.InteractionID
		if isOurs {
			records = append(records, record)
		}
	}

	return records
}

func drawEmbedOf(t *testing.T, record fakediscord.Record) (string, string) {
	t.Helper()

	if len(record.Embeds) != 1 {
		t.Fatalf("record %d has %d embeds, want 1", record.Seq, len(record.Embeds))
	}
	var embed discord.Embed
	err := json.Unmarshal(record.Embeds[0], &embed)
	if err != nil {
		t.Fatalf("decode embed: %v", err)
	}

	return embed.Title, embed.Description
}

func drawDMCount(h *harness) int {
	count := 0
	for _, record := range h.fake.Outbound() {
		if record.Source == "dm" {
			count++
		}
	}

	return count
}

func drawWaitDelivered(t *testing.T, id giftexchange.ExchangeID, want int) {
	t.Helper()

	waitFor(t, "the notices to be delivered", func() bool {
		return finishedCount(t, id, "delivered") == want
	})
}

func drawState(t *testing.T, h *harness, organizer drawMember, id giftexchange.ExchangeID) giftexchange.State {
	t.Helper()

	view, err := h.svc.Exchange(t.Context(), organizer.person.actor, h.group, id)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}

	return view.State
}

// drawPrompt runs the verb as user with the exchange option and returns the prompt record.
func drawPrompt(t *testing.T, h *harness, user, verb string, id giftexchange.ExchangeID) fakediscord.Record {
	t.Helper()

	result := slashOrFail(t, h, user, h.ge(verb, true, map[string]any{"exchange": id.String()}))
	return drawWait(t, h, result, "edit")
}

// drawClick presses the button of the prompt as user and returns the result.
func drawClick(t *testing.T, h *harness, user string, prompt fakediscord.Record, customID string) fakediscord.Result {
	t.Helper()

	result, err := h.fake.Click(t.Context(), h.users[user], fakediscord.Message{ID: prompt.MessageID}, customID)
	if err != nil {
		t.Fatalf("Click %s: %v", customID, err)
	}

	return result
}

// drawForged sends the custom ID from a message the user can see, as a forged click.
func drawForged(t *testing.T, h *harness, user string, in fakediscord.Slash, customID string) fakediscord.Record {
	t.Helper()

	card := slashOrFail(t, h, user, in)
	if card.Message == nil {
		t.Fatal("the card is not a message")
	}
	result, err := h.fake.ClickForged(t.Context(), h.users[user], *card.Message, customID)
	if err != nil {
		t.Fatalf("ClickForged %s: %v", customID, err)
	}

	// A custom ID the handler refuses is answered before any defer, as a direct message.
	if result.Message != nil {
		return fakediscord.Record{Content: result.Message.Content}
	}

	return drawWait(t, h, result, "edit")
}

func TestDrawPromptsThenConfirmDrawsAndAnnouncesWithoutAPair(t *testing.T) {
	h := newHarness(t)
	h.startRunner(t, h.world)
	organizer := drawNewMember(t, h, h.guild)
	id := drawOpenExchange(t, h, organizer)
	h.markDrawn(id)

	prompt := drawPrompt(t, h, organizer.user.ID, "draw", id)

	if prompt.Content != drawTextPrompt {
		t.Errorf("prompt = %q, want %q", prompt.Content, drawTextPrompt)
	}
	if !slices.Equal(prompt.VisibleTo, []string{organizer.user.ID}) {
		t.Errorf("prompt is visible to %v, want the organizer only", prompt.VisibleTo)
	}
	components := string(slices.Concat(prompt.Components...))
	for _, want := range []string{"/gx/draw-confirm/" + id.String(), "/gx/dismiss/" + id.String()} {
		if !strings.Contains(components, want) {
			t.Errorf("prompt lacks the button %q: %s", want, components)
		}
	}
	if got := drawState(t, h, organizer, id); got != giftexchange.StateOpen {
		t.Fatalf("state after the prompt = %q, want open", got)
	}

	result := drawClick(t, h, organizer.user.ID, prompt, "/gx/draw-confirm/"+id.String())

	if result.CallbackType != drawCallbackDeferredUpdate {
		t.Errorf("callback type = %d, want 6", result.CallbackType)
	}
	edit := drawWait(t, h, result, "edit")
	if edit.Content != drawTextDrawn || len(edit.Embeds) != 0 {
		t.Errorf("edit = %q with %d embeds, want %q", edit.Content, len(edit.Embeds), drawTextDrawn)
	}
	followup := drawWait(t, h, result, "followup")
	if followup.Content != drawTextDrawnPublic || followup.Ephemeral || len(followup.Embeds) != 0 {
		t.Errorf("followup = %+v, want the public text only", followup)
	}
	if got := drawState(t, h, organizer, id); got != giftexchange.StateDrawn {
		t.Errorf("state after the confirm = %q, want drawn", got)
	}

	drawWaitDelivered(t, id, 3)
	seenRecipients := map[string]bool{}
	for _, subject := range []string{userA, userB, userC} {
		dms := h.fake.DMs(h.users[subject])
		if len(dms) != 1 {
			t.Fatalf("giver %s has %d DMs, want 1", subject, len(dms))
		}
		title, description := embedOf(t, dms[0])
		others := drawMentionsOtherThan(subject)
		isOtherParticipant := slices.Contains(others, description)
		if title != "Your recipient" || !isOtherParticipant {
			t.Errorf("DM = %q %q, want a recipient among %v", title, description, others)
		}
		seenRecipients[description] = true
	}
	if len(seenRecipients) != 3 {
		t.Errorf("distinct recipients = %d, want 3 (every participant receives once)", len(seenRecipients))
	}
	if got := len(h.fake.DMs(organizer.user)); got != 0 {
		t.Errorf("the organizer who did not join got %d DMs, want none", got)
	}
}

func TestDrawConfirmTwiceChangesNothingTheSecondTime(t *testing.T) {
	h := newHarness(t)
	h.startRunner(t, h.world)
	organizer := drawNewMember(t, h, h.guild)
	id := drawOpenExchange(t, h, organizer)
	h.markDrawn(id)
	prompt := drawPrompt(t, h, organizer.user.ID, "draw", id)
	first := drawClick(t, h, organizer.user.ID, prompt, "/gx/draw-confirm/"+id.String())
	drawWait(t, h, first, "followup")
	drawWaitDelivered(t, id, 3)
	dmsBefore := drawDMCount(h)

	cardRecord := drawForged(t, h, organizer.user.ID, h.ge("help", true, nil), "/gx/draw-confirm/"+id.String())

	if cardRecord.Content != drawTextStateDrawn {
		t.Errorf("second confirm = %q, want %q", cardRecord.Content, drawTextStateDrawn)
	}
	publics := 0
	for _, record := range h.fake.Outbound() {
		if record.Content == drawTextDrawnPublic {
			publics++
		}
	}
	if publics != 1 {
		t.Errorf("%d public draw messages, want 1", publics)
	}
	if got := drawDMCount(h); got != dmsBefore {
		t.Errorf("DM records went from %d to %d, want no new DM", dmsBefore, got)
	}
}

func TestDrawConfirmAfterCancelAnswersTheState(t *testing.T) {
	h := newHarness(t)
	organizer := drawNewMember(t, h, h.guild)
	id := drawOpenExchange(t, h, organizer)
	prompt := drawPrompt(t, h, organizer.user.ID, "draw", id)
	cancelErr := h.svc.Cancel(t.Context(), organizer.person.actor, h.group, id)
	if cancelErr != nil {
		t.Fatalf("Cancel: %v", cancelErr)
	}

	result := drawClick(t, h, organizer.user.ID, prompt, "/gx/draw-confirm/"+id.String())

	got := drawWait(t, h, result, "edit")
	if got.Content != drawTextStateCancelled {
		t.Errorf("reply = %q, want %q", got.Content, drawTextStateCancelled)
	}
	if followups := drawRecordsOf(h, result, "followup"); len(followups) != 0 {
		t.Errorf("a followup was posted: %+v", followups)
	}
}

func TestDrawWithTooFewParticipantsAnswersTooFew(t *testing.T) {
	h := newHarness(t)
	organizer := drawNewMember(t, h, h.guild)
	view, err := h.svc.CreateExchange(t.Context(), organizer.person.actor, giftexchange.CreateExchangeRequest{
		Group: h.group, Name: exchangeName,
	})
	if err != nil {
		t.Fatalf("CreateExchange: %v", err)
	}
	for _, subject := range []string{userA, userB} {
		joinErr := h.svc.Join(t.Context(), h.people[subject].actor, h.group, view.ID)
		if joinErr != nil {
			t.Fatalf("Join: %v", joinErr)
		}
	}
	prompt := drawPrompt(t, h, organizer.user.ID, "draw", view.ID)

	result := drawClick(t, h, organizer.user.ID, prompt, "/gx/draw-confirm/"+view.ID.String())

	got := drawWait(t, h, result, "edit")
	if got.Content != drawTextTooFew {
		t.Errorf("reply = %q, want %q", got.Content, drawTextTooFew)
	}
	if state := drawState(t, h, organizer, view.ID); state != giftexchange.StateOpen {
		t.Errorf("state = %q, want open", state)
	}
}

func TestDrawWithInfeasibleExclusionsAnswersTheStrandedCount(t *testing.T) {
	h := newHarness(t)
	organizer := drawNewMember(t, h, h.guild)
	id := drawOpenExchange(t, h, organizer)
	for _, recipient := range []string{userB, userC} {
		excludeErr := h.svc.AddExclusion(t.Context(), organizer.person.actor, h.group, id,
			h.people[userA].id, h.people[recipient].id, false)
		if excludeErr != nil {
			t.Fatalf("AddExclusion: %v", excludeErr)
		}
	}
	prompt := drawPrompt(t, h, organizer.user.ID, "draw", id)

	result := drawClick(t, h, organizer.user.ID, prompt, "/gx/draw-confirm/"+id.String())

	got := drawWait(t, h, result, "edit")
	if got.Content != drawTextInfeasible {
		t.Errorf("reply = %q, want %q", got.Content, drawTextInfeasible)
	}
	if state := drawState(t, h, organizer, id); state != giftexchange.StateOpen {
		t.Errorf("state = %q, want open", state)
	}
}

func TestDrawTwoConfirmsAtOnceDrawOnce(t *testing.T) {
	h := newHarness(t)
	h.startRunner(t, h.world)
	organizer := drawNewMember(t, h, h.guild)
	id := drawOpenExchange(t, h, organizer)
	h.markDrawn(id)
	prompt := drawPrompt(t, h, organizer.user.ID, "draw", id)

	var wg sync.WaitGroup
	results := make([]fakediscord.Result, 2)
	errs := make([]error, 2)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()

			results[i], errs[i] = h.fake.Click(t.Context(), h.users[organizer.user.ID],
				fakediscord.Message{ID: prompt.MessageID}, "/gx/draw-confirm/"+id.String())
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("Click %d: %v", i, err)
		}
	}
	contents := []string{}
	for _, result := range results {
		contents = append(contents, drawWait(t, h, result, "edit").Content)
	}
	slices.Sort(contents)
	want := []string{drawTextDrawn, drawTextStateDrawn}
	slices.Sort(want)
	if !slices.Equal(contents, want) {
		t.Errorf("replies = %q, want %q", contents, want)
	}
	drawWaitDelivered(t, id, 3)
	publics := 0
	for _, record := range h.fake.Outbound() {
		if record.Content == drawTextDrawnPublic {
			publics++
		}
	}
	if publics != 1 {
		t.Errorf("%d public draw messages, want exactly 1", publics)
	}
}

func TestDrawRevealOutOfOrderAnswersTheState(t *testing.T) {
	h := newHarness(t)
	organizer := drawNewMember(t, h, h.guild)
	id := drawOpenExchange(t, h, organizer)

	byCommand := drawPrompt(t, h, organizer.user.ID, "reveal", id)
	byForgedButton := drawForged(t, h, organizer.user.ID, h.ge("help", true, nil), "/gx/reveal-confirm/"+id.String())
	recipient := drawPrompt(t, h, userB, "recipient", id)
	giver := drawPrompt(t, h, userB, "giver", id)

	for name, got := range map[string]string{
		"reveal command": byCommand.Content, "reveal confirm": byForgedButton.Content,
		"recipient": recipient.Content, "giver": giver.Content,
	} {
		if got != drawTextStateOpen {
			t.Errorf("%s = %q, want %q", name, got, drawTextStateOpen)
		}
	}
	if state := drawState(t, h, organizer, id); state != giftexchange.StateOpen {
		t.Errorf("state = %q, want open", state)
	}
}

func TestDrawRevealFlowRevealsAndAnnouncesWithoutAPair(t *testing.T) {
	h := newHarness(t)
	h.startRunner(t, h.world)
	organizer := drawNewMember(t, h, h.guild)
	id := drawDrawnExchange(t, h, organizer)
	drawWaitDelivered(t, id, 3)

	prompt := drawPrompt(t, h, organizer.user.ID, "reveal", id)
	if prompt.Content != drawTextConfirmReveal {
		t.Errorf("prompt = %q, want %q", prompt.Content, drawTextConfirmReveal)
	}
	result := drawClick(t, h, organizer.user.ID, prompt, "/gx/reveal-confirm/"+id.String())

	edit := drawWait(t, h, result, "edit")
	followup := drawWait(t, h, result, "followup")
	if edit.Content != drawTextRevealed {
		t.Errorf("edit = %q, want %q", edit.Content, drawTextRevealed)
	}
	if followup.Content != drawTextRevealedPub || followup.Ephemeral {
		t.Errorf("followup = %+v, want the public text", followup)
	}
	if state := drawState(t, h, organizer, id); state != giftexchange.StateRevealed {
		t.Errorf("state = %q, want revealed", state)
	}
	drawWaitDelivered(t, id, 6)
	for _, subject := range []string{userA, userB, userC} {
		dms := h.fake.DMs(h.users[subject])
		if len(dms) != 2 {
			t.Fatalf("participant %s has %d DMs, want the draw and the reveal", subject, len(dms))
		}
	}
}

func TestDrawRecipientAndGiverAnswerOnlyTheCaller(t *testing.T) {
	h := newHarness(t)
	organizer := drawNewMember(t, h, h.guild)
	id := drawRevealedExchange(t, h, organizer)

	for _, inGuild := range []bool{true, false} {
		for _, subject := range []string{userA, userB, userC} {
			assignment, err := h.svc.MyAssignment(t.Context(), h.people[subject].actor, id)
			if err != nil {
				t.Fatalf("MyAssignment: %v", err)
			}
			giver, err := h.svc.MyGiver(t.Context(), h.people[subject].actor, id)
			if err != nil {
				t.Fatalf("MyGiver: %v", err)
			}
			wantRecipient := drawMention(t, assignment.Recipient)
			drawAssertPairConsistent(t, h, subject, id, assignment.Recipient, giver.Giver)
			wantGiver := drawMention(t, giver.Giver)

			for verb, want := range map[string][2]string{
				"recipient": {"Your recipient", wantRecipient},
				"giver":     {"Your giver", wantGiver},
			} {
				result := slashOrFail(t, h, subject, h.ge(verb, inGuild, map[string]any{"exchange": id.String()}))
				record := drawWait(t, h, result, "edit")
				title, description := drawEmbedOf(t, record)

				if title != want[0] || description != want[1] {
					t.Errorf("%s guild=%v: embed = %q %q, want %q %q", verb, inGuild, title, description, want[0], want[1])
				}
				if !slices.Equal(record.VisibleTo, []string{subject}) {
					t.Errorf("%s guild=%v: visible to %v, want the caller only", verb, inGuild, record.VisibleTo)
				}
			}
		}
	}
}

var drawExpectedMentions = map[string]string{
	userA: "<@100000000000000001>",
	userB: "<@100000000000000002>",
	userC: "<@100000000000000003>",
}

func drawMention(t *testing.T, p giftexchange.PersonRef) string {
	t.Helper()

	if len(p.Identities) == 0 {
		t.Fatalf("person %+v has no identity", p)
	}
	subject := p.Identities[0].Subject
	mention, ok := drawExpectedMentions[subject]
	if !ok {
		t.Fatalf("no expected mention for subject %q", subject)
	}

	return mention
}

func drawMentionsOtherThan(subject string) []string {
	var others []string
	for other, mention := range drawExpectedMentions {
		if other != subject {
			others = append(others, mention)
		}
	}

	return others
}

func TestDrawGiverBeforeTheRevealAnswersTheState(t *testing.T) {
	h := newHarness(t)
	organizer := drawNewMember(t, h, h.guild)
	id := drawDrawnExchange(t, h, organizer)

	for _, inGuild := range []bool{true, false} {
		result := slashOrFail(t, h, userB, h.ge("giver", inGuild, map[string]any{"exchange": id.String()}))

		got := drawWait(t, h, result, "edit")
		if got.Content != drawTextStateDrawn || len(got.Embeds) != 0 {
			t.Errorf("guild=%v: reply = %q with %d embeds, want %q", inGuild, got.Content, len(got.Embeds), drawTextStateDrawn)
		}
	}
}

func TestDrawDeniesACallerWhoIsNotTheOrganizer(t *testing.T) {
	h := newHarness(t)
	organizer := drawNewMember(t, h, h.guild)
	open := drawOpenExchange(t, h, organizer)
	drawn := drawDrawnExchange(t, h, organizer)
	dmsBefore := drawDMCount(h)

	cases := map[string]fakediscord.Record{
		"draw command":   drawPrompt(t, h, userB, "draw", open),
		"reveal command": drawPrompt(t, h, userB, "reveal", drawn),
		"forged draw confirm": drawForged(t, h, userB, h.ge("help", true, nil),
			"/gx/draw-confirm/"+open.String()),
		"forged reveal confirm": drawForged(t, h, userB, h.ge("help", true, nil),
			"/gx/reveal-confirm/"+drawn.String()),
	}
	for name, record := range cases {
		if record.Content != drawTextForbidden || len(record.Components) != 0 {
			t.Errorf("%s = %q, want %q and no buttons", name, record.Content, drawTextForbidden)
		}
	}
	if state := drawState(t, h, organizer, open); state != giftexchange.StateOpen {
		t.Errorf("the open exchange is %q after the denied draws", state)
	}
	if state := drawState(t, h, organizer, drawn); state != giftexchange.StateDrawn {
		t.Errorf("the drawn exchange is %q after the denied reveals", state)
	}
	if got := drawDMCount(h); got != dmsBefore {
		t.Errorf("DM records went from %d to %d, want no new DM", dmsBefore, got)
	}
}

func TestDrawAnswersNotParticipantToAnOrganizerWhoDidNotJoin(t *testing.T) {
	h := newHarness(t)
	organizer := drawNewMember(t, h, h.guild)
	id := drawRevealedExchange(t, h, organizer)

	for _, verb := range []string{"recipient", "giver"} {
		record := drawPrompt(t, h, organizer.user.ID, verb, id)

		if record.Content != drawTextNotMember || len(record.Embeds) != 0 {
			t.Errorf("%s = %q, want %q", verb, record.Content, drawTextNotMember)
		}
	}
}

func TestDrawAnswersNotFoundToAnOutsiderInAnotherGuild(t *testing.T) {
	h := newHarness(t)
	organizer := drawNewMember(t, h, h.guild)
	id := drawDrawnExchange(t, h, organizer)
	other := h.fake.AddGuild(fakediscord.Guild{ID: uniqueSnowflake(t), Name: "Other server"})
	outsider := drawNewMember(t, h, other)
	dmsBefore := drawDMCount(h)
	inOther := fakediscord.Slash{Path: "/ge/help", Guild: &other}

	got := map[string]string{}
	for _, verb := range []string{"draw", "reveal", "recipient", "giver"} {
		result := slashOrFail(t, h, outsider.user.ID, fakediscord.Slash{
			Path: "/ge/" + verb, Guild: &other, Options: map[string]any{"exchange": id.String()},
		})
		got[verb] = drawWait(t, h, result, "edit").Content
	}
	got["forged draw confirm"] = drawForged(t, h, outsider.user.ID, inOther, "/gx/draw-confirm/"+id.String()).Content
	got["forged reveal confirm"] = drawForged(t, h, outsider.user.ID, inOther, "/gx/reveal-confirm/"+id.String()).Content

	for name, content := range got {
		if content != drawTextNotFound {
			t.Errorf("%s = %q, want %q", name, content, drawTextNotFound)
		}
	}
	if state := drawState(t, h, organizer, id); state != giftexchange.StateDrawn {
		t.Errorf("state = %q, want drawn", state)
	}
	if after := drawDMCount(h); after != dmsBefore {
		t.Errorf("DM records went from %d to %d, want no new DM", dmsBefore, after)
	}
}

func TestDrawAnswersNotFoundToAGuildMemberWithNoTie(t *testing.T) {
	h := newHarness(t)
	organizer := drawNewMember(t, h, h.guild)
	stranger := drawNewMember(t, h, h.guild)
	id := drawRevealedExchange(t, h, organizer)

	for _, verb := range []string{"recipient", "giver"} {
		record := drawPrompt(t, h, stranger.user.ID, verb, id)

		if record.Content != drawTextNotFound || len(record.Embeds) != 0 {
			t.Errorf("%s = %q, want %q", verb, record.Content, drawTextNotFound)
		}
	}
}

func TestDrawConfirmRefusesAForgedCustomID(t *testing.T) {
	h := newHarness(t)
	organizer := drawNewMember(t, h, h.guild)
	id := drawOpenExchange(t, h, organizer)

	for _, customID := range []string{
		"/gx/draw-confirm/not-a-uuid",
		"/gx/draw-confirm/" + strings.ToUpper(id.String()),
		"/gx/draw-confirm/" + id.String() + "/extra",
		"/gx/reveal-confirm/" + strings.Repeat("a", 120),
	} {
		record := drawForged(t, h, organizer.user.ID, h.ge("help", true, nil), customID)

		if record.Content != drawTextNotFound {
			t.Errorf("custom ID %q: reply = %q, want %q", customID, record.Content, drawTextNotFound)
		}
	}
	if state := drawState(t, h, organizer, id); state != giftexchange.StateOpen {
		t.Errorf("state = %q, want open", state)
	}
}

// The organizer who did not join sees only fixed texts and no embed, and every public record
// is one of the two announcements; the harness cleanup checks the pair records and the logs.
func TestDrawKeepsPairsOutOfPublicAndOrganizerReplies(t *testing.T) {
	h := newHarness(t)
	h.startRunner(t, h.world)
	organizer := drawNewMember(t, h, h.guild)
	id := drawOpenExchange(t, h, organizer)
	h.markDrawn(id)

	drawPrompt(t, h, organizer.user.ID, "recipient", id)
	prompt := drawPrompt(t, h, organizer.user.ID, "draw", id)
	drawWait(t, h, drawClick(t, h, organizer.user.ID, prompt, "/gx/draw-confirm/"+id.String()), "followup")
	drawWaitDelivered(t, id, 3)
	reveal := drawPrompt(t, h, organizer.user.ID, "reveal", id)
	drawWait(t, h, drawClick(t, h, organizer.user.ID, reveal, "/gx/reveal-confirm/"+id.String()), "followup")
	drawWaitDelivered(t, id, 6)
	for _, subject := range []string{userA, userB, userC} {
		for _, verb := range []string{"recipient", "giver"} {
			drawPrompt(t, h, subject, verb, id)
		}
	}

	allowed := []string{
		drawTextNotMember, drawTextPrompt, drawTextDrawn, drawTextConfirmReveal, drawTextRevealed,
		drawTextStateOpen,
	}
	publicAllowed := []string{drawTextDrawnPublic, drawTextRevealedPub}
	for _, record := range h.fake.Outbound() {
		if record.Source == "callback" {
			continue
		}
		seenByOrganizer := slices.Contains(record.VisibleTo, organizer.user.ID)
		isPublic := !record.Ephemeral && record.Source != "dm"
		if seenByOrganizer && record.Source != "dm" && !isPublic {
			if !slices.Contains(allowed, record.Content) || len(record.Embeds) != 0 {
				t.Errorf("organizer record %d = %q with %d embeds, want a fixed text", record.Seq, record.Content, len(record.Embeds))
			}
		}
		if isPublic && record.Source == "followup" && !slices.Contains(publicAllowed, record.Content) {
			t.Errorf("public record %d = %q, want an announcement", record.Seq, record.Content)
		}
		hasSnowflake := snowflakePattern.MatchString(record.Content) && record.Source != "dm" && record.Source != "callback"
		if isPublic && hasSnowflake {
			t.Errorf("public record %d holds a snowflake: %q", record.Seq, record.Content)
		}
	}
}

// drawAssertPairConsistent checks the pair the module reports, so the reply test does not
// only echo it: nobody gives to themselves, and the giver of the caller gives to the caller.
func drawAssertPairConsistent(
	t *testing.T, h *harness, subject string, id giftexchange.ExchangeID, recipient, giver giftexchange.PersonRef,
) {
	t.Helper()

	recipientMention := drawMention(t, recipient)
	giverMention := drawMention(t, giver)
	own := drawExpectedMentions[subject]
	if recipientMention == own || giverMention == own {
		t.Errorf("%s is paired with themselves: recipient %s, giver %s", subject, recipientMention, giverMention)
	}

	giverSubject := giver.Identities[0].Subject
	theirs, err := h.svc.MyAssignment(t.Context(), h.people[giverSubject].actor, id)
	if err != nil {
		t.Fatalf("MyAssignment of the giver: %v", err)
	}
	if got := drawMention(t, theirs.Recipient); got != own {
		t.Errorf("the giver of %s gives to %s, want %s", subject, got, own)
	}
}

func TestDrawConfirmStaysDoneWhenTheEditOfThePromptFails(t *testing.T) {
	h := newHarness(t)
	h.startRunner(t, h.world)
	organizer := drawNewMember(t, h, h.guild)
	id := drawOpenExchange(t, h, organizer)
	h.markDrawn(id)
	prompt := drawPrompt(t, h, organizer.user.ID, "draw", id)

	failErr := h.fake.FailNext("PATCH /webhooks/{app}/{token}/messages/@original", 500, 0)
	if failErr != nil {
		t.Fatalf("FailNext: %v", failErr)
	}
	result := drawClick(t, h, organizer.user.ID, prompt, "/gx/draw-confirm/"+id.String())

	followup := drawWait(t, h, result, "followup")
	if followup.Content != drawTextDrawnPublic || followup.Ephemeral {
		t.Errorf("followup = %+v, want the public text", followup)
	}
	if state := drawState(t, h, organizer, id); state != giftexchange.StateDrawn {
		t.Errorf("state = %q, want drawn", state)
	}
	for _, record := range drawRecordsOf(h, result, "followup") {
		if record.Ephemeral {
			t.Errorf("an ephemeral error followup was sent: %+v", record)
		}
	}
}
