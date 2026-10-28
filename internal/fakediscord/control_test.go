package fakediscord_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
)

// control sends a request to the control API and decodes a JSON object answer.
func control(t testing.TB, srv *fakediscord.Server, method, path, body string) reply {
	t.Helper()

	return call(t, srv, method, "/_control"+path, "", body)
}

func (r reply) array(t testing.TB) []map[string]any {
	t.Helper()

	var decoded []map[string]any
	err := json.Unmarshal(r.body, &decoded)
	if err != nil {
		t.Fatalf("response is not a JSON array: %v (status %d, body %q)", err, r.status, r.body)
	}

	return decoded
}

// controlInteraction posts to /_control/interactions in the background and returns the answer.
func (w *interactionWorld) controlInteraction(t *testing.T, body string) <-chan reply {
	t.Helper()

	done := make(chan reply, 1)
	go func() {
		done <- control(t, w.srv, http.MethodPost, "/interactions", body)
	}()

	return done
}

func TestHealthzReportsTheBotStatus(t *testing.T) {
	srv := startFake(t, fakediscord.Options{})

	absent := control(t, srv, http.MethodGet, "/healthz", "")
	absentGate := control(t, srv, http.MethodGet, "/healthz?bot=1", "")
	identifyRaw(t, srv)
	connecting := control(t, srv, http.MethodGet, "/healthz?bot=1", "")
	registerPing(t, srv)
	ready := control(t, srv, http.MethodGet, "/healthz?bot=1", "")

	if absent.status != http.StatusOK || absent.json(t)["server"] != "up" || absent.json(t)["bot"] != "absent" {
		t.Errorf("healthz = %d %s, want 200 with the bot absent", absent.status, absent.body)
	}
	if absentGate.status != http.StatusServiceUnavailable || absentGate.json(t)["bot"] != "absent" {
		t.Errorf("healthz?bot=1 without a bot = %d %s, want 503", absentGate.status, absentGate.body)
	}
	if connecting.status != http.StatusServiceUnavailable || connecting.json(t)["bot"] != "connecting" {
		t.Errorf("healthz?bot=1 before commands = %d %s, want 503 connecting", connecting.status, connecting.body)
	}
	if ready.status != http.StatusOK || ready.json(t)["bot"] != "ready" {
		t.Errorf("healthz?bot=1 once ready = %d %s, want 200 ready", ready.status, ready.body)
	}
	if err := srv.WaitReady(testContext(t, time.Second)); err != nil {
		t.Errorf("WaitReady = %v, but healthz said ready", err)
	}
}

func TestTokenEndpointReturnsOnlyTheToken(t *testing.T) {
	srv := newFake(t, fakediscord.Options{})

	got := control(t, srv, http.MethodGet, "/token", "")

	if got.status != http.StatusOK || string(got.body) != srv.Token() {
		t.Errorf("token = %d %q, want the fake's token", got.status, got.body)
	}
	if !strings.HasPrefix(got.header.Get("Content-Type"), "text/plain") {
		t.Errorf("content type = %q, want text/plain", got.header.Get("Content-Type"))
	}
}

func TestUsersGuildsAndMembersThroughTheControlAPI(t *testing.T) {
	srv := newFake(t, fakediscord.Options{})

	alice := control(t, srv, http.MethodPost, "/users", `{"name":"Alice","id":"`+aliceID+`"}`).json(t)
	generated := control(t, srv, http.MethodPost, "/users", `{"name":"Bruno"}`).json(t)
	if alice["id"] != aliceID || alice["name"] != "Alice" || alice["dms_open"] != true {
		t.Errorf("created user = %v", alice)
	}
	brunoGenerated, _ := generated["id"].(string)
	if brunoGenerated == "" {
		t.Fatalf("a user without an id got none: %v", generated)
	}

	guild := control(t, srv, http.MethodPost, "/guilds", `{"name":"Winter guild","members":["`+aliceID+`"]}`)
	if guild.status != http.StatusOK {
		t.Fatalf("create guild = %d %s", guild.status, guild.body)
	}
	guildBody := guild.json(t)
	guildID, _ := guildBody["id"].(string)
	if guildBody["channel_id"] == "" || guildBody["channel"] != "general" {
		t.Errorf("guild = %v, want a general channel", guildBody)
	}

	joined := control(t, srv, http.MethodPost, "/guilds/"+guildID+"/members", `{"user_id":"`+brunoGenerated+`"}`)
	if joined.status != http.StatusNoContent {
		t.Errorf("add member = %d %s, want 204", joined.status, joined.body)
	}

	state := control(t, srv, http.MethodGet, "/state", "").json(t)
	guilds, _ := state["guilds"].([]any)
	members, _ := guilds[0].(map[string]any)["members"].([]any)
	if len(guilds) != 1 || len(members) != 2 || len(state["users"].([]any)) != 2 {
		t.Errorf("state = %v, want 2 users and one guild with 2 members", state)
	}

	patched := control(t, srv, http.MethodPatch, "/users/"+aliceID, `{"dms_open":false}`)
	if patched.status != http.StatusOK || patched.json(t)["dms_open"] != false {
		t.Errorf("patch user = %d %s", patched.status, patched.body)
	}
	dm := openDM(t, srv, aliceID)
	closed := call(t, srv, http.MethodPost, "/api/v10/channels/"+dm+"/messages", botAuth(srv), `{"content":"hi"}`)
	if closed.status != http.StatusForbidden || closed.json(t)["code"] != float64(50007) {
		t.Errorf("DM after closing DMs through the control API = %d %s, want 403 50007", closed.status, closed.body)
	}
}

func TestControlAPIRefusesBadRequests(t *testing.T) {
	srv := newFake(t, fakediscord.Options{})
	srv.AddUser(fakediscord.User{ID: aliceID, Name: "Alice"})
	guild := srv.AddGuild(fakediscord.Guild{ID: winterGuildID, Name: "Winter guild"})
	tests := []struct {
		name   string
		method string
		path   string
		body   string
		want   int
	}{
		{"create user from malformed JSON", http.MethodPost, "/users", `{`, 400},
		{"create user with a bad id", http.MethodPost, "/users", `{"name":"x","id":"abc"}`, 400},
		{"create user with a name over 100 characters", http.MethodPost, "/users", `{"name":"` + strings.Repeat("n", 101) + `"}`, 400},
		{"create user with a name that is not a string", http.MethodPost, "/users", `{"name":5}`, 400},
		{"patch an unknown user", http.MethodPatch, "/users/" + missingID, `{"dms_open":true}`, 404},
		{"patch without dms_open", http.MethodPatch, "/users/" + aliceID, `{}`, 400},
		{"patch with dms_open not a boolean", http.MethodPatch, "/users/" + aliceID, `{"dms_open":"no"}`, 400},
		{"create guild without a name", http.MethodPost, "/guilds", `{"members":[]}`, 400},
		{"create guild with an unknown member", http.MethodPost, "/guilds", `{"name":"g","members":["` + missingID + `"]}`, 400},
		{"add a member to an unknown guild", http.MethodPost, "/guilds/" + missingID + "/members", `{"user_id":"` + aliceID + `"}`, 404},
		{"add an unknown user as a member", http.MethodPost, "/guilds/" + guild.ID + "/members", `{"user_id":"` + missingID + `"}`, 404},
		{"a body over 1 MiB", http.MethodPost, "/users", `{"name":"` + strings.Repeat("n", 2<<20) + `"}`, 413},
		{"messages without as", http.MethodGet, "/channels/" + guild.ChannelID + "/messages", "", 400},
		{"messages of an unknown channel", http.MethodGet, "/channels/" + missingID + "/messages?as=" + aliceID, "", 404},
		{"messages as an unknown user", http.MethodGet, "/channels/" + guild.ChannelID + "/messages?as=" + missingID, "", 404},
		{"DMs of an unknown user", http.MethodGet, "/dms/" + missingID, "", 404},
		{"modal of a user without one", http.MethodGet, "/modals/" + aliceID, "", 404},
		{"outbound with a bad since", http.MethodGet, "/outbound?since=abc", "", 400},
		{"outbound with a negative since", http.MethodGet, "/outbound?since=-1", "", 400},
		{"fault on an unknown route", http.MethodPost, "/faults", `{"route":"POST /nowhere","status":500}`, 400},
		{"fault with a success status", http.MethodPost, "/faults", `{"route":"GET /gateway","status":200}`, 400},
		{"fault from malformed JSON", http.MethodPost, "/faults", `[`, 400},
		{"drop with a code Discord never sends", http.MethodPost, "/gateway/drop", `{"code":5000}`, 400},
		{"events as an unknown user", http.MethodGet, "/events?as=" + missingID, "", 404},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := control(t, srv, tc.method, tc.path, tc.body)

			if got.status != tc.want {
				t.Errorf("%s %s = %d %.100s, want %d", tc.method, tc.path, got.status, got.body, tc.want)
			}
			var errBody struct {
				Code    *int   `json:"code"`
				Message string `json:"message"`
			}
			if json.Unmarshal(got.body, &errBody) != nil || errBody.Code == nil || errBody.Message == "" {
				t.Errorf("error body %.100q is not {code, message}", got.body)
			}
		})
	}
	if _, ok := srv.Intents(); ok || len(srv.Outbound()) != 0 {
		t.Error("a refused control request changed the world")
	}
}

func TestStateListsTheBotsFactsWithoutTheToken(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{})

	state := control(t, w.srv, http.MethodGet, "/state", "")
	commands := control(t, w.srv, http.MethodGet, "/commands", "")

	body := state.json(t)
	names, _ := body["commands"].([]any)
	if body["bot"] != "ready" || body["intents"] != float64(1) || len(names) != 2 {
		t.Errorf("state = %v, want a ready bot, intents 1 and 2 commands", body)
	}
	definitions := commands.array(t)
	if len(definitions) != 2 || definitions[0]["id"] == "" || definitions[0]["application_id"] != w.srv.ApplicationID() {
		t.Errorf("commands = %v, want the registered definitions as stored", definitions)
	}
	for name, endpoint := range map[string]reply{"state": state, "commands": commands} {
		if strings.Contains(string(endpoint.body), w.srv.Token()) {
			t.Errorf("/%s contains the bot token", name)
		}
	}
}

func TestInteractionsThroughTheControlAPI(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{})
	message := w.botMessage(t, "Join us", buttonRow)
	menu := w.botMessage(t, "Pick", menuRow)

	tests := []struct {
		name       string
		request    string
		callback   string
		wantStatus string
		check      func(t *testing.T, body map[string]any)
	}{
		{
			"a slash command answered with a message",
			`{"as":"` + aliceID + `","kind":"slash","guild_id":"` + winterGuildID + `","path":"/ping"}`,
			`{"type":4,"data":{"content":"pong"}}`, "responded",
			func(t *testing.T, body map[string]any) {
				if body["callback_type"] != float64(4) || body["message_id"] == nil || body["interaction_id"] == "" {
					t.Errorf("body = %v, want a type 4 with a message", body)
				}
			},
		},
		{
			"a slash command that is deferred",
			`{"as":"` + aliceID + `","kind":"slash","path":"/ping"}`,
			`{"type":5}`, "deferred",
			func(t *testing.T, body map[string]any) {
				if body["callback_type"] != float64(5) || body["message_id"] != nil {
					t.Errorf("body = %v, want a deferral with no message", body)
				}
			},
		},
		{
			"an autocomplete",
			`{"as":"` + aliceID + `","kind":"autocomplete","guild_id":"` + winterGuildID + `","path":"/ge/join","options":{"exchange":"wi"},"focused":"exchange"}`,
			`{"type":8,"data":{"choices":[{"name":"Winter","value":"w1"}]}}`, "responded",
			func(t *testing.T, body map[string]any) {
				choices, _ := body["choices"].([]any)
				if len(choices) != 1 || choices[0].(map[string]any)["value"] != "w1" {
					t.Errorf("body = %v, want the choice", body)
				}
			},
		},
		{
			"a slash command answered with a modal",
			`{"as":"` + brunoID + `","kind":"slash","path":"/ping"}`,
			`{"type":9,"data":{"custom_id":"wish:1","title":"Wish","components":[{"type":1,"components":[{"type":4,"custom_id":"wish","style":1,"label":"W"}]}]}}`,
			"responded",
			func(t *testing.T, body map[string]any) {
				modal, _ := body["modal"].(map[string]any)
				if modal["custom_id"] != "wish:1" || modal["user_id"] != brunoID {
					t.Errorf("body = %v, want the modal", body)
				}
			},
		},
		{
			"a button click",
			`{"as":"` + aliceID + `","kind":"button","message_id":"` + message.ID + `","custom_id":"join:1"}`,
			`{"type":7,"data":{"content":"Joined"}}`, "responded",
			func(t *testing.T, body map[string]any) {
				if body["message_id"] != message.ID {
					t.Errorf("body = %v, want the updated message", body)
				}
			},
		},
		{
			"a menu selection",
			`{"as":"` + aliceID + `","kind":"select","message_id":"` + menu.ID + `","custom_id":"pick","values":["a","b"]}`,
			`{"type":6}`, "deferred", func(*testing.T, map[string]any) {},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			done := w.controlInteraction(t, tc.request)
			sent := w.bot.nextInteraction(t)
			if got := w.respond(t, sent, tc.callback); got.status != http.StatusNoContent {
				t.Fatalf("callback = %d %s", got.status, got.body)
			}

			got := waitChan(t, done, actionTimeout, "the control API to answer")

			body := got.json(t)
			if got.status != http.StatusOK || body["status"] != tc.wantStatus || body["interaction_id"] != sent.ID {
				t.Fatalf("answer = %d %s, want %s for %s", got.status, got.body, tc.wantStatus, sent.ID)
			}
			tc.check(t, body)
		})
	}

	t.Run("a modal submit", func(t *testing.T) {
		pending := control(t, w.srv, http.MethodGet, "/modals/"+brunoID, "")
		if pending.status != http.StatusOK || pending.json(t)["custom_id"] != "wish:1" {
			t.Fatalf("pending modal = %d %s", pending.status, pending.body)
		}

		done := w.controlInteraction(t, `{"as":"`+brunoID+`","kind":"modal","fields":{"wish":"socks"}}`)
		sent := w.bot.nextInteraction(t)
		w.respond(t, sent, `{"type":4,"data":{"content":"thanks"}}`)

		got := waitChan(t, done, actionTimeout, "the control API to answer")
		if got.status != http.StatusOK || got.json(t)["status"] != "responded" || sent.Type != 5 {
			t.Errorf("answer = %d %s", got.status, got.body)
		}
		if after := control(t, w.srv, http.MethodGet, "/modals/"+brunoID, ""); after.status != http.StatusNotFound {
			t.Errorf("modal after submit = %d, want 404", after.status)
		}
	})
}

func TestControlInteractionStatusesForTimeoutAndRejection(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{CallbackDeadline: 30 * time.Millisecond})

	timeout := control(t, w.srv, http.MethodPost, "/interactions", `{"as":"`+aliceID+`","kind":"slash","path":"/ping"}`)
	rejected := control(t, w.srv, http.MethodPost, "/interactions", `{"as":"`+aliceID+`","kind":"slash","path":"/ge/join"}`)

	if timeout.status != http.StatusOK || timeout.json(t)["status"] != "timeout" {
		t.Errorf("timeout = %d %s, want status timeout", timeout.status, timeout.body)
	}
	body := rejected.json(t)
	if rejected.status != http.StatusOK || body["status"] != "rejected" || !strings.Contains(body["error"].(string), "exchange") {
		t.Errorf("rejected = %d %s, want status rejected naming the option", rejected.status, rejected.body)
	}
}

func TestControlInteractionsHonorBypassAndForgedFlags(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{})
	message := w.botMessage(t, "Join us", buttonRow)
	longID := strings.Repeat("c", 150)

	t.Run("a custom id over 100 characters is refused unless forged", func(t *testing.T) {
		request := `{"as":"` + aliceID + `","kind":"button","message_id":"` + message.ID + `","custom_id":"` + longID + `"`

		refused := control(t, w.srv, http.MethodPost, "/interactions", request+`}`)
		done := w.controlInteraction(t, request+`,"forged":true}`)
		sent := w.bot.nextInteraction(t)

		if refused.json(t)["status"] != "rejected" {
			t.Errorf("unforged = %s, want rejected", refused.body)
		}
		if !strings.Contains(string(sent.Data), longID) {
			t.Errorf("forged interaction data = %s, want the long custom id", sent.Data)
		}
		w.ackControl(t, sent, done)
	})

	t.Run("bypass_validation sends an over-long option", func(t *testing.T) {
		long := strings.Repeat("w", 300)
		request := `{"as":"` + aliceID + `","kind":"slash","path":"/ge/join","options":{"exchange":"x","wish":"` + long + `"},"bypass_validation":true}`

		done := w.controlInteraction(t, request)
		sent := w.bot.nextInteraction(t)

		if !strings.Contains(string(sent.Data), long) {
			t.Errorf("the bypassed value did not reach the bot")
		}
		w.ackControl(t, sent, done)
	})
}

// ackControl answers the interaction with a deferral and waits for the control API's answer.
func (w *interactionWorld) ackControl(t *testing.T, in sentInteraction, done <-chan reply) {
	t.Helper()

	w.respond(t, in, `{"type":5}`)
	waitChan(t, done, actionTimeout, "the control API to answer")
}

func TestControlInteractionRefusesMalformedRequests(t *testing.T) {
	srv := newFake(t, fakediscord.Options{})
	srv.AddUser(fakediscord.User{ID: aliceID, Name: "Alice"})
	call(t, srv, http.MethodPut, "/api/v10/applications/"+srv.ApplicationID()+"/commands", botAuth(srv), geCommands)
	tests := []struct {
		name string
		body string
		want int
	}{
		{"malformed JSON", `{"as":`, 400},
		{"not an object", `[]`, 400},
		{"an unknown kind", `{"as":"` + aliceID + `","kind":"swipe"}`, 400},
		{"no kind", `{"as":"` + aliceID + `"}`, 400},
		{"no actor", `{"kind":"slash","path":"/ping"}`, 400},
		{"a click without a message", `{"as":"` + aliceID + `","kind":"button","custom_id":"x"}`, 400},
		{"options that are not an object", `{"as":"` + aliceID + `","kind":"slash","path":"/ping","options":"x"}`, 400},
		{"values that are not a list", `{"as":"` + aliceID + `","kind":"select","message_id":"1","values":"a"}`, 400},
		{"fields that are not strings", `{"as":"` + aliceID + `","kind":"modal","fields":{"a":1}}`, 400},
		{"no bot connected", `{"as":"` + aliceID + `","kind":"slash","path":"/ping"}`, 503},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := control(t, srv, http.MethodPost, "/interactions", tc.body)

			if got.status != tc.want {
				t.Errorf("status = %d %.100s, want %d", got.status, got.body, tc.want)
			}
		})
	}
}

func FuzzControlInteractionBody(f *testing.F) {
	srv := newFake(f, fakediscord.Options{})
	alice := srv.AddUser(fakediscord.User{ID: aliceID, Name: "Alice"})
	srv.AddGuild(fakediscord.Guild{ID: winterGuildID, Name: "Winter guild"}, alice)
	registered := call(f, srv, http.MethodPut, "/api/v10/applications/"+srv.ApplicationID()+"/commands", botAuth(srv), geCommands)
	if registered.status != http.StatusOK {
		f.Fatalf("register commands: %d %s", registered.status, registered.body)
	}
	seeds := []string{
		``, `{`, `[]`, `null`, `{}`, `{"as":"` + aliceID + `","kind":"slash","path":"/ping"}`,
		`{"as":"` + aliceID + `","kind":"slash","path":"/ge/join","options":{"exchange":"x","budget":"ten"},"bypass_validation":true}`,
		`{"as":"` + aliceID + `","kind":"autocomplete","path":"/ge/join","options":{"exchange":"w"},"focused":"exchange"}`,
		`{"as":"` + aliceID + `","kind":"button","message_id":"1","custom_id":"` + strings.Repeat("x", 200) + `","forged":true}`,
		`{"as":"` + aliceID + `","kind":"select","message_id":"1","custom_id":"x","values":["\u202e","\u200b"]}`,
		`{"as":"` + aliceID + `","kind":"modal","custom_id":"m","fields":{"a":"😀","b":""},"forged":true}`,
		`{"as":"` + strings.Repeat("9", 40) + `","kind":"slash","path":"` + strings.Repeat("/a", 200) + `"}`,
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, body string) {
		got := control(t, srv, http.MethodPost, "/interactions", body)

		if got.status != 200 && got.status != 400 && got.status != 413 && got.status != 503 {
			t.Fatalf("status %d for body %.200q", got.status, body)
		}
		if !json.Valid(got.body) {
			t.Fatalf("answer %.200q is not JSON for body %.200q", got.body, body)
		}
	})
}

func TestOutboundCanBeReadFromACursor(t *testing.T) {
	w := newVisibilityWorld(t)
	for _, content := range []string{"one", "two", "three"} {
		postChannelMessage(t, w.srv, w.guild.ChannelID, content, "", "")
	}

	all := control(t, w.srv, http.MethodGet, "/outbound", "").array(t)
	since := control(t, w.srv, http.MethodGet, "/outbound?since=2", "").array(t)
	none := control(t, w.srv, http.MethodGet, "/outbound?since=3", "").array(t)

	if len(all) != 3 || all[0]["seq"] != float64(1) || all[2]["seq"] != float64(3) {
		t.Errorf("all = %v, want three records numbered 1 to 3", all)
	}
	if len(since) != 1 || since[0]["content"] != "three" {
		t.Errorf("since=2 = %v, want only the third record", since)
	}
	if none == nil || len(none) != 0 {
		t.Errorf("since=3 = %v, want an empty array", none)
	}
	for _, key := range []string{
		"at", "route", "source", "status", "deduplicated", "interaction_id", "message_id", "channel_id", "guild_id",
		"ephemeral", "visible_to", "content", "embeds", "components", "allowed_mentions_sent", "allowed_mentions_effective", "mentions_rendered", "pings",
	} {
		if _, ok := all[0][key]; !ok {
			t.Errorf("record lacks %q: %v", key, all[0])
		}
	}
}

func TestMessagesAreReadAsEachUserSeesThem(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{})
	w.answerSlash(t, w.alice, w.inGuild("/ping", nil), `{"type":4,"data":{"content":"for Alice","flags":64}}`)
	postChannelMessage(t, w.srv, w.guild.ChannelID, "for everyone", "", "")
	w.answerSlash(t, w.carol, fakediscord.Slash{Path: "/ping"}, `{"type":4,"data":{"content":"for Carol"}}`)
	channel := "/channels/" + w.guild.ChannelID + "/messages?as="

	alice := control(t, w.srv, http.MethodGet, channel+aliceID, "").array(t)
	bruno := control(t, w.srv, http.MethodGet, channel+brunoID, "").array(t)
	outsider := control(t, w.srv, http.MethodGet, channel+carolID, "").array(t)
	carolDMs := control(t, w.srv, http.MethodGet, "/dms/"+carolID, "").array(t)
	aliceDMs := control(t, w.srv, http.MethodGet, "/dms/"+aliceID, "").array(t)

	if len(alice) != 2 || len(bruno) != 1 || bruno[0]["content"] != "for everyone" {
		t.Errorf("alice sees %d, bruno sees %v", len(alice), bruno)
	}
	if len(outsider) != 0 {
		t.Errorf("a non-member sees %v", outsider)
	}
	if len(carolDMs) != 1 || carolDMs[0]["content"] != "for Carol" || len(aliceDMs) != 0 {
		t.Errorf("carol DMs = %v, alice DMs = %v", carolDMs, aliceDMs)
	}
}

func TestControlFaultsAndGatewayDrop(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{})
	path := "/api/v10/channels/" + w.guild.ChannelID + "/messages"

	queued := control(t, w.srv, http.MethodPost, "/faults", `{"route":"POST /channels/{id}/messages","status":429,"code":0}`)
	limited := call(t, w.srv, http.MethodPost, path, botAuth(w.srv), `{"content":"hi"}`)
	identify := control(t, w.srv, http.MethodPost, "/faults", `{"route":"GATEWAY identify","code":4004}`)
	dropped := control(t, w.srv, http.MethodPost, "/gateway/drop", `{"code":4000}`)

	if queued.status != http.StatusNoContent || limited.status != http.StatusTooManyRequests {
		t.Errorf("fault = %d, request = %d, want 204 and 429", queued.status, limited.status)
	}
	if dropped.status != http.StatusNoContent {
		t.Fatalf("drop = %d %s", dropped.status, dropped.body)
	}
	if code := w.bot.readClose(t); code != 4000 {
		t.Errorf("close code = %d, want 4000", code)
	}
	if identify.status != http.StatusNoContent {
		t.Fatalf("identify fault = %d %s", identify.status, identify.body)
	}
	faulted := dialRaw(t, w.srv)
	faulted.send(t, 2, `{"token":"`+w.srv.Token()+`","intents":1}`)
	if code := faulted.readClose(t); code != 4004 {
		t.Errorf("Identify after the control fault closed with %d, want 4004", code)
	}
}

func TestResetClearsTheWorldAndKeepsTheBotSession(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{})
	postChannelMessage(t, w.srv, w.guild.ChannelID, "before", "", "")
	before := lastRecord(t, w.srv)
	mustFailNext(t, w.srv, "GET /gateway", 500, 0)

	reset := control(t, w.srv, http.MethodPost, "/reset", "")

	if reset.status != http.StatusNoContent {
		t.Fatalf("reset = %d %s", reset.status, reset.body)
	}
	state := control(t, w.srv, http.MethodGet, "/state", "").json(t)
	if len(state["users"].([]any)) != 0 || len(state["guilds"].([]any)) != 0 {
		t.Errorf("state after reset = %v, want no users or guilds", state)
	}
	if state["bot"] != "ready" || len(state["commands"].([]any)) != 2 || state["intents"] != float64(1) {
		t.Errorf("state after reset = %v, want the bot session, commands and intents kept", state)
	}
	if len(w.srv.Outbound()) != 0 || len(w.srv.Visible(w.alice, w.guild.ChannelID)) != 0 {
		t.Error("records or messages survived the reset")
	}
	if got := call(t, w.srv, http.MethodGet, "/api/v10/gateway", "", ""); got.status != http.StatusOK {
		t.Errorf("a fault queued before the reset fired after it: %d", got.status)
	}

	alice := w.srv.AddUser(fakediscord.User{ID: aliceID, Name: "Alice"})
	guild := w.srv.AddGuild(fakediscord.Guild{ID: winterGuildID, Name: "Winter guild"}, alice)
	postChannelMessage(t, w.srv, guild.ChannelID, "after", "", "")
	if after := lastRecord(t, w.srv); after.Seq <= before.Seq {
		t.Errorf("record seq after reset = %d, want it to keep counting past %d", after.Seq, before.Seq)
	}
}

// sseEvent is one server-sent event.
type sseEvent struct {
	name string
	data string
}

// subscribe opens the event stream of a user ("" for everyone) and returns its events.
func subscribe(t *testing.T, srv *fakediscord.Server, as string) <-chan sseEvent {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL()+"/_control/events?as="+as, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	client := &http.Client{}
	//nolint:bodyclose // the body is closed by the cleanup below, which also ends the reader goroutine
	resp, err := client.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("GET events: %v", err)
	}
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		cancel()
		t.Fatalf("events = %d %q, want an event stream", resp.StatusCode, resp.Header.Get("Content-Type"))
	}

	events := make(chan sseEvent, 64)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		defer close(events)
		var current sseEvent
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				current.name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				current.data = strings.TrimPrefix(line, "data: ")
			case line == "" && current.name != "":
				events <- current
				current = sseEvent{}
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		_ = resp.Body.Close()
		<-finished
		client.CloseIdleConnections()
	})

	return events
}

func nextEvent(t *testing.T, events <-chan sseEvent, name string) sseEvent {
	t.Helper()

	return collectUntil(t, events, name, "")[0]
}

// collectUntil reads events until one named name arrives and returns it last, preceded by
// everything that came before it.
func collectUntil(t *testing.T, events <-chan sseEvent, name, contains string) []sseEvent {
	t.Helper()

	timeout := time.After(actionTimeout)
	var seen []sseEvent
	for {
		select {
		case event, isOpen := <-events:
			if !isOpen {
				t.Fatalf("the event stream ended before %s; saw %v", name, seen)
			}
			seen = append(seen, event)
			if event.name == name && strings.Contains(event.data, contains) {
				return append([]sseEvent{event}, seen[:len(seen)-1]...)
			}
		case <-timeout:
			t.Fatalf("timed out waiting for event %s; saw %v", name, seen)
		}
	}
}

func TestEventStreamDeliversMessagesToTheUsersWhoCanSeeThem(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{})
	aliceEvents := subscribe(t, w.srv, aliceID)
	brunoEvents := subscribe(t, w.srv, brunoID)
	carolEvents := subscribe(t, w.srv, carolID)

	w.answerSlash(t, w.alice, w.inGuild("/ping", nil), `{"type":4,"data":{"content":"for Alice","flags":64}}`)
	postChannelMessage(t, w.srv, w.guild.ChannelID, "for everyone", "", "")

	first := nextEvent(t, aliceEvents, "message_created")
	second := nextEvent(t, aliceEvents, "message_created")
	if !strings.Contains(first.data, "for Alice") || !strings.Contains(second.data, "for everyone") {
		t.Errorf("Alice's events = %s, %s, want her ephemeral and then the public message", first.data, second.data)
	}
	brunoFirst := nextEvent(t, brunoEvents, "message_created")
	if !strings.Contains(brunoFirst.data, "for everyone") || strings.Contains(brunoFirst.data, "for Alice") {
		t.Errorf("Bruno's first event = %s, want only the public message: Alice's ephemeral must not reach him", brunoFirst.data)
	}
	w.srv.AddUser(fakediscord.User{ID: "100000000000000009", Name: "Dana"})
	for _, event := range collectUntil(t, carolEvents, "world_changed", "") {
		if event.name == "message_created" {
			t.Errorf("an outsider received %s", event.data)
		}
	}
}

func TestEventStreamReportsEditsDeletesModalsAndTheWorld(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{})
	aliceEvents := subscribe(t, w.srv, aliceID)
	brunoEvents := subscribe(t, w.srv, brunoID)
	everything := subscribe(t, w.srv, "")

	sent, _ := w.answerSlash(t, w.alice, w.inGuild("/ping", nil), `{"type":4,"data":{"content":"v1"}}`)
	edited := call(t, w.srv, http.MethodPatch, sent.webhookPath(w.srv, "/messages/@original"), "", `{"content":"v2"}`)
	messageID, _ := edited.json(t)["id"].(string)
	call(t, w.srv, http.MethodDelete, sent.webhookPath(w.srv, "/messages/@original"), "", "")
	w.answerSlash(t, w.alice, w.inGuild("/ping", nil),
		`{"type":9,"data":{"custom_id":"m","title":"T","components":[{"type":1,"components":[{"type":4,"custom_id":"a","style":1,"label":"A"}]}]}}`)
	w.srv.AddUser(fakediscord.User{ID: "100000000000000009", Name: "Dana"})
	postChannelMessage(t, w.srv, w.guild.ChannelID, "sentinel", "", "")

	if updated := nextEvent(t, aliceEvents, "message_updated"); !strings.Contains(updated.data, "v2") {
		t.Errorf("message_updated = %s, want v2", updated.data)
	}
	if deleted := nextEvent(t, aliceEvents, "message_deleted"); messageID == "" || !strings.Contains(deleted.data, messageID) {
		t.Errorf("message_deleted = %s", deleted.data)
	}
	if modal := nextEvent(t, aliceEvents, "modal_opened"); !strings.Contains(modal.data, `"custom_id":"m"`) {
		t.Errorf("modal_opened = %s", modal.data)
	}
	nextEvent(t, everything, "modal_opened")
	nextEvent(t, everything, "world_changed")
	for _, event := range collectUntil(t, brunoEvents, "message_created", "sentinel") {
		if event.name == "modal_opened" {
			t.Errorf("Bruno received Alice's modal: %s", event.data)
		}
	}
}

func TestEventStreamReportsTheBotComingUp(t *testing.T) {
	srv := startFake(t, fakediscord.Options{})
	events := subscribe(t, srv, "")

	identifyRaw(t, srv)
	connecting := nextEvent(t, events, "bot_status")
	registerPing(t, srv)
	ready := nextEvent(t, events, "bot_status")

	if !strings.Contains(connecting.data, "connecting") || !strings.Contains(ready.data, "ready") {
		t.Errorf("bot_status events = %s, %s, want connecting then ready", connecting.data, ready.data)
	}
}

func TestClosingTheServerEndsEventStreams(t *testing.T) {
	srv := startFake(t, fakediscord.Options{})
	events := subscribe(t, srv, "")

	closeErr := srv.Close()

	if closeErr != nil {
		t.Fatalf("Close: %v", closeErr)
	}
	select {
	case _, isOpen := <-events:
		if isOpen {
			t.Error("an event arrived after Close")
		}
	case <-time.After(actionTimeout):
		t.Error("the event stream did not end when the server closed")
	}
}

func TestUserAndGuildLocaleThroughGoAndControlAPI(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{})

	if err := w.srv.SetUserLocale(w.alice, "es-419"); err != nil {
		t.Fatalf("SetUserLocale: %v", err)
	}
	if err := w.srv.SetGuildLocale(w.guild, "es-ES"); err != nil {
		t.Fatalf("SetGuildLocale: %v", err)
	}

	done := w.controlInteraction(t, `{"as":"`+aliceID+`","kind":"slash","guild_id":"`+winterGuildID+`","path":"/ping"}`)
	sent := w.bot.nextInteraction(t)
	if got := w.respond(t, sent, `{"type":4,"data":{"content":"pong"}}`); got.status != http.StatusNoContent {
		t.Fatalf("callback = %d %s", got.status, got.body)
	}
	waitChan(t, done, actionTimeout, "the control API to answer")

	if sent.Locale != "es-419" || sent.GuildLocale != "es-ES" {
		t.Errorf("interaction locales = (%q, %q), want (es-419, es-ES)", sent.Locale, sent.GuildLocale)
	}

	patchedUser := control(t, w.srv, http.MethodPatch, "/users/"+aliceID, `{"locale":"en-GB"}`)
	if patchedUser.status != http.StatusOK || patchedUser.json(t)["locale"] != "en-GB" {
		t.Errorf("patch user locale = %d %s", patchedUser.status, patchedUser.body)
	}
	patchedGuild := control(t, w.srv, http.MethodPatch, "/guilds/"+winterGuildID, `{"locale":"es-419"}`)
	if patchedGuild.status != http.StatusOK || patchedGuild.json(t)["locale"] != "es-419" {
		t.Errorf("patch guild locale = %d %s", patchedGuild.status, patchedGuild.body)
	}

	for name, tc := range map[string]struct {
		path       string
		body       string
		wantStatus int
	}{
		"empty user patch":   {"/users/" + aliceID, `{}`, http.StatusBadRequest},
		"blank user locale":  {"/users/" + aliceID, `{"locale":""}`, http.StatusBadRequest},
		"long user locale":   {"/users/" + aliceID, `{"locale":"` + strings.Repeat("x", 101) + `"}`, http.StatusBadRequest},
		"unknown user":       {"/users/999999999999999999", `{"locale":"es-419"}`, http.StatusNotFound},
		"empty guild patch":  {"/guilds/" + winterGuildID, `{}`, http.StatusBadRequest},
		"blank guild locale": {"/guilds/" + winterGuildID, `{"locale":""}`, http.StatusBadRequest},
		"long guild locale":  {"/guilds/" + winterGuildID, `{"locale":"` + strings.Repeat("x", 101) + `"}`, http.StatusBadRequest},
		"unknown guild":      {"/guilds/999999999999999999", `{"locale":"es-419"}`, http.StatusNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			if got := control(t, w.srv, http.MethodPatch, tc.path, tc.body); got.status != tc.wantStatus {
				t.Errorf("status = %d (%s), want %d", got.status, got.body, tc.wantStatus)
			}
		})
	}
}

func FuzzControlWorldBodies(f *testing.F) {
	targets := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/users"},
		{http.MethodPatch, "/users/" + aliceID},
		{http.MethodPost, "/guilds"},
		{http.MethodPatch, "/guilds/" + winterGuildID},
		{http.MethodPost, "/guilds/" + winterGuildID + "/members"},
		{http.MethodPost, "/faults"},
	}
	seeds := []string{
		``, `{`, `[]`, `null`, `{}`, `{"name":"Dana"}`, `{"name":"` + abuseText + `"}`,
		`{"id":"` + strings.Repeat("9", 40) + `","name":"x"}`, `{"user":"` + aliceID + `"}`,
		`{"route":"POST /channels/{id}/messages","status":500}`, `{"route":5,"status":"x"}`,
	}
	for _, seed := range seeds {
		for index := range targets {
			f.Add(index, seed)
		}
	}

	f.Fuzz(func(t *testing.T, index int, body string) {
		if index < 0 || index >= len(targets) {
			t.Skip()
		}
		srv := newFake(t, fakediscord.Options{})
		alice := srv.AddUser(fakediscord.User{ID: aliceID, Name: "Alice"})
		srv.AddGuild(fakediscord.Guild{ID: winterGuildID, Name: "Winter guild"}, alice)

		got := control(t, srv, targets[index].method, targets[index].path, body)

		if got.status < 200 || got.status > 499 {
			t.Fatalf("%s %s answered %d for body %.200q", targets[index].method, targets[index].path, got.status, body)
		}
	})
}
