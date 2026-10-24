//go:build !race

// These tests open a real disgo Gateway client; see gateway_harness_test.go for why they are
// left out of the race run.

package discord_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"

	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

const (
	membershipName = "Winter exchange"

	membershipForbidden      = "You are not allowed to do that in this exchange."
	membershipNotParticipant = "You are not in this exchange."
	membershipCancelledText  = "Not possible while the exchange is cancelled."
	membershipBadTime        = "Use in 3d, in 2h30m, YYYY-MM-DD HH:MM (Santiago time) or an RFC 3339 time."
	membershipNothing        = "Nothing changed."
	membershipOwnRecipient   = "You can't remove your own recipient. They can leave, or you can cancel the exchange."
	membershipPatched        = "Removed. One giver got a new recipient by DM."
	membershipRedrawn        = "Removed. The draw was redone and every giver got a DM."
	membershipReopened       = "Removed. Too few participants or no valid draw remained, so the draw was undone and " +
		"**" + membershipName + "** is open again."
	membershipCreated = "You created **" + membershipName + "** and you organize it. " +
		"You are not a participant unless you join. Configure it with /ge rules."
	membershipLeft          = "You left **" + membershipName + "**."
	membershipConfirmLeave  = "**" + membershipName + "** is already drawn. Leave anyway? Your giver may get a new recipient."
	membershipConfirmCancel = "Cancel **" + membershipName + "**? This can't be undone."
	membershipCancelled     = "**" + membershipName + "** is cancelled."
	membershipCancelPublic  = "**" + membershipName + "** was cancelled by the organizer."
	membershipCancelDM      = "was cancelled by the organizer."

	callbackDeferredMessage = 5
	callbackDeferredUpdate  = 6
	callbackUpdateMessage   = 7
)

var membershipTimeField = regexp.MustCompile(`^<t:(\d+):F>$`)

// membershipExtraUser adds a fifth or sixth member to the scene's guild.
func membershipExtraUser(t *testing.T, s *scene) string {
	t.Helper()

	user := s.fake.AddUser(fakediscord.User{ID: uniqueSnowflake(t), Name: "scene-extra"})
	s.users[user.ID] = user
	s.fake.AddMember(s.guild, user)

	actor, err := s.svc.ResolveActor(t.Context(), giftexchange.IdentityRef{
		Provider: giftexchange.ProviderDiscord, Subject: user.ID,
	})
	if err != nil {
		t.Fatalf("ResolveActor: %v", err)
	}
	s.people[user.ID] = person{actor: actor, id: actor.Person()}

	return user.ID
}

// membershipDrawn creates an exchange organized by A that the given users joined, and draws it.
func membershipDrawn(t *testing.T, s *scene, joiners ...string) giftexchange.ExchangeID {
	t.Helper()

	id := s.createExchange(t, s.a, membershipName)
	for _, user := range joiners {
		s.joinAs(t, user, id)
	}
	err := s.svc.Draw(t.Context(), s.people[s.a].actor, s.group, id)
	if err != nil {
		t.Fatalf("Draw: %v", err)
	}
	return id
}

// membershipState is what a denied or refused interaction must leave as it was.
type membershipState struct {
	summaries map[string][]giftexchange.ExchangeSummary
	view      giftexchange.ExchangeView
	dms       int
}

func membershipSnapshot(t *testing.T, s *scene, id giftexchange.ExchangeID) membershipState {
	t.Helper()

	state := membershipState{summaries: map[string][]giftexchange.ExchangeSummary{}}
	for user := range s.people {
		state.summaries[user] = s.summaries(t, user)
	}
	for _, user := range s.users {
		state.dms += len(s.fake.DMs(user))
	}
	view, err := s.svc.Exchange(t.Context(), s.people[s.a].actor, s.group, id)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	state.view = view

	return state
}

func membershipUnchanged(t *testing.T, s *scene, id giftexchange.ExchangeID, before membershipState) {
	t.Helper()

	after := membershipSnapshot(t, s, id)
	if !reflect.DeepEqual(before, after) {
		t.Errorf("the state changed:\nbefore %+v\nafter  %+v", before, after)
	}
}

// membershipMessageOf returns the message the bot answered an interaction with, as the user sees it.
func membershipMessageOf(t *testing.T, s *scene, user string, result fakediscord.Result) fakediscord.Message {
	t.Helper()

	if result.Message != nil {
		return *result.Message
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
	for _, message := range s.fake.Visible(s.users[user], record.ChannelID) {
		if message.ID == record.MessageID {
			return message
		}
	}
	t.Fatalf("message %s is not visible to %s", record.MessageID, user)

	return fakediscord.Message{}
}

// membershipButtons lists the custom IDs of the buttons of a message.
func membershipButtons(t *testing.T, raws []json.RawMessage) []string {
	t.Helper()

	var ids []string
	for _, raw := range raws {
		var row struct {
			Components []struct {
				CustomID string `json:"custom_id"`
			} `json:"components"`
		}
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

// membershipHelp returns a message of the user that carries no exchange button, to click forged IDs from.
func membershipHelp(t *testing.T, s *scene, user string) fakediscord.Message {
	t.Helper()

	result := slashOrFail(t, s.harness, user, s.ge("help", true, nil))
	return membershipMessageOf(t, s, user, result)
}

func membershipClick(t *testing.T, s *scene, user string, msg fakediscord.Message, customID string) fakediscord.Result {
	t.Helper()

	result, err := s.fake.Click(t.Context(), s.users[user], msg, customID)
	if err != nil {
		t.Fatalf("Click %s: %v", customID, err)
	}

	return result
}

func membershipForged(t *testing.T, s *scene, user string, msg fakediscord.Message, customID string) fakediscord.Result {
	t.Helper()

	result, err := s.fake.ClickForged(t.Context(), s.users[user], msg, customID)
	if err != nil {
		t.Fatalf("ClickForged %s: %v", customID, err)
	}

	return result
}

func membershipSlash(t *testing.T, s *scene, user, verb string, options map[string]any) reply {
	t.Helper()

	result := slashOrFail(t, s.harness, user, s.ge(verb, true, options))
	return replyTo(t, s.harness, result)
}

func membershipOnly(id giftexchange.ExchangeID, extra map[string]any) map[string]any {
	options := map[string]any{"exchange": id.String()}
	for key, value := range extra {
		options[key] = value
	}

	return options
}

// membershipFollowups waits for the followups of an interaction and returns them.
func membershipFollowups(t *testing.T, s *scene, result fakediscord.Result, count int) []fakediscord.Record {
	t.Helper()

	var found []fakediscord.Record
	waitFor(t, "followups", func() bool {
		found = nil
		for _, record := range s.fake.Outbound() {
			isFollowup := record.Source == "followup" && record.InteractionID != nil &&
				*record.InteractionID == result.InteractionID
			if isFollowup {
				found = append(found, record)
			}
		}

		return len(found) >= count
	})

	return found
}

func membershipEmbed(t *testing.T, record fakediscord.Record) discord.Embed {
	t.Helper()

	if len(record.Embeds) != 1 {
		t.Fatalf("record has %d embeds, want 1", len(record.Embeds))
	}
	var embed discord.Embed
	err := json.Unmarshal(record.Embeds[0], &embed)
	if err != nil {
		t.Fatalf("decode embed: %v", err)
	}

	return embed
}

func TestMembershipCreatePostsThePublicCardAndAnswersTheOrganizerAlone(t *testing.T) {
	before := time.Now()

	cases := map[string]struct {
		options         map[string]any
		wantDrawOffset  time.Duration
		wantRevealField string
	}{
		"without times":    {options: map[string]any{"name": membershipName}, wantRevealField: "not set (organizer runs it)"},
		"with a draw time": {options: map[string]any{"name": membershipName, "draw-at": "in 3d"}, wantDrawOffset: 72 * time.Hour},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := newScene(t)
			result := slashOrFail(t, s.harness, s.a, s.ge("create", true, tc.options))

			got := replyTo(t, s.harness, result)
			if got.content != membershipCreated || got.ephemeralTo != s.a {
				t.Errorf("reply = %+v, want %q to the organizer only", got, membershipCreated)
			}
			cards := membershipFollowups(t, s, result, 1)
			card := cards[0]
			if card.Ephemeral || len(card.Pings) != 0 {
				t.Errorf("card ephemeral = %v, pings = %v, want a public card that pings nobody", card.Ephemeral, card.Pings)
			}
			embed := membershipEmbed(t, card)
			if embed.Title != membershipName || embed.Description != fmt.Sprintf("Organized by <@%s>. Join with the button or /ge join.", s.a) {
				t.Errorf("embed = %q / %q", embed.Title, embed.Description)
			}
			if len(embed.Fields) != 2 || embed.Fields[0].Name != "Draw" || embed.Fields[1].Name != "Reveal" {
				t.Fatalf("fields = %+v, want Draw and Reveal", embed.Fields)
			}
			membershipCheckDrawField(t, embed.Fields[0].Value, tc.wantDrawOffset, before)
			if tc.wantRevealField != "" && embed.Fields[1].Value != tc.wantRevealField {
				t.Errorf("reveal field = %q, want %q", embed.Fields[1].Value, tc.wantRevealField)
			}

			buttons := membershipButtons(t, card.Components)
			id := membershipExchangeOf(t, s)
			want := []string{"/gx/join/" + id, "/gx/leave/" + id, "/gx/rules/" + id}
			if !reflect.DeepEqual(buttons, want) {
				t.Errorf("buttons = %v, want %v", buttons, want)
			}
			visible := s.fake.Visible(s.users[s.b], card.ChannelID)
			isCardVisible, isCreatedVisible := false, false
			for _, message := range visible {
				isCardVisible = isCardVisible || message.ID == card.MessageID
				isCreatedVisible = isCreatedVisible || message.Content == membershipCreated
			}
			if !isCardVisible || isCreatedVisible {
				t.Errorf("another user sees the card = %v and the confirmation = %v, want true and false", isCardVisible, isCreatedVisible)
			}
		})
	}
}

// membershipExchangeOf returns the ID of the only exchange of the organizer.
func membershipExchangeOf(t *testing.T, s *scene) string {
	t.Helper()

	listed := s.summaries(t, s.a)
	if len(listed) != 1 {
		t.Fatalf("the organizer sees %d exchanges, want 1", len(listed))
	}

	return listed[0].ID.String()
}

func membershipCheckDrawField(t *testing.T, value string, offset time.Duration, before time.Time) {
	t.Helper()

	if offset == 0 {
		if value != "not set (organizer runs it)" {
			t.Errorf("draw field = %q, want the manual text", value)
		}

		return
	}
	match := membershipTimeField.FindStringSubmatch(value)
	if match == nil {
		t.Fatalf("draw field = %q, want <t:unix:F>", value)
	}
	var unix int64
	_, err := fmt.Sscan(match[1], &unix)
	if err != nil {
		t.Fatalf("read unix time: %v", err)
	}
	lowest, highest := before.Add(offset).Unix(), time.Now().Add(offset).Unix()
	if unix < lowest || unix > highest {
		t.Errorf("draw time %d is outside [%d, %d]", unix, lowest, highest)
	}
}

func TestMembershipCreateRefusesWhatItCannotCreate(t *testing.T) {
	cases := map[string]struct {
		options map[string]any
		inGuild bool
		want    string
	}{
		"unreadable draw time":   {map[string]any{"name": "Winter", "draw-at": "next week"}, true, membershipBadTime},
		"unreadable reveal time": {map[string]any{"name": "Winter", "reveal-at": "soon"}, true, membershipBadTime},
		"draw time in the past":  {map[string]any{"name": "Winter", "draw-at": "2000-01-01 10:00"}, true, "Not saved:\n- draw\\_at must be in the future"},
		"reveal before draw": {
			map[string]any{"name": "Winter", "draw-at": "in 3d", "reveal-at": "in 2d"}, true, "Not saved:\n- reveal\\_at must be after draw\\_at",
		},
		"in the bot DM": {map[string]any{"name": "Winter"}, false, guildOnlyText},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := newScene(t)

			result := slashOrFail(t, s.harness, s.a, s.ge("create", tc.inGuild, tc.options))

			got := replyTo(t, s.harness, result)
			if got.content != tc.want {
				t.Errorf("reply = %q, want %q", got.content, tc.want)
			}
			if listed := s.summaries(t, s.a); len(listed) != 0 {
				t.Errorf("%d exchanges exist, want none", len(listed))
			}
			for _, record := range s.fake.Outbound() {
				if record.Source == "followup" {
					t.Errorf("a card was posted: %+v", record)
				}
			}
		})
	}
}

func TestMembershipCreateHandlesAnyName(t *testing.T) {
	s := newScene(t)
	cases := []struct {
		name     string
		typed    string
		rejected bool
		shown    string
	}{
		{name: "empty", typed: "", rejected: true},
		{name: "one rune", typed: "A", shown: "A"},
		{name: "100 runes", typed: strings.Repeat("a", 100), shown: strings.Repeat("a", 100)},
		{name: "101 runes", typed: strings.Repeat("a", 101), rejected: true},
		{name: "zero-width only", typed: "\u200b\u200b\u200b", rejected: true},
		{name: "combining marks", typed: "Café", shown: "Café"},
		{name: "right to left", typed: "שלום", shown: "שלום"},
		{name: "emoji", typed: "\U0001F381 Gifts", shown: "\U0001F381 Gifts"},
		{name: "everyone", typed: "@everyone", shown: `\@everyone`},
		{name: "here", typed: "@here", shown: `\@here`},
		{name: "user mention", typed: "<@100000000000000001>", shown: `\<\@100000000000000001\>`},
		{name: "markdown", typed: "*bold* _it_ `code`", shown: "\\*bold\\* \\_it\\_ \\`code\\`"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			options := map[string]any{"name": tc.typed}
			existing := len(s.summaries(t, s.a))
			in := s.ge("create", true, options)
			in.BypassValidation = true
			result := slashOrFail(t, s.harness, s.a, in)

			got := replyTo(t, s.harness, result)
			if tc.rejected {
				if !strings.HasPrefix(got.content, "Not saved:") {
					t.Errorf("reply = %q, want Not saved", got.content)
				}
				if listed := s.summaries(t, s.a); len(listed) != existing {
					t.Errorf("%d exchanges exist after a rejected name, want %d", len(listed), existing)
				}

				return
			}
			want := "You created **" + tc.shown + "** and you organize it. " +
				"You are not a participant unless you join. Configure it with /ge rules."
			if got.content != want {
				t.Errorf("reply = %q, want %q", got.content, want)
			}
			for _, card := range membershipFollowups(t, s, result, 1) {
				if len(card.Pings) != 0 {
					t.Errorf("card pings %v", card.Pings)
				}
				if card.AllowedMentionsSent == nil || len(card.AllowedMentionsSent.Parse) != 0 ||
					len(card.AllowedMentionsSent.Users) != 0 {
					t.Errorf("allowed mentions sent = %+v, want empty", card.AllowedMentionsSent)
				}
			}
		})
	}
}

func TestMembershipLeaveInOpenActsAtOnce(t *testing.T) {
	s := newScene(t)
	id := s.createExchange(t, s.a, membershipName)
	s.joinAs(t, s.b, id)

	got := membershipSlash(t, s, s.b, "leave", nil)

	if got.content != membershipLeft || got.ephemeralTo != s.b {
		t.Errorf("reply = %+v, want %q to the caller", got, membershipLeft)
	}
	if count := s.summaries(t, s.a)[0].ParticipantCount; count != 0 {
		t.Errorf("participant count = %d, want 0", count)
	}
}

func TestMembershipLeaveByTheCardButtonActsAndLeavesTheCardAlone(t *testing.T) {
	s := newScene(t)
	id := s.createExchange(t, s.a, membershipName)
	s.joinAs(t, s.b, id)
	card := membershipHelp(t, s, s.b)
	before := len(s.fake.Outbound())

	result := membershipForged(t, s, s.b, card, "/gx/leave/"+id.String())

	got := replyTo(t, s.harness, result)
	if result.CallbackType != callbackDeferredMessage || got.content != membershipLeft || got.ephemeralTo != s.b {
		t.Errorf("callback %d, reply = %+v", result.CallbackType, got)
	}
	for _, record := range s.fake.Outbound()[before:] {
		if record.Route == "PATCH /channels/{channel}/messages/{id}" {
			t.Errorf("the card was edited: %+v", record)
		}
	}
}

func TestMembershipLeaveWhenNotInTheExchangeAnswersNotParticipant(t *testing.T) {
	s := newScene(t)
	open := s.createExchange(t, s.a, "Open one")
	drawn := s.drawnExchange(t, "Drawn one")

	for name, id := range map[string]giftexchange.ExchangeID{"open": open, "drawn": drawn} {
		before := membershipSnapshot(t, s, id)

		got := membershipSlash(t, s, s.d, "leave", map[string]any{"exchange": id.String()})

		if got.content != membershipNotParticipant {
			t.Errorf("%s: reply = %q, want %q", name, got.content, membershipNotParticipant)
		}
		membershipUnchanged(t, s, id, before)
	}
}

func TestMembershipLeaveAfterTheDrawAsksFirstAndConfirmApplies(t *testing.T) {
	s := newScene(t)
	id := membershipDrawn(t, s, s.a, s.b, s.c)

	asked := slashOrFail(t, s.harness, s.b, s.ge("leave", true, nil))

	got := replyTo(t, s.harness, asked)
	if got.content != membershipConfirmLeave || got.ephemeralTo != s.b {
		t.Fatalf("reply = %+v, want %q", got, membershipConfirmLeave)
	}
	question := membershipMessageOf(t, s, s.b, asked)
	wantButtons := []string{"/gx/leave-confirm/" + id.String(), "/gx/dismiss/" + id.String()}
	if buttons := membershipButtons(t, question.Components); !reflect.DeepEqual(buttons, wantButtons) {
		t.Errorf("buttons = %v, want %v", buttons, wantButtons)
	}
	if !membershipIsIn(s.summaries(t, s.b), id) {
		t.Fatal("the caller left before confirming")
	}

	confirmed := membershipClick(t, s, s.b, question, wantButtons[0])

	if confirmed.CallbackType != callbackDeferredUpdate {
		t.Errorf("callback type = %d, want %d", confirmed.CallbackType, callbackDeferredUpdate)
	}
	if got := replyTo(t, s.harness, confirmed); got.content != membershipLeft {
		t.Errorf("reply = %q, want %q", got.content, membershipLeft)
	}
	if membershipIsIn(s.summaries(t, s.b), id) {
		t.Error("the caller is still a participant after confirming")
	}
	message := membershipMessageOf(t, s, s.b, confirmed)
	if len(message.Components) != 0 {
		t.Errorf("the question still has components: %v", message.Components)
	}
}

func membershipIsIn(listed []giftexchange.ExchangeSummary, id giftexchange.ExchangeID) bool {
	for _, summary := range listed {
		if summary.ID == id && summary.CallerIsParticipant {
			return true
		}
	}

	return false
}

func TestMembershipBackAnswersNothingChangedAndRemovesTheButtons(t *testing.T) {
	s := newScene(t)
	id := membershipDrawn(t, s, s.a, s.b, s.c)
	asked := slashOrFail(t, s.harness, s.b, s.ge("leave", true, nil))
	question := membershipMessageOf(t, s, s.b, asked)
	before := membershipSnapshot(t, s, id)

	result := membershipClick(t, s, s.b, question, "/gx/dismiss/"+id.String())

	if result.CallbackType != callbackUpdateMessage || result.Message == nil {
		t.Fatalf("callback type = %d, message = %v", result.CallbackType, result.Message)
	}
	if result.Message.Content != membershipNothing || len(result.Message.Components) != 0 {
		t.Errorf("message = %q with %d components", result.Message.Content, len(result.Message.Components))
	}
	membershipUnchanged(t, s, id, before)
}

func TestMembershipRemoveInOpenActsAtOnceAndKeepsSnowflakesOutOfTheLogs(t *testing.T) {
	s := newScene(t)
	id := s.createExchange(t, s.a, membershipName)
	s.joinAs(t, s.b, id)

	got := membershipSlash(t, s, s.a, "remove", membershipOnly(id, map[string]any{"participant": s.b}))

	want := fmt.Sprintf("Removed <@%s> from **%s**.", s.b, membershipName)
	if got.content != want || got.ephemeralTo != s.a {
		t.Errorf("reply = %+v, want %q to the organizer", got, want)
	}
	if membershipIsIn(s.summaries(t, s.b), id) {
		t.Error("the participant is still in")
	}
	s.waitInteractionLogs(t, 1)
	logs := s.logs.String()
	for _, secret := range []string{s.a, s.b, membershipName} {
		if strings.Contains(logs, secret) {
			t.Errorf("logs hold %q", secret)
		}
	}
}

func TestMembershipRemoveAfterTheDrawAsksFirstAndReportsEachOutcome(t *testing.T) {
	cases := map[string]struct {
		joiners   int
		want      string
		wantState giftexchange.State
	}{
		"five to four patches":  {5, membershipPatched, giftexchange.StateDrawn},
		"four to three redraws": {4, membershipRedrawn, giftexchange.StateDrawn},
		"three to two reopens":  {3, membershipReopened, giftexchange.StateOpen},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := newScene(t)
			pool := []string{s.b, s.c, s.d, membershipExtraUser(t, s), membershipExtraUser(t, s)}
			joiners := pool[:tc.joiners]
			id := membershipDrawn(t, s, joiners...)
			target := membershipPatchableTarget(t, s, joiners)

			asked := slashOrFail(t, s.harness, s.a, s.ge("remove", true, membershipOnly(id, map[string]any{"participant": target})))

			wantAsk := fmt.Sprintf("**%s** is already drawn. Remove <@%s>? A giver may get a new recipient.", membershipName, target)
			if got := replyTo(t, s.harness, asked); got.content != wantAsk {
				t.Fatalf("reply = %q, want %q", got.content, wantAsk)
			}
			if count := s.summaries(t, s.a)[0].ParticipantCount; count != tc.joiners {
				t.Fatalf("participants = %d before confirming, want %d", count, tc.joiners)
			}
			question := membershipMessageOf(t, s, s.a, asked)
			confirmID := "/gx/remove-confirm/" + id.String() + "/" + s.people[target].id.String()

			confirmed := membershipClick(t, s, s.a, question, confirmID)

			got := replyTo(t, s.harness, confirmed)
			if got.content != tc.want || got.ephemeralTo != s.a {
				t.Errorf("reply = %+v, want %q to the organizer", got, tc.want)
			}
			summary := s.summaries(t, s.a)[0]
			if summary.State != tc.wantState || summary.ParticipantCount != tc.joiners-1 {
				t.Errorf("state %s with %d participants, want %s with %d",
					summary.State, summary.ParticipantCount, tc.wantState, tc.joiners-1)
			}
			s.waitInteractionLogs(t, 1)
			logs := s.logs.String()
			for _, user := range append([]string{s.a}, joiners...) {
				if strings.Contains(logs, user) {
					t.Errorf("logs hold the user id %q", user)
				}
			}
		})
	}
}

func TestMembershipRemoveByAnOrganizerWhoIsTheGiverOfTheTargetIsRefused(t *testing.T) {
	s := newScene(t)
	id := membershipDrawn(t, s, s.a, s.b, s.c)
	assignment, err := s.svc.MyAssignment(t.Context(), s.people[s.a].actor, id)
	if err != nil {
		t.Fatalf("MyAssignment: %v", err)
	}
	recipient := assignment.Recipient.Identities[0].Subject
	asked := slashOrFail(t, s.harness, s.a, s.ge("remove", true, membershipOnly(id, map[string]any{"participant": recipient})))
	question := membershipMessageOf(t, s, s.a, asked)
	before := membershipSnapshot(t, s, id)
	confirmID := "/gx/remove-confirm/" + id.String() + "/" + s.people[recipient].id.String()

	confirmed := membershipClick(t, s, s.a, question, confirmID)

	if got := replyTo(t, s.harness, confirmed); got.content != membershipOwnRecipient {
		t.Errorf("reply = %q, want %q", got.content, membershipOwnRecipient)
	}
	membershipUnchanged(t, s, id, before)
}

func TestMembershipRemoveRefusesUnknownTargets(t *testing.T) {
	s := newScene(t)
	id := s.createExchange(t, s.a, membershipName)
	s.joinAs(t, s.b, id)
	drawn := membershipDrawn(t, s, s.a, s.b, s.c)

	targets := map[string]string{
		"the bot":                         s.fake.ApplicationID(),
		"a user nobody knows":             userGap,
		"a guild member who did not join": s.d,
	}
	for name, target := range targets {
		for _, exchange := range []giftexchange.ExchangeID{id, drawn} {
			before := membershipSnapshot(t, s, exchange)
			in := s.ge("remove", true, membershipOnly(exchange, map[string]any{"participant": target}))
			in.BypassValidation = true

			got := replyTo(t, s.harness, slashOrFail(t, s.harness, s.a, in))

			if got.content != membershipNotParticipant {
				t.Errorf("%s: reply = %q, want %q", name, got.content, membershipNotParticipant)
			}
			membershipUnchanged(t, s, exchange, before)
		}
	}
}

func TestMembershipRemoveConfirmForAPersonWhoIsNotInAnswersNotParticipant(t *testing.T) {
	s := newScene(t)
	id := membershipDrawn(t, s, s.a, s.b, s.c)
	help := membershipHelp(t, s, s.a)
	before := membershipSnapshot(t, s, id)

	result := membershipForged(t, s, s.a, help, "/gx/remove-confirm/"+id.String()+"/"+s.people[s.d].id.String())

	if got := replyTo(t, s.harness, result); got.content != membershipNotParticipant {
		t.Errorf("reply = %q, want %q", got.content, membershipNotParticipant)
	}
	membershipUnchanged(t, s, id, before)
}

func TestMembershipRenameChangesTheNameForTheOrganizer(t *testing.T) {
	s := newScene(t)
	id := s.createExchange(t, s.a, membershipName)

	got := membershipSlash(t, s, s.a, "rename", membershipOnly(id, map[string]any{"name": "Spring *party*"}))

	if got.content != "Renamed to **Spring \\*party\\***." || got.ephemeralTo != s.a {
		t.Errorf("reply = %+v", got)
	}
	if name := s.summaries(t, s.a)[0].Name; name != "Spring *party*" {
		t.Errorf("name = %q", name)
	}
}

func TestMembershipRenameRefusesBadNames(t *testing.T) {
	s := newScene(t)
	id := s.createExchange(t, s.a, membershipName)
	before := membershipSnapshot(t, s, id)

	cases := []struct {
		typed string
		want  string
	}{
		{"", "Not saved:\n- name is empty"},
		{strings.Repeat("a", 101), "Not saved:\n- name is too long"},
		{"\u200b\u200b", "Not saved:\n- name has no visible character"},
	}
	for _, tc := range cases {
		in := s.ge("rename", true, membershipOnly(id, map[string]any{"name": tc.typed}))
		in.BypassValidation = true

		got := replyTo(t, s.harness, slashOrFail(t, s.harness, s.a, in))

		if got.content != tc.want {
			t.Errorf("rename to %d runes: reply = %q, want %q", len([]rune(tc.typed)), got.content, tc.want)
		}
	}
	membershipUnchanged(t, s, id, before)
}

func TestMembershipCancelAsksFirstThenCancelsAndAnnounces(t *testing.T) {
	s := newScene(t)
	s.startRunner(t, s.world)
	id := membershipDrawn(t, s, s.a, s.b, s.c)

	asked := slashOrFail(t, s.harness, s.a, s.ge("cancel", true, nil))

	if got := replyTo(t, s.harness, asked); got.content != membershipConfirmCancel || got.ephemeralTo != s.a {
		t.Fatalf("reply = %+v, want %q", got, membershipConfirmCancel)
	}
	if state := s.summaries(t, s.a)[0].State; state != giftexchange.StateDrawn {
		t.Fatalf("state = %s before confirming, want drawn", state)
	}
	question := membershipMessageOf(t, s, s.a, asked)

	confirmed := membershipClick(t, s, s.a, question, "/gx/cancel-confirm/"+id.String())

	got := replyTo(t, s.harness, confirmed)
	if got.content != membershipCancelled || got.ephemeralTo != s.a {
		t.Errorf("reply = %+v, want %q to the organizer", got, membershipCancelled)
	}
	announcements := membershipFollowups(t, s, confirmed, 1)
	if announcements[0].Ephemeral || announcements[0].Content != membershipCancelPublic || len(announcements[0].Pings) != 0 {
		t.Errorf("announcement = %+v, want the public text without pings", announcements[0])
	}
	view, err := s.svc.Exchange(t.Context(), s.people[s.a].actor, s.group, id)
	if err != nil || view.State != giftexchange.StateCancelled {
		t.Errorf("state = %s, err = %v, want cancelled", view.State, err)
	}
	// The module sends no direct message to the organizer who cancels.
	for _, user := range []string{s.b, s.c} {
		waitCtx, cancel := context.WithTimeout(t.Context(), deliveryDeadline)
		_, waitErr := s.fake.WaitDM(waitCtx, s.users[user], func(m fakediscord.Message) bool {
			return strings.Contains(m.Content, membershipCancelDM)
		})
		cancel()
		if waitErr != nil {
			t.Errorf("%s got no cancelled DM: %v", user, waitErr)
		}
	}
}

func TestMembershipAParticipantWhoIsNotTheOrganizerIsDenied(t *testing.T) {
	for _, drawn := range []bool{false, true} {
		t.Run(fmt.Sprintf("drawn=%v", drawn), func(t *testing.T) {
			s := newScene(t)
			var id giftexchange.ExchangeID
			if drawn {
				id = membershipDrawn(t, s, s.b, s.c, s.d)
			} else {
				id = s.createExchange(t, s.a, membershipName)
				s.joinAs(t, s.b, id)
				s.joinAs(t, s.c, id)
			}
			help := membershipHelp(t, s, s.b)
			before := membershipSnapshot(t, s, id)
			option := membershipOnly(id, nil)

			byCommand := map[string]map[string]any{
				"remove": membershipOnly(id, map[string]any{"participant": s.c}),
				"rename": membershipOnly(id, map[string]any{"name": "Taken over"}),
				"cancel": option,
			}
			for verb, options := range byCommand {
				if got := membershipSlash(t, s, s.b, verb, options); got.content != membershipForbidden {
					t.Errorf("%s: reply = %q, want %q", verb, got.content, membershipForbidden)
				}
			}
			forged := []string{
				"/gx/cancel-confirm/" + id.String(),
				"/gx/remove-confirm/" + id.String() + "/" + s.people[s.c].id.String(),
			}
			for _, customID := range forged {
				result := membershipForged(t, s, s.b, help, customID)
				if got := replyTo(t, s.harness, result); got.content != membershipForbidden {
					t.Errorf("%s: reply = %q, want %q", customID, got.content, membershipForbidden)
				}
			}
			membershipUnchanged(t, s, id, before)
		})
	}
}

func TestMembershipAnOutsiderFromAnotherGuildFindsNothing(t *testing.T) {
	s := newScene(t)
	id := membershipDrawn(t, s, s.a, s.b, s.c)
	other := s.otherGuildWith(t, s.d)
	help := slashOrFail(t, s.harness, s.d, fakediscord.Slash{Path: "/ge/help", Guild: &other})
	message := membershipMessageOf(t, s, s.d, help)
	before := membershipSnapshot(t, s, id)

	slashes := map[string]map[string]any{
		"leave":  membershipOnly(id, nil),
		"remove": membershipOnly(id, map[string]any{"participant": s.b}),
		"rename": membershipOnly(id, map[string]any{"name": "Mine now"}),
		"cancel": membershipOnly(id, nil),
	}
	for verb, options := range slashes {
		in := fakediscord.Slash{Path: "/ge/" + verb, Guild: &other, Options: options}
		got := replyTo(t, s.harness, slashOrFail(t, s.harness, s.d, in))
		if got.content != textNotFound {
			t.Errorf("%s: reply = %q, want %q", verb, got.content, textNotFound)
		}
	}
	buttons := []string{
		"/gx/leave/" + id.String(),
		"/gx/leave-confirm/" + id.String(),
		"/gx/cancel-confirm/" + id.String(),
		"/gx/remove-confirm/" + id.String() + "/" + s.people[s.b].id.String(),
	}
	for _, customID := range buttons {
		result := membershipForged(t, s, s.d, message, customID)
		if got := replyTo(t, s.harness, result); got.content != textNotFound {
			t.Errorf("%s: reply = %q, want %q", customID, got.content, textNotFound)
		}
	}
	membershipUnchanged(t, s, id, before)
}

func TestMembershipGuildVerbsInTheBotDMAnswerGuildOnly(t *testing.T) {
	s := newScene(t)
	id := s.createExchange(t, s.a, membershipName)
	before := membershipSnapshot(t, s, id)
	othersBefore := membershipDMsOfOthers(s)

	for _, verb := range []string{"create", "leave", "remove", "rename", "cancel"} {
		options := map[string]any{"name": "Winter"}
		switch verb {
		case "leave", "cancel":
			options = map[string]any{"exchange": id.String()}
		case "remove":
			options = map[string]any{"participant": s.b, "exchange": id.String()}
		case "rename":
			options = map[string]any{"name": "Winter", "exchange": id.String()}
		}
		result := slashOrFail(t, s.harness, s.a, s.ge(verb, false, options))

		if got := replyTo(t, s.harness, result); got.content != guildOnlyText {
			t.Errorf("%s: reply = %q, want %q", verb, got.content, guildOnlyText)
		}
	}
	// The replies to a command typed in the bot's DM are messages of that DM, so the exchange itself is compared.
	after := membershipSnapshot(t, s, id)
	after.dms = before.dms
	if !reflect.DeepEqual(before, after) {
		t.Error("the exchange changed")
	}
	if othersAfter := membershipDMsOfOthers(s); !reflect.DeepEqual(othersBefore, othersAfter) {
		t.Errorf("other users received DMs: before %v, after %v", othersBefore, othersAfter)
	}
}

// membershipDMsOfOthers counts the DMs of every user except the organizer, who is the only one typing in the bot's DM.
func membershipDMsOfOthers(s *scene) map[string]int {
	counts := map[string]int{}
	for id, user := range s.users {
		if id == s.a {
			continue
		}
		counts[id] = len(s.fake.DMs(user))
	}

	return counts
}

func TestMembershipForgedCustomIDsAnswerNotFound(t *testing.T) {
	s := newScene(t)
	id := s.createExchange(t, s.a, membershipName)
	help := membershipHelp(t, s, s.a)
	before := membershipSnapshot(t, s, id)

	var forged []string
	for _, action := range []string{"leave", "leave-confirm", "cancel-confirm", "dismiss"} {
		forged = append(forged,
			"/gx/"+action+"/not-a-uuid",
			"/gx/"+action+"/"+id.String()+"/extra",
			"/gx/"+action+"/"+strings.ToUpper(id.String()),
			"/gx/"+action+"/"+strings.Repeat("a", 120),
		)
	}
	forged = append(forged,
		"/gx/remove-confirm/"+id.String()+"/not-a-uuid",
		"/gx/remove-confirm/"+id.String()+"/100000000000000001",
	)
	for _, customID := range forged {
		result := membershipForged(t, s, s.a, help, customID)

		if got := replyTo(t, s.harness, result); got.content != textNotFound {
			t.Errorf("%s: reply = %q, want %q", customID, got.content, textNotFound)
		}
	}
	membershipUnchanged(t, s, id, before)
}

func TestMembershipRepeatedConfirmationsChangeNothingTheSecondTime(t *testing.T) {
	s := newScene(t)
	pool := []string{s.b, s.c, s.d, membershipExtraUser(t, s)}
	id := membershipDrawn(t, s, pool...)
	help := membershipHelp(t, s, s.a)
	helpB := membershipHelp(t, s, s.b)

	removeID := "/gx/remove-confirm/" + id.String() + "/" + s.people[s.d].id.String()
	first := membershipForged(t, s, s.a, help, removeID)
	if got := replyTo(t, s.harness, first); got.content != membershipRedrawn {
		t.Fatalf("first removal: reply = %q, want %q", got.content, membershipRedrawn)
	}
	afterFirst := membershipSnapshot(t, s, id)

	second := membershipForged(t, s, s.a, help, removeID)

	if got := replyTo(t, s.harness, second); got.content != membershipNotParticipant {
		t.Errorf("second removal: reply = %q, want %q", got.content, membershipNotParticipant)
	}
	membershipUnchanged(t, s, id, afterFirst)

	leaveID := "/gx/leave-confirm/" + id.String()
	replyTo(t, s.harness, membershipForged(t, s, s.b, helpB, leaveID))
	afterLeave := membershipSnapshot(t, s, id)
	again := membershipForged(t, s, s.b, helpB, leaveID)
	if got := replyTo(t, s.harness, again); got.content != membershipNotParticipant {
		t.Errorf("second leave: reply = %q, want %q", got.content, membershipNotParticipant)
	}
	membershipUnchanged(t, s, id, afterLeave)

	cancelID := "/gx/cancel-confirm/" + id.String()
	replyTo(t, s.harness, membershipForged(t, s, s.a, help, cancelID))
	afterCancel := membershipSnapshot(t, s, id)
	secondCancel := membershipForged(t, s, s.a, help, cancelID)
	if got := replyTo(t, s.harness, secondCancel); got.content != membershipCancelledText {
		t.Errorf("second cancel: reply = %q, want %q", got.content, membershipCancelledText)
	}
	membershipUnchanged(t, s, id, afterCancel)
}

func TestMembershipClicksOnACancelledExchangeAnswerInvalidState(t *testing.T) {
	s := newScene(t)
	id := s.createExchange(t, s.a, membershipName)
	s.joinAs(t, s.b, id)
	help := membershipHelp(t, s, s.b)
	cancelErr := s.svc.Cancel(t.Context(), s.people[s.a].actor, s.group, id)
	if cancelErr != nil {
		t.Fatalf("Cancel: %v", cancelErr)
	}
	before := membershipSnapshot(t, s, id)

	for _, customID := range []string{"/gx/leave/" + id.String(), "/gx/leave-confirm/" + id.String(), "/gx/join/" + id.String()} {
		result := membershipForged(t, s, s.b, help, customID)

		if got := replyTo(t, s.harness, result); got.content != membershipCancelledText {
			t.Errorf("%s: reply = %q, want %q", customID, got.content, membershipCancelledText)
		}
	}
	membershipUnchanged(t, s, id, before)
}

// membershipPatchableTarget picks a participant whose giver and recipient differ, so that the
// module can patch the draw around them when enough people remain.
func membershipPatchableTarget(t *testing.T, s *scene, joiners []string) string {
	t.Helper()

	// Which exchange is not needed: the scene holds exactly the one drawn exchange.
	listed := s.summaries(t, s.a)
	if len(listed) != 1 {
		t.Fatalf("the organizer sees %d exchanges, want 1", len(listed))
	}
	recipientOf := map[string]string{}
	for _, user := range joiners {
		assignment, err := s.svc.MyAssignment(t.Context(), s.people[user].actor, listed[0].ID)
		if err != nil {
			t.Fatalf("MyAssignment: %v", err)
		}
		recipientOf[user] = assignment.Recipient.Identities[0].Subject
	}
	for _, candidate := range joiners {
		for giver, recipient := range recipientOf {
			if recipient == candidate && recipientOf[candidate] != giver {
				return candidate
			}
		}
	}
	t.Fatal("every participant is in a pair of two")

	return ""
}
