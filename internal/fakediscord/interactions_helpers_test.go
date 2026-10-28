package fakediscord_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
)

const actionTimeout = 20 * time.Second

// geCommands registers one command with a subcommand, a group with a subcommand, and
// options of several types.
const geCommands = `[
 {"name":"ge","description":"Gift exchange","type":1,"options":[
  {"type":1,"name":"join","description":"Join an exchange","options":[
   {"type":3,"name":"exchange","description":"Which exchange","required":true,"autocomplete":true},
   {"type":3,"name":"wish","description":"A wish","max_length":20},
   {"type":4,"name":"budget","description":"Budget","min_value":1,"max_value":100},
   {"type":5,"name":"anonymous","description":"Hide the name"},
   {"type":6,"name":"friend","description":"A friend"},
   {"type":3,"name":"size","description":"Size","choices":[{"name":"S","value":"s"},{"name":"L","value":"l"}]}
  ]},
  {"type":2,"name":"admin","description":"Admin","options":[
   {"type":1,"name":"create","description":"Create","options":[
    {"type":3,"name":"title","description":"Title","required":true,"min_length":2}
   ]}
  ]}
 ]},
 {"name":"ping","description":"Ping the bot","type":1}
]`

// sentInteraction is an INTERACTION_CREATE as the bot receives it.
type sentInteraction struct {
	ID            string          `json:"id"`
	Token         string          `json:"token"`
	ApplicationID string          `json:"application_id"`
	Type          int             `json:"type"`
	GuildID       string          `json:"guild_id"`
	ChannelID     string          `json:"channel_id"`
	Locale        string          `json:"locale"`
	GuildLocale   string          `json:"guild_locale"`
	Data          json.RawMessage `json:"data"`
	Message       json.RawMessage `json:"message"`
	Member        json.RawMessage `json:"member"`
	User          json.RawMessage `json:"user"`
}

func (i sentInteraction) callbackPath() string {
	return "/api/v10/interactions/" + i.ID + "/" + i.Token + "/callback"
}

func (i sentInteraction) webhookPath(srv *fakediscord.Server, tail string) string {
	return "/api/v10/webhooks/" + srv.ApplicationID() + "/" + i.Token + tail
}

// nextInteraction reads frames until the next INTERACTION_CREATE.
func (g *rawGateway) nextInteraction(t *testing.T) sentInteraction {
	t.Helper()

	for {
		frame := g.read(t)
		if frame.T != "INTERACTION_CREATE" {
			continue
		}
		var sent sentInteraction
		decodeJSON(t, frame.D, &sent)

		return sent
	}
}

// interactionWorld is a fake with two guild members, one outsider and a bot that is a raw
// WebSocket connection the test answers for.
type interactionWorld struct {
	srv                 *fakediscord.Server
	alice, bruno, carol fakediscord.User
	guild               fakediscord.Guild
	bot                 *rawGateway
}

func newInteractionWorld(t *testing.T, opts fakediscord.Options) *interactionWorld {
	t.Helper()

	srv := startFake(t, opts)
	w := &interactionWorld{
		srv:   srv,
		alice: srv.AddUser(fakediscord.User{ID: aliceID, Name: "Alice"}),
		bruno: srv.AddUser(fakediscord.User{ID: brunoID, Name: "Bruno"}),
		carol: srv.AddUser(fakediscord.User{ID: carolID, Name: "Carol"}),
	}
	w.guild = srv.AddGuild(fakediscord.Guild{ID: winterGuildID, Name: "Winter guild"}, w.alice, w.bruno)
	w.bot, _ = identifyRaw(t, srv)

	path := "/api/v10/applications/" + srv.ApplicationID() + "/commands"
	got := call(t, srv, http.MethodPut, path, botAuth(srv), geCommands)
	if got.status != http.StatusOK {
		t.Fatalf("register commands: status %d, body %s", got.status, got.body)
	}

	return w
}

// asyncResult is what an action returned.
type asyncResult struct {
	result fakediscord.Result
	err    error
}

// startAction runs an action in the background so the test can play the bot meanwhile.
func startAction(t *testing.T, action func(ctx context.Context) (fakediscord.Result, error)) <-chan asyncResult {
	t.Helper()

	done := make(chan asyncResult, 1)
	ctx := testContext(t, actionTimeout)
	go func() {
		result, err := action(ctx)
		done <- asyncResult{result: result, err: err}
	}()

	return done
}

func finish(t *testing.T, done <-chan asyncResult) asyncResult {
	t.Helper()

	return waitChan(t, done, actionTimeout, "the action to return")
}

// slashAs sends a slash command and returns the interaction the bot received together with
// the pending action.
func (w *interactionWorld) slashAs(t *testing.T, as fakediscord.User, in fakediscord.Slash) (sentInteraction, <-chan asyncResult) {
	t.Helper()

	done := startAction(t, func(ctx context.Context) (fakediscord.Result, error) {
		return w.srv.Slash(ctx, as, in)
	})

	return w.bot.nextInteraction(t), done
}

func (w *interactionWorld) inGuild(path string, options map[string]any) fakediscord.Slash {
	return fakediscord.Slash{Guild: &w.guild, Path: path, Options: options}
}

// respond posts the bot's callback for the interaction.
func (w *interactionWorld) respond(t *testing.T, in sentInteraction, body string) reply {
	t.Helper()

	return call(t, w.srv, http.MethodPost, in.callbackPath(), "", body)
}

// answerSlash runs a slash command and answers it with the callback body, returning the
// action's result.
func (w *interactionWorld) answerSlash(t *testing.T, as fakediscord.User, in fakediscord.Slash, body string) (sentInteraction, fakediscord.Result) {
	t.Helper()

	sent, done := w.slashAs(t, as, in)
	got := w.respond(t, sent, body)
	if got.status != http.StatusNoContent {
		t.Fatalf("callback status = %d, body %s", got.status, got.body)
	}
	finished := finish(t, done)
	if finished.err != nil {
		t.Fatalf("Slash: %v", finished.err)
	}

	return sent, finished.result
}

// botMessage has the bot post a message with the given components to the guild channel and
// returns it.
func (w *interactionWorld) botMessage(t *testing.T, content, components string) fakediscord.Message {
	t.Helper()

	body := `{"content":"` + content + `","components":` + components + `}`
	got := call(t, w.srv, http.MethodPost, "/api/v10/channels/"+w.guild.ChannelID+"/messages", botAuth(w.srv), body)
	if got.status != http.StatusOK {
		t.Fatalf("post bot message: status %d, body %s", got.status, got.body)
	}
	id, _ := got.json(t)["id"].(string)
	for _, msg := range w.srv.Visible(w.alice, w.guild.ChannelID) {
		if msg.ID == id {
			return msg
		}
	}
	t.Fatalf("message %s is not visible to Alice", id)

	return fakediscord.Message{}
}

const (
	buttonRow = `[{"type":1,"components":[{"type":2,"style":1,"label":"Join","custom_id":"join:1"},` +
		`{"type":2,"style":1,"label":"Off","custom_id":"off:1","disabled":true}]}]`
	menuRow = `[{"type":1,"components":[{"type":3,"custom_id":"pick","min_values":1,"max_values":2,` +
		`"options":[{"label":"A","value":"a"},{"label":"B","value":"b"},{"label":"C","value":"c"}]}]}]`
)

func mustFailNext(t *testing.T, srv *fakediscord.Server, route string, status, code int) {
	t.Helper()

	err := srv.FailNext(route, status, code)
	if err != nil {
		t.Fatalf("FailNext(%q, %d, %d): %v", route, status, code, err)
	}
}

func mustSetDMsOpen(t *testing.T, srv *fakediscord.Server, u fakediscord.User, open bool) {
	t.Helper()

	err := srv.SetDMsOpen(u, open)
	if err != nil {
		t.Fatalf("SetDMsOpen(%q, %t): %v", u.ID, open, err)
	}
}
