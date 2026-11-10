package fakediscord_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
)

const (
	generatePath   = "/v1/publishers/google/models/gemini-3.5-flash-lite:generateContent"
	classifyBody   = `{"systemInstruction":{"parts":[{"text":"classify the message"}]},"contents":[{"parts":[{"text":"  ahora: now\nmensaje: hola  "}]}]}`
	banterBody     = `{"systemInstruction":{"parts":[{"text":"write a joke"}]},"contents":[{"parts":[{"text":"hola"}]}]}`
	mediaOnlyBody  = `{"contents":[{"parts":[{"inline_data":{"mime_type":"image/png","data":"c3ludGhldGlj"}},{"inline_data":{"mime_type":"audio/ogg","data":"c3ludGhldGlj"}}]}]}`
	scriptedAnswer = `{"arrival":true,"day":"","clock":"","minutes":0}`
)

// generatedText returns the text of the first candidate of a generateContent answer.
func generatedText(t *testing.T, got reply) string {
	t.Helper()

	if got.status != http.StatusOK {
		t.Fatalf("status = %d, body %s", got.status, got.body)
	}

	var parsed struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	err := json.Unmarshal(got.body, &parsed)
	if err != nil || len(parsed.Candidates) != 1 || len(parsed.Candidates[0].Content.Parts) != 1 {
		t.Fatalf("body = %s, want one candidate with one part (%v)", got.body, err)
	}

	return parsed.Candidates[0].Content.Parts[0].Text
}

func generate(t *testing.T, srv *fakediscord.Server, body string) reply {
	t.Helper()

	return callHeader(t, srv, http.MethodPost, generatePath, "x-goog-api-key", srv.Token(), body)
}

func TestGenerateContentAnswersWithTheFirstMatchingScriptedRule(t *testing.T) {
	t.Parallel()
	srv := startFake(t, fakediscord.Options{})

	srv.SetRephrased("hola", "Rephrased hola")
	mustSetGenerated(t, srv, "classify", "mensaje: hola", scriptedAnswer)
	mustSetGenerated(t, srv, "classify", "", `{"arrival":false}`)

	if got := generatedText(t, generate(t, srv, classifyBody)); got != scriptedAnswer {
		t.Errorf("first rule answer = %q, want %q", got, scriptedAnswer)
	}

	other := strings.Replace(classifyBody, "mensaje: hola", "mensaje: chao", 1)
	if got := generatedText(t, generate(t, srv, other)); got != `{"arrival":false}` {
		t.Errorf("second rule answer = %q, want the catch-all of the classifier", got)
	}

	if got := generatedText(t, generate(t, srv, banterBody)); got != "Rephrased hola" {
		t.Errorf("unscripted answer = %q, want the rephrase map to still apply", got)
	}

	unmapped := strings.Replace(banterBody, `"hola"`, `"  Do you like tea?  "`, 1)
	if got := generatedText(t, generate(t, srv, unmapped)); got != "Do you like tea?" {
		t.Errorf("unscripted unmapped answer = %q, want the trimmed text echoed", got)
	}
}

func TestGenerateContentAcceptsMediaWithoutText(t *testing.T) {
	t.Parallel()
	srv := startFake(t, fakediscord.Options{})

	if got := generatedText(t, generate(t, srv, mediaOnlyBody)); got != `{"arrival":false}` {
		t.Errorf("unscripted media-only answer = %q, want a no-arrival answer", got)
	}

	mustSetGenerated(t, srv, "", "", scriptedAnswer)
	if got := generatedText(t, generate(t, srv, mediaOnlyBody)); got != scriptedAnswer {
		t.Errorf("scripted media-only answer = %q, want %q", got, scriptedAnswer)
	}

	for _, empty := range []string{`{}`, `{"contents":[{"parts":[{"text":"   "}]}]}`, `{"contents":[]}`} {
		if got := generate(t, srv, empty); got.status != http.StatusBadRequest {
			t.Errorf("body %s answered %d, want 400 for a request with neither text nor media", empty, got.status)
		}
	}
}

func TestGenerateCallsRecordsSystemUserAndMediaTypes(t *testing.T) {
	t.Parallel()
	srv := startFake(t, fakediscord.Options{})

	generate(t, srv, classifyBody)
	generate(t, srv, mediaOnlyBody)
	generate(t, srv, `{}`)
	callHeader(t, srv, http.MethodPost, generatePath, "x-goog-api-key", "wrong-key", classifyBody)

	calls := srv.GenerateCalls()
	if len(calls) != 2 {
		t.Fatalf("GenerateCalls = %+v, want the two answered requests", calls)
	}
	if calls[0].System != "classify the message" || calls[0].User != "ahora: now\nmensaje: hola" || len(calls[0].MIMETypes) != 0 {
		t.Errorf("calls[0] = %+v, want the system text, the trimmed user text and no media", calls[0])
	}
	if calls[1].User != "" || !slices.Equal(calls[1].MIMETypes, []string{"image/png", "audio/ogg"}) {
		t.Errorf("calls[1] = %+v, want the two media types in order", calls[1])
	}

	listed := control(t, srv, http.MethodGet, "/generate-calls", "")
	if listed.status != http.StatusOK || len(listed.array(t)) != 2 {
		t.Fatalf("GET /generate-calls = %d %s, want both calls", listed.status, listed.body)
	}
	if first := listed.array(t)[0]; first["system"] != "classify the message" || first["user"] != "ahora: now\nmensaje: hola" {
		t.Errorf("GET /generate-calls first = %v, want the recorded texts", first)
	}
}

func TestSetGeneratedRejectsAnEmptyResponse(t *testing.T) {
	t.Parallel()
	srv := startFake(t, fakediscord.Options{})

	for _, response := range []string{"", "  \n "} {
		err := srv.SetGenerated("", "", response)
		if !errors.Is(err, fakediscord.ErrInvalidGenerated) {
			t.Errorf("SetGenerated(%q) = %v, want ErrInvalidGenerated", response, err)
		}
	}

	rejected := control(t, srv, http.MethodPost, "/generated", `{"user_contains":"hola"}`)
	if rejected.status != http.StatusBadRequest {
		t.Errorf("POST /generated without a response = %d, want 400", rejected.status)
	}
}

func TestControlGeneratedScriptsAnAnswerAndResetClearsIt(t *testing.T) {
	t.Parallel()
	srv := startFake(t, fakediscord.Options{})

	body := `{"system_contains":"classify","user_contains":"mensaje: hola","response":"{\"arrival\":true,\"day\":\"\",\"clock\":\"\",\"minutes\":0}"}`
	scripted := control(t, srv, http.MethodPost, "/generated", body)
	if scripted.status != http.StatusNoContent {
		t.Fatalf("POST /generated = %d %s, want 204", scripted.status, scripted.body)
	}
	if got := generatedText(t, generate(t, srv, classifyBody)); got != scriptedAnswer {
		t.Fatalf("answer = %q, want the scripted one", got)
	}

	if reset := control(t, srv, http.MethodPost, "/reset", ""); reset.status != http.StatusNoContent {
		t.Fatalf("reset = %d %s", reset.status, reset.body)
	}
	if calls := srv.GenerateCalls(); len(calls) != 0 {
		t.Errorf("GenerateCalls after reset = %+v, want none", calls)
	}
	if got := generatedText(t, generate(t, srv, classifyBody)); got != "ahora: now\nmensaje: hola" {
		t.Errorf("answer after reset = %q, want the user text echoed", got)
	}
}

func mustSetGenerated(t *testing.T, srv *fakediscord.Server, systemContains, userContains, response string) {
	t.Helper()

	err := srv.SetGenerated(systemContains, userContains, response)
	if err != nil {
		t.Fatalf("SetGenerated: %v", err)
	}
}
