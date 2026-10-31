//go:build !race

// These tests open a real disgo Gateway client. disgo v0.19 touches the fields of its gateway
// from several goroutines without synchronization, so the race detector reports races inside
// the library; the adapter's own Gateway tests carry the same tag.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
)

const (
	// userID is a fixed fake snowflake.
	userID = "100000000000000001"

	readyDeadline    = 30 * time.Second
	shutdownDeadline = 40 * time.Second
)

// startFake serves a fake Discord on a loopback port until the test ends. The fake is not tied
// to the test context, which ends before cleanups run and would stop it under a client that
// has not closed yet.
func startFake(t *testing.T) *fakediscord.Server {
	t.Helper()

	fake, err := fakediscord.New(fakediscord.Options{})
	if err != nil {
		t.Fatalf("fakediscord.New: %v", err)
	}

	err = fake.Start(context.Background())
	if err != nil {
		t.Fatalf("fake Start: %v", err)
	}

	t.Cleanup(func() {
		closeErr := fake.Close()
		if closeErr != nil {
			t.Errorf("close fake: %v", closeErr)
		}
	})

	return fake
}

// configureDiscord stores the fake's token and REST base URL through the CLI.
func configureDiscord(t *testing.T, env environment, fake *fakediscord.Server) {
	t.Helper()

	storeSetting(t, env, "discord.token", fake.Token())
	storeSetting(t, env, "discord.api_base_url", fake.RESTBase())
	storeSetting(t, env, "llm.api_key", fake.Token())
	storeSetting(t, env, "llm.base_url", fake.LLMBase())
	storeSetting(t, env, "uc.api_base_url", fake.UCBase())
}

// running is one invocation of `run` in the background.
type running struct {
	// finished is closed after final is set.
	finished chan struct{}
	final    outcome
	cancel   context.CancelFunc
}

// startRun invokes `run` in a goroutine. Cancelling the returned running stands for the signal.
func startRun(t *testing.T, env environment) *running {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	r := &running{finished: make(chan struct{}), cancel: cancel}

	go func() {
		defer close(r.finished)

		var stdout, stderr bytes.Buffer

		code := run(ctx, []string{"run"}, strings.NewReader(""), &stdout, &stderr, env.lookup)
		r.final = outcome{code: code, stdout: stdout.String(), stderr: stderr.String()}
	}()

	t.Cleanup(func() {
		cancel()
		<-r.finished
	})

	return r
}

// result waits for run to return.
func (r *running) result(t *testing.T) outcome {
	t.Helper()

	select {
	case <-r.finished:
		return r.final
	case <-time.After(shutdownDeadline):
		t.Fatalf("run did not return within %s", shutdownDeadline)

		return outcome{}
	}
}

func globalCommandNames(fake *fakediscord.Server) []string {
	var names []string

	for _, command := range fake.Commands() {
		if command.GuildID == "" {
			names = append(names, command.Name)
		}
	}

	return names
}

func TestRunServesDiscordAndStopsCleanlyOnCancellation(t *testing.T) {
	noLeaks(t)

	fake := startFake(t)
	database := newClone(t)
	env := envFor(database, "local")
	configureDiscord(t, env, fake)

	r := startRun(t, env)

	readyCtx, cancelReady := context.WithTimeout(t.Context(), readyDeadline)
	defer cancelReady()

	err := fake.WaitReady(readyCtx)
	if err != nil {
		t.Fatalf("the bot never became ready: %v", err)
	}

	// A reply proves the Gateway is fully open and routes interactions to the module.
	user := fake.AddUser(fakediscord.User{ID: userID, Name: "tester"})

	reply, err := fake.Slash(readyCtx, user, fakediscord.Slash{Path: "/ge/help"})
	if err != nil || reply.Message == nil {
		t.Fatalf("/ge help: got %+v and error %v, want a reply", reply, err)
	}

	r.cancel()
	got := r.result(t)

	if got.code != 0 || got.stdout != "" {
		t.Errorf("got code %d and stdout %q, want exit 0 and empty stdout (stderr=%q)", got.code, got.stdout, got.stderr)
	}

	assertNoneOf(t, got, fake.Token(), masterKey, database.URL().Reveal())
}

func TestRunRegistersTheGlobalCommands(t *testing.T) {
	noLeaks(t)

	fake := startFake(t)
	database := newClone(t)
	env := envFor(database, "local")
	configureDiscord(t, env, fake)

	r := startRun(t, env)

	readyCtx, cancelReady := context.WithTimeout(t.Context(), readyDeadline)
	defer cancelReady()

	err := fake.WaitReady(readyCtx)
	if err != nil {
		t.Fatalf("the bot never became ready: %v", err)
	}

	// A reply proves the Gateway is fully open before the stop signal arrives.
	user := fake.AddUser(fakediscord.User{ID: userID, Name: "tester"})

	_, err = fake.Slash(readyCtx, user, fakediscord.Slash{Path: "/ge/help"})
	if err != nil {
		t.Fatalf("/ge help: %v", err)
	}

	names := globalCommandNames(fake)
	for _, want := range []string{"chatisimo", "ge", "uc", "uc-config"} {
		if !slices.Contains(names, want) {
			t.Errorf("registered commands = %v, want %q among them", names, want)
		}
	}

	r.cancel()
	r.result(t)
}

func TestRunExitsOneWhenDiscordRejectsTheToken(t *testing.T) {
	noLeaks(t)

	fake := startFake(t)
	database := newClone(t)
	env := envFor(database, "local")
	configureDiscord(t, env, fake)

	// 4004 is Discord's close code for a failed authentication.
	err := fake.FailNext("GATEWAY identify", 0, 4004)
	if err != nil {
		t.Fatalf("FailNext: %v", err)
	}

	r := startRun(t, env)
	got := r.result(t)

	if got.code != 1 || got.stdout != "" {
		t.Errorf("got code %d and stdout %q, want exit 1 and empty stdout", got.code, got.stdout)
	}

	if !strings.Contains(got.stderr, "chatisimo: ") || !strings.Contains(got.stderr, "open discord gateway") {
		t.Errorf("stderr = %q, want a chatisimo: line about opening the Gateway", got.stderr)
	}

	assertNoneOf(t, got, fake.Token(), masterKey, database.URL().Reveal())
}

func TestRunExitsZeroWhenStoppedWhileDiscordIsOpening(t *testing.T) {
	noLeaks(t)

	database := newClone(t)
	env := envFor(database, "local")

	var r *running

	// Closed once r is assigned, so the stub handler never reads r before the write.
	started := make(chan struct{})

	// The first REST request of the Gateway open stands for the signal arriving mid-Open: it
	// cancels the run and holds the request until the client gives up.
	stub := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		<-started
		r.cancel()
		<-req.Context().Done()
	}))
	defer stub.Close()

	// The token only has to be well formed: the stub never validates it.
	tokenSource, err := fakediscord.New(fakediscord.Options{})
	if err != nil {
		t.Fatalf("fakediscord.New: %v", err)
	}

	ucStub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"events":[]}`)
	}))
	defer ucStub.Close()

	storeSetting(t, env, "discord.token", tokenSource.Token())
	storeSetting(t, env, "discord.api_base_url", stub.URL)
	storeSetting(t, env, "llm.api_key", tokenSource.Token())
	storeSetting(t, env, "llm.base_url", stub.URL)
	storeSetting(t, env, "uc.api_base_url", ucStub.URL)

	r = startRun(t, env)
	close(started)
	got := r.result(t)

	if got.code != 0 || got.stdout != "" {
		t.Errorf("got code %d and stdout %q, want exit 0 and empty stdout (stderr=%q)", got.code, got.stdout, got.stderr)
	}
}

func TestRunSendsConfiguredMultilineLLMPromptOnAsk(t *testing.T) {
	noLeaks(t)

	const (
		organizerID       = "100000000000000011"
		giverID           = "100000000000000012"
		otherID           = "100000000000000013"
		customPrompt      = "Rule 1: Keep 2nd person singular.\nRule 2: Use neutral Spanish tuteo.\nRule 3: Drop vocative slang."
		rawQuestion       = "hola kingcito que te gusta?"
		rephrasedQuestion = "Que te gusta recibir de regalo?"
	)

	var (
		mu              sync.Mutex
		gotSystemPrompt string
	)

	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			http.Error(w, "read error", http.StatusBadRequest)
			return
		}

		var payload struct {
			SystemInstruction struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"systemInstruction"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(w, "json error", http.StatusBadRequest)
			return
		}

		if len(payload.SystemInstruction.Parts) > 0 {
			mu.Lock()
			gotSystemPrompt = payload.SystemInstruction.Parts[0].Text
			mu.Unlock()
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"`+rephrasedQuestion+`"}]}}]}`)
	}))
	defer llmServer.Close()

	fake := startFake(t)
	database := newClone(t)
	env := envFor(database, "local")
	configureDiscord(t, env, fake)
	storeSetting(t, env, "llm.base_url", llmServer.URL+"/v1/publishers/google/models")

	setPrompt := invoke(
		t,
		env,
		strings.NewReader(strings.ReplaceAll(customPrompt, "\n", "\r\n")+"\r\n"),
		"config", "set", "llm.prompt",
	)
	if setPrompt.code != 0 {
		t.Fatalf("config set llm.prompt: %+v", setPrompt)
	}

	r := startRun(t, env)

	readyCtx, cancelReady := context.WithTimeout(t.Context(), readyDeadline)
	defer cancelReady()

	if err := fake.WaitReady(readyCtx); err != nil {
		t.Fatalf("the bot never became ready: %v", err)
	}

	organizer := fake.AddUser(fakediscord.User{ID: organizerID, Name: "Alice"})
	giver := fake.AddUser(fakediscord.User{ID: giverID, Name: "Bruno"})
	other := fake.AddUser(fakediscord.User{ID: otherID, Name: "Carla"})
	guild := fake.AddGuild(fakediscord.Guild{Name: "Test Guild"}, organizer, giver, other)

	createRes, err := fake.Slash(readyCtx, organizer, fakediscord.Slash{
		Guild:   &guild,
		Path:    "/ge/create",
		Options: map[string]any{"name": "Winter exchange"},
	})
	if err != nil {
		t.Fatalf("/ge/create: %v", err)
	}
	_ = awaitInteractionMessage(t, fake, organizer, createRes)

	for _, member := range []fakediscord.User{organizer, giver, other} {
		joinRes, joinErr := fake.Slash(readyCtx, member, fakediscord.Slash{
			Guild: &guild,
			Path:  "/ge/join",
		})
		if joinErr != nil {
			t.Fatalf("/ge/join for %s: %v", member.Name, joinErr)
		}
		_ = awaitInteractionMessage(t, fake, member, joinRes)
	}

	drawRes, err := fake.Slash(readyCtx, organizer, fakediscord.Slash{
		Guild: &guild,
		Path:  "/ge/draw",
	})
	if err != nil {
		t.Fatalf("/ge/draw: %v", err)
	}
	drawPromptMsg := awaitInteractionMessage(t, fake, organizer, drawRes)

	confirmID := findConfirmButtonID(t, drawPromptMsg)
	confirmRes, err := fake.Click(readyCtx, organizer, drawPromptMsg, confirmID)
	if err != nil {
		t.Fatalf("confirm draw: %v", err)
	}
	_ = awaitInteractionMessage(t, fake, organizer, confirmRes)

	askRes, err := fake.Slash(readyCtx, giver, fakediscord.Slash{
		Guild:   &guild,
		Path:    "/ge/ask",
		Options: map[string]any{"question": rawQuestion},
	})
	if err != nil {
		t.Fatalf("/ge/ask: %v", err)
	}
	askMsg := awaitInteractionMessage(t, fake, giver, askRes)
	if !strings.Contains(askMsg.Content, "sent anonymously") {
		t.Errorf("/ge/ask reply = %q, want anonymous confirmation", askMsg.Content)
	}

	mu.Lock()
	deliveredPrompt := gotSystemPrompt
	mu.Unlock()

	if deliveredPrompt != customPrompt {
		t.Errorf("delivered systemInstruction = %q, want %q", deliveredPrompt, customPrompt)
	}

	r.cancel()
	got := r.result(t)
	if got.code != 0 || got.stdout != "" {
		t.Errorf("got code %d and stdout %q, want exit 0 and empty stdout (stderr=%q)", got.code, got.stdout, got.stderr)
	}

	assertNoneOf(t, got, fake.Token(), masterKey, database.URL().Reveal(), rawQuestion, rephrasedQuestion)
}

func awaitInteractionMessage(
	t *testing.T, fake *fakediscord.Server, as fakediscord.User, result fakediscord.Result,
) fakediscord.Message {
	t.Helper()

	if result.Message != nil {
		return *result.Message
	}

	ctx, cancel := context.WithTimeout(t.Context(), readyDeadline)
	defer cancel()

	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()

	for {
		for _, candidate := range fake.Outbound() {
			isMatch := candidate.InteractionID != nil &&
				*candidate.InteractionID == result.InteractionID &&
				(candidate.Source == "edit" || candidate.Source == "followup")
			if !isMatch {
				continue
			}

			for _, message := range fake.Visible(as, candidate.ChannelID) {
				if message.ID == candidate.MessageID {
					return message
				}
			}
		}

		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for deferred reply to interaction %s", result.InteractionID)
			return fakediscord.Message{}
		case <-ticker.C:
		}
	}
}

func findConfirmButtonID(t *testing.T, msg fakediscord.Message) string {
	t.Helper()

	var row struct {
		Components []struct {
			CustomID string `json:"custom_id"`
		} `json:"components"`
	}

	for _, raw := range msg.Components {
		if err := json.Unmarshal(raw, &row); err != nil {
			t.Fatalf("decode component: %v", err)
		}

		for _, comp := range row.Components {
			if strings.HasPrefix(comp.CustomID, "/gx/draw-confirm/") {
				return comp.CustomID
			}
		}
	}

	t.Fatalf("no /gx/draw-confirm/ button in message components")
	return ""
}

func TestRunServesUCCalendarSlashCommand(t *testing.T) {
	noLeaks(t)

	const (
		callerID = "100000000000000081"
		friendID = "100000000000000082"
	)

	fake := startFake(t)
	fake.SetUCFixtures([]fakediscord.UCFixture{
		{
			ID:        "9101",
			Date:      "2026-10-31T21:00Z",
			Opponent:  "Deportes Iquique",
			Home:      true,
			Venue:     "Claro Arena",
			TimeValid: true,
		},
	})

	database := newClone(t)
	env := envFor(database, "local")
	configureDiscord(t, env, fake)
	storeSetting(t, env, "uc.slang_cache_ttl", "0s")

	r := startRun(t, env)

	readyCtx, cancelReady := context.WithTimeout(t.Context(), readyDeadline)
	defer cancelReady()

	if err := fake.WaitReady(readyCtx); err != nil {
		t.Fatalf("the bot never became ready: %v", err)
	}

	caller := fake.AddUser(fakediscord.User{ID: callerID, Name: "Caller", Locale: "es-419"})
	friend := fake.AddUser(fakediscord.User{ID: friendID, Name: "Cruzado"})
	guild := fake.AddGuild(fakediscord.Guild{Name: "Test Guild"}, caller, friend)

	unconfiguredRes, err := fake.Slash(readyCtx, caller, fakediscord.Slash{
		Guild:   &guild,
		Path:    "/uc",
		Options: map[string]any{"date": "2026-10-31"},
	})
	if err != nil {
		t.Fatalf("unconfigured /uc: %v", err)
	}

	unconfiguredMsg := awaitInteractionMessage(t, fake, caller, unconfiguredRes)
	if !strings.Contains(unconfiguredMsg.Content, "el hincha cruzado") || strings.Contains(unconfiguredMsg.Content, "<@") {
		t.Errorf("unconfigured /uc reply = %q, want generic fallback without mention", unconfiguredMsg.Content)
	}

	if err := fake.SetMemberPermissions(guild, caller, "32"); err != nil {
		t.Fatalf("SetMemberPermissions: %v", err)
	}

	cfgRes, err := fake.Slash(readyCtx, caller, fakediscord.Slash{
		Guild:   &guild,
		Path:    "/uc-config/set",
		Options: map[string]any{"friend": friend},
	})
	if err != nil {
		t.Fatalf("/uc-config/set: %v", err)
	}

	cfgMsg := awaitInteractionMessage(t, fake, caller, cfgRes)
	if cfgMsg.EphemeralFor != callerID {
		t.Errorf("/uc-config/set EphemeralFor = %q, want %q", cfgMsg.EphemeralFor, callerID)
	}

	res, err := fake.Slash(readyCtx, caller, fakediscord.Slash{
		Guild:   &guild,
		Path:    "/uc",
		Options: map[string]any{"date": "2026-10-31"},
	})
	if err != nil {
		t.Fatalf("/uc: %v", err)
	}

	msg := awaitInteractionMessage(t, fake, caller, res)
	if msg.EphemeralFor != "" {
		t.Errorf("/uc reply EphemeralFor = %q, want public channel message", msg.EphemeralFor)
	}
	if !strings.Contains(msg.Content, "Deportes Iquique") || !strings.Contains(msg.Content, "<@"+friendID+">") {
		t.Errorf("/uc reply = %q, want Deportes Iquique and silent <@%s> mention", msg.Content, friendID)
	}

	r.cancel()
	got := r.result(t)
	if got.code != 0 || got.stdout != "" {
		t.Errorf("got code %d and stdout %q, want exit 0 and empty stdout (stderr=%q)", got.code, got.stdout, got.stderr)
	}

	assertNoneOf(t, got, fake.Token(), masterKey, database.URL().Reveal(), callerID, friendID)
}

func TestRunBootsWhenInitialUCScheduleSyncFails(t *testing.T) {
	noLeaks(t)

	const callerID = "100000000000000083"

	brokenESPN := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "upstream down", http.StatusBadGateway)
	}))
	defer brokenESPN.Close()

	fake := startFake(t)

	database := newClone(t)
	env := envFor(database, "local")
	configureDiscord(t, env, fake)
	storeSetting(t, env, "uc.api_base_url", brokenESPN.URL)

	r := startRun(t, env)

	readyCtx, cancelReady := context.WithTimeout(t.Context(), readyDeadline)
	defer cancelReady()

	if err := fake.WaitReady(readyCtx); err != nil {
		t.Fatalf("the bot never became ready when UC sync failed: %v", err)
	}

	caller := fake.AddUser(fakediscord.User{ID: callerID, Name: "Caller", Locale: "es-419"})
	guild := fake.AddGuild(fakediscord.Guild{Name: "Test Guild"}, caller)

	res, err := fake.Slash(readyCtx, caller, fakediscord.Slash{
		Guild: &guild,
		Path:  "/uc",
	})
	if err != nil {
		t.Fatalf("/uc: %v", err)
	}

	msg := awaitInteractionMessage(t, fake, caller, res)
	if msg.EphemeralFor != callerID {
		t.Errorf("/uc unavailable reply EphemeralFor = %q, want %q", msg.EphemeralFor, callerID)
	}
	if !strings.Contains(msg.Content, "no está disponible") {
		t.Errorf("/uc unavailable reply = %q, want schedule unavailable text", msg.Content)
	}

	r.cancel()
	got := r.result(t)
	if got.code != 0 || got.stdout != "" {
		t.Errorf("got code %d and stdout %q, want exit 0 and empty stdout (stderr=%q)", got.code, got.stdout, got.stderr)
	}

	assertNoneOf(t, got, fake.Token(), masterKey, database.URL().Reveal(), callerID)
}
