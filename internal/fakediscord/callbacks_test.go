package fakediscord_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
)

func TestSlashReplyIsPublicToTheGuild(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{})

	sent, result := w.answerSlash(t, w.alice, w.inGuild("/ping", nil), `{"type":4,"data":{"content":"pong"}}`)

	if sent.ApplicationID != w.srv.ApplicationID() {
		t.Errorf("application_id = %q, want the fake's application", sent.ApplicationID)
	}
	if result.Status != fakediscord.StatusResponded || result.CallbackType != 4 || result.Message == nil {
		t.Fatalf("result = %+v, want a responded type 4 with a message", result)
	}
	if result.Message.Content != "pong" || result.Message.EphemeralFor != "" {
		t.Errorf("message = %+v, want public pong", result.Message)
	}
	for _, member := range []fakediscord.User{w.alice, w.bruno} {
		visible := w.srv.Visible(member, w.guild.ChannelID)
		if len(visible) != 1 || visible[0].Content != "pong" {
			t.Errorf("%s sees %+v, want the reply", member.Name, visible)
		}
	}
	if seen := w.srv.Visible(w.carol, w.guild.ChannelID); len(seen) != 0 {
		t.Errorf("an outsider sees %+v", seen)
	}
	rec := lastRecord(t, w.srv)
	if !slices.Equal(rec.VisibleTo, []string{aliceID, brunoID}) || rec.Source != "callback" || rec.Ephemeral {
		t.Errorf("record = %+v, want a public callback visible to both members", rec)
	}
	if rec.InteractionID == nil || *rec.InteractionID != sent.ID {
		t.Errorf("record interaction_id = %v, want %s", rec.InteractionID, sent.ID)
	}
}

func TestEphemeralReplyIsVisibleToTheActorOnly(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{})

	_, result := w.answerSlash(t, w.alice, w.inGuild("/ping", nil), `{"type":4,"data":{"content":"just you","flags":64}}`)

	if result.Message == nil || result.Message.EphemeralFor != aliceID {
		t.Fatalf("message = %+v, want ephemeral for Alice", result.Message)
	}
	if got := w.srv.Visible(w.alice, w.guild.ChannelID); len(got) != 1 {
		t.Errorf("Alice sees %d messages, want 1", len(got))
	}
	if got := w.srv.Visible(w.bruno, w.guild.ChannelID); len(got) != 0 {
		t.Errorf("Bruno sees %+v, want nothing", got)
	}
	rec := lastRecord(t, w.srv)
	if !rec.Ephemeral || !slices.Equal(rec.VisibleTo, []string{aliceID}) {
		t.Errorf("record = %+v, want ephemeral visible only to Alice", rec)
	}
}

func TestDMInteractionReplyIsVisibleToThatUser(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{})

	sent, result := w.answerSlash(t, w.carol, fakediscord.Slash{Path: "/ping"}, `{"type":4,"data":{"content":"hi Carol"}}`)

	if sent.GuildID != "" || len(sent.Member) != 0 || len(sent.User) == 0 {
		t.Errorf("DM interaction = %+v, want a user and no guild or member", sent)
	}
	if result.Message == nil {
		t.Fatal("no message in the result")
	}
	if dms := w.srv.DMs(w.carol); len(dms) != 1 || dms[0].Content != "hi Carol" {
		t.Errorf("Carol's DMs = %+v, want the reply", dms)
	}
	if got := w.srv.Visible(w.carol, result.Message.ChannelID); len(got) != 1 {
		t.Errorf("Visible agrees with DMs: got %+v", got)
	}
	if got := w.srv.Visible(w.alice, result.Message.ChannelID); len(got) != 0 {
		t.Errorf("another user sees Carol's DM: %+v", got)
	}
	rec := lastRecord(t, w.srv)
	if !slices.Equal(rec.VisibleTo, []string{carolID}) || rec.GuildID != nil {
		t.Errorf("record = %+v, want visible to Carol with no guild", rec)
	}
}

func TestDeferredReplyBecomesAMessageOnEdit(t *testing.T) {
	tests := []struct {
		name          string
		deferBody     string
		wantBrunoSees bool
	}{
		{"public", `{"type":5}`, true},
		{"ephemeral", `{"type":5,"data":{"flags":64}}`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := newInteractionWorld(t, fakediscord.Options{})
			sent, result := w.answerSlash(t, w.alice, w.inGuild("/ping", nil), tc.deferBody)
			if result.Status != fakediscord.StatusDeferred || result.CallbackType != 5 || result.Message != nil {
				t.Fatalf("result = %+v, want deferred with no message", result)
			}
			if got := w.srv.Visible(w.alice, w.guild.ChannelID); len(got) != 0 {
				t.Fatalf("a deferred reply shows %+v before it is edited", got)
			}

			edited := call(t, w.srv, http.MethodPatch, sent.webhookPath(w.srv, "/messages/@original"), "", `{"content":"done"}`)

			if edited.status != http.StatusOK || edited.json(t)["content"] != "done" {
				t.Fatalf("edit = %d %s, want 200 with the content", edited.status, edited.body)
			}
			if got := w.srv.Visible(w.alice, w.guild.ChannelID); len(got) != 1 || got[0].Content != "done" {
				t.Errorf("Alice sees %+v, want the edited reply", got)
			}
			if got := w.srv.Visible(w.bruno, w.guild.ChannelID); (len(got) == 1) != tc.wantBrunoSees {
				t.Errorf("Bruno sees %+v, want visible=%v", got, tc.wantBrunoSees)
			}
			rec := lastRecord(t, w.srv)
			if rec.Source != "edit" || rec.Route != "PATCH /webhooks/{app}/{token}/messages/@original" {
				t.Errorf("record = %+v, want an edit of @original", rec)
			}
		})
	}
}

func TestDeferredReplyCanBeAnsweredByAFollowup(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{})
	sent, _ := w.answerSlash(t, w.alice, w.inGuild("/ping", nil), `{"type":5,"data":{"flags":64}}`)

	got := call(t, w.srv, http.MethodPost, sent.webhookPath(w.srv, ""), "", `{"content":"first"}`)

	if got.status != http.StatusOK {
		t.Fatalf("followup = %d %s", got.status, got.body)
	}
	if seen := w.srv.Visible(w.bruno, w.guild.ChannelID); len(seen) != 0 {
		t.Errorf("Bruno sees %+v, want the first followup to take the deferral's ephemeral visibility", seen)
	}
	original := call(t, w.srv, http.MethodGet, sent.webhookPath(w.srv, "/messages/@original"), "", "")
	if original.status != http.StatusOK || original.json(t)["content"] != "first" {
		t.Errorf("@original = %d %s, want the first followup", original.status, original.body)
	}
}

func TestFollowupsAndOriginalMessageRoutes(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{})
	sent, _ := w.answerSlash(t, w.alice, w.inGuild("/ping", nil), `{"type":4,"data":{"content":"pong"}}`)
	original := sent.webhookPath(w.srv, "/messages/@original")

	t.Run("GET @original returns the reply", func(t *testing.T) {
		got := call(t, w.srv, http.MethodGet, original, "", "")

		if got.status != http.StatusOK || got.json(t)["content"] != "pong" {
			t.Errorf("GET = %d %s", got.status, got.body)
		}
	})

	t.Run("PATCH @original changes the reply", func(t *testing.T) {
		got := call(t, w.srv, http.MethodPatch, original, "", `{"content":"pong 2"}`)

		if got.status != http.StatusOK || got.json(t)["content"] != "pong 2" {
			t.Errorf("PATCH = %d %s", got.status, got.body)
		}
		rec := lastRecord(t, w.srv)
		if rec.Source != "edit" || rec.Content != "pong 2" {
			t.Errorf("record = %+v, want the edit with the new content", rec)
		}
	})

	var followupID string
	t.Run("a followup is a new public message", func(t *testing.T) {
		got := call(t, w.srv, http.MethodPost, sent.webhookPath(w.srv, ""), "", `{"content":"more"}`)

		if got.status != http.StatusOK {
			t.Fatalf("followup = %d %s", got.status, got.body)
		}
		followupID, _ = got.json(t)["id"].(string)
		if seen := w.srv.Visible(w.bruno, w.guild.ChannelID); len(seen) != 2 {
			t.Errorf("Bruno sees %d messages, want the reply and the followup", len(seen))
		}
		if rec := lastRecord(t, w.srv); rec.Source != "followup" {
			t.Errorf("record source = %q, want followup", rec.Source)
		}
	})

	t.Run("a followup with flags 64 is ephemeral", func(t *testing.T) {
		got := call(t, w.srv, http.MethodPost, sent.webhookPath(w.srv, ""), "", `{"content":"psst","flags":64}`)

		if got.status != http.StatusOK {
			t.Fatalf("followup = %d %s", got.status, got.body)
		}
		if rec := lastRecord(t, w.srv); !rec.Ephemeral || !slices.Equal(rec.VisibleTo, []string{aliceID}) {
			t.Errorf("record = %+v, want ephemeral for Alice", rec)
		}
	})

	t.Run("a followup can be read, edited and deleted by ID", func(t *testing.T) {
		path := sent.webhookPath(w.srv, "/messages/"+followupID)

		if got := call(t, w.srv, http.MethodGet, path, "", ""); got.status != http.StatusOK {
			t.Errorf("GET = %d %s", got.status, got.body)
		}
		if got := call(t, w.srv, http.MethodPatch, path, "", `{"content":"more 2"}`); got.status != http.StatusOK || got.json(t)["content"] != "more 2" {
			t.Errorf("PATCH = %d %s", got.status, got.body)
		}
		if got := call(t, w.srv, http.MethodDelete, path, "", ""); got.status != http.StatusNoContent {
			t.Errorf("DELETE = %d %s", got.status, got.body)
		}
		if got := call(t, w.srv, http.MethodGet, path, "", ""); got.status != http.StatusNotFound || got.json(t)["code"] != float64(10008) {
			t.Errorf("GET after DELETE = %d %s, want 404 10008", got.status, got.body)
		}
	})

	t.Run("DELETE @original removes the reply", func(t *testing.T) {
		got := call(t, w.srv, http.MethodDelete, original, "", "")

		if got.status != http.StatusNoContent {
			t.Errorf("DELETE = %d %s", got.status, got.body)
		}
		if after := call(t, w.srv, http.MethodGet, original, "", ""); after.status != http.StatusNotFound {
			t.Errorf("GET after DELETE = %d, want 404", after.status)
		}
	})
}

func TestWebhookRoutesRefuseMessagesTheInteractionDoesNotOwn(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{})
	first, _ := w.answerSlash(t, w.alice, w.inGuild("/ping", nil), `{"type":4,"data":{"content":"one"}}`)
	second, secondResult := w.answerSlash(t, w.bruno, w.inGuild("/ping", nil), `{"type":4,"data":{"content":"two"}}`)
	posted := postChannelMessage(t, w.srv, w.guild.ChannelID, "from the channel", "", "")
	channelMessageID, _ := posted.json(t)["id"].(string)

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"another interaction's reply", http.MethodPatch, first.webhookPath(w.srv, "/messages/"+secondResult.Message.ID)},
		{"a channel message", http.MethodPatch, first.webhookPath(w.srv, "/messages/"+channelMessageID)},
		{"a channel message read", http.MethodGet, first.webhookPath(w.srv, "/messages/"+channelMessageID)},
		{"a channel message delete", http.MethodDelete, second.webhookPath(w.srv, "/messages/"+channelMessageID)},
		{"an unknown message", http.MethodPatch, first.webhookPath(w.srv, "/messages/"+missingID)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := call(t, w.srv, tc.method, tc.path, "", `{"content":"hijack"}`)

			if got.status != http.StatusNotFound || got.json(t)["code"] != float64(10008) {
				t.Errorf("%s = %d %s, want 404 10008", tc.method, got.status, got.body)
			}
		})
	}
	for _, msg := range w.srv.Visible(w.alice, w.guild.ChannelID) {
		if msg.Content == "hijack" {
			t.Errorf("a refused edit changed %+v", msg)
		}
	}
}

func TestUpdateAndDeferUpdateCallbacksOnAComponent(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantType    int
		wantStatus  string
		wantContent string
	}{
		{"type 6 acknowledges without changing", `{"type":6}`, 6, fakediscord.StatusDeferred, "Join us"},
		{"type 7 updates the message", `{"type":7,"data":{"content":"Joined"}}`, 7, fakediscord.StatusResponded, "Joined"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := newInteractionWorld(t, fakediscord.Options{})
			message := w.botMessage(t, "Join us", buttonRow)
			done := startAction(t, func(ctx context.Context) (fakediscord.Result, error) {
				return w.srv.Click(ctx, w.alice, message, "join:1")
			})
			sent := w.bot.nextInteraction(t)

			got := w.respond(t, sent, tc.body)

			if got.status != http.StatusNoContent {
				t.Fatalf("callback = %d %s", got.status, got.body)
			}
			finished := finish(t, done)
			if finished.err != nil {
				t.Fatalf("Click: %v", finished.err)
			}
			if finished.result.CallbackType != tc.wantType || finished.result.Status != tc.wantStatus {
				t.Errorf("result = %+v, want type %d %s", finished.result, tc.wantType, tc.wantStatus)
			}
			visible := w.srv.Visible(w.bruno, w.guild.ChannelID)
			if len(visible) != 1 || visible[0].Content != tc.wantContent || visible[0].ID != message.ID {
				t.Errorf("Bruno sees %+v, want the same message with %q", visible, tc.wantContent)
			}
		})
	}
}

func TestAutocompleteReturnsTheBotsChoices(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{})
	type outcome struct {
		choices []fakediscord.Choice
		err     error
	}
	done := make(chan outcome, 1)
	ctx := testContext(t, actionTimeout)
	go func() {
		choices, err := w.srv.Autocomplete(ctx, w.alice, w.inGuild("/ge/join", map[string]any{"exchange": "wi"}), "exchange")
		done <- outcome{choices, err}
	}()
	sent := w.bot.nextInteraction(t)
	if sent.Type != 4 || !strings.Contains(string(sent.Data), `"focused":true`) || !strings.Contains(string(sent.Data), `"value":"wi"`) {
		t.Fatalf("autocomplete interaction = type %d data %s, want type 4 with the focused partial value", sent.Type, sent.Data)
	}

	got := w.respond(t, sent, `{"type":8,"data":{"choices":[{"name":"Winter exchange","value":"x1"},{"name":"Wish list","value":"x2"}]}}`)

	if got.status != http.StatusNoContent {
		t.Fatalf("callback = %d %s", got.status, got.body)
	}
	finished := waitChan(t, done, actionTimeout, "Autocomplete")
	if finished.err != nil || len(finished.choices) != 2 || finished.choices[0].Name != "Winter exchange" || finished.choices[1].Value != "x2" {
		t.Errorf("choices = %+v, err %v", finished.choices, finished.err)
	}
	if len(w.srv.Outbound()) != 0 {
		t.Errorf("autocomplete choices were recorded as outbound messages: %+v", w.srv.Outbound())
	}
}

func TestModalIsReturnedAndCanBeSubmitted(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{})
	modalBody := `{"type":9,"data":{"custom_id":"wish:1","title":"Your wish","components":[` +
		`{"type":1,"components":[{"type":4,"custom_id":"wish","style":1,"label":"Wish","max_length":10}]}]}}`
	_, opened := w.answerSlash(t, w.alice, w.inGuild("/ping", nil), modalBody)
	if opened.Status != fakediscord.StatusResponded || opened.CallbackType != 9 || opened.Modal == nil || opened.Modal.CustomID != "wish:1" {
		t.Fatalf("result = %+v, want the modal", opened)
	}

	done := startAction(t, func(ctx context.Context) (fakediscord.Result, error) {
		return w.srv.SubmitModal(ctx, w.alice, *opened.Modal, map[string]string{"wish": "socks"})
	})
	submit := w.bot.nextInteraction(t)

	if submit.Type != 5 || !strings.Contains(string(submit.Data), `"custom_id":"wish:1"`) || !strings.Contains(string(submit.Data), `"value":"socks"`) {
		t.Fatalf("submit = type %d data %s, want a modal submit with the field", submit.Type, submit.Data)
	}
	if got := w.respond(t, submit, `{"type":4,"data":{"content":"thanks","flags":64}}`); got.status != http.StatusNoContent {
		t.Fatalf("callback = %d %s", got.status, got.body)
	}
	if finished := finish(t, done); finished.err != nil || finished.result.Message == nil {
		t.Errorf("SubmitModal = %+v, %v", finished.result, finished.err)
	}

	again, err := w.srv.SubmitModal(testContext(t, actionTimeout), w.alice, *opened.Modal, map[string]string{"wish": "again"})
	if err == nil {
		t.Errorf("a second submit of a closed modal succeeded: %+v", again)
	}
}

func TestInvalidCallbackTypesAreRefusedAndChangeNothing(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"pong", `{"type":1}`},
		{"zero", `{"type":0}`},
		{"acknowledge pseudo type", `{"type":-1}`},
		{"launch activity", `{"type":12}`},
		{"unknown", `{"type":99}`},
		{"no type", `{}`},
		{"autocomplete on a slash command", `{"type":8,"data":{"choices":[]}}`},
		{"message update on a slash command", `{"type":7,"data":{"content":"x"}}`},
		{"deferred update on a slash command", `{"type":6}`},
	}
	w := newInteractionWorld(t, fakediscord.Options{})
	sent, done := w.slashAs(t, w.alice, w.inGuild("/ping", nil))
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := w.respond(t, sent, tc.body)

			if got.status != http.StatusBadRequest || got.json(t)["code"] != float64(50035) {
				t.Errorf("callback = %d %s, want 400 50035", got.status, got.body)
			}
		})
	}

	valid := w.respond(t, sent, `{"type":4,"data":{"content":"still open"}}`)

	if valid.status != http.StatusNoContent {
		t.Errorf("a valid callback after the refused ones = %d %s, want 204", valid.status, valid.body)
	}
	finish(t, done)
}

func TestBadCallbackDataIsRefused(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{"empty message", `{"type":4,"data":{}}`, 50006},
		{"no data for a message", `{"type":4}`, 50035},
		{"data of the wrong shape", `{"type":4,"data":[1]}`, 50035},
		{"bad allowed_mentions", `{"type":4,"data":{"content":"x","allowed_mentions":[1]}}`, 50035},
		{"body is not JSON", `nope`, 50035},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := newInteractionWorld(t, fakediscord.Options{})
			sent, done := w.slashAs(t, w.alice, w.inGuild("/ping", nil))

			got := w.respond(t, sent, tc.body)

			if got.status != http.StatusBadRequest || got.json(t)["code"] != float64(tc.want) {
				t.Errorf("callback = %d %s, want 400 %d", got.status, got.body, tc.want)
			}
			if len(w.srv.Outbound()) != 0 {
				t.Errorf("a refused callback was recorded: %+v", w.srv.Outbound())
			}
			w.respond(t, sent, `{"type":5}`)
			finish(t, done)
		})
	}
}

func TestBadAutocompleteAndModalCallbacksAreRefused(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{})
	tooMany := `{"type":8,"data":{"choices":[` + strings.Repeat(`{"name":"a","value":"b"},`, 25) + `{"name":"a","value":"b"}]}}`
	done := make(chan error, 1)
	ctx := testContext(t, actionTimeout)
	go func() {
		_, err := w.srv.Autocomplete(ctx, w.alice, w.inGuild("/ge/join", map[string]any{"exchange": "x"}), "exchange")
		done <- err
	}()
	sent := w.bot.nextInteraction(t)

	for name, body := range map[string]string{
		"26 choices":         tooMany,
		"a choice w/o name":  `{"type":8,"data":{"choices":[{"value":"b"}]}}`,
		"choices not a list": `{"type":8,"data":{"choices":"x"}}`,
	} {
		if got := w.respond(t, sent, body); got.status != http.StatusBadRequest {
			t.Errorf("%s: callback = %d %s, want 400", name, got.status, got.body)
		}
	}
	w.respond(t, sent, `{"type":8,"data":{"choices":[]}}`)
	if err := waitChan(t, done, actionTimeout, "Autocomplete"); err != nil {
		t.Errorf("Autocomplete: %v", err)
	}

	slashInteraction, slashDone := w.slashAs(t, w.alice, w.inGuild("/ping", nil))
	for name, body := range map[string]string{
		"modal without a title":       `{"type":9,"data":{"custom_id":"m","components":[{"type":1}]}}`,
		"modal with a long custom id": `{"type":9,"data":{"custom_id":"` + strings.Repeat("x", 101) + `","title":"t","components":[{"type":1}]}}`,
		"modal without components":    `{"type":9,"data":{"custom_id":"m","title":"t","components":[]}}`,
	} {
		if got := w.respond(t, slashInteraction, body); got.status != http.StatusBadRequest {
			t.Errorf("%s: callback = %d %s, want 400", name, got.status, got.body)
		}
	}
	w.respond(t, slashInteraction, `{"type":5}`)
	finish(t, slashDone)
}

func TestSecondCallbackIsRefused(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{})
	sent, _ := w.answerSlash(t, w.alice, w.inGuild("/ping", nil), `{"type":4,"data":{"content":"first"}}`)

	got := w.respond(t, sent, `{"type":4,"data":{"content":"second"}}`)

	if got.status != http.StatusBadRequest || got.json(t)["code"] != float64(40060) {
		t.Errorf("second callback = %d %s, want 400 40060", got.status, got.body)
	}
	visible := w.srv.Visible(w.alice, w.guild.ChannelID)
	if len(visible) != 1 || visible[0].Content != "first" {
		t.Errorf("Alice sees %+v, want only the first reply", visible)
	}
	records := w.srv.Outbound()
	rejected := records[len(records)-1]
	if rejected.Status != http.StatusBadRequest || rejected.Content != "second" || rejected.MessageID != "" {
		t.Errorf("last record = %+v, want the rejected attempt with status 400", rejected)
	}
}

func TestCallbackAndWebhookRoutesRefuseUnknownTokens(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{})
	sent, done := w.slashAs(t, w.alice, w.inGuild("/ping", nil))
	other := sentInteraction{ID: sent.ID, Token: strings.Repeat("A", 32)}
	wrongID := sentInteraction{ID: missingID, Token: sent.Token}

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"callback with an unknown token", http.MethodPost, other.callbackPath()},
		{"callback with another interaction's id", http.MethodPost, wrongID.callbackPath()},
		{"followup with an unknown token", http.MethodPost, other.webhookPath(w.srv, "")},
		{"original edit with an unknown token", http.MethodPatch, other.webhookPath(w.srv, "/messages/@original")},
		{"original read with an unknown token", http.MethodGet, other.webhookPath(w.srv, "/messages/@original")},
		{"original delete with an unknown token", http.MethodDelete, other.webhookPath(w.srv, "/messages/@original")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := call(t, w.srv, tc.method, tc.path, "", `{"type":4,"content":"x","data":{"content":"x"}}`)

			if got.status != http.StatusNotFound || got.json(t)["code"] != float64(10062) {
				t.Errorf("%s = %d %s, want 404 10062", tc.method, got.status, got.body)
			}
		})
	}

	t.Run("a webhook path with the wrong application", func(t *testing.T) {
		got := call(t, w.srv, http.MethodGet, "/api/v10/webhooks/"+missingID+"/"+sent.Token+"/messages/@original", "", "")

		if got.status != http.StatusNotFound || got.json(t)["code"] != float64(10002) {
			t.Errorf("GET = %d %s, want 404 10002", got.status, got.body)
		}
	})

	w.respond(t, sent, `{"type":5}`)
	finish(t, done)
}

func TestWebhookRoutesBeforeTheCallbackAnswerUnknownMessage(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{})
	sent, done := w.slashAs(t, w.alice, w.inGuild("/ping", nil))
	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"edit original", http.MethodPatch, sent.webhookPath(w.srv, "/messages/@original")},
		{"read original", http.MethodGet, sent.webhookPath(w.srv, "/messages/@original")},
		{"delete original", http.MethodDelete, sent.webhookPath(w.srv, "/messages/@original")},
		{"followup", http.MethodPost, sent.webhookPath(w.srv, "")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := call(t, w.srv, tc.method, tc.path, "", `{"content":"too early"}`)

			if got.status != http.StatusNotFound || got.json(t)["code"] != float64(10008) {
				t.Errorf("%s = %d %s, want 404 10008", tc.method, got.status, got.body)
			}
		})
	}
	if len(w.srv.Outbound()) != 0 || len(w.srv.Visible(w.alice, w.guild.ChannelID)) != 0 {
		t.Error("an out-of-order call changed the world")
	}

	w.respond(t, sent, `{"type":5}`)
	finish(t, done)
}

func TestInteractionRoutesDoNotWantTheBotToken(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{})
	sent, done := w.slashAs(t, w.alice, w.inGuild("/ping", nil))

	got := w.respond(t, sent, `{"type":5}`)

	if got.status != http.StatusNoContent {
		t.Errorf("callback without Authorization = %d, want 204", got.status)
	}
	finish(t, done)
}

func TestCallbackAfterTheDeadlineIsRefused(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{CallbackDeadline: 50 * time.Millisecond})
	sent, done := w.slashAs(t, w.alice, w.inGuild("/ping", nil))

	finished := finish(t, done)

	if finished.err != nil || finished.result.Status != fakediscord.StatusTimeout || finished.result.CallbackType != 0 {
		t.Fatalf("result = %+v, err %v, want a timeout", finished.result, finished.err)
	}
	late := w.respond(t, sent, `{"type":4,"data":{"content":"too late"}}`)
	if late.status != http.StatusNotFound || late.json(t)["code"] != float64(10062) {
		t.Errorf("late callback = %d %s, want 404 10062", late.status, late.body)
	}
	for _, tc := range []struct{ method, tail string }{
		{http.MethodPost, ""},
		{http.MethodPatch, "/messages/@original"},
		{http.MethodGet, "/messages/@original"},
	} {
		got := call(t, w.srv, tc.method, sent.webhookPath(w.srv, tc.tail), "", `{"content":"late"}`)
		if got.status != http.StatusNotFound || got.json(t)["code"] != float64(10062) {
			t.Errorf("late %s %s = %d %s, want 404 10062", tc.method, tc.tail, got.status, got.body)
		}
	}
	if seen := w.srv.Visible(w.alice, w.guild.ChannelID); len(seen) != 0 {
		t.Errorf("a late callback showed %+v", seen)
	}
	rec := lastRecord(t, w.srv)
	if rec.Status != http.StatusNotFound || rec.Content != "too late" {
		t.Errorf("last record = %+v, want the rejected late attempt", rec)
	}
}

func TestAutocompleteTimesOutWithAnError(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{CallbackDeadline: 30 * time.Millisecond})

	_, err := w.srv.Autocomplete(testContext(t, actionTimeout), w.alice, w.inGuild("/ge/join", map[string]any{"exchange": "x"}), "exchange")

	if !errors.Is(err, fakediscord.ErrNoCallback) {
		t.Errorf("Autocomplete error = %v, want ErrNoCallback", err)
	}
}

func TestActionsFailWithoutABotAndOnAClosedServer(t *testing.T) {
	srv := startFake(t, fakediscord.Options{})
	alice := srv.AddUser(fakediscord.User{ID: aliceID, Name: "Alice"})
	srv.AddGuild(fakediscord.Guild{ID: winterGuildID, Name: "Winter guild"}, alice)
	call(t, srv, http.MethodPut, "/api/v10/applications/"+srv.ApplicationID()+"/commands", botAuth(srv), geCommands)

	_, err := srv.Slash(testContext(t, actionTimeout), alice, fakediscord.Slash{Path: "/ping"})
	if !errors.Is(err, fakediscord.ErrNoBot) {
		t.Errorf("Slash without a bot = %v, want ErrNoBot", err)
	}

	closeErr := srv.Close()
	if closeErr != nil {
		t.Fatalf("Close: %v", closeErr)
	}
	_, err = srv.Slash(testContext(t, actionTimeout), alice, fakediscord.Slash{Path: "/ping"})
	if !errors.Is(err, fakediscord.ErrClosed) {
		t.Errorf("Slash on a closed server = %v, want ErrClosed", err)
	}
}

func TestCancellingTheContextEndsTheWait(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := w.srv.Slash(ctx, w.alice, w.inGuild("/ping", nil))
		done <- err
	}()
	w.bot.nextInteraction(t)

	cancel()

	err := waitChan(t, done, actionTimeout, "Slash to return")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Slash error = %v, want context.Canceled", err)
	}
}

func TestCallbackToAnAbandonedInteractionIsRefusedAfterTheDeadline(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{CallbackDeadline: 50 * time.Millisecond})
	ctx, cancel := context.WithCancel(t.Context())
	abandoned := make(chan error, 1)
	go func() {
		_, err := w.srv.Slash(ctx, w.alice, w.inGuild("/ping", nil))
		abandoned <- err
	}()
	first := w.bot.nextInteraction(t)
	cancel()
	if err := waitChan(t, abandoned, actionTimeout, "the abandoned Slash"); !errors.Is(err, context.Canceled) {
		t.Fatalf("abandoned Slash = %v, want context.Canceled", err)
	}

	// A later interaction with the same deadline times out, so by then the first one is past its own.
	_, later := w.slashAs(t, w.alice, w.inGuild("/ping", nil))
	if finished := finish(t, later); finished.result.Status != fakediscord.StatusTimeout {
		t.Fatalf("later result = %+v, want a timeout", finished.result)
	}

	got := w.respond(t, first, `{"type":4,"data":{"content":"too late"}}`)

	if got.status != http.StatusNotFound || got.json(t)["code"] != float64(10062) {
		t.Errorf("callback to the abandoned interaction = %d %s, want 404 10062", got.status, got.body)
	}
}

func TestDeletedDeferredOriginalIsGone(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{})
	sent, _ := w.answerSlash(t, w.alice, w.inGuild("/ping", nil), `{"type":5}`)
	original := sent.webhookPath(w.srv, "/messages/@original")
	created := call(t, w.srv, http.MethodPatch, original, "", `{"content":"done"}`)
	if created.status != http.StatusOK {
		t.Fatalf("first PATCH = %d %s, want 200", created.status, created.body)
	}
	deleted := call(t, w.srv, http.MethodDelete, original, "", "")
	if deleted.status != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s, want 204", deleted.status, deleted.body)
	}

	got := call(t, w.srv, http.MethodPatch, original, "", `{"content":"again"}`)

	if got.status != http.StatusNotFound {
		t.Errorf("PATCH after DELETE = %d %s, want 404", got.status, got.body)
	}
}

func FuzzCallbackBody(f *testing.F) {
	seeds := []string{
		``, `nope`, `{}`, `{"type":4}`, `{"type":4,"data":{}}`, `{"type":4,"data":[1]}`,
		`{"type":4,"data":{"content":"x","allowed_mentions":[1]}}`,
		`{"type":4,"data":{"content":5}}`, `{"type":7,"data":null}`, `{"type":8,"data":{"choices":[{}]}}`,
		`{"type":9,"data":{"custom_id":"a","components":"x"}}`, `{"type":5,"data":{"flags":"x"}}`,
		`{"type":"4"}`, `{"type":-1}`, `{"type":4,"data":{"content":"` + strings.Repeat("a", 4096) + `"}}`,
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, body string) {
		w := newInteractionWorld(t, fakediscord.Options{})
		sent, done := w.slashAs(t, w.alice, w.inGuild("/ping", nil))

		got := w.respond(t, sent, body)

		if got.status < 200 || got.status > 499 {
			t.Fatalf("callback answered %d for body %q", got.status, body)
		}
		if got.status >= 400 {
			w.respond(t, sent, `{"type":5}`)
		}
		finish(t, done)
	})
}
