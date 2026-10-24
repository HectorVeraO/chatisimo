package fakediscord_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/gateway"
	"github.com/gorilla/websocket"

	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
)

// Synthetic placeholders for template tests.
const (
	tplAppID         = "300000000000000001"
	tplInteractionID = "400000000000000001"
	tplChannelID     = "500000000000000001"
	tplDMChannelID   = "500000000000000002"
	tplCommandID     = "600000000000000001"
	tplToken         = "synthetic-interaction-token-0001"
)

var (
	tplBot  = fakediscord.UserJSON{ID: tplAppID, Username: "Fake Bot", Discriminator: "0", Bot: true}
	tplUser = fakediscord.UserJSON{ID: aliceID, Username: "Alice", Discriminator: "0"}
)

// templateInputs is a complete set of synthetic values for any template.
func templateInputs(guild bool) fakediscord.TemplateData {
	data := fakediscord.TemplateData{
		AppID:         tplAppID,
		Bot:           tplBot,
		User:          tplUser,
		InteractionID: tplInteractionID,
		Token:         tplToken,
		ChannelID:     tplDMChannelID,
		CommandID:     tplCommandID,
		CommandName:   "ge",
		Options: []map[string]any{{"name": "join", "type": 1, "options": []map[string]any{
			{"name": "exchange", "type": 3, "value": "Winter", "focused": true},
			{"name": "budget", "type": 4, "value": 20},
			{"name": "anonymous", "type": 5, "value": true},
		}}},
		Message: map[string]any{
			"id": "700000000000000001", "channel_id": tplChannelID, "author": tplBot, "content": "Join us",
			"timestamp": "2026-01-01T00:00:00Z", "type": 0, "flags": 0, "embeds": []any{},
			"components": []any{map[string]any{"type": 1, "components": []any{
				map[string]any{"type": 2, "style": 1, "label": "Join", "custom_id": "join:1"},
			}}},
			"attachments": []any{}, "mentions": []any{},
		},
		CustomID: "join:1",
		Values:   []string{"a", "b"},
		ModalComponents: []map[string]any{{"type": 1, "components": []map[string]any{
			{"type": 4, "custom_id": "wish", "value": "socks"},
		}}},
		HeartbeatInterval:  41250,
		Seq:                1,
		SessionID:          "synthetic-session",
		ResumeURL:          "ws://127.0.0.1:1/ws",
		UnavailableGuilds:  []map[string]any{{"id": winterGuildID, "unavailable": true}},
		GuildName:          "Winter guild",
		OwnerID:            aliceID,
		MessageID:          "700000000000000002",
		Content:            "hello",
		Timestamp:          "2026-01-01T00:00:00Z",
		Embeds:             []any{},
		MessageComponents:  []any{},
		CommandVersion:     "800000000000000001",
		CommandDescription: "Gift exchange",
	}
	if guild {
		data.GuildID = winterGuildID
		data.ChannelID = tplChannelID
	}

	return data
}

func TestEveryPayloadTemplateIsPresentAndFillsToValidJSON(t *testing.T) {
	names, err := fakediscord.TemplateNames()
	if err != nil {
		t.Fatalf("TemplateNames: %v", err)
	}
	want := []string{
		"command.json", "dm_channel.json", "guild_create.json", "hello.json",
		"interaction_autocomplete_dm.json", "interaction_autocomplete_guild.json",
		"interaction_button_dm.json", "interaction_button_guild.json",
		"interaction_modal_dm.json", "interaction_modal_guild.json",
		"interaction_select_dm.json", "interaction_select_guild.json",
		"interaction_slash_dm.json", "interaction_slash_guild.json",
		"message.json", "ready.json",
	}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("templates = %v, want %v", names, want)
	}

	for _, name := range names {
		isGuild := strings.HasSuffix(name, "_guild.json")
		filled, fillErr := fakediscord.FillTemplate(name, templateInputs(isGuild))

		if fillErr != nil || !json.Valid(filled) {
			t.Errorf("%s: filled = %.80q, err %v", name, filled, fillErr)
		}
		if strings.Contains(string(filled), "{{") || strings.Contains(string(filled), "<no value>") {
			t.Errorf("%s: leftover placeholder in %s", name, filled)
		}
	}
}

func TestTemplatesHoldPlaceholdersNeverRealData(t *testing.T) {
	names, err := fakediscord.TemplateNames()
	if err != nil {
		t.Fatalf("TemplateNames: %v", err)
	}
	longNumber := regexp.MustCompile(`[0-9]{9,}`)

	for _, name := range names {
		source, readErr := fakediscord.TemplateSource(name)
		if readErr != nil {
			t.Fatal(readErr)
		}

		if longNumber.MatchString(source) {
			t.Errorf("%s holds a literal ID: %s", name, longNumber.FindString(source))
		}
		for _, forbidden := range []string{"@", "http", "discord.com", "gmail", "Authorization"} {
			if strings.Contains(source, forbidden) {
				t.Errorf("%s contains %q", name, forbidden)
			}
		}
	}
}

func TestInteractionTemplatesDecodeThroughDisgo(t *testing.T) {
	tests := []struct {
		name      string
		template  string
		guild     bool
		wantType  discord.InteractionType
		checkData func(t *testing.T, i discord.Interaction)
	}{
		{"slash in a guild", "interaction_slash_guild.json", true, discord.InteractionTypeApplicationCommand, checkSlash},
		{"slash in a DM", "interaction_slash_dm.json", false, discord.InteractionTypeApplicationCommand, checkSlash},
		{"autocomplete in a guild", "interaction_autocomplete_guild.json", true, discord.InteractionTypeAutocomplete, checkAutocomplete},
		{"autocomplete in a DM", "interaction_autocomplete_dm.json", false, discord.InteractionTypeAutocomplete, checkAutocomplete},
		{"button in a guild", "interaction_button_guild.json", true, discord.InteractionTypeComponent, checkButton},
		{"button in a DM", "interaction_button_dm.json", false, discord.InteractionTypeComponent, checkButton},
		{"select in a guild", "interaction_select_guild.json", true, discord.InteractionTypeComponent, checkSelect},
		{"select in a DM", "interaction_select_dm.json", false, discord.InteractionTypeComponent, checkSelect},
		{"modal in a guild", "interaction_modal_guild.json", true, discord.InteractionTypeModalSubmit, checkModal},
		{"modal in a DM", "interaction_modal_dm.json", false, discord.InteractionTypeModalSubmit, checkModal},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			filled, err := fakediscord.FillTemplate(tc.template, templateInputs(tc.guild))
			if err != nil {
				t.Fatalf("fill: %v", err)
			}

			interaction, err := discord.UnmarshalInteraction(filled)
			if err != nil {
				t.Fatalf("UnmarshalInteraction: %v\n%s", err, filled)
			}
			if interaction.Type() != tc.wantType || interaction.ApplicationID().String() != tplAppID || interaction.Token() != tplToken {
				t.Errorf("interaction = type %v app %v token %v", interaction.Type(), interaction.ApplicationID(), interaction.Token())
			}
			if interaction.User().ID.String() != aliceID {
				t.Errorf("User().ID = %v, want Alice", interaction.User().ID)
			}
			hasGuild := interaction.GuildID() != nil
			if hasGuild != tc.guild {
				t.Errorf("has guild = %v, want %v", hasGuild, tc.guild)
			}
			if tc.guild && (interaction.Member() == nil || interaction.GuildID().String() != winterGuildID) {
				t.Errorf("a guild interaction needs its member and guild: %+v", interaction.Member())
			}
			if tc.guild && interaction.Channel().Type() != discord.ChannelTypeGuildText {
				t.Errorf("channel type = %v, want guild text", interaction.Channel().Type())
			}
			if !tc.guild && interaction.Channel().Type() != discord.ChannelTypeDM {
				t.Errorf("channel type = %v, want DM", interaction.Channel().Type())
			}
			tc.checkData(t, interaction)
		})
	}
}

func checkSlash(t *testing.T, i discord.Interaction) {
	t.Helper()

	data := i.(discord.ApplicationCommandInteraction).SlashCommandInteractionData()
	if data.CommandName() != "ge" || data.CommandPath() != "/ge/join" || data.String("exchange") != "Winter" ||
		data.Int("budget") != 20 || !data.Bool("anonymous") {
		t.Errorf("slash data = %+v, want /ge/join with the filled options", data)
	}
}

func checkAutocomplete(t *testing.T, i discord.Interaction) {
	t.Helper()

	data := i.(discord.AutocompleteInteraction).Data
	if data.Focused().Name != "exchange" || data.String("exchange") != "Winter" || data.SubCommandName == nil || *data.SubCommandName != "join" {
		t.Errorf("autocomplete data = %+v, want the focused exchange of /ge/join", data)
	}
}

func checkButton(t *testing.T, i discord.Interaction) {
	t.Helper()

	component := i.(discord.ComponentInteraction)
	if component.Data.CustomID() != "join:1" || component.Message.Content != "Join us" {
		t.Errorf("button = %q on %q, want join:1 on the message", component.Data.CustomID(), component.Message.Content)
	}
}

func checkSelect(t *testing.T, i discord.Interaction) {
	t.Helper()

	menu := i.(discord.ComponentInteraction).StringSelectMenuInteractionData()
	if menu.CustomID() != "join:1" || strings.Join(menu.Values, ",") != "a,b" {
		t.Errorf("select = %q %v, want join:1 with a and b", menu.CustomID(), menu.Values)
	}
}

func checkModal(t *testing.T, i discord.Interaction) {
	t.Helper()

	modal := i.(discord.ModalSubmitInteraction)
	if modal.Data.CustomID != "join:1" || modal.Data.Text("wish") != "socks" {
		t.Errorf("modal = %q wish %q, want join:1 with socks", modal.Data.CustomID, modal.Data.Text("wish"))
	}
	if modal.Message == nil {
		t.Error("a modal opened from a component carries its message")
	}
}

func TestModalSubmitWithoutAMessageDecodes(t *testing.T) {
	data := templateInputs(true)
	data.Message = nil

	filled, err := fakediscord.FillTemplate("interaction_modal_guild.json", data)
	if err != nil {
		t.Fatalf("fill: %v", err)
	}
	interaction, err := discord.UnmarshalInteraction(filled)
	if err != nil {
		t.Fatalf("UnmarshalInteraction: %v", err)
	}
	if modal := interaction.(discord.ModalSubmitInteraction); modal.Message != nil {
		t.Errorf("message = %+v, want none", modal.Message)
	}
}

func TestHostileStringsStayValuesNotJSON(t *testing.T) {
	hostile := []string{
		`"quote"`, `back\slash`, "new\nline", `{{.AppID}}`, "\u202eRTL\u200b", "e\u0301", "\U0001F600", `","type":99,"x":"`,
		strings.Repeat("a", 10000),
	}
	for _, text := range hostile {
		data := templateInputs(true)
		data.CommandName = text
		data.User = fakediscord.UserJSON{ID: aliceID, Username: text, Discriminator: "0"}
		data.Options = []map[string]any{{"name": "x", "type": 3, "value": text}}

		filled, err := fakediscord.FillTemplate("interaction_slash_guild.json", data)
		if err != nil {
			t.Fatalf("fill %.20q: %v", text, err)
		}
		interaction, err := discord.UnmarshalInteraction(filled)
		if err != nil {
			t.Fatalf("decode %.20q: %v", text, err)
		}

		slash := interaction.(discord.ApplicationCommandInteraction).SlashCommandInteractionData()
		if slash.CommandName() != text || slash.String("x") != text || interaction.User().Username != text {
			t.Errorf("%.20q did not round-trip: %q %q %q", text, slash.CommandName(), slash.String("x"), interaction.User().Username)
		}
	}
}

func FuzzTemplatesStayValidJSON(f *testing.F) {
	names, err := fakediscord.TemplateNames()
	if err != nil {
		f.Fatalf("TemplateNames: %v", err)
	}
	for _, seed := range []string{"", "plain", `"`, `\`, "\n", `{{.AppID}}`, "\u202e\u200b", "\U0001F600", strings.Repeat("x", 500)} {
		f.Add(seed, seed)
	}

	f.Fuzz(func(t *testing.T, a, b string) {
		for _, name := range names {
			data := templateInputs(strings.HasSuffix(name, "_guild.json"))
			data.CustomID, data.CommandName, data.Content, data.GuildName, data.SessionID = a, b, a, b, a
			data.User = fakediscord.UserJSON{ID: aliceID, Username: a, Discriminator: "0"}
			data.Values = []string{a, b}
			data.Options = []map[string]any{{"name": b, "type": 3, "value": a}}

			filled, fillErr := fakediscord.FillTemplate(name, data)

			if fillErr != nil || !json.Valid(filled) {
				t.Fatalf("%s: %.100q err %v", name, filled, fillErr)
			}
		}
	})
}

// canonical decodes a payload with decode and returns the value re-encoded, so two payloads
// can be compared by what disgo understood of them.
func canonical(t *testing.T, label string, decode func() (any, error)) string {
	t.Helper()

	value, err := decode()
	if err != nil {
		t.Fatalf("%s: disgo cannot decode it: %v", label, err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("%s: re-encode: %v", label, err)
	}

	return string(encoded)
}

func decodeGatewayData(data []byte) func() (any, error) {
	return func() (any, error) {
		var message gateway.Message
		err := json.Unmarshal(data, &message)
		if err != nil {
			return nil, fmt.Errorf("decode gateway message: %w", err)
		}

		return message.D, nil
	}
}

// readRaw reads the next frame as the fake sent it.
func readRaw(t *testing.T, ws *websocket.Conn) []byte {
	t.Helper()

	_, data, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}

	return data
}

func bodyOf(t *testing.T, got reply) []byte {
	t.Helper()

	if got.status != http.StatusOK {
		t.Fatalf("status = %d, body %s", got.status, got.body)
	}

	return got.body
}

// TestTemplatesAgreeWithTheFramesAndBodiesTheFakeBuilds is the wire contract: the fake builds
// frames and REST bodies from Go structs, and disgo must understand them exactly as it
// understands the recorded template filled with the same values.
func TestTemplatesAgreeWithTheFramesAndBodiesTheFakeBuilds(t *testing.T) {
	srv := startFake(t, fakediscord.Options{HeartbeatInterval: 12345 * time.Millisecond})
	alice := srv.AddUser(fakediscord.User{ID: aliceID, Name: "Alice"})
	guild := srv.AddGuild(fakediscord.Guild{ID: winterGuildID, Name: "Winter guild"}, alice)
	bot := fakediscord.UserJSON{ID: srv.ApplicationID(), Username: "Fake Bot", Discriminator: "0", Bot: true}
	user := fakediscord.UserJSON{ID: aliceID, Username: "Alice", Discriminator: "0"}

	url := "ws" + strings.TrimPrefix(srv.URL(), "http") + "/ws"
	ws, resp, err := websocket.DefaultDialer.DialContext(testContext(t, gatewayTimeout), url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = resp.Body.Close()
	t.Cleanup(func() { _ = ws.Close() })

	hello := readRaw(t, ws)
	t.Run("hello", func(t *testing.T) {
		filled, fillErr := fakediscord.FillTemplate("hello.json", fakediscord.TemplateData{HeartbeatInterval: 12345})

		if fillErr != nil {
			t.Fatal(fillErr)
		}
		if got, want := canonical(t, "fake", decodeGatewayData(hello)), canonical(t, "template", decodeGatewayData(filled)); got != want {
			t.Errorf("fake hello decodes as %s, template as %s", got, want)
		}
	})

	err = ws.WriteMessage(websocket.TextMessage, []byte(`{"op":2,"d":{"token":"`+srv.Token()+`","intents":1}}`))
	if err != nil {
		t.Fatalf("identify: %v", err)
	}
	ready := readRaw(t, ws)
	created := readRaw(t, ws)

	t.Run("ready", func(t *testing.T) {
		var frame struct {
			S int `json:"s"`
			D struct {
				SessionID string `json:"session_id"`
				ResumeURL string `json:"resume_gateway_url"`
			} `json:"d"`
		}
		decodeJSON(t, ready, &frame)
		data := fakediscord.TemplateData{
			Seq: frame.S, Bot: bot, AppID: srv.ApplicationID(), SessionID: frame.D.SessionID, ResumeURL: frame.D.ResumeURL,
			UnavailableGuilds: []map[string]any{{"id": guild.ID, "unavailable": true}},
		}

		filled, fillErr := fakediscord.FillTemplate("ready.json", data)

		if fillErr != nil {
			t.Fatal(fillErr)
		}
		if got, want := canonical(t, "fake", decodeGatewayData(ready)), canonical(t, "template", decodeGatewayData(filled)); got != want {
			t.Errorf("fake READY decodes as %s, template as %s", got, want)
		}
	})

	t.Run("guild create", func(t *testing.T) {
		var frame struct {
			S int `json:"s"`
		}
		decodeJSON(t, created, &frame)
		data := fakediscord.TemplateData{Seq: frame.S, GuildID: guild.ID, GuildName: guild.Name, OwnerID: aliceID}

		filled, fillErr := fakediscord.FillTemplate("guild_create.json", data)

		if fillErr != nil {
			t.Fatal(fillErr)
		}
		if got, want := canonical(t, "fake", decodeGatewayData(created)), canonical(t, "template", decodeGatewayData(filled)); got != want {
			t.Errorf("fake GUILD_CREATE decodes as %s, template as %s", got, want)
		}
	})

	t.Run("message", func(t *testing.T) {
		body := bodyOf(t, call(t, srv, http.MethodPost, "/api/v10/channels/"+guild.ChannelID+"/messages", botAuth(srv),
			`{"content":"hello <@`+aliceID+`>","embeds":[{"title":"t"}],"components":[]}`))
		var actual struct {
			ID        string `json:"id"`
			Timestamp string `json:"timestamp"`
		}
		decodeJSON(t, body, &actual)
		data := fakediscord.TemplateData{
			MessageID: actual.ID, ChannelID: guild.ChannelID, Bot: bot, Content: "hello <@" + aliceID + ">",
			Timestamp: actual.Timestamp, Embeds: []any{map[string]any{"title": "t"}}, MessageComponents: []any{},
		}

		filled, fillErr := fakediscord.FillTemplate("message.json", data)

		if fillErr != nil {
			t.Fatal(fillErr)
		}
		decodeMessage := func(raw []byte) func() (any, error) {
			return func() (any, error) {
				var message discord.Message
				decodeErr := json.Unmarshal(raw, &message)
				if decodeErr != nil {
					return nil, fmt.Errorf("decode message: %w", decodeErr)
				}

				return message, nil
			}
		}
		if got, want := canonical(t, "fake", decodeMessage(body)), canonical(t, "template", decodeMessage(filled)); got != want {
			t.Errorf("fake message decodes as %s, template as %s", got, want)
		}
	})

	t.Run("dm channel", func(t *testing.T) {
		body := bodyOf(t, call(t, srv, http.MethodPost, "/api/v10/users/@me/channels", botAuth(srv), `{"recipient_id":"`+aliceID+`"}`))
		var actual struct {
			ID string `json:"id"`
		}
		decodeJSON(t, body, &actual)

		filled, fillErr := fakediscord.FillTemplate("dm_channel.json", fakediscord.TemplateData{ChannelID: actual.ID, User: user})

		if fillErr != nil {
			t.Fatal(fillErr)
		}
		decodeChannel := func(raw []byte) func() (any, error) {
			return func() (any, error) {
				var channel discord.UnmarshalChannel
				decodeErr := json.Unmarshal(raw, &channel)
				if decodeErr != nil {
					return nil, fmt.Errorf("decode channel: %w", decodeErr)
				}

				return channel.Channel, nil
			}
		}
		if got, want := canonical(t, "fake", decodeChannel(body)), canonical(t, "template", decodeChannel(filled)); got != want {
			t.Errorf("fake DM channel decodes as %s, template as %s", got, want)
		}
	})

	t.Run("command", func(t *testing.T) {
		body := bodyOf(t, call(t, srv, http.MethodPut, "/api/v10/applications/"+srv.ApplicationID()+"/commands", botAuth(srv),
			`[{"name":"ping","description":"Ping the bot","type":1}]`))
		var elements []json.RawMessage
		decodeJSON(t, body, &elements)
		var actual struct {
			ID      string `json:"id"`
			Version string `json:"version"`
		}
		decodeJSON(t, elements[0], &actual)
		data := fakediscord.TemplateData{
			AppID: srv.ApplicationID(), CommandID: actual.ID, CommandVersion: actual.Version,
			CommandName: "ping", CommandDescription: "Ping the bot",
		}

		filled, fillErr := fakediscord.FillTemplate("command.json", data)

		if fillErr != nil {
			t.Fatal(fillErr)
		}
		decodeCommand := func(raw []byte) func() (any, error) {
			return func() (any, error) {
				var command discord.UnmarshalApplicationCommand
				decodeErr := json.Unmarshal(raw, &command)
				if decodeErr != nil {
					return nil, fmt.Errorf("decode command: %w", decodeErr)
				}

				return command.ApplicationCommand, nil
			}
		}
		if got, want := canonical(t, "fake", decodeCommand(elements[0])), canonical(t, "template", decodeCommand(filled)); got != want {
			t.Errorf("fake command decodes as %s, template as %s", got, want)
		}
	})
}
