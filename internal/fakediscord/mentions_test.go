package fakediscord_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
)

// postChannelMessage posts a message to a guild channel as the bot. allowedMentions and
// embeds are raw JSON, omitted when empty.
func postChannelMessage(t testing.TB, srv *fakediscord.Server, channelID, content, allowedMentions, embeds string) reply {
	t.Helper()

	encodedContent, err := json.Marshal(content)
	if err != nil {
		t.Fatalf("encode content: %v", err)
	}
	body := `{"content":` + string(encodedContent)
	if allowedMentions != "" {
		body += `,"allowed_mentions":` + allowedMentions
	}
	if embeds != "" {
		body += `,"embeds":` + embeds
	}
	body += `}`

	return call(t, srv, http.MethodPost, "/api/v10/channels/"+channelID+"/messages", botAuth(srv), body)
}

func lastRecord(t testing.TB, srv *fakediscord.Server) fakediscord.Record {
	t.Helper()

	records := srv.Outbound()
	if len(records) == 0 {
		t.Fatal("no outbound record")
	}

	return records[len(records)-1]
}

func TestMentionsAreRecordedWithTheirEffectivePings(t *testing.T) {
	alice := "<@" + aliceID + ">"
	bruno := "<@" + brunoID + ">"
	everyone := []string{"users", "roles", "everyone"}

	tests := []struct {
		name            string
		content         string
		allowedMentions string
		embeds          string
		wantSentNil     bool
		wantParse       []string
		wantUsers       []string
		wantRendered    []string
		wantPings       []string
	}{
		{
			name:        "absent allowed_mentions uses Discord's default and pings everyone and users",
			content:     alice + " @everyone @here",
			wantSentNil: true, wantParse: everyone,
			wantRendered: []string{aliceID}, wantPings: []string{aliceID, "everyone", "here"},
		},
		{
			name:            "null allowed_mentions counts as absent",
			content:         alice + " @everyone",
			allowedMentions: `null`,
			wantSentNil:     true, wantParse: everyone,
			wantRendered: []string{aliceID}, wantPings: []string{aliceID, "everyone"},
		},
		{
			name:            "empty object pings nobody",
			content:         alice + " @everyone @here",
			allowedMentions: `{}`,
			wantParse:       []string{},
			wantRendered:    []string{aliceID}, wantPings: []string{},
		},
		{
			name:            "empty parse pings nobody",
			content:         alice + " @everyone",
			allowedMentions: `{"parse":[]}`,
			wantParse:       []string{},
			wantRendered:    []string{aliceID}, wantPings: []string{},
		},
		{
			name:            "null members count as empty",
			content:         alice + " @everyone",
			allowedMentions: `{"parse":null,"users":null,"roles":null}`,
			wantParse:       []string{},
			wantRendered:    []string{aliceID}, wantPings: []string{},
		},
		{
			name:            "parse users pings users but not everyone",
			content:         alice + " " + bruno + " @everyone @here",
			allowedMentions: `{"parse":["users"]}`,
			wantParse:       []string{"users"},
			wantRendered:    []string{aliceID, brunoID}, wantPings: []string{aliceID, brunoID},
		},
		{
			name:            "explicit users ping only those users",
			content:         alice + " " + bruno,
			allowedMentions: `{"parse":[],"users":["` + brunoID + `"]}`,
			wantParse:       []string{}, wantUsers: []string{brunoID},
			wantRendered: []string{aliceID, brunoID}, wantPings: []string{brunoID},
		},
		{
			name:            "parse everyone pings @everyone and @here but not users",
			content:         alice + " @everyone @here",
			allowedMentions: `{"parse":["everyone"]}`,
			wantParse:       []string{"everyone"},
			wantRendered:    []string{aliceID}, wantPings: []string{"everyone", "here"},
		},
		{
			name:            "nickname form renders and pings",
			content:         "<@!" + aliceID + ">",
			allowedMentions: `{"parse":["users"]}`,
			wantParse:       []string{"users"},
			wantRendered:    []string{aliceID}, wantPings: []string{aliceID},
		},
		{
			name:         "a repeated mention is listed once",
			content:      alice + alice + "<@!" + aliceID + ">",
			wantSentNil:  true,
			wantParse:    everyone,
			wantRendered: []string{aliceID}, wantPings: []string{aliceID},
		},
		{
			name:            "mentions in embeds render but never ping",
			content:         "see embed",
			allowedMentions: `{"parse":["users","everyone"]}`,
			embeds:          `[{"title":"hi ` + alice + `","description":"@everyone","fields":[{"name":"n","value":"` + bruno + `"}]}]`,
			wantParse:       []string{"users", "everyone"},
			wantRendered:    []string{brunoID, aliceID}, wantPings: []string{},
		},
		{
			name:         "mentions inside a code block count as mentions",
			content:      "```\n" + alice + "\n```",
			wantSentNil:  true,
			wantParse:    everyone,
			wantRendered: []string{aliceID}, wantPings: []string{aliceID},
		},
		{
			name:            "a zero-width character breaks a mention",
			content:         "<@" + zeroWidthSpace + aliceID + "> @" + zeroWidthSpace + "everyone",
			allowedMentions: `{"parse":["users","everyone"]}`,
			wantParse:       []string{"users", "everyone"},
			wantRendered:    []string{}, wantPings: []string{},
		},
		{
			name:            "plain text pings nobody",
			content:         "hello",
			allowedMentions: `{"parse":["users","roles","everyone"]}`,
			wantParse:       everyone,
			wantRendered:    []string{}, wantPings: []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := newFake(t, fakediscord.Options{})
			guild := srv.AddGuild(fakediscord.Guild{ID: winterGuildID, Name: "Winter guild"})

			got := postChannelMessage(t, srv, guild.ChannelID, tc.content, tc.allowedMentions, tc.embeds)

			if got.status != http.StatusOK {
				t.Fatalf("status = %d, body %s", got.status, got.body)
			}
			rec := lastRecord(t, srv)
			if (rec.AllowedMentionsSent == nil) != tc.wantSentNil {
				t.Errorf("allowed_mentions_sent = %+v, want nil: %v", rec.AllowedMentionsSent, tc.wantSentNil)
			}
			if !slices.Equal(rec.AllowedMentionsEffective.Parse, tc.wantParse) {
				t.Errorf("effective parse = %v, want %v", rec.AllowedMentionsEffective.Parse, tc.wantParse)
			}
			wantUsers := tc.wantUsers
			if wantUsers == nil {
				wantUsers = []string{}
			}
			if !slices.Equal(rec.AllowedMentionsEffective.Users, wantUsers) {
				t.Errorf("effective users = %v, want %v", rec.AllowedMentionsEffective.Users, wantUsers)
			}
			if !slices.Equal(rec.MentionsRendered, tc.wantRendered) {
				t.Errorf("mentions_rendered = %v, want %v", rec.MentionsRendered, tc.wantRendered)
			}
			if !slices.Equal(rec.Pings, tc.wantPings) {
				t.Errorf("pings = %v, want %v", rec.Pings, tc.wantPings)
			}
		})
	}
}

func TestOutboundRecordSerializesToTheDocumentedShape(t *testing.T) {
	srv := newFake(t, fakediscord.Options{})
	alice := srv.AddUser(fakediscord.User{ID: aliceID, Name: "Alice"})
	guild := srv.AddGuild(fakediscord.Guild{ID: winterGuildID, Name: "Winter guild"}, alice)
	postChannelMessage(t, srv, guild.ChannelID, "hello", `{"parse":[],"replied_user":true}`, "")

	encoded, err := json.Marshal(srv.Outbound())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var records []map[string]any
	decodeJSON(t, encoded, &records)
	rec := records[0]
	want := map[string]any{
		"seq":            float64(1),
		"route":          "POST /channels/{id}/messages",
		"source":         "channel",
		"status":         float64(200),
		"deduplicated":   false,
		"interaction_id": nil,
		"channel_id":     guild.ChannelID,
		"guild_id":       guild.ID,
		"ephemeral":      false,
		"content":        "hello",
	}
	for key, value := range want {
		if rec[key] != value {
			t.Errorf("%s = %v, want %v", key, rec[key], value)
		}
	}
	for _, key := range []string{
		"at", "message_id", "visible_to", "embeds", "components",
		"allowed_mentions_sent", "allowed_mentions_effective", "mentions_rendered", "pings",
	} {
		if _, ok := rec[key]; !ok {
			t.Errorf("record lacks %q", key)
		}
	}
	sent, _ := rec["allowed_mentions_sent"].(map[string]any)
	if sent["replied_user"] != true {
		t.Errorf("allowed_mentions_sent = %v, want replied_user true", sent)
	}
	if strings.Contains(string(encoded), `null,"embeds"`) || rec["embeds"] == nil {
		t.Errorf("embeds must be an array, got %v", rec["embeds"])
	}
}

func FuzzEffectiveMentions(f *testing.F) {
	alice := "<@" + aliceID + ">"
	contents := []string{
		"",
		"hello",
		"@everyone",
		"@here",
		alice,
		"<@!" + aliceID + ">",
		alice + " @everyone @here " + "<@" + brunoID + ">",
		"a" + zeroWidthJoiner + alice + zeroWidthJoiner + "@everyone",
		rightToLeftOverride + alice + popDirectionalFormatting + " @here",
		strings.Repeat("@everyone ", 1000),
		strings.Repeat("a", 10_000),
	}
	allowed := [][]byte{
		nil,
		[]byte(`null`),
		[]byte(`{}`),
		[]byte(`{"parse":[]}`),
		[]byte(`{"parse":["users","roles","everyone"]}`),
		[]byte(`{"parse":["users"],"users":["` + aliceID + `"]}`),
		[]byte(`{"parse":null,"users":null,"roles":null}`),
		[]byte(`{"users":["` + brunoID + `"],"replied_user":true}`),
		[]byte(`[`),
		[]byte(`"parse"`),
	}
	for _, content := range contents {
		for _, raw := range allowed {
			f.Add(content, raw)
		}
	}

	f.Fuzz(func(t *testing.T, content string, allowedMentions []byte) {
		srv := newFake(t, fakediscord.Options{})
		guild := srv.AddGuild(fakediscord.Guild{ID: winterGuildID, Name: "Winter guild"})
		before := len(srv.Outbound())

		got := postChannelMessage(t, srv, guild.ChannelID, content, string(allowedMentions), "")

		switch got.status {
		case http.StatusBadRequest:
			if len(srv.Outbound()) != before {
				t.Fatalf("a rejected request left a record")
			}
			return
		case http.StatusOK:
		default:
			t.Fatalf("status = %d, want 200 or 400 (body %s)", got.status, got.body)
		}

		records := srv.Outbound()
		if len(records) != before+1 {
			t.Fatalf("records = %d, want %d", len(records), before+1)
		}
		rec := records[len(records)-1]
		for _, ping := range rec.Pings {
			switch ping {
			case "everyone":
				if !strings.Contains(rec.Content, "@everyone") {
					t.Fatalf("pinged everyone without @everyone in %q", rec.Content)
				}
			case "here":
				if !strings.Contains(rec.Content, "@here") {
					t.Fatalf("pinged here without @here in %q", rec.Content)
				}
			default:
				plain := strings.Contains(rec.Content, "<@"+ping+">")
				nick := strings.Contains(rec.Content, "<@!"+ping+">")
				if !plain && !nick {
					t.Fatalf("pinged %q who is not mentioned in %q", ping, rec.Content)
				}
			}
		}
		eff := rec.AllowedMentionsEffective
		nothingAllowed := len(eff.Parse) == 0 && len(eff.Users) == 0 && len(eff.Roles) == 0
		if nothingAllowed && len(rec.Pings) != 0 {
			t.Fatalf("pings %v with an empty effective object %+v", rec.Pings, eff)
		}
	})
}

// Invisible characters are built from code points so the source holds none.
const (
	zeroWidthSpace           = string(rune(0x200B))
	zeroWidthJoiner          = string(rune(0x200D))
	rightToLeftOverride      = string(rune(0x202E))
	popDirectionalFormatting = string(rune(0x202C))
)
