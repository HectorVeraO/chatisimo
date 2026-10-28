//go:build !race

// These tests open a real disgo Gateway client; see gateway_harness_test.go for why they are
// left out of the race run.

package discord_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/disgoorg/disgo/discord"

	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

const (
	rulesTextForbidden   = "You are not allowed to do that in this exchange."
	rulesTextUnknownRule = "Unknown rule."
	rulesTextFixed       = "This rule can't be changed."
	rulesTextLockedDrawn = "This rule can't change while the exchange is drawn."
	rulesTextNotSaved    = "Not saved:"
	rulesInputID         = "value"

	rulesCallbackModal  = 9
	rulesCallbackUpdate = 7
)

// rulesReply is what the bot last showed for an interaction.
type rulesReply struct {
	content   string
	embed     discord.Embed
	menus     []rulesMenu
	visibleTo []string
	message   fakediscord.Message
}

type rulesMenu struct {
	CustomID    string `json:"custom_id"`
	Placeholder string `json:"placeholder"`
	Options     []struct {
		Label string `json:"label"`
		Value string `json:"value"`
	} `json:"options"`
}

type rulesRawComponent struct {
	Type        int               `json:"type"`
	CustomID    string            `json:"custom_id"`
	Placeholder string            `json:"placeholder"`
	Components  []json.RawMessage `json:"components"`
	Options     []struct {
		Label string `json:"label"`
		Value string `json:"value"`
	} `json:"options"`
}

func rulesMenusOf(t *testing.T, raws []json.RawMessage) []rulesMenu {
	t.Helper()

	var menus []rulesMenu
	for _, raw := range raws {
		var component rulesRawComponent
		if err := json.Unmarshal(raw, &component); err != nil {
			t.Fatalf("decode component: %v", err)
		}
		if component.Type == 3 {
			menus = append(menus, rulesMenu{
				CustomID: component.CustomID, Placeholder: component.Placeholder, Options: component.Options,
			})
		}
		menus = append(menus, rulesMenusOf(t, component.Components)...)
	}

	return menus
}

// rulesReplyOf returns the last message the bot sent or edited for the interaction of result,
// waiting for it when the interaction was deferred.
func rulesReplyOf(t *testing.T, s *scene, result fakediscord.Result) rulesReply {
	t.Helper()

	var found fakediscord.Record
	waitFor(t, "the reply of the interaction", func() bool {
		for _, record := range s.fake.Outbound() {
			isReply := record.InteractionID != nil && *record.InteractionID == result.InteractionID &&
				slices.Contains([]string{"callback", "edit", "followup"}, record.Source) && record.Status < 300
			if isReply {
				found = record
			}
		}

		return found.Seq != 0
	})

	reply := rulesReply{
		content:   found.Content,
		visibleTo: found.VisibleTo,
		menus:     rulesMenusOf(t, found.Components),
		message:   fakediscord.Message{ID: found.MessageID, ChannelID: found.ChannelID},
	}
	if len(found.Embeds) > 0 {
		if err := json.Unmarshal(found.Embeds[0], &reply.embed); err != nil {
			t.Fatalf("decode embed: %v", err)
		}
	}

	return reply
}

// rulesField returns the value of the last field with the name.
func rulesField(t *testing.T, embed discord.Embed, name string) string {
	t.Helper()

	for i := len(embed.Fields) - 1; i >= 0; i-- {
		if embed.Fields[i].Name == name {
			return embed.Fields[i].Value
		}
	}

	t.Fatalf("no field %q in %+v", name, embed.Fields)
	return ""
}

func rulesOpenExchange(t *testing.T, s *scene, name string) giftexchange.ExchangeID {
	t.Helper()

	id := s.createExchange(t, s.a, name)
	for _, user := range []string{s.b, s.c} {
		s.joinAs(t, user, id)
	}

	return id
}

func rulesShow(t *testing.T, s *scene, user string, id giftexchange.ExchangeID) rulesReply {
	t.Helper()

	result := slashOrFail(t, s.harness, user, s.ge("rules", true, map[string]any{"exchange": id.String()}))
	return rulesReplyOf(t, s, result)
}

func rulesViewOf(t *testing.T, s *scene, user string, id giftexchange.ExchangeID) map[giftexchange.RuleName]giftexchange.RuleView {
	t.Helper()

	rules, err := s.svc.Rules(t.Context(), s.people[user].actor, s.group, id)
	if err != nil {
		t.Fatalf("Rules: %v", err)
	}

	byName := map[giftexchange.RuleName]giftexchange.RuleView{}
	for _, rule := range rules {
		byName[rule.Name] = rule
	}

	return byName
}

func rulesPairsOf(t *testing.T, s *scene, id giftexchange.ExchangeID) []giftexchange.Exclusion {
	t.Helper()

	pairs, err := s.svc.Exclusions(t.Context(), s.people[s.a].actor, s.group, id)
	if err != nil {
		t.Fatalf("Exclusions: %v", err)
	}

	return pairs
}

func rulesPick(t *testing.T, s *scene, user string, view rulesReply, id giftexchange.ExchangeID, rule string) fakediscord.Result {
	t.Helper()

	result, err := s.fake.Select(t.Context(), s.users[user], view.message, "/gx/rule-pick/"+id.String(), rule)
	if err != nil {
		t.Fatalf("Select %s: %v", rule, err)
	}

	return result
}

// rulesSubmit opens the modal of a time or duration rule and submits value.
func rulesSubmit(t *testing.T, s *scene, id giftexchange.ExchangeID, rule, value string) (rulesReply, fakediscord.Result) {
	t.Helper()

	view := rulesShow(t, s, s.a, id)
	picked := rulesPick(t, s, s.a, view, id, rule)
	if picked.Modal == nil {
		t.Fatalf("picking %s opened no modal: %+v", rule, picked)
	}

	submitted, err := s.fake.SubmitModal(t.Context(), s.users[s.a], *picked.Modal, map[string]string{rulesInputID: value})
	if err != nil {
		t.Fatalf("SubmitModal: %v", err)
	}

	return rulesReplyOf(t, s, submitted), submitted
}

func TestRulesViewIsEphemeralOnBothPathsAndLinksTheSelectToTheOrganizer(t *testing.T) {
	s := newScene(t)
	id := rulesOpenExchange(t, s, "Winter")

	for _, path := range []string{"/ge/rules", "/chatisimo/giftexchange/rules"} {
		in := s.slashAt(path, true, map[string]any{"exchange": id.String()})
		got := rulesReplyOf(t, s, slashOrFail(t, s.harness, s.a, in))

		if !slices.Equal(got.visibleTo, []string{s.a}) {
			t.Errorf("%s: visible to %v, want only the caller", path, got.visibleTo)
		}
		if got.embed.Title != "Rules - Winter" {
			t.Errorf("%s: title = %q", path, got.embed.Title)
		}
		if got.embed.Description != "State: open. Participants: 2. You are the organizer." {
			t.Errorf("%s: description = %q", path, got.embed.Description)
		}
		if len(got.embed.Fields) != 15 {
			t.Errorf("%s: %d fields, want 15 (13 rules, Participants, Exclusions)", path, len(got.embed.Fields))
		}
		if len(got.menus) != 1 || got.menus[0].CustomID != "/gx/rule-pick/"+id.String() ||
			got.menus[0].Placeholder != "Change a rule" || len(got.menus[0].Options) != 6 {
			t.Errorf("%s: menus = %+v, want one select of six rules", path, got.menus)
		}
		for _, option := range got.menus[0].Options {
			if option.Value == "exclusions" {
				t.Errorf("%s: the select offers exclusions", path)
			}
		}
	}
}

func TestRulesViewByTheCardButtonIsEphemeralAndLeavesTheCardAlone(t *testing.T) {
	s := newScene(t)
	id := rulesOpenExchange(t, s, "Winter")
	card := slashOrFail(t, s.harness, s.b, s.ge("help", true, nil))
	before := len(s.fake.Outbound())

	result, err := s.fake.ClickForged(t.Context(), s.users[s.b], *card.Message, "/gx/rules/"+id.String())
	if err != nil {
		t.Fatalf("ClickForged: %v", err)
	}

	if result.CallbackType != 5 {
		t.Errorf("callback type = %d, want 5 (deferred message)", result.CallbackType)
	}
	got := rulesReplyOf(t, s, result)
	if got.embed.Title != "Rules - Winter" || !slices.Equal(got.visibleTo, []string{s.b}) {
		t.Errorf("reply = %+v", got)
	}
	for _, record := range s.fake.Outbound()[before:] {
		if record.Route == "PATCH /channels/{channel}/messages/{id}" {
			t.Errorf("the card was edited: %+v", record)
		}
	}
}

func TestRulesViewForAParticipantAndAMemberHasNoComponentsAndOnlyTheExclusionCount(t *testing.T) {
	s := newScene(t)
	id := rulesOpenExchange(t, s, "Winter")
	organizer := s.people[s.a].actor
	pair := s.svc.AddExclusion(t.Context(), organizer, s.group, id, s.people[s.b].id, s.people[s.c].id, false)
	if pair != nil {
		t.Fatalf("AddExclusion: %v", pair)
	}

	for _, user := range []string{s.b, s.d} {
		got := rulesShow(t, s, user, id)

		if len(got.menus) != 0 {
			t.Errorf("user %s sees selects %+v", user, got.menus)
		}
		if exclusions := rulesField(t, got.embed, "Exclusions"); exclusions != "1" {
			t.Errorf("user %s sees exclusions %q, want the count", user, exclusions)
		}
		if strings.Contains(got.content+got.embed.Description, "->") {
			t.Errorf("user %s sees a pair", user)
		}
	}
}

func TestRulesOrganizerSeesTheDrawTimeAsDefaultAndLockedAfterTheDraw(t *testing.T) {
	s := newScene(t)
	id := s.drawnExchange(t, "Winter")

	got := rulesField(t, rulesShow(t, s, s.a, id).embed, "Draw time")

	if !strings.Contains(got, "(default)") || !strings.HasSuffix(got, " - locked") {
		t.Errorf("organizer draw time = %q, want default and locked", got)
	}
}

func TestRulesOrganizerSeesTheDropoutPolicyOpenAfterTheDraw(t *testing.T) {
	s := newScene(t)
	id := s.drawnExchange(t, "Winter")

	got := rulesField(t, rulesShow(t, s, s.a, id).embed, "Dropout policy")

	if strings.Contains(got, "locked") {
		t.Errorf("organizer dropout policy = %q, want it open after the draw", got)
	}
}

func TestRulesViewMarksTheMinimumParticipantsAsFixed(t *testing.T) {
	s := newScene(t)
	id := s.drawnExchange(t, "Winter")

	got := rulesField(t, rulesShow(t, s, s.a, id).embed, "Minimum participants")

	if !strings.HasSuffix(got, " - fixed") {
		t.Errorf("minimum participants = %q, want fixed", got)
	}
}

func TestRulesViewStatesThatSelfAssignmentNeverHappens(t *testing.T) {
	s := newScene(t)
	id := s.drawnExchange(t, "Winter")

	got := rulesField(t, rulesShow(t, s, s.a, id).embed, "Self-assignment")

	if !strings.HasPrefix(got, "never") {
		t.Errorf("self-assignment = %q, want it to start with never", got)
	}
}

func TestRulesParticipantViewHasNoLockedSuffixAndNoSelects(t *testing.T) {
	s := newScene(t)
	id := s.drawnExchange(t, "Winter")

	participant := rulesShow(t, s, s.b, id)

	if got := rulesField(t, participant.embed, "Draw time"); strings.Contains(got, "locked") {
		t.Errorf("participant draw time = %q, want no locked suffix", got)
	}
	if len(participant.menus) != 0 {
		t.Errorf("participant selects = %+v", participant.menus)
	}
}

func TestRulesOrganizerSelectOffersOnlyTheFourRulesChangeableAfterTheDraw(t *testing.T) {
	s := newScene(t)
	id := s.drawnExchange(t, "Winter")

	organizer := rulesShow(t, s, s.a, id)

	if len(organizer.menus) != 1 || len(organizer.menus[0].Options) != 4 {
		t.Errorf("organizer menus after the draw = %+v, want one select of four rules", organizer.menus)
	}
}

func TestRulesEnumAndBoolRulesChangeThroughTheSelect(t *testing.T) {
	s := newScene(t)
	id := rulesOpenExchange(t, s, "Winter")
	cases := []struct {
		rule, option string
	}{
		{"dropout_policy", "redraw"},
		{"avoid_last_round", "on"},
		{"questions_enabled", "off"},
	}

	for _, tc := range cases {
		view := rulesShow(t, s, s.a, id)
		picked := rulesPick(t, s, s.a, view, id, tc.rule)

		if picked.CallbackType != rulesCallbackUpdate {
			t.Fatalf("%s: callback type = %d, want 7", tc.rule, picked.CallbackType)
		}
		options := rulesReplyOf(t, s, picked)
		wantID := "/gx/rule-option/" + id.String() + "/" + tc.rule
		if len(options.menus) != 1 || options.menus[0].CustomID != wantID {
			t.Fatalf("%s: menus = %+v, want one select %s", tc.rule, options.menus, wantID)
		}

		chosen, err := s.fake.Select(t.Context(), s.users[s.a], options.message, wantID, tc.option)
		if err != nil {
			t.Fatalf("Select option: %v", err)
		}
		refreshed := rulesReplyOf(t, s, chosen)

		if chosen.CallbackType != 6 {
			t.Errorf("%s: callback type = %d, want 6", tc.rule, chosen.CallbackType)
		}
		if got := rulesViewOf(t, s, s.a, id)[giftexchange.RuleName(tc.rule)].Value; got != tc.option {
			t.Errorf("%s = %q, want %q", tc.rule, got, tc.option)
		}
		if len(refreshed.menus) != 1 || refreshed.menus[0].CustomID != "/gx/rule-pick/"+id.String() {
			t.Errorf("%s: the refreshed view lost the rule select: %+v", tc.rule, refreshed.menus)
		}
	}
}

func TestRulesTimeRuleChangesThroughANeverPrefilledModal(t *testing.T) {
	s := newScene(t)
	id := rulesOpenExchange(t, s, "Winter")
	view := rulesShow(t, s, s.a, id)

	picked := rulesPick(t, s, s.a, view, id, "draw_at")

	if picked.CallbackType != rulesCallbackModal || picked.Modal == nil {
		t.Fatalf("callback type = %d, want a modal", picked.CallbackType)
	}
	modal := picked.Modal
	if modal.CustomID != "/gx/rule-modal/"+id.String()+"/draw_at" || modal.Title != "Set Draw time" {
		t.Errorf("modal = %q %q", modal.CustomID, modal.Title)
	}
	raw := string(modal.Components[0])
	for _, want := range []string{`"custom_id":"value"`, `"max_length":64`, `"required":false`, "New value", "in 3d, in 2h30m"} {
		if !strings.Contains(raw, want) {
			t.Errorf("modal %s lacks %s", raw, want)
		}
	}
	if strings.Contains(raw, `"value":"`) {
		t.Errorf("modal %s is prefilled", raw)
	}

	before := time.Now().UTC()
	reply, _ := rulesSubmit(t, s, id, "draw_at", "in 3d")

	drawAt, err := time.Parse(time.RFC3339, rulesViewOf(t, s, s.a, id)["draw_at"].Value)
	if err != nil {
		t.Fatalf("draw_at is not a time: %v", err)
	}
	if drawAt.Before(before.Add(71*time.Hour)) || drawAt.After(before.Add(73*time.Hour)) {
		t.Errorf("draw_at = %v, want about three days from %v", drawAt, before)
	}
	if got := rulesField(t, reply.embed, "Draw time"); !strings.HasPrefix(got, "<t:") {
		t.Errorf("refreshed draw time = %q, want Discord timestamps", got)
	}
}

func TestRulesEmptyModalInputClearsATimeRule(t *testing.T) {
	s := newScene(t)
	id := rulesOpenExchange(t, s, "Winter")
	rulesSubmit(t, s, id, "reveal_at", "in 10d")
	if got := rulesViewOf(t, s, s.a, id)["reveal_at"].Value; got == "unset" {
		t.Fatal("reveal_at was not set")
	}

	reply, _ := rulesSubmit(t, s, id, "reveal_at", "")

	if got := rulesViewOf(t, s, s.a, id)["reveal_at"].Value; got != "unset" {
		t.Errorf("reveal_at = %q, want unset", got)
	}
	if got := rulesField(t, reply.embed, "Reveal time"); !strings.HasPrefix(got, "not set") {
		t.Errorf("refreshed reveal time = %q", got)
	}
}

func TestRulesDurationRuleChangesThroughTheModal(t *testing.T) {
	s := newScene(t)
	id := rulesOpenExchange(t, s, "Winter")

	reply, _ := rulesSubmit(t, s, id, "reminder_lead", " 2d ")

	if got := rulesViewOf(t, s, s.a, id)["reminder_lead"].Value; got != "2d" {
		t.Errorf("reminder_lead = %q, want 2d", got)
	}
	if got := rulesField(t, reply.embed, "Reminder before each"); got != "2d" {
		t.Errorf("refreshed reminder = %q", got)
	}

	rulesSubmit(t, s, id, "reminder_lead", "0s")
	if got := rulesViewOf(t, s, s.a, id)["reminder_lead"].Value; got != "0s" {
		t.Errorf("reminder_lead = %q, want 0s", got)
	}

	rulesSubmit(t, s, id, "reminder_lead", "14d")
	if got := rulesViewOf(t, s, s.a, id)["reminder_lead"].Value; got != "14d" {
		t.Errorf("reminder_lead = %q, want 14d", got)
	}
}

func TestRulesDrawTimeAcceptsTheMaximumHorizon(t *testing.T) {
	s := newScene(t)
	id := rulesOpenExchange(t, s, "Winter")

	before := time.Now().UTC()
	reply, _ := rulesSubmit(t, s, id, "draw_at", "in 730d")

	if strings.HasPrefix(reply.content, rulesTextNotSaved) {
		t.Errorf("content = %q, want the 730-day draw time accepted", reply.content)
	}
	drawAt, err := time.Parse(time.RFC3339, rulesViewOf(t, s, s.a, id)["draw_at"].Value)
	if err != nil {
		t.Fatalf("draw_at is not a time: %v", err)
	}
	horizon := before.Add(730 * 24 * time.Hour)
	if drawAt.Before(horizon.Add(-time.Hour)) || drawAt.After(horizon.Add(time.Hour)) {
		t.Errorf("draw_at = %v, want about 730 days from %v", drawAt, before)
	}
}

func TestRulesInvalidModalInputKeepsTheViewAndListsTheProblemUnderNotSaved(t *testing.T) {
	s := newScene(t)
	id := rulesOpenExchange(t, s, "Winter")
	before := rulesViewOf(t, s, s.a, id)

	cases := map[string]struct {
		rule, typed, want string
	}{
		"a past draw time":    {"draw_at", "2020-01-01 10:00", "draw_at must be in the future"},
		"an unreadable time":  {"draw_at", "someday", "draw_at is not a time"},
		"more than 730 days":  {"draw_at", "in 9999d", "draw_at is not a time"},
		"an unreadable lead":  {"reminder_lead", "soon", "reminder_lead is not a duration"},
		"a lead over 14 days": {"reminder_lead", "15d", "reminder_lead is outside 0 to 14 days"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			reply, _ := rulesSubmit(t, s, id, tc.rule, tc.typed)

			if !strings.HasPrefix(reply.content, rulesTextNotSaved) || !strings.Contains(strings.ReplaceAll(reply.content, "\\", ""), tc.want) {
				t.Errorf("content = %q, want Not saved and %q", reply.content, tc.want)
			}
			if reply.embed.Title != "Rules - Winter" {
				t.Errorf("the reply dropped the view: %+v", reply.embed)
			}
		})
	}
	after := rulesViewOf(t, s, s.a, id)
	if after["draw_at"].Value != before["draw_at"].Value || after["reminder_lead"].Value != before["reminder_lead"].Value {
		t.Errorf("rules changed: %+v -> %+v", before, after)
	}
}

func TestRulesAbusiveModalTextIsRefusedAndNeverEchoed(t *testing.T) {
	s := newScene(t)
	id := rulesOpenExchange(t, s, "Winter")
	view := rulesShow(t, s, s.a, id)
	before := rulesViewOf(t, s, s.a, id)
	abusive := []string{
		strings.Repeat("9", 65),
		strings.Repeat("in 3d ", 40),
		"\u202ein 3d",
		"in\u200b 3d",
		"**in 3d** <@" + s.b + "> @everyone",
		"\U0001F381\U0001F381",
		"é́́",
	}

	for _, typed := range abusive {
		picked := rulesPick(t, s, s.a, view, id, "draw_at")
		submitted, err := s.fake.SubmitModalForged(t.Context(), s.users[s.a], *picked.Modal, map[string]string{rulesInputID: typed})
		if err != nil {
			t.Fatalf("SubmitModalForged: %v", err)
		}

		reply := rulesReplyOf(t, s, submitted)
		if !strings.HasPrefix(reply.content, rulesTextNotSaved) {
			t.Errorf("%q: content = %q, want Not saved", typed, reply.content)
		}
		if strings.Contains(reply.content, "everyone") || strings.Contains(reply.content, s.b) {
			t.Errorf("%q: the reply echoes the input: %q", typed, reply.content)
		}
	}
	if after := rulesViewOf(t, s, s.a, id); after["draw_at"].Value != before["draw_at"].Value {
		t.Errorf("draw_at changed to %q", after["draw_at"].Value)
	}
}

func TestRulesAForgedRuleOptionIsRefusedWithNothingChanged(t *testing.T) {
	s := newScene(t)
	id := rulesOpenExchange(t, s, "Winter")
	view := rulesShow(t, s, s.a, id)
	before := rulesViewOf(t, s, s.a, id)
	prefix := "/gx/rule-option/" + id.String() + "/"

	cases := map[string]struct {
		customID, value, want string
	}{
		"an unknown rule":              {prefix + "no_such_rule", "on", rulesTextUnknownRule},
		"a fixed rule":                 {prefix + "min_participants", "9", rulesTextFixed},
		"the exclusions rule":          {prefix + "exclusions", "9", "rule is edited"},
		"a value that is no option":    {prefix + "dropout_policy", "bogus", rulesTextNotSaved},
		"a malformed custom ID":        {"/gx/rule-option/" + id.String() + "/Bad-Rule", "on", textNotFound},
		"another action's custom ID":   {"/gx/rule-option/not-a-uuid/dropout_policy", "on", textNotFound},
		"a rule name with a long tail": {prefix + strings.Repeat("a", 40), "on", textNotFound},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			result, err := s.fake.SelectForged(t.Context(), s.users[s.a], view.message, tc.customID, tc.value)
			if err != nil {
				t.Fatalf("SelectForged: %v", err)
			}

			reply := rulesReplyOf(t, s, result)
			if tc.want != "" && !strings.Contains(reply.content, tc.want) {
				t.Errorf("content = %q, want %q", reply.content, tc.want)
			}
			if strings.Contains(reply.content, "bogus") {
				t.Errorf("the reply echoes the value: %q", reply.content)
			}
		})
	}
	if after := rulesViewOf(t, s, s.a, id); !rulesSameValues(before, after) {
		t.Errorf("rules changed: %+v -> %+v", before, after)
	}
}

func rulesSameValues(a, b map[giftexchange.RuleName]giftexchange.RuleView) bool {
	if len(a) != len(b) {
		return false
	}
	for name, rule := range a {
		if b[name].Value != rule.Value {
			return false
		}
	}

	return true
}

func TestRulesALockedRuleIsRefusedAfterTheDraw(t *testing.T) {
	s := newScene(t)
	id := s.drawnExchange(t, "Winter")
	view := rulesShow(t, s, s.a, id)
	before := rulesViewOf(t, s, s.a, id)

	result, err := s.fake.SelectForged(t.Context(), s.users[s.a], view.message, "/gx/rule-pick/"+id.String(), "draw_at")
	if err != nil {
		t.Fatalf("SelectForged: %v", err)
	}

	if result.Modal != nil {
		t.Error("a locked rule opened its modal")
	}
	if got := rulesReplyOf(t, s, result); got.content != rulesTextLockedDrawn {
		t.Errorf("content = %q, want %q", got.content, rulesTextLockedDrawn)
	}
	option, err := s.fake.SelectForged(t.Context(), s.users[s.a], view.message, "/gx/rule-option/"+id.String()+"/draw_at", "on")
	if err != nil {
		t.Fatalf("SelectForged: %v", err)
	}
	if got := rulesReplyOf(t, s, option); got.content != rulesTextLockedDrawn {
		t.Errorf("option content = %q, want %q", got.content, rulesTextLockedDrawn)
	}
	if after := rulesViewOf(t, s, s.a, id); !rulesSameValues(before, after) {
		t.Errorf("rules changed: %+v -> %+v", before, after)
	}
}

func TestRulesAForgedRulePickIsRefused(t *testing.T) {
	s := newScene(t)
	id := rulesOpenExchange(t, s, "Winter")
	view := rulesShow(t, s, s.a, id)

	for name, tc := range map[string]struct {
		value, want string
	}{
		"an unknown rule":     {"no_such_rule", rulesTextUnknownRule},
		"a fixed rule":        {"min_participants", rulesTextFixed},
		"the exclusions rule": {"exclusions", "rule is edited"},
	} {
		t.Run(name, func(t *testing.T) {
			result, err := s.fake.SelectForged(t.Context(), s.users[s.a], view.message, "/gx/rule-pick/"+id.String(), tc.value)
			if err != nil {
				t.Fatalf("SelectForged: %v", err)
			}

			if result.Modal != nil || result.CallbackType == rulesCallbackUpdate {
				t.Errorf("callback type %d opened something", result.CallbackType)
			}
			if got := rulesReplyOf(t, s, result); !strings.Contains(strings.ReplaceAll(got.content, "\\", ""), tc.want) {
				t.Errorf("content = %q, want %q", got.content, tc.want)
			}
		})
	}
}

func TestRulesAParticipantCannotChangeRulesByForgedComponents(t *testing.T) {
	s := newScene(t)
	id := rulesOpenExchange(t, s, "Winter")
	other := s.createExchange(t, s.b, "Spring")
	view := rulesShow(t, s, s.b, id)
	before := rulesViewOf(t, s, s.a, id)
	dmsBefore := len(s.fake.DMs(s.users[s.a])) + len(s.fake.DMs(s.users[s.b])) + len(s.fake.DMs(s.users[s.c]))

	results := map[string]fakediscord.Result{}
	var err error
	results["rule-pick"], err = s.fake.SelectForged(t.Context(), s.users[s.b], view.message, "/gx/rule-pick/"+id.String(), "draw_at")
	if err != nil {
		t.Fatalf("rule-pick: %v", err)
	}
	results["rule-option"], err = s.fake.SelectForged(t.Context(), s.users[s.b], view.message,
		"/gx/rule-option/"+id.String()+"/dropout_policy", "redraw")
	if err != nil {
		t.Fatalf("rule-option: %v", err)
	}

	opener := rulesShow(t, s, s.b, other)
	opened := rulesPick(t, s, s.b, opener, other, "draw_at")
	if opened.Modal == nil {
		t.Fatal("the organizer of the other exchange got no modal")
	}
	results["rule-modal"], err = s.fake.SubmitModalForged(t.Context(), s.users[s.b],
		fakediscord.Modal{CustomID: "/gx/rule-modal/" + id.String() + "/draw_at"}, map[string]string{rulesInputID: "in 3d"})
	if err != nil {
		t.Fatalf("rule-modal: %v", err)
	}

	for name, result := range results {
		isParticipantAction := name != "rule-modal"
		if got := rulesReplyOf(t, s, result); got.content != rulesTextForbidden {
			t.Errorf("%s: content = %q, want %q (participant action: %v)", name, got.content, rulesTextForbidden, isParticipantAction)
		}
	}
	if after := rulesViewOf(t, s, s.a, id); !rulesSameValues(before, after) {
		t.Errorf("rules changed: %+v -> %+v", before, after)
	}
	dmsAfter := len(s.fake.DMs(s.users[s.a])) + len(s.fake.DMs(s.users[s.b])) + len(s.fake.DMs(s.users[s.c]))
	if dmsAfter != dmsBefore {
		t.Errorf("DMs went from %d to %d", dmsBefore, dmsAfter)
	}
}

func TestRulesAnOutsiderOfAnotherGuildGetsNotFoundForEveryEntryPoint(t *testing.T) {
	s := newScene(t)
	id := rulesOpenExchange(t, s, "Winter")
	other := s.otherGuildWith(t, s.c)
	card := slashOrFail(t, s.harness, s.c, fakediscord.Slash{Path: "/ge/help", Guild: &other})
	before := rulesViewOf(t, s, s.a, id)
	pairsBefore := rulesPairsOf(t, s, id)

	results := map[string]fakediscord.Result{}
	var err error
	results["slash"] = slashOrFail(t, s.harness, s.c, fakediscord.Slash{
		Path: "/ge/rules", Guild: &other, Options: map[string]any{"exchange": id.String()},
	})
	results["exclude"] = slashOrFail(t, s.harness, s.c, fakediscord.Slash{
		Path: "/ge/exclude", Guild: &other,
		Options: map[string]any{"exchange": id.String(), "giver": s.b, "recipient": s.d},
	})
	results["button"], err = s.fake.ClickForged(t.Context(), s.users[s.c], *card.Message, "/gx/rules/"+id.String())
	if err != nil {
		t.Fatalf("button: %v", err)
	}
	results["rule-pick"], err = s.fake.SelectForged(t.Context(), s.users[s.c], *card.Message, "/gx/rule-pick/"+id.String(), "draw_at")
	if err != nil {
		t.Fatalf("rule-pick: %v", err)
	}
	results["rule-option"], err = s.fake.SelectForged(t.Context(), s.users[s.c], *card.Message,
		"/gx/rule-option/"+id.String()+"/dropout_policy", "redraw")
	if err != nil {
		t.Fatalf("rule-option: %v", err)
	}

	for name, result := range results {
		got := rulesReplyOf(t, s, result)
		if got.content != textNotFound || got.embed.Title != "" {
			t.Errorf("%s: reply = %q %+v, want only %q", name, got.content, got.embed, textNotFound)
		}
	}
	if after := rulesViewOf(t, s, s.a, id); !rulesSameValues(before, after) {
		t.Errorf("rules changed: %+v -> %+v", before, after)
	}
	if pairsAfter := rulesPairsOf(t, s, id); len(pairsAfter) != len(pairsBefore) {
		t.Errorf("exclusions changed: %d -> %d", len(pairsBefore), len(pairsAfter))
	}
}

func TestRulesInTheBotDMAnswerGuildOnly(t *testing.T) {
	s := newScene(t)
	id := rulesOpenExchange(t, s, "Winter")
	card := slashOrFail(t, s.harness, s.a, s.ge("help", false, nil))

	for _, verb := range []string{"rules", "exclude", "unexclude"} {
		options := map[string]any{"exchange": id.String()}
		if verb != "rules" {
			options["giver"] = s.b
			options["recipient"] = s.c
		}
		result := slashOrFail(t, s.harness, s.a, s.ge(verb, false, options))
		if got := replyTo(t, s.harness, result); got.content != guildOnlyText {
			t.Errorf("%s: reply = %q, want %q", verb, got.content, guildOnlyText)
		}
	}

	for name, customID := range map[string]string{
		"button":      "/gx/rules/" + id.String(),
		"rule-option": "/gx/rule-option/" + id.String() + "/dropout_policy",
	} {
		result, err := s.fake.ClickForged(t.Context(), s.users[s.a], *card.Message, customID)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := replyTo(t, s.harness, result); got.content != guildOnlyText {
			t.Errorf("%s: reply = %q, want %q", name, got.content, guildOnlyText)
		}
	}
}

func TestRulesAButtonClickOnARuleSelectCustomIDIsRefused(t *testing.T) {
	s := newScene(t)
	id := rulesOpenExchange(t, s, "Winter")
	view := rulesShow(t, s, s.a, id)
	before := rulesViewOf(t, s, s.a, id)

	for _, customID := range []string{
		"/gx/rule-pick/" + id.String(), "/gx/rule-option/" + id.String() + "/dropout_policy",
	} {
		result, err := s.fake.ClickForged(t.Context(), s.users[s.a], view.message, customID)
		if err != nil {
			t.Fatalf("ClickForged: %v", err)
		}

		if got := rulesReplyOf(t, s, result); got.content != textNotFound {
			t.Errorf("%s: content = %q, want %q", customID, got.content, textNotFound)
		}
	}
	if after := rulesViewOf(t, s, s.a, id); !rulesSameValues(before, after) {
		t.Errorf("rules changed: %+v -> %+v", before, after)
	}
}

func rulesExclude(t *testing.T, s *scene, verb, user string, id giftexchange.ExchangeID, options map[string]any) rulesReply {
	t.Helper()

	options["exchange"] = id.String()
	in := s.ge(verb, true, options)
	in.BypassValidation = true
	return rulesReplyOf(t, s, slashOrFail(t, s.harness, user, in))
}

func TestRulesExcludeUnexcludeAndTheOrganizersPairList(t *testing.T) {
	s := newScene(t)
	id := rulesOpenExchange(t, s, "Winter")
	s.joinAs(t, s.a, id)

	one := rulesExclude(t, s, "exclude", s.a, id, map[string]any{"giver": s.b, "recipient": s.c})
	if want := "Exclusion added: <@" + s.b + "> will not draw <@" + s.c + ">."; one.content != want {
		t.Errorf("exclude reply = %q, want %q", one.content, want)
	}
	if len(rulesPairsOf(t, s, id)) != 1 {
		t.Fatalf("pairs = %d, want 1", len(rulesPairsOf(t, s, id)))
	}

	mutual := rulesExclude(t, s, "exclude", s.a, id, map[string]any{"giver": s.a, "recipient": s.b, "both": true})
	if !strings.HasSuffix(mutual.content, " The reverse is excluded too.") {
		t.Errorf("mutual reply = %q", mutual.content)
	}
	if len(rulesPairsOf(t, s, id)) != 3 {
		t.Fatalf("pairs = %d, want 3", len(rulesPairsOf(t, s, id)))
	}

	organizer := rulesShow(t, s, s.a, id)
	lines := rulesField(t, organizer.embed, "Exclusions")
	for _, want := range []string{
		"<@" + s.b + "> -> <@" + s.c + ">", "<@" + s.a + "> -> <@" + s.b + ">", "<@" + s.b + "> -> <@" + s.a + ">",
	} {
		if !strings.Contains(lines, want) {
			t.Errorf("organizer exclusions %q lack %q", lines, want)
		}
	}

	removed := rulesExclude(t, s, "unexclude", s.a, id, map[string]any{"giver": s.b, "recipient": s.c})
	if want := "Exclusion removed: <@" + s.b + "> may draw <@" + s.c + "> again."; removed.content != want {
		t.Errorf("unexclude reply = %q, want %q", removed.content, want)
	}
	if len(rulesPairsOf(t, s, id)) != 2 {
		t.Errorf("pairs = %d, want 2", len(rulesPairsOf(t, s, id)))
	}
	if !slices.Equal(removed.visibleTo, []string{s.a}) {
		t.Errorf("unexclude reply is visible to %v", removed.visibleTo)
	}
}

func TestRulesOrganizerExclusionListCapsItsLinesAndCountsTheRest(t *testing.T) {
	s := newScene(t)
	id := rulesOpenExchange(t, s, "Winter")

	people := []string{s.a, s.b, s.c, s.d}
	for range 3 {
		subject := uniqueSnowflake(t)
		actor, err := s.svc.ResolveActor(t.Context(), giftexchange.IdentityRef{
			Provider: giftexchange.ProviderDiscord, Subject: subject,
		})
		if err != nil {
			t.Fatalf("ResolveActor: %v", err)
		}
		s.people[subject] = person{actor: actor, id: actor.Person()}
		people = append(people, subject)
	}
	for _, user := range people {
		err := s.svc.Join(t.Context(), s.people[user].actor, s.group, id)
		if err != nil && !errors.Is(err, giftexchange.ErrAlreadyParticipant) {
			t.Fatalf("Join: %v", err)
		}
	}

	pairs := 0
	for i, giver := range people {
		for _, recipient := range people[i+1:] {
			err := s.svc.AddExclusion(t.Context(), s.people[s.a].actor, s.group, id,
				s.people[giver].id, s.people[recipient].id, true)
			if err != nil {
				t.Fatalf("AddExclusion: %v", err)
			}
			pairs += 2
		}
	}
	if pairs <= 20 {
		t.Fatalf("pairs = %d, want more than the 20 lines the view lists", pairs)
	}

	lines := rulesField(t, rulesShow(t, s, s.a, id).embed, "Exclusions")

	if got := strings.Count(lines, " -> "); got != 20 {
		t.Errorf("listed pairs = %d, want 20", got)
	}
	if want := fmt.Sprintf("and %d more", pairs-20); !strings.HasSuffix(lines, want) {
		t.Errorf("exclusions %q do not end with %q", lines, want)
	}
	if got := utf8.RuneCountInString(lines); got > rulesEmbedFieldMaxRunes {
		t.Errorf("exclusions field has %d runes, want at most %d", got, rulesEmbedFieldMaxRunes)
	}
}

func TestRulesExclusionPairsReachNobodyButTheOrganizer(t *testing.T) {
	s := newScene(t)
	id := rulesOpenExchange(t, s, "Winter")
	rulesExclude(t, s, "exclude", s.a, id, map[string]any{"giver": s.b, "recipient": s.c, "both": true})

	rulesShow(t, s, s.a, id)
	rulesShow(t, s, s.b, id)
	rulesShow(t, s, s.c, id)
	rulesShow(t, s, s.d, id)

	for _, record := range s.fake.Outbound() {
		isOrganizerOnly := slices.Equal(record.VisibleTo, []string{s.a})
		if isOrganizerOnly {
			continue
		}

		text := record.Content
		for _, embed := range record.Embeds {
			text += string(embed)
		}
		if strings.Contains(text, "->") || strings.Contains(text, "will not draw") {
			t.Errorf("record %d, visible to %v, shows an exclusion pair: %s", record.Seq, record.VisibleTo, text)
		}
	}
	if logs := s.logs.String(); strings.Contains(logs, "->") || strings.Contains(logs, "will not draw") {
		t.Error("the logs hold an exclusion pair")
	}
}

func TestRulesExcludeRefusesTheWrongPeopleTogether(t *testing.T) {
	s := newScene(t)
	id := rulesOpenExchange(t, s, "Winter")
	unknown := "399999999999999999"

	cases := map[string]struct {
		options map[string]any
		want    []string
	}{
		"the same user twice": {map[string]any{"giver": s.b, "recipient": s.b}, []string{"recipient must differ from the giver"}},
		"a non-participant":   {map[string]any{"giver": s.b, "recipient": s.d}, []string{"giver and recipient must both be participants"}},
		"the bot":             {map[string]any{"giver": s.b, "recipient": s.fake.ApplicationID()}, []string{"recipient must be a person in this exchange"}},
		"an unknown user":     {map[string]any{"giver": unknown, "recipient": s.b}, []string{"giver must be a person in this exchange"}},
		"two unusable users":  {map[string]any{"giver": unknown, "recipient": unknown}, []string{"giver must be a person", "recipient must be a person"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			reply := rulesExclude(t, s, "exclude", s.a, id, tc.options)

			plain := strings.ReplaceAll(reply.content, "\\", "")
			if !strings.HasPrefix(plain, rulesTextNotSaved) {
				t.Fatalf("content = %q, want Not saved", reply.content)
			}
			for _, want := range tc.want {
				if !strings.Contains(plain, want) {
					t.Errorf("content = %q, want %q", plain, want)
				}
			}
		})
	}
	if pairs := rulesPairsOf(t, s, id); len(pairs) != 0 {
		t.Errorf("pairs = %d, want none", len(pairs))
	}
}

func TestRulesParticipantsAndMembersCannotExcludeOrUnexclude(t *testing.T) {
	s := newScene(t)
	id := rulesOpenExchange(t, s, "Winter")
	rulesExclude(t, s, "exclude", s.a, id, map[string]any{"giver": s.a, "recipient": s.b})
	pairsBefore := rulesPairsOf(t, s, id)
	dmsBefore := len(s.fake.DMs(s.users[s.a])) + len(s.fake.DMs(s.users[s.b])) + len(s.fake.DMs(s.users[s.c]))

	for _, user := range []string{s.b, s.d} {
		for _, verb := range []string{"exclude", "unexclude"} {
			reply := rulesExclude(t, s, verb, user, id, map[string]any{"giver": s.b, "recipient": s.c})

			if reply.content != rulesTextForbidden {
				t.Errorf("%s by %s: content = %q, want %q", verb, user, reply.content, rulesTextForbidden)
			}
		}
	}

	bare := slashOrFail(t, s.harness, s.b, s.ge("exclude", true, map[string]any{"giver": s.b, "recipient": s.c}))
	if got := rulesReplyOf(t, s, bare); got.content != textNoCandidates {
		t.Errorf("exclude without the option: content = %q, want %q", got.content, textNoCandidates)
	}
	pairsAfter := rulesPairsOf(t, s, id)
	if len(pairsAfter) != len(pairsBefore) {
		t.Errorf("pairs went from %d to %d", len(pairsBefore), len(pairsAfter))
	}
	dmsAfter := len(s.fake.DMs(s.users[s.a])) + len(s.fake.DMs(s.users[s.b])) + len(s.fake.DMs(s.users[s.c]))
	if dmsAfter != dmsBefore {
		t.Errorf("DMs went from %d to %d", dmsBefore, dmsAfter)
	}
}

func TestRulesExcludeAfterTheDrawIsRefused(t *testing.T) {
	s := newScene(t)
	id := s.drawnExchange(t, "Winter")

	reply := rulesExclude(t, s, "exclude", s.a, id, map[string]any{"giver": s.b, "recipient": s.c})

	if reply.content != textNotPossibleDrawn {
		t.Errorf("content = %q, want %q", reply.content, textNotPossibleDrawn)
	}
	if pairs := rulesPairsOf(t, s, id); len(pairs) != 0 {
		t.Errorf("pairs = %d, want none", len(pairs))
	}
}

func TestRulesExcludeWithoutTheExchangeOptionPicksTheOnlyOpenExchange(t *testing.T) {
	s := newScene(t)
	id := rulesOpenExchange(t, s, "Winter")

	result := slashOrFail(t, s.harness, s.a, s.ge("exclude", true, map[string]any{"giver": s.b, "recipient": s.c}))

	if got := rulesReplyOf(t, s, result); !strings.HasPrefix(got.content, "Exclusion added") {
		t.Errorf("content = %q", got.content)
	}
	if len(rulesPairsOf(t, s, id)) != 1 {
		t.Errorf("pairs = %d, want 1", len(rulesPairsOf(t, s, id)))
	}
}

func TestRulesSelectsAndModalsInTheBotDMAnswerGuildOnly(t *testing.T) {
	s := newScene(t)
	id := rulesOpenExchange(t, s, "Winter")
	card := slashOrFail(t, s.harness, s.a, s.ge("help", false, nil))
	before := rulesViewOf(t, s, s.a, id)

	pick, err := s.fake.SelectForged(t.Context(), s.users[s.a], *card.Message, "/gx/rule-pick/"+id.String(), "draw_at")
	if err != nil {
		t.Fatalf("rule-pick: %v", err)
	}
	option, err := s.fake.SelectForged(t.Context(), s.users[s.a], *card.Message,
		"/gx/rule-option/"+id.String()+"/dropout_policy", "redraw")
	if err != nil {
		t.Fatalf("rule-option: %v", err)
	}
	modal, err := s.fake.SubmitModalForged(t.Context(), s.users[s.a],
		fakediscord.Modal{CustomID: "/gx/rule-modal/" + id.String() + "/draw_at"}, map[string]string{rulesInputID: "in 3d"})
	if err != nil {
		t.Fatalf("rule-modal: %v", err)
	}

	for name, result := range map[string]fakediscord.Result{"rule-pick": pick, "rule-option": option, "rule-modal": modal} {
		if got := rulesReplyOf(t, s, result); got.content != guildOnlyText {
			t.Errorf("%s: content = %q, want %q", name, got.content, guildOnlyText)
		}
	}
	if after := rulesViewOf(t, s, s.a, id); !rulesSameValues(before, after) {
		t.Errorf("rules changed: %+v -> %+v", before, after)
	}
}

func TestLocaleResolutionUsesUserThenGuildLocaleAndFallsBackToEnglish(t *testing.T) {
	s := newScene(t)

	cases := []struct {
		name        string
		guildLocale string
		userLocale  string
		exchange    string
		wantConfirm string
		wantCard    string
	}{
		{
			name:        "es-CL user in en-US guild gets Spanish",
			guildLocale: "en-US",
			userLocale:  "es-CL",
			exchange:    "Navidad",
			wantConfirm: "Creaste **Navidad** y lo organizas.",
			wantCard:    "Sorteo",
		},
		{
			name:        "es-419 user in fr guild gets Spanish",
			guildLocale: "fr",
			userLocale:  "es-419",
			exchange:    "Invierno",
			wantConfirm: "Creaste **Invierno** y lo organizas.",
			wantCard:    "Sorteo",
		},
		{
			name:        "unsupported user locale in es-ES guild falls back to guild Spanish",
			guildLocale: "es-ES",
			userLocale:  "fr",
			exchange:    "Reyes",
			wantConfirm: "Creaste **Reyes** y lo organizas.",
			wantCard:    "Sorteo",
		},
		{
			name:        "en-US user in es-419 guild gets guild Spanish",
			guildLocale: "es-419",
			userLocale:  "en-US",
			exchange:    "Winter",
			wantConfirm: "Creaste **Winter** y lo organizas.",
			wantCard:    "Sorteo",
		},
		{
			name:        "unsupported user and guild locales fall back to en-US",
			guildLocale: "fr",
			userLocale:  "de",
			exchange:    "Hiver",
			wantConfirm: "You created **Hiver** and you organize it.",
			wantCard:    "Draw",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.fake.SetGuildLocale(s.guild, tc.guildLocale); err != nil {
				t.Fatalf("SetGuildLocale: %v", err)
			}
			if err := s.fake.SetUserLocale(s.users[s.a], tc.userLocale); err != nil {
				t.Fatalf("SetUserLocale: %v", err)
			}

			before := len(s.fake.Outbound())
			res := slashOrFail(t, s.harness, s.a, s.ge("create", true, map[string]any{"name": tc.exchange}))
			got := replyTo(t, s.harness, res)
			if !strings.HasPrefix(got.content, tc.wantConfirm) {
				t.Errorf("create confirmation = %q, want prefix %q", got.content, tc.wantConfirm)
			}

			waitFor(t, "public card followup", func() bool {
				for _, rec := range s.fake.Outbound()[before:] {
					if rec.Source == "followup" && !rec.Ephemeral {
						return true
					}
				}
				return false
			})
			var cardRaw string
			for _, rec := range s.fake.Outbound()[before:] {
				if rec.Source == "followup" && !rec.Ephemeral && len(rec.Embeds) > 0 {
					cardRaw = string(rec.Embeds[0])
				}
			}
			if !strings.Contains(cardRaw, tc.wantCard) {
				t.Errorf("card embed = %s, want field %q", cardRaw, tc.wantCard)
			}
		})
	}
}

func TestLocaleSwitchingRendersInteractionsAndDMsInSpanishOrEnglish(t *testing.T) {
	s := newScene(t)
	s.startRunner(t, s.world)

	if err := s.fake.SetGuildLocale(s.guild, "es-419"); err != nil {
		t.Fatalf("SetGuildLocale: %v", err)
	}
	for _, u := range []string{s.a, s.b, s.c} {
		if err := s.fake.SetUserLocale(s.users[u], "es-419"); err != nil {
			t.Fatalf("SetUserLocale(%s): %v", u, err)
		}
	}

	id := rulesOpenExchange(t, s, "Winter")
	s.joinAs(t, s.a, id)

	view := rulesShow(t, s, s.a, id)
	if view.embed.Title != "Reglas - Winter" {
		t.Errorf("rules title = %q, want Reglas - Winter", view.embed.Title)
	}

	// Non-organizer attempting to draw gets Spanish forbidden error.
	denied := replyTo(t, s.harness, slashOrFail(t, s.harness, s.b, s.ge("draw", true, map[string]any{"exchange": id.String()})))
	if denied.content != "No tienes permiso para hacer eso en este intercambio." {
		t.Errorf("forbidden draw reply = %q, want Spanish forbidden text", denied.content)
	}

	// Organizer draws in Spanish and participants receive Spanish draw DMs.
	drawAsk := rulesReplyOf(t, s, slashOrFail(t, s.harness, s.a, s.ge("draw", true, map[string]any{"exchange": id.String()})))
	if !strings.Contains(drawAsk.content, "¿Sortear **Winter** ahora con 3 ") {
		t.Errorf("draw prompt = %q, want Spanish prompt", drawAsk.content)
	}
	confirmed, err := s.fake.Click(t.Context(), s.users[s.a], drawAsk.message, "/gx/draw-confirm/"+id.String())
	if err != nil {
		t.Fatalf("Click draw-confirm: %v", err)
	}
	if got := replyTo(t, s.harness, confirmed); got.content != "Sorteo realizado para **Winter**." {
		t.Errorf("draw confirm reply = %q, want Spanish drawn text", got.content)
	}
	if got := rulesReplyOf(t, s, confirmed); !strings.HasPrefix(got.content, "El sorteo de **Winter** está listo.") {
		t.Errorf("draw public followup = %q, want Spanish public drawn text", got.content)
	}
	waitFor(t, "the three Spanish draw DMs", func() bool {
		return finishedCount(t, id, "delivered") == 3
	})
	dms := s.fake.DMs(s.users[s.b])
	if len(dms) == 0 || !strings.Contains(dms[len(dms)-1].Content, "El sorteo de **Winter** (Test server) está listo.") {
		t.Errorf("participant DM = %+v, want Spanish draw DM", dms)
	}

	// Switch guild and user a to en-US; subsequent interactions for user a render in English.
	if err := s.fake.SetGuildLocale(s.guild, "en-US"); err != nil {
		t.Fatalf("SetGuildLocale: %v", err)
	}
	if err := s.fake.SetUserLocale(s.users[s.a], "en-US"); err != nil {
		t.Fatalf("SetUserLocale: %v", err)
	}
	drawnView := rulesShow(t, s, s.a, id)
	if drawnView.embed.Title != "Rules - Winter" {
		t.Errorf("rules title = %q, want Rules - Winter", drawnView.embed.Title)
	}

	revealAsk := rulesReplyOf(t, s, slashOrFail(t, s.harness, s.a, s.ge("reveal", true, map[string]any{"exchange": id.String()})))
	if !strings.Contains(revealAsk.content, "Reveal **Winter** now? Each participant will learn their giver.") {
		t.Errorf("reveal prompt = %q, want English prompt", revealAsk.content)
	}
}

func TestExchangeLessRepliesUseInteractionLocaleWithFallbackToEnglish(t *testing.T) {
	s := newScene(t)

	if err := s.fake.SetUserLocale(s.users[s.a], "es-419"); err != nil {
		t.Fatalf("SetUserLocale(es-419): %v", err)
	}

	helpES := replyTo(t, s.harness, slashOrFail(t, s.harness, s.a, s.ge("help", true, nil)))
	if !strings.Contains(helpES.content, "/ge create - crear un intercambio y organizarlo") {
		t.Errorf("help (es-419) = %q, want Spanish help line", helpES.content)
	}

	listES := rulesReplyOf(t, s, slashOrFail(t, s.harness, s.a, s.ge("list", true, nil)))
	if listES.embed.Title != "Intercambios en este servidor" {
		t.Errorf("list (es-419) title = %q, want Intercambios en este servidor", listES.embed.Title)
	}

	dmCreateES := replyTo(t, s.harness, slashOrFail(t, s.harness, s.a, s.ge("create", false, map[string]any{"name": "X"})))
	if !strings.HasSuffix(dmCreateES.content, "solo funciona en un servidor.") {
		t.Errorf("guild-only in DM (es-419) = %q, want Spanish guild-only text", dmCreateES.content)
	}

	noCandES := replyTo(t, s.harness, slashOrFail(t, s.harness, s.a, s.ge("join", true, nil)))
	if noCandES.content != "No tienes ningún intercambio donde puedas hacer esto." {
		t.Errorf("no candidates (es-419) = %q, want Spanish text", noCandES.content)
	}

	// Unsupported user locale falls back to English.
	if err := s.fake.SetUserLocale(s.users[s.a], "de"); err != nil {
		t.Fatalf("SetUserLocale(de): %v", err)
	}
	helpFallback := replyTo(t, s.harness, slashOrFail(t, s.harness, s.a, s.ge("help", true, nil)))
	if !strings.Contains(helpFallback.content, "Every command also works as /chatisimo giftexchange <verb>.") {
		t.Errorf("help (de fallback) = %q, want English lead", helpFallback.content)
	}
}

func TestSpanishRelativeTimesInCreateAndRulesModalAcrossExchangeLanguages(t *testing.T) {
	s := newScene(t)

	for _, guildLocale := range []string{"en-US", "es-419"} {
		t.Run("create/"+guildLocale, func(t *testing.T) {
			if err := s.fake.SetGuildLocale(s.guild, guildLocale); err != nil {
				t.Fatalf("SetGuildLocale(%s): %v", guildLocale, err)
			}

			exchangeName := "Exchange-" + guildLocale
			before := time.Now().UTC()
			res := slashOrFail(t, s.harness, s.a, s.ge("create", true, map[string]any{
				"name":      exchangeName,
				"draw-at":   "en 1d12h",
				"reveal-at": "en 3d",
			}))
			_ = replyTo(t, s.harness, res)

			listed, err := s.svc.ExchangesFor(t.Context(), s.people[s.a].actor, &s.group)
			if err != nil {
				t.Fatalf("ExchangesFor: %v", err)
			}
			var id giftexchange.ExchangeID
			for _, ex := range listed {
				if ex.Name == exchangeName {
					id = ex.ID
					break
				}
			}
			if id == (giftexchange.ExchangeID{}) {
				t.Fatalf("exchange %q not found", exchangeName)
			}

			rules := rulesViewOf(t, s, s.a, id)
			drawAt, err := time.Parse(time.RFC3339, rules["draw_at"].Value)
			if err != nil {
				t.Fatalf("parse draw_at: %v", err)
			}
			if drawAt.Before(before.Add(35*time.Hour)) || drawAt.After(before.Add(37*time.Hour)) {
				t.Errorf("draw_at = %v, want ~36h after %v", drawAt, before)
			}

			revealAt, err := time.Parse(time.RFC3339, rules["reveal_at"].Value)
			if err != nil {
				t.Fatalf("parse reveal_at: %v", err)
			}
			if revealAt.Before(before.Add(71*time.Hour)) || revealAt.After(before.Add(73*time.Hour)) {
				t.Errorf("reveal_at = %v, want ~72h after %v", revealAt, before)
			}

			// Update reveal_at then draw_at via /ge rules modal using Spanish relative times.
			modalBefore := time.Now().UTC()
			rulesSubmit(t, s, id, "reveal_at", "en 3d")
			rulesSubmit(t, s, id, "draw_at", "en 1d12h")

			afterRules := rulesViewOf(t, s, s.a, id)
			modalDrawAt, err := time.Parse(time.RFC3339, afterRules["draw_at"].Value)
			if err != nil {
				t.Fatalf("parse modal draw_at: %v", err)
			}
			if modalDrawAt.Before(modalBefore.Add(35*time.Hour)) || modalDrawAt.After(modalBefore.Add(37*time.Hour)) {
				t.Errorf("modal draw_at = %v, want ~36h after %v", modalDrawAt, modalBefore)
			}
			modalRevealAt, err := time.Parse(time.RFC3339, afterRules["reveal_at"].Value)
			if err != nil {
				t.Fatalf("parse modal reveal_at: %v", err)
			}
			if modalRevealAt.Before(modalBefore.Add(71*time.Hour)) || modalRevealAt.After(modalBefore.Add(73*time.Hour)) {
				t.Errorf("modal reveal_at = %v, want ~72h after %v", modalRevealAt, modalBefore)
			}
		})
	}
}

func TestES419TimeModalPlaceholderAndBadTimeHintsShowSpanishForm(t *testing.T) {
	s := newScene(t)
	if err := s.fake.SetGuildLocale(s.guild, "es-419"); err != nil {
		t.Fatalf("SetGuildLocale(es-419): %v", err)
	}

	// Invalid draw-at on /ge create in es-419 guild replies with textBadTime showing "en 3d, en 2h30m".
	badCreate := replyTo(t, s.harness, slashOrFail(t, s.harness, s.a, s.ge("create", true, map[string]any{
		"name":    "BadTimeES",
		"draw-at": "mañana",
	})))
	const wantBadCreate = "Usa en 3d, en 2h30m, YYYY-MM-DD HH:MM (hora de Santiago) o una fecha RFC 3339."
	if badCreate.content != wantBadCreate {
		t.Errorf("create bad time reply = %q, want %q", badCreate.content, wantBadCreate)
	}

	// Create valid es-419 exchange via /ge create and inspect time modal placeholder and invalid modal error.
	res := slashOrFail(t, s.harness, s.a, s.ge("create", true, map[string]any{"name": "Navidad"}))
	_ = replyTo(t, s.harness, res)
	listed, err := s.svc.ExchangesFor(t.Context(), s.people[s.a].actor, &s.group)
	if err != nil || len(listed) != 1 {
		t.Fatalf("ExchangesFor: %v, len=%d", err, len(listed))
	}
	id := listed[0].ID

	view := rulesShow(t, s, s.a, id)
	picked := rulesPick(t, s, s.a, view, id, "draw_at")
	if picked.Modal == nil {
		t.Fatalf("picking draw_at opened no modal: %+v", picked)
	}
	rawModal := string(picked.Modal.Components[0])
	if !strings.Contains(rawModal, "en 3d, en 2h30m, 2026-12-20 18:00 (hora de Santiago), o vacío para borrar") {
		t.Errorf("es-419 time modal = %s, want Spanish placeholder with en 3d, en 2h30m", rawModal)
	}

	reply, _ := rulesSubmit(t, s, id, "draw_at", "mañana")
	plain := strings.ReplaceAll(reply.content, "\\", "")
	const wantModalErr = "fecha de sorteo no es una fecha válida como en 3d o 2026-12-20 18:00 (hora de Santiago)"
	if !strings.HasPrefix(plain, "No se guardó:") || !strings.Contains(plain, wantModalErr) {
		t.Errorf("es-419 modal bad time reply = %q, want prefix 'No se guardó:' and %q", plain, wantModalErr)
	}
}
