package fakediscord_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
)

const unauthorizedBody = `{"code":0,"message":"401: Unauthorized"}`

func TestBotRoutesRequireBotAuthorization(t *testing.T) {
	srv := newFake(t, fakediscord.Options{})
	alice := srv.AddUser(fakediscord.User{ID: aliceID, Name: "Alice"})
	guild := srv.AddGuild(fakediscord.Guild{ID: winterGuildID, Name: "Winter guild"}, alice)
	app := srv.ApplicationID()

	routes := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"global commands", http.MethodPut, "/api/v10/applications/" + app + "/commands", `[]`},
		{"guild commands", http.MethodPut, "/api/v10/applications/" + app + "/guilds/" + guild.ID + "/commands", `[]`},
		{"create DM", http.MethodPost, "/api/v10/users/@me/channels", `{"recipient_id":"` + aliceID + `"}`},
		{"create message", http.MethodPost, "/api/v10/channels/" + guild.ChannelID + "/messages", `{"content":"hi"}`},
	}
	credentials := []struct {
		name string
		auth string
	}{
		{"no header", ""},
		{"bearer scheme", "Bearer " + srv.Token()},
		{"token without scheme", srv.Token()},
		{"wrong token", "Bot " + srv.Token() + "x"},
		{"lowercase scheme", "bot " + srv.Token()},
	}

	for _, route := range routes {
		for _, cred := range credentials {
			t.Run(route.name+" rejects "+cred.name, func(t *testing.T) {
				got := call(t, srv, route.method, route.path, cred.auth, route.body)

				if got.status != http.StatusUnauthorized {
					t.Errorf("status = %d, want 401", got.status)
				}
				if string(got.body) != unauthorizedBody {
					t.Errorf("body = %q, want %q", got.body, unauthorizedBody)
				}
			})
		}
	}

	t.Run("rejected requests change nothing", func(t *testing.T) {
		if len(srv.Outbound()) != 0 {
			t.Errorf("outbound records = %d, want 0", len(srv.Outbound()))
		}
		if len(srv.Commands()) != 0 {
			t.Errorf("commands = %d, want 0", len(srv.Commands()))
		}
	})
}

func TestGatewayURLFollowsTheRequestHost(t *testing.T) {
	srv := newFake(t, fakediscord.Options{})

	tests := []struct {
		name string
		host string
		want string
	}{
		{"host with port", "127.0.0.1:10010", "ws://127.0.0.1:10010/ws"},
		{"compose service name", "fakediscord:10010", "ws://fakediscord:10010/ws"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := callWithHost(t, srv, http.MethodGet, "/api/v10/gateway", "", "", tc.host)

			if got.status != http.StatusOK {
				t.Fatalf("status = %d, want 200", got.status)
			}
			if url := got.json(t)["url"]; url != tc.want {
				t.Errorf("url = %v, want %q", url, tc.want)
			}
		})
	}
}

func TestUnservedRoutesAnswerJSONNotFound(t *testing.T) {
	srv := newFake(t, fakediscord.Options{})
	want := `{"code":0,"message":"404: Not Found"}`

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"unknown path", http.MethodGet, "/nope"},
		{"unknown path under the API", http.MethodGet, "/api/v10/guilds/" + winterGuildID},
		{"wrong method on a served path", http.MethodGet, "/api/v10/channels/" + winterGuildID + "/messages"},
		{"unversioned API path", http.MethodGet, "/api/gateway"},
		{"root", http.MethodGet, "/"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := call(t, srv, tc.method, tc.path, botAuth(srv), "")

			if got.status != http.StatusNotFound {
				t.Errorf("status = %d, want 404", got.status)
			}
			if string(got.body) != want {
				t.Errorf("body = %q, want %q", got.body, want)
			}
		})
	}
}

func TestGlobalCommandsAreStoredAndReplaced(t *testing.T) {
	srv := newFake(t, fakediscord.Options{})
	path := "/api/v10/applications/" + srv.ApplicationID() + "/commands"

	first := call(t, srv, http.MethodPut, path, botAuth(srv),
		`[{"name":"ping","description":"Ping"},{"name":"menu","type":2}]`)
	if first.status != http.StatusOK {
		t.Fatalf("first PUT status = %d, body %s", first.status, first.body)
	}
	var echoed []map[string]any
	decodeJSON(t, first.body, &echoed)
	if len(echoed) != 2 {
		t.Fatalf("echoed %d commands, want 2", len(echoed))
	}
	for i, cmd := range echoed {
		for _, key := range []string{"id", "application_id", "version"} {
			if s, ok := cmd[key].(string); !ok || s == "" {
				t.Errorf("command %d lacks string %q: %v", i, key, cmd)
			}
		}
		if cmd["application_id"] != srv.ApplicationID() {
			t.Errorf("command %d application_id = %v, want %s", i, cmd["application_id"], srv.ApplicationID())
		}
	}
	if echoed[0]["type"] != float64(1) {
		t.Errorf("default type = %v, want 1", echoed[0]["type"])
	}
	if echoed[1]["type"] != float64(2) {
		t.Errorf("explicit type = %v, want 2", echoed[1]["type"])
	}
	if echoed[0]["description"] != "Ping" {
		t.Errorf("posted fields were not kept: %v", echoed[0])
	}
}

func TestGlobalCommandsPutReplacesThePreviousSet(t *testing.T) {
	srv := newFake(t, fakediscord.Options{})
	path := "/api/v10/applications/" + srv.ApplicationID() + "/commands"
	first := call(t, srv, http.MethodPut, path, botAuth(srv),
		`[{"name":"ping","description":"Ping"},{"name":"menu","type":2}]`)
	if first.status != http.StatusOK {
		t.Fatalf("first PUT status = %d, body %s", first.status, first.body)
	}

	second := call(t, srv, http.MethodPut, path, botAuth(srv), `[{"name":"only"}]`)
	if second.status != http.StatusOK {
		t.Fatalf("second PUT status = %d", second.status)
	}
	commands := srv.Commands()
	if len(commands) != 1 || commands[0].Name != "only" {
		t.Errorf("commands after the second PUT = %+v, want just \"only\"", commands)
	}
	if commands[0].GuildID != "" || commands[0].Type != 1 || commands[0].ID == "" || commands[0].Version == "" {
		t.Errorf("stored command = %+v", commands[0])
	}
}

func TestGuildCommandsCarryTheGuildID(t *testing.T) {
	srv := newFake(t, fakediscord.Options{})
	srv.AddGuild(fakediscord.Guild{ID: winterGuildID, Name: "Winter guild"})
	prefix := "/api/v10/applications/" + srv.ApplicationID()

	got := call(t, srv, http.MethodPut, prefix+"/guilds/"+winterGuildID+"/commands", botAuth(srv), `[{"name":"ping"}]`)

	if got.status != http.StatusOK {
		t.Fatalf("status = %d, body %s", got.status, got.body)
	}
	var echoed []map[string]any
	decodeJSON(t, got.body, &echoed)
	if echoed[0]["guild_id"] != winterGuildID {
		t.Errorf("guild_id = %v, want %s", echoed[0]["guild_id"], winterGuildID)
	}

	global := call(t, srv, http.MethodPut, prefix+"/commands", botAuth(srv), `[{"name":"global"}]`)
	if global.status != http.StatusOK {
		t.Fatalf("global PUT status = %d", global.status)
	}
	commands := srv.Commands()
	if len(commands) != 2 || commands[0].GuildID != "" || commands[1].GuildID != winterGuildID {
		t.Errorf("commands = %+v, want the global one first and the guild one second", commands)
	}
}

func TestCommandsRejectBadRequests(t *testing.T) {
	srv := newFake(t, fakediscord.Options{})
	srv.AddGuild(fakediscord.Guild{ID: winterGuildID, Name: "Winter guild"})
	app := srv.ApplicationID()

	tests := []struct {
		name     string
		path     string
		body     string
		wantCode int
		wantHTTP int
	}{
		{"wrong application on the global route", "/api/v10/applications/" + missingID + "/commands", `[]`, 10002, 404},
		{"wrong application on the guild route", "/api/v10/applications/" + missingID + "/guilds/" + winterGuildID + "/commands", `[]`, 10002, 404},
		{"unknown guild", "/api/v10/applications/" + app + "/guilds/" + missingID + "/commands", `[]`, 10004, 404},
		{"body is not JSON", "/api/v10/applications/" + app + "/commands", `[{`, 50035, 400},
		{"body is not an array", "/api/v10/applications/" + app + "/commands", `{"name":"x"}`, 50035, 400},
		{"command without a name", "/api/v10/applications/" + app + "/commands", `[{"description":"x"}]`, 50035, 400},
		{"command with a numeric name", "/api/v10/applications/" + app + "/commands", `[{"name":7}]`, 50035, 400},
		{"command with a bad type", "/api/v10/applications/" + app + "/commands", `[{"name":"x","type":"slash"}]`, 50035, 400},
		{"null command", "/api/v10/applications/" + app + "/commands", `[null]`, 50035, 400},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := call(t, srv, http.MethodPut, tc.path, botAuth(srv), tc.body)

			if got.status != tc.wantHTTP {
				t.Errorf("status = %d, want %d", got.status, tc.wantHTTP)
			}
			if code := got.json(t)["code"]; code != float64(tc.wantCode) {
				t.Errorf("code = %v, want %d", code, tc.wantCode)
			}
		})
	}
	if len(srv.Commands()) != 0 {
		t.Errorf("rejected requests stored %d commands", len(srv.Commands()))
	}
}

func TestCreateDMReturnsTheRecipientsChannel(t *testing.T) {
	srv := newFake(t, fakediscord.Options{})
	srv.AddUser(fakediscord.User{ID: aliceID, Name: "Alice"})
	body := `{"recipient_id":"` + aliceID + `"}`

	first := call(t, srv, http.MethodPost, "/api/v10/users/@me/channels", botAuth(srv), body)
	second := call(t, srv, http.MethodPost, "/api/v10/users/@me/channels", botAuth(srv), body)

	if first.status != http.StatusOK {
		t.Fatalf("status = %d, body %s", first.status, first.body)
	}
	channel := first.json(t)
	if channel["type"] != float64(1) {
		t.Errorf("type = %v, want 1", channel["type"])
	}
	recipients, _ := channel["recipients"].([]any)
	if len(recipients) != 1 {
		t.Fatalf("recipients = %v, want one", channel["recipients"])
	}
	recipient, _ := recipients[0].(map[string]any)
	if recipient["id"] != aliceID || recipient["username"] != "Alice" {
		t.Errorf("recipient = %v", recipient)
	}
	if second.json(t)["id"] != channel["id"] {
		t.Errorf("a second request returned another channel: %v vs %v", second.json(t)["id"], channel["id"])
	}
}

func TestCreateDMRejectsBadRequests(t *testing.T) {
	srv := newFake(t, fakediscord.Options{})

	tests := []struct {
		name string
		body string
	}{
		{"unknown user", `{"recipient_id":"` + missingID + `"}`},
		{"missing recipient", `{}`},
		{"body is not JSON", `nope`},
		{"recipient is a number", `{"recipient_id":100000000000000001}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := call(t, srv, http.MethodPost, "/api/v10/users/@me/channels", botAuth(srv), tc.body)

			if got.status != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", got.status)
			}
			if code := got.json(t)["code"]; code != float64(50035) {
				t.Errorf("code = %v, want 50035", code)
			}
		})
	}
}

func TestCreateMessageAnswersWithTheMessage(t *testing.T) {
	srv := newFake(t, fakediscord.Options{})
	alice := srv.AddUser(fakediscord.User{ID: aliceID, Name: "Alice"})
	guild := srv.AddGuild(fakediscord.Guild{ID: winterGuildID, Name: "Winter guild"}, alice)

	got := call(t, srv, http.MethodPost, "/api/v10/channels/"+guild.ChannelID+"/messages", botAuth(srv),
		`{"content":"hello","flags":0,"embeds":[{"title":"t"}],"components":[]}`)

	if got.status != http.StatusOK {
		t.Fatalf("status = %d, body %s", got.status, got.body)
	}
	msg := got.json(t)
	if msg["channel_id"] != guild.ChannelID || msg["content"] != "hello" || msg["type"] != float64(0) {
		t.Errorf("message = %v", msg)
	}
	author, _ := msg["author"].(map[string]any)
	if author["id"] != srv.ApplicationID() || author["bot"] != true || author["discriminator"] != "0" {
		t.Errorf("author = %v", author)
	}
	for _, key := range []string{"embeds", "components", "attachments", "mentions"} {
		if _, ok := msg[key].([]any); !ok {
			t.Errorf("%s = %v, want an array", key, msg[key])
		}
	}
	if ts, _ := msg["timestamp"].(string); !strings.HasSuffix(ts, "Z") {
		t.Errorf("timestamp = %v, want RFC 3339 UTC", msg["timestamp"])
	}
	if id, _ := msg["id"].(string); id == "" {
		t.Errorf("message has no id: %v", msg)
	}
}

func TestCreateMessageRejectsBadRequests(t *testing.T) {
	srv := newFake(t, fakediscord.Options{})
	guild := srv.AddGuild(fakediscord.Guild{ID: winterGuildID, Name: "Winter guild"})
	channelPath := "/api/v10/channels/" + guild.ChannelID + "/messages"

	tests := []struct {
		name     string
		path     string
		body     string
		wantHTTP int
		wantCode int
	}{
		{"unknown channel", "/api/v10/channels/" + missingID + "/messages", `{"content":"x"}`, 404, 10003},
		{"body is not JSON", channelPath, `{"content":`, 400, 50035},
		{"body is an array", channelPath, `[]`, 400, 50035},
		{"content is a number", channelPath, `{"content":5}`, 400, 50035},
		{"allowed_mentions is not an object", channelPath, `{"content":"x","allowed_mentions":[1]}`, 400, 50035},
		{"allowed_mentions parse is a string", channelPath, `{"content":"x","allowed_mentions":{"parse":"users"}}`, 400, 50035},
		{"empty message", channelPath, `{}`, 400, 50006},
		{"empty message with a bad allowed_mentions", channelPath, `{"allowed_mentions":[1]}`, 400, 50035},
		{"oversized body", channelPath, `{"content":"` + strings.Repeat("a", 2<<20) + `"}`, 413, 40005},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := call(t, srv, http.MethodPost, tc.path, botAuth(srv), tc.body)

			if got.status != tc.wantHTTP {
				t.Errorf("status = %d, want %d", got.status, tc.wantHTTP)
			}
			if code := got.json(t)["code"]; code != float64(tc.wantCode) {
				t.Errorf("code = %v, want %d", code, tc.wantCode)
			}
		})
	}
	if len(srv.Outbound()) != 0 {
		t.Errorf("rejected requests left %d outbound records", len(srv.Outbound()))
	}
}

func TestTokenFirstSegmentIsTheApplicationID(t *testing.T) {
	srv := newFake(t, fakediscord.Options{})

	segments := strings.Split(srv.Token(), ".")
	if len(segments) != 3 {
		t.Fatalf("token has %d segments, want 3", len(segments))
	}
	decoded, err := base64.RawStdEncoding.DecodeString(segments[0])
	if err != nil {
		t.Fatalf("first segment is not unpadded base64: %v", err)
	}
	if string(decoded) != srv.ApplicationID() {
		t.Errorf("first segment decodes to %q, want the application ID %q", decoded, srv.ApplicationID())
	}
	if len(segments[2]) != 27 {
		t.Errorf("third segment has %d characters, want 27", len(segments[2]))
	}
}

func TestEveryFakeGetsItsOwnTokenAndIDs(t *testing.T) {
	first := newFake(t, fakediscord.Options{})
	second := newFake(t, fakediscord.Options{})

	if first.Token() == second.Token() {
		t.Error("two fakes share a token")
	}
	if first.ApplicationID() == second.ApplicationID() {
		t.Error("two fakes share an application ID")
	}
}

func TestGeneratedIDsAreUnique(t *testing.T) {
	srv := newFake(t, fakediscord.Options{})
	seen := map[string]bool{}

	for i := range 500 {
		user := srv.AddUser(fakediscord.User{Name: fmt.Sprintf("User %d", i)})

		if user.ID == "" {
			t.Fatal("generated user has no ID")
		}
		if seen[user.ID] {
			t.Fatalf("ID %s was generated twice", user.ID)
		}
		seen[user.ID] = true
	}
}

func TestNewRejectsAnInvalidHeartbeatInterval(t *testing.T) {
	tests := []struct {
		name     string
		interval time.Duration
	}{
		{"negative", -time.Second},
		{"below one millisecond", time.Microsecond},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := fakediscord.New(fakediscord.Options{HeartbeatInterval: tc.interval})

			if err == nil {
				t.Error("New accepted the interval")
			}
		})
	}
}

func FuzzRESTBodies(f *testing.F) {
	srv := newFake(f, fakediscord.Options{})
	alice := srv.AddUser(fakediscord.User{ID: aliceID, Name: "Alice"})
	guild := srv.AddGuild(fakediscord.Guild{ID: winterGuildID, Name: "Winter guild"}, alice)
	targets := []struct {
		method string
		path   string
	}{
		{http.MethodPut, "/api/v10/applications/" + srv.ApplicationID() + "/commands"},
		{http.MethodPost, "/api/v10/users/@me/channels"},
		{http.MethodPost, "/api/v10/channels/" + guild.ChannelID + "/messages"},
	}
	seeds := []string{
		``, `{`, `[]`, `null`, `[null]`, `{}`, `{"content":5}`,
		`{"content":"<@` + aliceID + `> <@&1> @everyone"}`,
		`{"content":"x","allowed_mentions":{"parse":["users"],"users":["` + aliceID + `"]}}`,
		`{"content":"\u202eRTL \u200b zero-width e\u0301 \ud83d\ude00"}`,
		`{"recipient_id":"` + aliceID + `"}`, `{"recipient_id":"` + missingID + `"}`,
		`[{"name":"ping","type":1,"description":"x"}]`, `[{"name":7}]`,
		`{"content":"` + strings.Repeat("a", 4096) + `"}`,
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, body string) {
		for _, target := range targets {
			got := call(t, srv, target.method, target.path, botAuth(srv), body)

			if got.status < 200 || got.status > 499 {
				t.Fatalf("%s %s answered %d for body %q", target.method, target.path, got.status, body)
			}
			var decoded map[string]any
			if json.Unmarshal(got.body, &decoded) != nil && got.status >= 400 {
				t.Fatalf("%s %s: error body is not a JSON object: %q", target.method, target.path, got.body)
			}
			isSuccess := got.status >= 200 && got.status < 300
			if isSuccess && !json.Valid(got.body) {
				t.Fatalf("%s %s answered %d with a body that is not JSON: %q", target.method, target.path, got.status, got.body)
			}
			if isSuccess && decoded != nil {
				id, isString := decoded["id"].(string)
				if !isString || id == "" {
					t.Fatalf("%s %s answered %d without a valid id: %q", target.method, target.path, got.status, got.body)
				}
			}
		}
	})
}

func TestCreateMessageReportsEveryProblemTogether(t *testing.T) {
	t.Parallel()
	srv := startFake(t, fakediscord.Options{})
	guild := srv.AddGuild(fakediscord.Guild{ID: winterGuildID, Name: "Winter guild"})

	got := call(t, srv, http.MethodPost, "/api/v10/channels/"+guild.ChannelID+"/messages", botAuth(srv), `{"allowed_mentions":[1]}`)

	message, _ := got.json(t)["message"].(string)
	if !strings.Contains(message, "allowed_mentions") || !strings.Contains(message, "empty message") {
		t.Errorf("message = %q, want both the allowed_mentions and the empty message problems", message)
	}
}
