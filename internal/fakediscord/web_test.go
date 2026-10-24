package fakediscord_test

import (
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
)

const (
	htmlType = "text/html; charset=utf-8"
	cssType  = "text/css; charset=utf-8"
	jsType   = "text/javascript; charset=utf-8"
)

var webAssetTypes = map[string]string{
	"/web/app.css":   cssType,
	"/web/app.js":    jsType,
	"/web/api.js":    jsType,
	"/web/render.js": jsType,
	"/web/slash.js":  jsType,
	"/web/modal.js":  jsType,
}

func webAssetPaths() []string {
	paths := make([]string, 0, len(webAssetTypes))
	for path := range webAssetTypes {
		paths = append(paths, path)
	}
	slices.Sort(paths)

	return paths
}

func TestPageIsServedAtTheRoot(t *testing.T) {
	srv := newFake(t, fakediscord.Options{})

	got := call(t, srv, http.MethodGet, "/", "", "")

	if got.status != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", got.status)
	}
	if got.header.Get("Content-Type") != htmlType {
		t.Errorf("content type = %q, want %q", got.header.Get("Content-Type"), htmlType)
	}
	if !strings.Contains(string(got.body), `<span>User</span>`) {
		t.Errorf("the page does not label the user switcher: %.200s", got.body)
	}
}

func TestAssetsAreServedWithTheirContentType(t *testing.T) {
	srv := newFake(t, fakediscord.Options{})

	for _, path := range webAssetPaths() {
		t.Run(path, func(t *testing.T) {
			got := call(t, srv, http.MethodGet, path, "", "")

			if got.status != http.StatusOK {
				t.Fatalf("GET %s = %d, want 200", path, got.status)
			}
			if got.header.Get("Content-Type") != webAssetTypes[path] {
				t.Errorf("content type = %q, want %q", got.header.Get("Content-Type"), webAssetTypes[path])
			}
			if len(got.body) == 0 {
				t.Errorf("GET %s answered an empty body", path)
			}
		})
	}
}

func TestOnlyTheListedAssetsAreServed(t *testing.T) {
	srv := newFake(t, fakediscord.Options{})

	tests := []struct {
		name string
		path string
	}{
		{"unknown script", "/web/x.js"},
		{"the page itself under the asset prefix", "/web/index.html"},
		{"directory", "/web/"},
		{"nested path", "/web/a/app.js"},
		{"Go source beside the page", "/fakediscord.go"},
		{"encoded traversal", "/web/%2e%2e%2ffakediscord.go"},
		{"asset name in other case", "/web/APP.JS"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := call(t, srv, http.MethodGet, tc.path, "", "")

			if got.status != http.StatusNotFound {
				t.Errorf("GET %s = %d, want 404", tc.path, got.status)
			}
		})
	}
}

func TestTraversalNeverServesSource(t *testing.T) {
	srv := newFake(t, fakediscord.Options{})

	got := call(t, srv, http.MethodGet, "/web/../fakediscord.go", "", "")

	if got.status == http.StatusOK {
		t.Errorf("GET /web/../fakediscord.go = 200, want a redirect or an error")
	}
	if strings.Contains(string(got.body), "package fakediscord") {
		t.Errorf("Go source was served: %.200s", got.body)
	}
}

func TestPageAndAssetsCarryTheSecurityHeaders(t *testing.T) {
	srv := newFake(t, fakediscord.Options{})

	paths := append([]string{"/"}, webAssetPaths()...)
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			got := call(t, srv, http.MethodGet, path, "", "")

			if got.header.Get("X-Content-Type-Options") != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q, want nosniff", got.header.Get("X-Content-Type-Options"))
			}
			policy := got.header.Get("Content-Security-Policy")
			for _, want := range []string{"default-src 'none'", "script-src 'self'", "style-src 'self'", "connect-src 'self'", "frame-ancestors 'none'"} {
				if !strings.Contains(policy, want) {
					t.Errorf("Content-Security-Policy = %q, want it to contain %q", policy, want)
				}
			}
			if strings.Contains(policy, "unsafe") {
				t.Errorf("Content-Security-Policy = %q, must not allow anything unsafe", policy)
			}
		})
	}
}

func TestExistingRoutesStillAnswerNextToThePage(t *testing.T) {
	srv := newFake(t, fakediscord.Options{})

	gateway := call(t, srv, http.MethodGet, "/api/v10/gateway", "", "")
	health := call(t, srv, http.MethodGet, "/_control/healthz", "", "")
	unknown := call(t, srv, http.MethodGet, "/nope", "", "")
	post := call(t, srv, http.MethodPost, "/", "", "")

	if gateway.status != http.StatusOK || gateway.json(t)["url"] == nil {
		t.Errorf("GET /api/v10/gateway = %d %s, want 200 with a url", gateway.status, gateway.body)
	}
	if health.status != http.StatusOK || health.json(t)["server"] != "up" {
		t.Errorf("GET /_control/healthz = %d %s, want 200 up", health.status, health.body)
	}
	if unknown.status != http.StatusNotFound || string(unknown.body) != `{"code":0,"message":"404: Not Found"}` {
		t.Errorf("GET /nope = %d %s, want the JSON 404", unknown.status, unknown.body)
	}
	if post.status != http.StatusNotFound {
		t.Errorf("POST / = %d, want 404: the page answers GET only", post.status)
	}
}

var (
	pageReference = regexp.MustCompile(`(?i)\b(?:src|href|action)\s*=\s*"([^"]*)"`)
	pageInline    = regexp.MustCompile(`(?is)<script(?:\s[^>]*)?>\s*[^<\s]|<style\b|\sstyle\s*=|\son[a-z]+\s*=`)
)

func TestPageReferencesOnlyItsOwnAssets(t *testing.T) {
	srv := newFake(t, fakediscord.Options{})
	page := call(t, srv, http.MethodGet, "/", "", "")

	references := pageReference.FindAllStringSubmatch(string(page.body), -1)
	if len(references) == 0 {
		t.Fatal("the page references no script or style")
	}
	for _, match := range references {
		if _, listed := webAssetTypes[match[1]]; !listed {
			t.Errorf("the page references %q, which is not one of its own assets", match[1])
		}
	}
	if pageInline.MatchString(string(page.body)) {
		t.Errorf("the page holds an inline script, style or event handler")
	}
}

func TestAssetsUseNoMarkupBuildingAndNoOutsideUrls(t *testing.T) {
	srv := newFake(t, fakediscord.Options{})
	forbidden := []string{
		"innerHTML", "outerHTML", "insertAdjacentHTML", "createContextualFragment", "DOMParser", "srcdoc",
		"document.write", "eval(", "new Function",
		"/_control/token", "http://", "https://", "localStorage", "sessionStorage",
	}

	for _, path := range webAssetPaths() {
		got := call(t, srv, http.MethodGet, path, "", "")
		for _, word := range forbidden {
			if strings.Contains(string(got.body), word) {
				t.Errorf("%s contains %q", path, word)
			}
		}
	}
}

// controlMessages reads a guild channel as the user sees it through the control API.
func controlMessages(t *testing.T, srv *fakediscord.Server, channelID, as string) []map[string]any {
	t.Helper()

	got := control(t, srv, http.MethodGet, "/channels/"+channelID+"/messages?as="+as, "")
	if got.status != http.StatusOK {
		t.Fatalf("messages = %d %s", got.status, got.body)
	}

	return got.array(t)
}

func pingsOf(t *testing.T, message map[string]any) []string {
	t.Helper()

	raw, err := json.Marshal(message["pings"])
	if err != nil {
		t.Fatalf("encode pings: %v", err)
	}
	var pings []string
	decodeJSON(t, raw, &pings)

	return pings
}

func TestControlMessagesCarryWhoTheyPing(t *testing.T) {
	alice := "<@" + aliceID + ">"
	bruno := "<@" + brunoID + ">"
	embeds := `[{"description":"` + bruno + `"}]`

	tests := []struct {
		name            string
		content         string
		allowedMentions string
		embeds          string
		want            []string
	}{
		{"a mention in the content pings, one in an embed does not", alice, "", embeds, []string{aliceID}},
		{"a mention only in an embed pings nobody", "hello", "", embeds, []string{}},
		{"an empty allowed_mentions pings nobody", alice, `{}`, "", []string{}},
		{"allowed users limit the pings", alice + " " + bruno, `{"users":["` + brunoID + `"]}`, "", []string{brunoID}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := newVisibilityWorld(t)
			postChannelMessage(t, w.srv, w.guild.ChannelID, tc.content, tc.allowedMentions, tc.embeds)

			messages := controlMessages(t, w.srv, w.guild.ChannelID, aliceID)

			if len(messages) != 1 {
				t.Fatalf("messages = %v, want one", messages)
			}
			if got := pingsOf(t, messages[0]); !slices.Equal(got, tc.want) {
				t.Errorf("pings = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestControlMessagePingsFollowTheLatestEdit(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{})
	sent, _ := w.answerSlash(t, w.alice, w.inGuild("/ping", nil), `{"type":4,"data":{"content":"hi <@`+brunoID+`>"}}`)
	before := controlMessages(t, w.srv, w.guild.ChannelID, aliceID)

	edited := call(t, w.srv, http.MethodPatch, sent.webhookPath(w.srv, "/messages/@original"), "",
		`{"content":"no mention now","allowed_mentions":{"parse":[]}}`)
	after := controlMessages(t, w.srv, w.guild.ChannelID, aliceID)

	if edited.status != http.StatusOK {
		t.Fatalf("edit = %d %s", edited.status, edited.body)
	}
	if got := pingsOf(t, before[0]); !slices.Equal(got, []string{brunoID}) {
		t.Errorf("pings before the edit = %v, want [%s]", got, brunoID)
	}
	if got := pingsOf(t, after[0]); len(got) != 0 {
		t.Errorf("pings after the edit = %v, want none", got)
	}
}

func TestControlMessagePingsSurviveARetriedSend(t *testing.T) {
	w := newVisibilityWorld(t)
	path := "/api/v10/channels/" + w.guild.ChannelID + "/messages"
	body := `{"content":"hi <@` + aliceID + `>","nonce":"n-1","enforce_nonce":true}`
	call(t, w.srv, http.MethodPost, path, botAuth(w.srv), body)
	retryBody := `{"content":"hi <@` + aliceID + `>","nonce":"n-1","enforce_nonce":true,"allowed_mentions":{"parse":[]}}`
	retry := call(t, w.srv, http.MethodPost, path, botAuth(w.srv), retryBody)

	messages := controlMessages(t, w.srv, w.guild.ChannelID, aliceID)

	if retry.status != http.StatusOK {
		t.Fatalf("retry = %d %s", retry.status, retry.body)
	}
	if len(messages) != 1 {
		t.Fatalf("messages = %v, want one", messages)
	}
	if got := pingsOf(t, messages[0]); !slices.Equal(got, []string{aliceID}) {
		t.Errorf("pings after a retried send = %v, want [%s]", got, aliceID)
	}
}

func TestControlMessagesNameTheInteractionThatMadeThem(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{})
	_, result := w.answerSlash(t, w.alice, w.inGuild("/ping", nil), `{"type":4,"data":{"content":"pong"}}`)
	postChannelMessage(t, w.srv, w.guild.ChannelID, "direct", "", "")

	messages := controlMessages(t, w.srv, w.guild.ChannelID, aliceID)

	if len(messages) != 2 {
		t.Fatalf("messages = %v, want two", messages)
	}
	if messages[0]["interaction_id"] != result.InteractionID || result.InteractionID == "" {
		t.Errorf("interaction_id = %v, want %q", messages[0]["interaction_id"], result.InteractionID)
	}
	if messages[1]["interaction_id"] != "" {
		t.Errorf("a message sent straight to the channel has interaction_id %v, want empty", messages[1]["interaction_id"])
	}
}

func TestEventStreamMessagesCarryPingsAndTheInteraction(t *testing.T) {
	w := newInteractionWorld(t, fakediscord.Options{})
	events := subscribe(t, w.srv, aliceID)

	postChannelMessage(t, w.srv, w.guild.ChannelID, "hello <@"+aliceID+">", "", "")

	created := nextEvent(t, events, "message_created")
	var payload struct {
		Message struct {
			Pings         []string `json:"pings"`
			InteractionID string   `json:"interaction_id"`
		} `json:"message"`
	}
	decodeJSON(t, []byte(created.data), &payload)
	if !slices.Equal(payload.Message.Pings, []string{aliceID}) || payload.Message.InteractionID != "" {
		t.Errorf("message_created = %s, want pings [%s] and no interaction", created.data, aliceID)
	}
}

func TestRESTMessagesDoNotGainControlFields(t *testing.T) {
	w := newVisibilityWorld(t)

	got := postChannelMessage(t, w.srv, w.guild.ChannelID, "hello", "", "")

	body := got.json(t)
	if _, hasPings := body["pings"]; hasPings {
		t.Errorf("a REST message carries pings: %s", got.body)
	}
	if _, hasInteraction := body["interaction_id"]; hasInteraction {
		t.Errorf("a REST message carries interaction_id: %s", got.body)
	}
}
