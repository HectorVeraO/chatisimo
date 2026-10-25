//go:build !race

// These tests drive the fake with a real disgo client. disgo v0.19 touches the fields of its
// gateway (heartbeat state, close function, last sequence) from several goroutines without
// synchronization, so the race detector reports races inside the library, not in the fake. The
// same behaviors are covered under -race by the raw WebSocket tests in gateway_test.go.

package fakediscord_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/rest"

	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
)

// noBucketLimiter stands in for disgo's default REST rate limiter, whose cleanup goroutine
// has no way to stop and would fail the goroutine-leak check. It does no bucket accounting
// and keeps the default retry count, so a 429 is still retried.
type noBucketLimiter struct{}

func (noBucketLimiter) MaxRetries() int { return rest.MaxRetries }

func (noBucketLimiter) Close(context.Context) {}

func (noBucketLimiter) Reset() {}

func (noBucketLimiter) Wait(context.Context, *rest.CompiledEndpoint) error { return nil }

func (noBucketLimiter) Unlock(*rest.CompiledEndpoint, *http.Response) error { return nil }

// botHarness is a real disgo client built the way the adapter builds it.
type botHarness struct {
	client  *bot.Client
	guilds  chan string
	joined  chan string
	resumed chan struct{}
	closed  chan error
}

// newBot builds a disgo client against the fake and closes it when the test ends.
func newBot(t *testing.T, srv *fakediscord.Server, token string, extra ...bot.ConfigOpt) *botHarness {
	t.Helper()

	h := &botHarness{
		guilds:  make(chan string, 16),
		joined:  make(chan string, 16),
		resumed: make(chan struct{}, 16),
		closed:  make(chan error, 16),
	}
	httpClient := &http.Client{Timeout: 10 * time.Second}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	opts := []bot.ConfigOpt{
		bot.WithLogger(quiet),
		bot.WithRestClientConfigOpts(rest.WithURL(srv.RESTBase()), rest.WithHTTPClient(httpClient), rest.WithRateLimiter(noBucketLimiter{})),
		bot.WithGatewayConfigOpts(
			gateway.WithLogger(quiet),
			gateway.WithIntents(gateway.IntentGuilds),
			gateway.WithCompression(gateway.CompressionNone),
			gateway.WithCloseHandler(func(_ gateway.Gateway, err error, _ bool) {
				h.closed <- err
			}),
		),
		bot.WithCacheConfigOpts(cache.WithCaches(cache.FlagGuilds)),
		bot.WithEventListenerFunc(func(e *events.GuildReady) { h.guilds <- e.GuildID.String() }),
		bot.WithEventListenerFunc(func(e *events.GuildJoin) { h.joined <- e.GuildID.String() }),
		bot.WithEventListenerFunc(func(*events.Resumed) { h.resumed <- struct{}{} }),
	}
	client, err := disgo.New(token, append(opts, extra...)...)
	if err != nil {
		t.Fatalf("disgo.New: %v", err)
	}
	h.client = client

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		client.Close(ctx)
		httpClient.CloseIdleConnections()
	})

	return h
}

func (h *botHarness) registerGlobalCommands(t *testing.T) []discord.ApplicationCommand {
	t.Helper()

	commands, err := h.client.Rest.SetGlobalCommands(h.client.ApplicationID, []discord.ApplicationCommandCreate{
		discord.SlashCommandCreate{Name: "ping", Description: "Ping the bot"},
	})
	if err != nil {
		t.Fatalf("SetGlobalCommands: %v", err)
	}

	return commands
}

func (h *botHarness) open(t *testing.T) {
	t.Helper()

	err := h.client.OpenGateway(testContext(t, gatewayTimeout))
	if err != nil {
		t.Fatalf("OpenGateway: %v", err)
	}
}

func TestBotReachesReady(t *testing.T) {
	t.Parallel()
	srv := startFake(t, fakediscord.Options{})
	alice := srv.AddUser(fakediscord.User{ID: aliceID, Name: "Alice"})
	winter := srv.AddGuild(fakediscord.Guild{ID: winterGuildID, Name: "Winter guild"}, alice)
	spring := srv.AddGuild(fakediscord.Guild{ID: springGuildID, Name: "Spring guild"}, alice)
	h := newBot(t, srv, srv.Token())

	registered := h.registerGlobalCommands(t)
	h.open(t)

	got := map[string]bool{}
	for range 2 {
		got[waitChan(t, h.guilds, gatewayTimeout, "GuildReady")] = true
	}
	if !got[winter.ID] || !got[spring.ID] {
		t.Errorf("GuildReady for %v, want both %s and %s", got, winter.ID, spring.ID)
	}
	err := srv.WaitReady(testContext(t, gatewayTimeout))
	if err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
	if h.client.ApplicationID.String() != srv.ApplicationID() {
		t.Errorf("client application ID = %s, want %s", h.client.ApplicationID, srv.ApplicationID())
	}
	intents, identified := srv.Intents()
	if !identified || intents != int(gateway.IntentGuilds) {
		t.Errorf("Intents() = %d, %v, want %d, true", intents, identified, gateway.IntentGuilds)
	}

	commands := srv.Commands()
	if len(commands) != 1 || commands[0].Name != "ping" {
		t.Fatalf("Commands() = %+v, want just ping", commands)
	}
	command := commands[0]
	if command.ID == "" || command.Version == "" || command.ApplicationID != srv.ApplicationID() || command.Type != 1 {
		t.Errorf("stored command = %+v", command)
	}
	if len(registered) != 1 || registered[0].ID().String() != command.ID {
		t.Errorf("the client was told %v, the fake stored %s", registered, command.ID)
	}
}

func TestHeartbeatsAreAcknowledged(t *testing.T) {
	t.Parallel()
	const (
		interval         = 250 * time.Millisecond
		wantAcknowledged = 4
	)
	srv := startFake(t, fakediscord.Options{HeartbeatInterval: interval})
	srv.AddGuild(fakediscord.Guild{ID: winterGuildID, Name: "Winter guild"})
	h := newBot(t, srv, srv.Token())
	h.open(t)
	waitChan(t, h.guilds, gatewayTimeout, "GuildReady")

	// Wait on the condition, never on a window: the session must have been acknowledged
	// several times, however slowly the machine runs.
	waitFor(t, gatewayTimeout, "several heartbeat ACKs", func() bool {
		return srv.HeartbeatAcks() >= wantAcknowledged
	})

	select {
	case <-h.resumed:
		t.Fatal("the client reconnected although heartbeats were acknowledged")
	case err := <-h.closed:
		t.Fatalf("the client's gateway closed: %v", err)
	default:
	}
	if resumes := srv.Resumes(); resumes != 0 {
		t.Errorf("Resumes() = %d, want 0", resumes)
	}
	if status := h.client.Gateway.Status(); status != gateway.StatusReady {
		t.Errorf("gateway status = %v, want ready", status)
	}
}

func TestGatewayResumesAfterADrop(t *testing.T) {
	t.Parallel()
	srv := startFake(t, fakediscord.Options{})
	srv.AddGuild(fakediscord.Guild{ID: winterGuildID, Name: "Winter guild"})
	h := newBot(t, srv, srv.Token())
	h.open(t)
	waitChan(t, h.guilds, gatewayTimeout, "GuildReady")
	if len(h.joined) != 0 {
		t.Fatal("GuildJoin fired for a guild announced in READY")
	}

	srv.DropGateway(4000)
	spring := srv.AddGuild(fakediscord.Guild{ID: springGuildID, Name: "Spring guild"})

	waitChan(t, h.resumed, gatewayTimeout, "Resumed")
	if got := srv.Resumes(); got != 1 {
		t.Errorf("Resumes() = %d, want 1", got)
	}
	if joined := waitChan(t, h.joined, gatewayTimeout, "GuildJoin for the guild added while away"); joined != spring.ID {
		t.Errorf("GuildJoin for %s, want %s", joined, spring.ID)
	}
	cached := false
	for guild := range h.client.Caches.Guilds() {
		if guild.ID.String() == spring.ID {
			cached = true
		}
	}
	if !cached {
		t.Error("the replayed guild is missing from the client's cache")
	}
}

func TestBadTokenIsClosedWithAuthenticationFailed(t *testing.T) {
	t.Parallel()
	var logs syncBuffer
	srv := startFake(t, fakediscord.Options{Logger: capturingLogger(&logs)})
	srv.AddGuild(fakediscord.Guild{ID: winterGuildID, Name: "Winter guild"})
	segments := strings.Split(srv.Token(), ".")
	wrong := segments[0] + "." + strings.Repeat("a", 8) + "." + strings.Repeat("b", 27)
	h := newBot(t, srv, wrong)

	err := h.client.OpenGateway(testContext(t, gatewayTimeout))

	if err == nil {
		t.Fatal("OpenGateway succeeded with a wrong token")
	}
	if !strings.Contains(err.Error(), "4004") {
		t.Errorf("OpenGateway error = %v, want a 4004 close", err)
	}
	if _, identified := srv.Intents(); identified {
		t.Error("the fake recorded intents for a rejected Identify")
	}
	waitFor(t, gatewayTimeout, "the rejected identify to be logged", func() bool {
		return strings.Contains(logs.String(), "accepted=false")
	})
	if n := strings.Count(logs.String(), "gateway identify"); n != 1 {
		t.Errorf("the fake saw %d identifies, want 1:\n%s", n, logs.String())
	}
}

func TestTokenNeverReachesRecordsOrLogs(t *testing.T) {
	t.Parallel()
	assertTokenStaysPrivate(t, func(t *testing.T, srv *fakediscord.Server) {
		t.Helper()

		h := newBot(t, srv, srv.Token())
		h.registerGlobalCommands(t)
		h.open(t)
		waitChan(t, h.guilds, gatewayTimeout, "GuildReady")
	})
}

func TestWaitReadyNeedsAGlobalCommandSet(t *testing.T) {
	t.Parallel()
	srv := startFake(t, fakediscord.Options{})
	srv.AddGuild(fakediscord.Guild{ID: winterGuildID, Name: "Winter guild"})
	h := newBot(t, srv, srv.Token())
	h.open(t)
	waitChan(t, h.guilds, gatewayTimeout, "GuildReady")

	early, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	if err := srv.WaitReady(early); err == nil {
		t.Fatal("WaitReady returned before any global command set was registered")
	}

	h.registerGlobalCommands(t)
	err := srv.WaitReady(testContext(t, gatewayTimeout))
	if err != nil {
		t.Errorf("WaitReady after registering commands: %v", err)
	}
}
