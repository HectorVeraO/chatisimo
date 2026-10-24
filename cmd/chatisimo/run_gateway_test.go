//go:build !race

// These tests open a real disgo Gateway client. disgo v0.19 touches the fields of its gateway
// from several goroutines without synchronization, so the race detector reports races inside
// the library; the adapter's own Gateway tests carry the same tag.

package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
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

	names := globalCommandNames(fake)
	for _, want := range []string{"chatisimo", "ge"} {
		if !slices.Contains(names, want) {
			t.Errorf("registered commands = %v, want %q among them", names, want)
		}
	}

	r.cancel()
	got := r.result(t)

	if got.code != 0 || got.stdout != "" {
		t.Errorf("got code %d and stdout %q, want exit 0 and empty stdout (stderr=%q)", got.code, got.stdout, got.stderr)
	}

	assertNoneOf(t, got, fake.Token(), masterKey, database.URL().Reveal())
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

	storeSetting(t, env, "discord.token", tokenSource.Token())
	storeSetting(t, env, "discord.api_base_url", stub.URL)

	r = startRun(t, env)
	close(started)
	got := r.result(t)

	if got.code != 0 || got.stdout != "" {
		t.Errorf("got code %d and stdout %q, want exit 0 and empty stdout (stderr=%q)", got.code, got.stdout, got.stderr)
	}
}
