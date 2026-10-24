//go:build !race

// These tests drive the fake with a real disgo client answering interactions. See
// gateway_disgo_test.go for why they stay out of -race; the raw WebSocket tests cover the
// same routes under -race.

package fakediscord_test

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
)

// botHandlers are the listeners a test gives its real disgo client.
type botHandlers struct {
	slash        func(e *events.ApplicationCommandInteractionCreate)
	autocomplete func(e *events.AutocompleteInteractionCreate)
	component    func(e *events.ComponentInteractionCreate)
	modal        func(e *events.ModalSubmitInteractionCreate)
}

// interactiveWorld is a fake with Alice and Bruno in a guild, the ge commands registered and
// a real disgo client connected and ready.
type interactiveWorld struct {
	*interactionWorld
	client *botHarness
}

func newInteractiveWorld(t *testing.T, opts fakediscord.Options, handlers botHandlers) *interactiveWorld {
	t.Helper()

	srv := startFake(t, opts)
	w := &interactionWorld{
		srv:   srv,
		alice: srv.AddUser(fakediscord.User{ID: aliceID, Name: "Alice"}),
		bruno: srv.AddUser(fakediscord.User{ID: brunoID, Name: "Bruno"}),
		carol: srv.AddUser(fakediscord.User{ID: carolID, Name: "Carol"}),
	}
	w.guild = srv.AddGuild(fakediscord.Guild{ID: winterGuildID, Name: "Winter guild"}, w.alice, w.bruno)

	var extra []bot.ConfigOpt
	if handlers.slash != nil {
		extra = append(extra, bot.WithEventListenerFunc(handlers.slash))
	}
	if handlers.autocomplete != nil {
		extra = append(extra, bot.WithEventListenerFunc(handlers.autocomplete))
	}
	if handlers.component != nil {
		extra = append(extra, bot.WithEventListenerFunc(handlers.component))
	}
	if handlers.modal != nil {
		extra = append(extra, bot.WithEventListenerFunc(handlers.modal))
	}
	h := newBot(t, srv, srv.Token(), extra...)

	path := "/api/v10/applications/" + srv.ApplicationID() + "/commands"
	if got := call(t, srv, http.MethodPut, path, botAuth(srv), geCommands); got.status != http.StatusOK {
		t.Fatalf("register commands: %d %s", got.status, got.body)
	}
	h.open(t)
	waitChan(t, h.guilds, gatewayTimeout, "GuildReady")
	if err := srv.WaitReady(testContext(t, gatewayTimeout)); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}

	return &interactiveWorld{interactionWorld: w, client: h}
}

func replyTo[T interface {
	CreateMessage(discord.MessageCreate, ...rest.RequestOpt) error
}](e T, content string, ephemeral bool) error {
	err := e.CreateMessage(discord.MessageCreate{Content: content}.WithEphemeral(ephemeral))
	if err != nil {
		return fmt.Errorf("create message: %w", err)
	}

	return nil
}

// answeringHandlers is a small bot that exercises every response type.
func answeringHandlers(t *testing.T, tokens chan<- string) botHandlers {
	t.Helper()

	check := func(err error) {
		if err != nil {
			t.Errorf("the bot's response failed: %v", err)
		}
	}

	return botHandlers{
		slash: func(e *events.ApplicationCommandInteractionCreate) {
			if tokens != nil {
				tokens <- e.Token()
			}
			data := e.SlashCommandInteractionData()
			switch data.CommandPath() {
			case "/ping":
				check(replyTo(e, "pong", false))
			case "/ge/join":
				if data.String("exchange") == "later" {
					check(e.DeferCreateMessage(true))
					content := "edited later"
					_, err := e.Client().Rest.UpdateInteractionResponse(e.ApplicationID(), e.Token(), discord.MessageUpdate{Content: &content})
					check(err)
					_, err = e.Client().Rest.CreateFollowupMessage(e.ApplicationID(), e.Token(), discord.MessageCreate{Content: "and a followup"}.WithEphemeral(true))
					check(err)

					return
				}
				content := fmt.Sprintf("%s|%d|%t|%s", data.String("exchange"), data.Int("budget"), data.Bool("anonymous"), data.User("friend").Username)
				check(replyTo(e, content, true))
			case "/ge/admin/create":
				input := discord.NewTextInput("title", discord.TextInputStyleShort)
				check(e.Modal(discord.NewModalCreate("title:1", "New exchange", discord.NewActionRow(input))))
			default:
				t.Errorf("unexpected command %s", data.CommandPath())
			}
		},
		autocomplete: func(e *events.AutocompleteInteractionCreate) {
			choices := []discord.AutocompleteChoice{discord.AutocompleteChoiceString{Name: "Winter exchange", Value: "x:" + e.Data.String("exchange")}}
			check(e.AutocompleteResult(choices))
		},
		component: func(e *events.ComponentInteractionCreate) {
			if e.Data.CustomID() == "pick" {
				check(replyTo(e, "picked "+strings.Join(e.StringSelectMenuInteractionData().Values, "+"), true))

				return
			}
			content := "Joined by " + e.User().Username
			check(e.UpdateMessage(discord.MessageUpdate{Content: &content}))
		},
		modal: func(e *events.ModalSubmitInteractionCreate) {
			check(replyTo(e, "thanks "+e.Data.Text("title"), false))
		},
	}
}

func TestARealBotAnswersEveryKindOfInteraction(t *testing.T) {
	w := newInteractiveWorld(t, fakediscord.Options{}, answeringHandlers(t, nil))
	ctx := testContext(t, gatewayTimeout)

	t.Run("a slash command in a guild", func(t *testing.T) {
		result, err := w.srv.Slash(ctx, w.alice, w.inGuild("/ping", nil))

		if err != nil || result.Message == nil || result.Message.Content != "pong" || result.CallbackType != 4 {
			t.Errorf("Slash = %+v, %v, want the pong", result, err)
		}
	})

	t.Run("a slash command in the bot's DM", func(t *testing.T) {
		result, err := w.srv.Slash(ctx, w.carol, fakediscord.Slash{Path: "/ping"})

		if err != nil || result.Message == nil {
			t.Fatalf("Slash = %+v, %v", result, err)
		}
		if dms := w.srv.DMs(w.carol); len(dms) != 1 || dms[0].Content != "pong" {
			t.Errorf("Carol's DMs = %+v, want the pong", dms)
		}
	})

	t.Run("option getters return the filled values", func(t *testing.T) {
		options := map[string]any{"exchange": "winter", "budget": 20, "anonymous": true, "friend": brunoID}

		result, err := w.srv.Slash(ctx, w.alice, w.inGuild("/ge/join", options))

		if err != nil || result.Message == nil || result.Message.Content != "winter|20|true|Bruno" {
			t.Errorf("Slash = %+v, %v, want the echoed options", result.Message, err)
		}
		if result.Message != nil && result.Message.EphemeralFor != aliceID {
			t.Errorf("the reply is visible to %q, want Alice only", result.Message.EphemeralFor)
		}
	})

	t.Run("a deferred reply edited and followed up", func(t *testing.T) {
		result, err := w.srv.Slash(ctx, w.bruno, w.inGuild("/ge/join", map[string]any{"exchange": "later"}))
		if err != nil || result.Status != fakediscord.StatusDeferred {
			t.Fatalf("Slash = %+v, %v, want a deferral", result, err)
		}

		waitFor(t, gatewayTimeout, "the edit and the followup", func() bool {
			return len(w.srv.Visible(w.bruno, w.guild.ChannelID)) >= 2
		})
		contents := []string{}
		for _, message := range w.srv.Visible(w.bruno, w.guild.ChannelID) {
			contents = append(contents, message.Content)
		}
		if !strings.Contains(strings.Join(contents, "|"), "edited later") || !strings.Contains(strings.Join(contents, "|"), "and a followup") {
			t.Errorf("Bruno sees %v, want the edit and the followup", contents)
		}
		for _, message := range w.srv.Visible(w.alice, w.guild.ChannelID) {
			if message.Content == "edited later" || message.Content == "and a followup" {
				t.Errorf("Alice sees Bruno's ephemeral %q", message.Content)
			}
		}
	})

	t.Run("an autocomplete", func(t *testing.T) {
		choices, err := w.srv.Autocomplete(ctx, w.alice, w.inGuild("/ge/join", map[string]any{"exchange": "wi"}), "exchange")

		if err != nil || len(choices) != 1 || choices[0].Name != "Winter exchange" || choices[0].Value != "x:wi" {
			t.Errorf("Autocomplete = %+v, %v", choices, err)
		}
	})

	t.Run("a modal opened and submitted", func(t *testing.T) {
		opened, err := w.srv.Slash(ctx, w.alice, w.inGuild("/ge/admin/create", map[string]any{"title": "Winter"}))
		if err != nil || opened.Modal == nil || opened.Modal.CustomID != "title:1" || opened.Modal.Title != "New exchange" {
			t.Fatalf("Slash = %+v, %v, want the modal", opened, err)
		}

		result, err := w.srv.SubmitModal(ctx, w.alice, *opened.Modal, map[string]string{"title": "Socks swap"})

		if err != nil || result.Message == nil || result.Message.Content != "thanks Socks swap" {
			t.Errorf("SubmitModal = %+v, %v, want the thanks", result, err)
		}
	})

	t.Run("a button click updates the message", func(t *testing.T) {
		message := w.botMessage(t, "Join us", buttonRow)

		result, err := w.srv.Click(ctx, w.bruno, message, "join:1")

		if err != nil || result.CallbackType != 7 || result.Message == nil || result.Message.Content != "Joined by Bruno" {
			t.Errorf("Click = %+v, %v", result, err)
		}
		if after := w.srv.Visible(w.alice, w.guild.ChannelID); !containsMessage(after, message.ID, "Joined by Bruno") {
			t.Errorf("the original message did not change: %+v", after)
		}
	})

	t.Run("a menu selection", func(t *testing.T) {
		message := w.botMessage(t, "Pick", menuRow)

		result, err := w.srv.Select(ctx, w.alice, message, "pick", "a", "c")

		if err != nil || result.Message == nil || result.Message.Content != "picked a+c" {
			t.Errorf("Select = %+v, %v", result, err)
		}
	})
}

func containsMessage(messages []fakediscord.Message, id, content string) bool {
	for _, message := range messages {
		if message.ID == id && message.Content == content {
			return true
		}
	}

	return false
}

func TestARealBotsLateCallbackIsRefusedWithUnknownInteraction(t *testing.T) {
	release := make(chan struct{})
	lateErr := make(chan error, 1)
	w := newInteractiveWorld(t, fakediscord.Options{CallbackDeadline: 100 * time.Millisecond}, botHandlers{
		slash: func(e *events.ApplicationCommandInteractionCreate) {
			<-release
			lateErr <- replyTo(e, "too late", false)
		},
	})

	result, err := w.srv.Slash(testContext(t, gatewayTimeout), w.alice, w.inGuild("/ping", nil))
	if err != nil || result.Status != fakediscord.StatusTimeout {
		t.Fatalf("Slash = %+v, %v, want a timeout", result, err)
	}
	close(release)

	got := waitChan(t, lateErr, gatewayTimeout, "the bot's late response")
	var restErr *rest.Error
	if !errors.As(got, &restErr) || restErr.Code != 10062 {
		t.Errorf("late response error = %v, want a Discord error with code 10062", got)
	}
	if visible := w.srv.Visible(w.alice, w.guild.ChannelID); len(visible) != 0 {
		t.Errorf("a late reply was shown: %+v", visible)
	}
}

func TestARealBotRetriesAfterOneRateLimit(t *testing.T) {
	w := newInteractiveWorld(t, fakediscord.Options{}, botHandlers{
		slash: func(e *events.ApplicationCommandInteractionCreate) {
			_, err := e.Client().Rest.CreateMessage(e.Channel().ID(), discord.MessageCreate{Content: "announcement"})
			if err != nil {
				t.Errorf("CreateMessage: %v", err)
			}
			_ = replyTo(e, "done", false)
		},
	})
	mustFailNext(t, w.srv, "POST /channels/{id}/messages", 429, 0)

	result, err := w.srv.Slash(testContext(t, gatewayTimeout), w.alice, w.inGuild("/ping", nil))

	if err != nil || result.Message == nil {
		t.Fatalf("Slash = %+v, %v", result, err)
	}
	announcements := 0
	for _, message := range w.srv.Visible(w.alice, w.guild.ChannelID) {
		if message.Content == "announcement" {
			announcements++
		}
	}
	if announcements != 1 {
		t.Errorf("the announcement arrived %d times, want exactly once after the retry", announcements)
	}
}

func TestARealBotCannotDMAUserWhoClosedTheirDMs(t *testing.T) {
	w := newInteractiveWorld(t, fakediscord.Options{}, botHandlers{})
	restClient := w.client.client.Rest
	channel, err := restClient.CreateDMChannel(snowflake.MustParse(aliceID))
	if err != nil {
		t.Fatalf("CreateDMChannel: %v", err)
	}
	mustSetDMsOpen(t, w.srv, w.alice, false)

	_, err = restClient.CreateMessage(channel.ID(), discord.MessageCreate{Content: "hello Alice"})

	var restErr *rest.Error
	if !errors.As(err, &restErr) || restErr.Code != 50007 {
		t.Fatalf("CreateMessage error = %v, want Discord error 50007", err)
	}
	last := w.srv.Outbound()
	if got := last[len(last)-1]; got.Status != http.StatusForbidden || got.Content != "hello Alice" {
		t.Errorf("last record = %+v, want the refused DM with status 403", got)
	}
}

func TestARealBotsRepeatedNonceSendsOnce(t *testing.T) {
	w := newInteractiveWorld(t, fakediscord.Options{}, botHandlers{})
	restClient := w.client.client.Rest
	channelID := snowflake.MustParse(w.guild.ChannelID)
	message := discord.MessageCreate{Content: "once"}.WithNonce("nonce-1").WithEnforceNonce(true)

	first, err := restClient.CreateMessage(channelID, message)
	if err != nil {
		t.Fatalf("first CreateMessage: %v", err)
	}
	second, err := restClient.CreateMessage(channelID, message)
	if err != nil {
		t.Fatalf("second CreateMessage: %v", err)
	}

	if first.ID != second.ID {
		t.Errorf("ids = %v, %v, want the first message again", first.ID, second.ID)
	}
	if visible := w.srv.Visible(w.alice, w.guild.ChannelID); len(visible) != 1 {
		t.Errorf("%d messages exist, want 1", len(visible))
	}
	records := w.srv.Outbound()
	if len(records) != 2 || !records[1].Deduplicated {
		t.Errorf("records = %+v, want the second marked deduplicated", records)
	}
}

func TestIdentifyFaultStopsARealClient(t *testing.T) {
	srv := startFake(t, fakediscord.Options{})
	srv.AddGuild(fakediscord.Guild{ID: winterGuildID, Name: "Winter guild"})
	mustFailNext(t, srv, "GATEWAY identify", 0, 4004)
	h := newBot(t, srv, srv.Token())

	err := h.client.OpenGateway(testContext(t, gatewayTimeout))

	if err == nil || !strings.Contains(err.Error(), "4004") {
		t.Errorf("OpenGateway error = %v, want a 4004 close although the token is right", err)
	}
}
