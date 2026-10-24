package fakediscord_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
)

func TestSecretsStayOutOfLogsRecordsAndControlAnswers(t *testing.T) {
	var logs syncBuffer
	w := newInteractionWorld(t, fakediscord.Options{Logger: capturingLogger(&logs)})

	first, _ := w.answerSlash(t, w.alice, w.inGuild("/ping", nil), `{"type":4,"data":{"content":"pong"}}`)
	second, _ := w.answerSlash(t, w.bruno, w.inGuild("/ge/join", map[string]any{"exchange": "later"}), `{"type":5}`)
	followup := call(t, w.srv, http.MethodPost, second.webhookPath(w.srv, ""), "", `{"content":"done"}`)
	if followup.status != http.StatusOK {
		t.Fatalf("followup = %d %s, want 200", followup.status, followup.body)
	}
	mustFailNext(t, w.srv, "POST /channels/{id}/messages", 500, 0)
	postChannelMessage(t, w.srv, w.guild.ChannelID, "x", "", "")
	control(t, w.srv, http.MethodPost, "/users", `{"name":"Dana"}`)

	secrets := map[string]string{"bot token": w.srv.Token()}
	for index, segment := range strings.Split(w.srv.Token(), ".")[1:] {
		secrets[fmt.Sprintf("bot token segment %d", index+2)] = segment
	}
	secrets["interaction token 1"] = first.Token
	secrets["interaction token 2"] = second.Token

	outbound, marshalErr := json.Marshal(w.srv.Outbound())
	if marshalErr != nil {
		t.Fatalf("marshal outbound: %v", marshalErr)
	}
	surfaces := map[string]string{"the logs": logs.String(), "the outbound record": string(outbound)}
	for _, path := range []string{
		"/state", "/commands", "/outbound", "/healthz", "/channels/" + w.guild.ChannelID + "/messages?as=" + aliceID,
		"/dms/" + aliceID, "/modals/" + aliceID,
	} {
		surfaces["GET /_control"+path] = string(control(t, w.srv, http.MethodGet, path, "").body)
	}
	surfaces["the interaction answer"] = string(control(t, w.srv, http.MethodPost, "/interactions", `{"as":"`+aliceID+`","kind":"slash","path":"/nope"}`).body)

	for surface, text := range surfaces {
		for name, secret := range secrets {
			if strings.Contains(text, secret) {
				t.Errorf("%s contains the %s", surface, name)
			}
		}
	}
	if !strings.Contains(logs.String(), "route=") || strings.Contains(logs.String(), "/webhooks/"+w.srv.ApplicationID()) {
		t.Errorf("the logs should name route patterns only:\n%.600s", logs.String())
	}
}
