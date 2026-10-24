package fakediscord_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
)

// ack answers the interaction with a deferral so its action returns.
func (w *interactionWorld) ack(t *testing.T, in sentInteraction, done <-chan asyncResult) {
	t.Helper()

	if got := w.respond(t, in, `{"type":5}`); got.status != http.StatusNoContent {
		t.Fatalf("ack = %d %s", got.status, got.body)
	}
	finish(t, done)
}

// optionTree decodes data.options of a slash interaction.
type optionTree struct {
	Name    string          `json:"name"`
	Type    int             `json:"type"`
	Value   json.RawMessage `json:"value"`
	Focused bool            `json:"focused"`
	Options []optionTree    `json:"options"`
}

func interactionData(t *testing.T, in sentInteraction) (name string, options []optionTree, resolved map[string]map[string]json.RawMessage) {
	t.Helper()

	var data struct {
		Name     string                                `json:"name"`
		Options  []optionTree                          `json:"options"`
		Resolved map[string]map[string]json.RawMessage `json:"resolved"`
	}
	decodeJSON(t, in.Data, &data)

	return data.Name, data.Options, data.Resolved
}

func TestSlashSendsValidatedOptionsInTheShapeDiscordUses(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{})
	options := map[string]any{
		"exchange": "winter", "wish": "socks", "budget": 20, "anonymous": true, "friend": brunoID, "size": "l",
	}

	sent, done := w.slashAs(t, w.alice, w.inGuild("/ge/join", options))

	name, tree, resolved := interactionData(t, sent)
	if name != "ge" || len(tree) != 1 || tree[0].Name != "join" || tree[0].Type != 1 {
		t.Fatalf("data = %s %+v, want ge with the join subcommand", name, tree)
	}
	got := map[string]string{}
	for _, leaf := range tree[0].Options {
		got[leaf.Name] = string(leaf.Value)
	}
	want := map[string]string{
		"exchange": `"winter"`, "wish": `"socks"`, "budget": `20`, "anonymous": `true`,
		"friend": `"` + brunoID + `"`, "size": `"l"`,
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("option %s = %s, want %s", key, got[key], value)
		}
	}
	if _, ok := resolved["users"][brunoID]; !ok {
		t.Errorf("resolved = %v, want Bruno among the users", resolved)
	}
	if _, ok := resolved["members"][brunoID]; !ok {
		t.Errorf("resolved = %v, want Bruno among the members", resolved)
	}
	w.ack(t, sent, done)
}

func TestSlashNestsGroupsAndSubcommands(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{})

	sent, done := w.slashAs(t, w.alice, w.inGuild("/ge/admin/create", map[string]any{"title": "Winter"}))

	_, tree, _ := interactionData(t, sent)
	isNested := len(tree) == 1 && tree[0].Name == "admin" && tree[0].Type == 2 &&
		len(tree[0].Options) == 1 && tree[0].Options[0].Name == "create" && tree[0].Options[0].Type == 1 &&
		len(tree[0].Options[0].Options) == 1 && string(tree[0].Options[0].Options[0].Value) == `"Winter"`
	if !isNested {
		t.Errorf("options = %+v, want admin > create > title", tree)
	}
	w.ack(t, sent, done)
}

func TestSlashRefusesWhatADiscordClientCouldNotSend(t *testing.T) {
	long := strings.Repeat("w", 21)
	tests := []struct {
		name    string
		as      fakediscord.User
		guild   *fakediscord.Guild
		path    string
		options map[string]any
		want    string
	}{
		{"a missing required option", fakediscord.User{ID: aliceID}, nil, "/ge/join", nil, `"exchange": required`},
		{"a string for an integer", fakediscord.User{ID: aliceID}, nil, "/ge/join", map[string]any{"exchange": "x", "budget": "ten"}, "must be an integer"},
		{"a fraction for an integer", fakediscord.User{ID: aliceID}, nil, "/ge/join", map[string]any{"exchange": "x", "budget": 1.5}, "must be an integer"},
		{"a number for a string", fakediscord.User{ID: aliceID}, nil, "/ge/join", map[string]any{"exchange": 5}, "must be a string"},
		{"a string for a boolean", fakediscord.User{ID: aliceID}, nil, "/ge/join", map[string]any{"exchange": "x", "anonymous": "yes"}, "must be a boolean"},
		{"a value over max_length", fakediscord.User{ID: aliceID}, nil, "/ge/join", map[string]any{"exchange": "x", "wish": long}, "max_length 20"},
		{"a value under min_length", fakediscord.User{ID: aliceID}, nil, "/ge/admin/create", map[string]any{"title": "x"}, "min_length 2"},
		{"a value above max_value", fakediscord.User{ID: aliceID}, nil, "/ge/join", map[string]any{"exchange": "x", "budget": 101}, "max_value"},
		{"a value below min_value", fakediscord.User{ID: aliceID}, nil, "/ge/join", map[string]any{"exchange": "x", "budget": 0}, "min_value"},
		{"a value that is not a choice", fakediscord.User{ID: aliceID}, nil, "/ge/join", map[string]any{"exchange": "x", "size": "xl"}, "offered choices"},
		{"an unknown option", fakediscord.User{ID: aliceID}, nil, "/ge/join", map[string]any{"exchange": "x", "colour": "red"}, "not defined"},
		{"an unknown user", fakediscord.User{ID: aliceID}, nil, "/ge/join", map[string]any{"exchange": "x", "friend": missingID}, "unknown user"},
		{"an unregistered command", fakediscord.User{ID: aliceID}, nil, "/nope", nil, "no registered command"},
		{"an unknown subcommand", fakediscord.User{ID: aliceID}, nil, "/ge/leave", nil, "not a subcommand"},
		{"a path missing its subcommand", fakediscord.User{ID: aliceID}, nil, "/ge", nil, "needs a subcommand"},
		{"a group missing its subcommand", fakediscord.User{ID: aliceID}, nil, "/ge/admin", nil, "needs a subcommand"},
		{"a path without a leading slash", fakediscord.User{ID: aliceID}, nil, "ge/join", nil, "must look like"},
		{"a path that is too deep", fakediscord.User{ID: aliceID}, nil, "/a/b/c/d", nil, "must look like"},
		{"a user the fake does not know", fakediscord.User{ID: missingID}, nil, "/ping", nil, "not known"},
		{"an unknown guild", fakediscord.User{ID: aliceID}, &fakediscord.Guild{ID: missingID}, "/ping", nil, "not known"},
		{"a user who is not in the guild", fakediscord.User{ID: carolID}, &fakediscord.Guild{ID: winterGuildID}, "/ping", nil, "not a member"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := newInteractionWorld(t, fakediscord.Options{})

			result, err := w.srv.Slash(testContext(t, actionTimeout), tc.as, fakediscord.Slash{Guild: tc.guild, Path: tc.path, Options: tc.options})

			if !errors.Is(err, fakediscord.ErrRejected) {
				t.Fatalf("Slash = %+v, %v, want ErrRejected", result, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestSlashReportsEveryProblemTogetherAndSendsNothing(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{})

	_, err := w.srv.Slash(testContext(t, actionTimeout), w.alice,
		w.inGuild("/ge/join", map[string]any{"budget": "ten", "wish": strings.Repeat("w", 30)}))

	for _, want := range []string{`"exchange": required`, "must be an integer", "max_length"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to mention %q", err, want)
		}
	}
	valid, done := w.slashAs(t, w.alice, w.inGuild("/ping", nil))
	name, _, _ := interactionData(t, valid)
	if name != "ping" {
		t.Errorf("the first interaction the bot received is %q, want the later valid /ping", name)
	}
	w.ack(t, valid, done)
}

func TestBypassValidationSendsTheRawValues(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{})
	long := strings.Repeat("w", 500)
	slash := w.inGuild("/ge/join", map[string]any{"budget": "ten", "wish": long, "size": "xl", "extra": 7})
	slash.BypassValidation = true

	sent, done := w.slashAs(t, w.alice, slash)

	_, tree, _ := interactionData(t, sent)
	got := map[string]optionTree{}
	for _, leaf := range tree[0].Options {
		got[leaf.Name] = leaf
	}
	if string(got["wish"].Value) != `"`+long+`"` {
		t.Errorf("wish was not sent whole")
	}
	if got["budget"].Type != 3 || string(got["budget"].Value) != `"ten"` {
		t.Errorf("budget = %+v, want the raw string typed as a string so the bot's getters do not panic", got["budget"])
	}
	if got["extra"].Type != 4 || string(got["extra"].Value) != `7` {
		t.Errorf("extra = %+v, want an undeclared option sent as an integer", got["extra"])
	}
	if _, hasRequired := got["exchange"]; hasRequired {
		t.Errorf("an unset option appeared: %+v", got["exchange"])
	}
	w.ack(t, sent, done)
}

func TestAutocompleteFocusesAnAutocompleteOption(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{})
	ctx := testContext(t, actionTimeout)

	tests := []struct {
		name    string
		options map[string]any
		focused string
	}{
		{"an option without autocomplete", map[string]any{"wish": "so"}, "wish"},
		{"an option that is not defined", map[string]any{"exchange": "x"}, "nope"},
		{"a focused option with no value", map[string]any{"wish": "so"}, "exchange"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := w.srv.Autocomplete(ctx, w.alice, w.inGuild("/ge/join", tc.options), tc.focused)

			if !errors.Is(err, fakediscord.ErrRejected) {
				t.Errorf("Autocomplete error = %v, want ErrRejected", err)
			}
		})
	}
}

func TestClickAndSelectRefuseWhatTheMessageDoesNotCarry(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{})
	buttons := w.botMessage(t, "Join us", buttonRow)
	menu := w.botMessage(t, "Pick", menuRow)
	longID := strings.Repeat("j", 101)
	ctx := testContext(t, actionTimeout)

	tests := []struct {
		name string
		as   fakediscord.User
		run  func() (fakediscord.Result, error)
		want string
	}{
		{"a custom id the message lacks", w.alice, func() (fakediscord.Result, error) { return w.srv.Click(ctx, w.alice, buttons, "nope:1") }, "carries no button"},
		{"a custom id over 100 characters", w.alice, func() (fakediscord.Result, error) { return w.srv.Click(ctx, w.alice, buttons, longID) }, "carries no button"},
		{"a disabled button", w.alice, func() (fakediscord.Result, error) { return w.srv.Click(ctx, w.alice, buttons, "off:1") }, "disabled"},
		{"a menu clicked as a button", w.alice, func() (fakediscord.Result, error) { return w.srv.Click(ctx, w.alice, menu, "pick") }, "carries no button"},
		{"a button used as a menu", w.alice, func() (fakediscord.Result, error) { return w.srv.Select(ctx, w.alice, buttons, "join:1", "a") }, "carries no select"},
		{"a value the menu does not offer", w.alice, func() (fakediscord.Result, error) { return w.srv.Select(ctx, w.alice, menu, "pick", "z") }, "does not offer"},
		{"too many values", w.alice, func() (fakediscord.Result, error) { return w.srv.Select(ctx, w.alice, menu, "pick", "a", "b", "c") }, "takes 1 to 2"},
		{"no values", w.alice, func() (fakediscord.Result, error) { return w.srv.Select(ctx, w.alice, menu, "pick") }, "takes 1 to 2"},
		{"a user outside the guild", w.carol, func() (fakediscord.Result, error) { return w.srv.Click(ctx, w.carol, buttons, "join:1") }, "cannot see"},
		{"a message that does not exist", w.alice, func() (fakediscord.Result, error) {
			return w.srv.Click(ctx, w.alice, fakediscord.Message{ID: missingID, ChannelID: w.guild.ChannelID}, "join:1")
		}, "does not exist"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.run()

			if !errors.Is(err, fakediscord.ErrRejected) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want ErrRejected mentioning %q", err, tc.want)
			}
		})
	}
}

func TestForgedInteractionsReachTheBotUnchecked(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{})
	buttons := w.botMessage(t, "Join us", buttonRow)
	menu := w.botMessage(t, "Pick", menuRow)
	longID := strings.Repeat("j", 150)

	t.Run("a click on a custom id the message lacks", func(t *testing.T) {
		done := startAction(t, func(ctx context.Context) (fakediscord.Result, error) {
			return w.srv.ClickForged(ctx, w.alice, buttons, longID)
		})

		sent := w.bot.nextInteraction(t)

		if !strings.Contains(string(sent.Data), longID) || sent.Type != 3 {
			t.Errorf("interaction = type %d data %s, want the forged custom id", sent.Type, sent.Data)
		}
		w.ack(t, sent, done)
	})

	t.Run("a click by someone who cannot see the message", func(t *testing.T) {
		done := startAction(t, func(ctx context.Context) (fakediscord.Result, error) {
			return w.srv.ClickForged(ctx, w.carol, buttons, "join:1")
		})

		sent := w.bot.nextInteraction(t)

		if !strings.Contains(string(sent.User)+string(sent.Member), carolID) {
			t.Errorf("interaction user = %s %s, want Carol", sent.User, sent.Member)
		}
		w.ack(t, sent, done)
	})

	t.Run("a selection of values the menu does not offer", func(t *testing.T) {
		done := startAction(t, func(ctx context.Context) (fakediscord.Result, error) {
			return w.srv.SelectForged(ctx, w.alice, menu, "pick", "z", "y", "x")
		})

		sent := w.bot.nextInteraction(t)

		if !strings.Contains(string(sent.Data), `"values":["z","y","x"]`) {
			t.Errorf("data = %s, want the forged values", sent.Data)
		}
		w.ack(t, sent, done)
	})

	t.Run("a modal submit nobody opened", func(t *testing.T) {
		done := startAction(t, func(ctx context.Context) (fakediscord.Result, error) {
			return w.srv.SubmitModalForged(ctx, w.alice, fakediscord.Modal{CustomID: "ghost"}, map[string]string{"x": strings.Repeat("v", 5000)})
		})

		sent := w.bot.nextInteraction(t)

		if sent.Type != 5 || !strings.Contains(string(sent.Data), `"custom_id":"ghost"`) {
			t.Errorf("interaction = type %d data %.80s, want a forged modal submit", sent.Type, sent.Data)
		}
		w.ack(t, sent, done)
	})
}

func TestSubmitModalRefusesWhatTheModalDoesNotAllow(t *testing.T) {
	modalBody := `{"type":9,"data":{"custom_id":"wish:1","title":"Your wish","components":[` +
		`{"type":1,"components":[{"type":4,"custom_id":"wish","style":1,"label":"Wish","max_length":10}]},` +
		`{"type":1,"components":[{"type":4,"custom_id":"note","style":2,"label":"Note","required":false}]}]}}`
	tests := []struct {
		name   string
		modal  func(opened fakediscord.Modal) fakediscord.Modal
		fields map[string]string
		want   string
	}{
		{"a missing required field", same, map[string]string{"note": "n"}, "required"},
		{"a field the modal lacks", same, map[string]string{"wish": "w", "extra": "e"}, "no text input"},
		{"a value over max_length", same, map[string]string{"wish": strings.Repeat("w", 11)}, "max_length 10"},
		{"another modal's custom id", func(m fakediscord.Modal) fakediscord.Modal { m.CustomID = "other"; return m }, map[string]string{"wish": "w"}, `the open modal is "wish:1", not "other"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := newInteractionWorld(t, fakediscord.Options{})
			_, opened := w.answerSlash(t, w.alice, w.inGuild("/ping", nil), modalBody)

			_, err := w.srv.SubmitModal(testContext(t, actionTimeout), w.alice, tc.modal(*opened.Modal), tc.fields)

			if !errors.Is(err, fakediscord.ErrRejected) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want ErrRejected mentioning %q", err, tc.want)
			}
		})
	}

	t.Run("someone else's modal", func(t *testing.T) {
		w := newInteractionWorld(t, fakediscord.Options{})
		_, opened := w.answerSlash(t, w.alice, w.inGuild("/ping", nil), modalBody)

		_, err := w.srv.SubmitModal(testContext(t, actionTimeout), w.bruno, *opened.Modal, map[string]string{"wish": "w"})

		if !errors.Is(err, fakediscord.ErrRejected) {
			t.Errorf("Bruno submitting Alice's modal = %v, want ErrRejected", err)
		}
	})
}

func same(m fakediscord.Modal) fakediscord.Modal {
	return m
}

func TestWaitDMReturnsExistingAndFutureMessages(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{})
	dm := openDM(t, w.srv, aliceID)
	send := func(content string) {
		call(t, w.srv, http.MethodPost, "/api/v10/channels/"+dm+"/messages", botAuth(w.srv), `{"content":"`+content+`"}`)
	}
	send("first")

	existing, err := w.srv.WaitDM(testContext(t, actionTimeout), w.alice, func(m fakediscord.Message) bool { return m.Content == "first" })
	if err != nil || existing.Content != "first" {
		t.Fatalf("WaitDM for an existing message = %+v, %v", existing, err)
	}

	type outcome struct {
		msg fakediscord.Message
		err error
	}
	done := make(chan outcome, 1)
	ctx := testContext(t, actionTimeout)
	go func() {
		msg, waitErr := w.srv.WaitDM(ctx, w.alice, func(m fakediscord.Message) bool { return m.Content == "later" })
		done <- outcome{msg, waitErr}
	}()
	send("unrelated")
	send("later")

	got := waitChan(t, done, actionTimeout, "WaitDM")
	if got.err != nil || got.msg.Content != "later" {
		t.Errorf("WaitDM for a later message = %+v, %v", got.msg, got.err)
	}

	_, nilErr := w.srv.WaitDM(testContext(t, actionTimeout), w.alice, nil)
	if nilErr == nil {
		t.Error("WaitDM with a nil matcher succeeded")
	}
}

func TestWaitDMEndsWithItsContext(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{})
	ctx := testContext(t, 50*time.Millisecond)

	_, err := w.srv.WaitDM(ctx, w.carol, func(fakediscord.Message) bool { return true })

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("WaitDM = %v, want context.DeadlineExceeded", err)
	}
}

func FuzzSlashInput(f *testing.F) {
	srv := newFake(f, fakediscord.Options{})
	alice := srv.AddUser(fakediscord.User{ID: aliceID, Name: "Alice"})
	guild := srv.AddGuild(fakediscord.Guild{ID: winterGuildID, Name: "Winter guild"}, alice)
	registered := call(f, srv, http.MethodPut, "/api/v10/applications/"+srv.ApplicationID()+"/commands", botAuth(srv), geCommands)
	if registered.status != http.StatusOK {
		f.Fatalf("register commands: %d %s", registered.status, registered.body)
	}
	seeds := []struct{ path, option, text string }{
		{"/ge/join", "exchange", "winter"},
		{"/ge/join", "budget", "20"},
		{"/ge/admin/create", "title", "Winter"},
		{"/ping", "", ""},
		{"", "", ""},
		{"/", "", ""},
		{"//", "x", "y"},
		{"/ge/join/join/join", "wish", "x"},
		{"/ge/join", "wish", strings.Repeat("w", 5000)},
		{"/ge/join", "exchange", "\u202eRTL \u200b e\u0301 \U0001F600"},
		{"/ge/join", "friend", "<@" + aliceID + ">"},
		{"/ge/join", "friend", aliceID},
		{"/" + strings.Repeat("a", 300), "", ""},
	}
	for _, seed := range seeds {
		f.Add(seed.path, seed.option, seed.text, false)
	}

	f.Fuzz(func(t *testing.T, path, option, text string, bypass bool) {
		options := map[string]any{option: text}
		for _, as := range []any{text, len(text), true, 1.5} {
			options["n"+option] = as
		}

		_, err := srv.Slash(testContext(t, time.Second), alice, fakediscord.Slash{
			Guild: &guild, Path: path, Options: options, BypassValidation: bypass,
		})

		if err == nil {
			t.Fatalf("Slash succeeded with no bot connected for %q", path)
		}
		if !errors.Is(err, fakediscord.ErrRejected) && !errors.Is(err, fakediscord.ErrNoBot) {
			t.Fatalf("Slash error = %v, want ErrRejected or ErrNoBot", err)
		}
	})
}
