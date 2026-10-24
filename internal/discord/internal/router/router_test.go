//go:build !race

// These tests drive the router with a real disgo client against the fake. disgo v0.19 touches
// the fields of its gateway from several goroutines without synchronization, so the race
// detector reports races inside the library; the fake's own Gateway tests carry the same tag.

package router_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/handler"
	"github.com/disgoorg/disgo/rest"

	"github.com/HectorVeraO/chatisimo/internal/discord/internal/router"
	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

const (
	userID  = "100000000000000001"
	guildID = "200000000000000001"

	rejectedText    = "rejected for the test"
	guildOnlyText   = "guild only for the test"
	internalPrefix  = "internal for the test "
	panicValue      = "panic-value-that-must-not-be-logged"
	waitDeadline    = 20 * time.Second
	commandDeadline = 10 * time.Second
)

var (
	errRejected  = errors.New("test: rejected")
	errGuildOnly = errors.New("test: guild only")
	errBoom      = errors.New("test: boom")
)

// noBucketLimiter replaces disgo's REST rate limiter, whose cleanup goroutine cannot be
// stopped.
type noBucketLimiter struct{}

func (noBucketLimiter) MaxRetries() int { return rest.MaxRetries }

func (noBucketLimiter) Close(context.Context) {}

func (noBucketLimiter) Reset() {}

func (noBucketLimiter) Wait(context.Context, *rest.CompiledEndpoint) error { return nil }

func (noBucketLimiter) Unlock(*rest.CompiledEndpoint, *http.Response) error { return nil }

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p) //nolint:wrapcheck // bytes.Buffer never fails
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// resolver stands in for the module's identity calls; it can be told to fail.
type resolver struct {
	fail atomic.Bool
}

func (r *resolver) ResolveActor(context.Context, giftexchange.IdentityRef) (giftexchange.Actor, error) {
	if r.fail.Load() {
		return giftexchange.Actor{}, errors.New("test: resolver down")
	}

	return giftexchange.Actor{}, nil
}

func (r *resolver) ResolveGroup(context.Context, giftexchange.GroupRef) (giftexchange.GroupID, error) {
	return giftexchange.GroupID{}, nil
}

func mapError(requestID string, err error) router.Rejection {
	switch {
	case errors.Is(err, errRejected):
		return router.Rejection{Text: rejectedText, Outcome: "rejected:test"}
	case errors.Is(err, errGuildOnly):
		return router.Rejection{Text: guildOnlyText, Outcome: "rejected:bad_input"}
	}

	return router.Rejection{Text: internalPrefix + requestID, Outcome: "error", Internal: true}
}

type rig struct {
	fake       *fakediscord.Server
	resolver   *resolver
	logs       *syncBuffer
	user       fakediscord.User
	guild      fakediscord.Guild
	handlerRan atomic.Int32
}

func tree() []discord.ApplicationCommandCreate {
	sub := func(name string, options ...discord.ApplicationCommandOption) discord.ApplicationCommandOptionSubCommand {
		return discord.ApplicationCommandOptionSubCommand{Name: name, Description: "test verb", Options: options}
	}
	query := discord.ApplicationCommandOptionString{Name: "q", Description: "query", Autocomplete: true}

	return []discord.ApplicationCommandCreate{discord.SlashCommandCreate{
		Name:        "t",
		Description: "test commands",
		Options: []discord.ApplicationCommandOption{
			sub("boom"), sub("reject"), sub("deferred"), sub("ok"), sub("card"), sub("public-card"),
			sub("guild"), sub("ac", query),
		},
		Contexts:         []discord.InteractionContextType{discord.InteractionContextTypeGuild, discord.InteractionContextTypeBotDM},
		IntegrationTypes: []discord.ApplicationIntegrationType{discord.ApplicationIntegrationTypeGuildInstall},
	}}
}

func request(t *testing.T, ctx context.Context) *router.Request {
	t.Helper()

	req, found := router.FromContext(ctx)
	if !found {
		t.Error("handler ran without a request")
	}

	return req
}

func card(customID string) discord.MessageCreate {
	return discord.MessageCreate{
		Content: "a card",
		Components: []discord.LayoutComponent{
			discord.NewActionRow(discord.NewPrimaryButton("Go", customID)),
		},
	}
}

// newRig starts the fake, builds a router with test routes and connects a real disgo client.
func newRig(t *testing.T) *rig {
	t.Helper()

	fake, err := fakediscord.New(fakediscord.Options{})
	if err != nil {
		t.Fatalf("fakediscord.New: %v", err)
	}
	startErr := fake.Start(context.Background())
	if startErr != nil {
		t.Fatalf("Start: %v", startErr)
	}
	t.Cleanup(func() {
		closeErr := fake.Close()
		if closeErr != nil {
			t.Errorf("Close fake: %v", closeErr)
		}
	})

	user := fake.AddUser(fakediscord.User{ID: userID, Name: "tester"})
	guild := fake.AddGuild(fakediscord.Guild{ID: guildID, Name: "Test server"}, user)
	g := &rig{fake: fake, resolver: &resolver{}, logs: &syncBuffer{}, user: user, guild: guild}

	logger := slog.New(slog.NewJSONHandler(g.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	r, err := router.New(router.Config{Resolver: g.resolver, MapError: mapError, Logger: logger})
	if err != nil {
		t.Fatalf("router.New: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		r.Close(ctx)
	})
	g.register(t, r)

	httpClient := &http.Client{Timeout: 10 * time.Second}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	client, err := disgo.New(fake.Token(),
		bot.WithLogger(quiet),
		bot.WithRestClientConfigOpts(rest.WithURL(fake.RESTBase()), rest.WithHTTPClient(httpClient), rest.WithRateLimiter(noBucketLimiter{})),
		bot.WithGatewayConfigOpts(gateway.WithIntents(gateway.IntentGuilds), gateway.WithCompression(gateway.CompressionNone)),
		bot.WithEventManagerConfigOpts(bot.WithAsyncEventsEnabled()),
		bot.WithEventListeners(r.Listener()),
	)
	if err != nil {
		t.Fatalf("disgo.New: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		client.Close(ctx)
		httpClient.CloseIdleConnections()
	})

	_, err = client.Rest.SetGlobalCommands(client.ApplicationID, tree())
	if err != nil {
		t.Fatalf("SetGlobalCommands: %v", err)
	}
	openCtx, cancel := context.WithTimeout(t.Context(), waitDeadline)
	defer cancel()
	openErr := client.OpenGateway(openCtx)
	if openErr != nil {
		t.Fatalf("OpenGateway: %v", openErr)
	}
	readyErr := fake.WaitReady(openCtx)
	if readyErr != nil {
		t.Fatalf("WaitReady: %v", readyErr)
	}

	return g
}

func (g *rig) register(t *testing.T, r *router.Router) {
	t.Helper()

	r.SlashCommand("/t/boom", func(_ discord.SlashCommandInteractionData, _ *handler.CommandEvent) error {
		panic(panicValue)
	})
	r.SlashCommand("/t/reject", func(_ discord.SlashCommandInteractionData, _ *handler.CommandEvent) error {
		return errRejected
	})
	r.SlashCommand("/t/deferred", func(_ discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
		req := request(t, e.Ctx)
		err := req.DeferEphemeral(e.Ctx)
		if err != nil {
			return err //nolint:wrapcheck // test double
		}
		return errBoom
	})
	r.SlashCommand("/t/ok", func(_ discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
		req := request(t, e.Ctx)
		return req.Ephemeral(e.Ctx, discord.MessageCreate{Content: "fine"}) //nolint:wrapcheck // test double
	})
	r.SlashCommand("/t/card", func(_ discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
		req := request(t, e.Ctx)
		return req.Ephemeral(e.Ctx, card("/t/fail/1")) //nolint:wrapcheck // test double
	})
	r.SlashCommand("/t/public-card", func(_ discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
		req := request(t, e.Ctx)
		err := req.DeferEphemeral(e.Ctx)
		if err != nil {
			return err //nolint:wrapcheck // test double
		}
		text := "created"
		err = req.EditOriginal(e.Ctx, discord.MessageUpdate{Content: &text})
		if err != nil {
			return err //nolint:wrapcheck // test double
		}
		return req.FollowupPublic(e.Ctx, card("/t/fail/1")) //nolint:wrapcheck // test double
	})
	r.Component("/t/fail/{n}", func(e *handler.ComponentEvent) error {
		req := request(t, e.Ctx)
		err := req.DeferUpdate(e.Ctx)
		if err != nil {
			return err //nolint:wrapcheck // test double
		}
		return errRejected
	})
	r.Autocomplete("/t/ac", func(*handler.AutocompleteEvent) error {
		return errRejected
	})
	r.RegisterVerb(router.Verb{
		Roots:   []string{"/t"},
		Name:    "guild",
		NotInDM: errGuildOnly,
		Command: func(_ discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
			g.handlerRan.Add(1)
			return request(t, e.Ctx).Ephemeral(e.Ctx, discord.MessageCreate{Content: "fine"}) //nolint:wrapcheck // test double
		},
	})
}

func (g *rig) slash(t *testing.T, path string, inGuild bool) fakediscord.Result {
	t.Helper()

	in := fakediscord.Slash{Path: path}
	if inGuild {
		guild := g.guild
		in.Guild = &guild
	}
	ctx, cancel := context.WithTimeout(t.Context(), commandDeadline)
	defer cancel()

	result, err := g.fake.Slash(ctx, g.user, in)
	if err != nil {
		t.Fatalf("Slash %s: %v", path, err)
	}

	return result
}

func (g *rig) waitRecord(t *testing.T, what string, match func(fakediscord.Record) bool) fakediscord.Record {
	t.Helper()

	var found fakediscord.Record
	pollUntil(t, what, func() bool {
		for _, record := range g.fake.Outbound() {
			if match(record) {
				found = record
				return true
			}
		}

		return false
	})

	return found
}

// pollUntil re-evaluates cond on a ticker until it holds or waitDeadline passes.
func pollUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()

	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(waitDeadline)
	defer timeout.Stop()

	for !cond() {
		select {
		case <-timeout.C:
			t.Fatalf("timed out waiting for %s", what)
		case <-ticker.C:
		}
	}
}

func (g *rig) logLines(t *testing.T, msg string) []map[string]any {
	t.Helper()

	var lines []map[string]any
	for _, line := range strings.Split(g.logs.String(), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record map[string]any
		err := json.Unmarshal([]byte(line), &record)
		if err != nil {
			t.Fatalf("log line is not JSON: %v", err)
		}
		if record["msg"] == msg {
			lines = append(lines, record)
		}
	}

	return lines
}

func (g *rig) waitLine(t *testing.T, outcome string) map[string]any {
	t.Helper()

	var line map[string]any
	pollUntil(t, "a log line with outcome "+outcome, func() bool {
		for _, candidate := range g.logLines(t, "interaction") {
			if candidate["outcome"] == outcome {
				line = candidate
				return true
			}
		}

		return false
	})

	return line
}

func TestAnErrorBeforeAnyResponseIsShownAsANewEphemeralMessage(t *testing.T) {
	g := newRig(t)

	result := g.slash(t, "/t/reject", true)

	if result.CallbackType != 4 || result.Message == nil {
		t.Fatalf("result = %+v, want a type 4 message", result)
	}
	if result.Message.Content != rejectedText || result.Message.EphemeralFor != userID {
		t.Errorf("message = %+v, want the rejection, ephemeral to the caller", result.Message)
	}

	line := g.waitLine(t, "rejected:test")
	if line["route"] != "/t/reject" {
		t.Errorf("route = %v, want the pattern", line["route"])
	}
}

func TestAnErrorAfterADeferredEphemeralResponseEditsTheOriginal(t *testing.T) {
	g := newRig(t)

	result := g.slash(t, "/t/deferred", true)
	if result.Status != fakediscord.StatusDeferred {
		t.Fatalf("status = %q, want deferred", result.Status)
	}

	record := g.waitRecord(t, "the edit of @original", func(r fakediscord.Record) bool {
		return r.Source == "edit" && strings.HasPrefix(r.Content, internalPrefix)
	})
	if !record.Ephemeral || len(record.VisibleTo) != 1 || record.VisibleTo[0] != userID {
		t.Errorf("edit is visible to %v (ephemeral %v), want the caller only", record.VisibleTo, record.Ephemeral)
	}

	line := g.waitLine(t, "error")
	reference := strings.TrimPrefix(record.Content, internalPrefix)
	if line["request_id"] != reference {
		t.Errorf("reply reference %q is not the logged request_id %v", reference, line["request_id"])
	}
	errorLines := g.logLines(t, "interaction failed")
	if len(errorLines) != 1 || errorLines[0]["request_id"] != reference || errorLines[0]["error"] != errBoom.Error() {
		t.Errorf("Error lines = %v, want one with the request_id and the error text", errorLines)
	}
}

func TestAnErrorAfterUpdatingAnEphemeralMessageReplacesIt(t *testing.T) {
	g := newRig(t)
	shown := g.slash(t, "/t/card", true)
	if shown.Message == nil {
		t.Fatal("no card")
	}

	result, err := g.fake.Click(t.Context(), g.user, *shown.Message, "/t/fail/1")
	if err != nil {
		t.Fatalf("Click: %v", err)
	}
	if result.CallbackType != 6 {
		t.Fatalf("callback type = %d, want 6", result.CallbackType)
	}

	record := g.waitRecord(t, "the edit of the card", func(r fakediscord.Record) bool {
		return r.Source == "edit" && r.Content == rejectedText
	})
	if len(record.Components) != 0 {
		t.Errorf("the replaced card still has components: %v", record.Components)
	}
}

func TestAnErrorAfterUpdatingAPublicMessageIsAnEphemeralFollowup(t *testing.T) {
	g := newRig(t)
	g.slash(t, "/t/public-card", true)

	var shown fakediscord.Message
	pollUntil(t, "the public card", func() bool {
		for _, message := range g.fake.Visible(g.user, g.guild.ChannelID) {
			if message.Content == "a card" {
				shown = message
			}
		}

		return shown.ID != ""
	})

	_, err := g.fake.Click(t.Context(), g.user, shown, "/t/fail/1")
	if err != nil {
		t.Fatalf("Click: %v", err)
	}

	record := g.waitRecord(t, "the ephemeral followup", func(r fakediscord.Record) bool {
		return r.Source == "followup" && r.Content == rejectedText
	})
	if !record.Ephemeral || len(record.VisibleTo) != 1 || record.VisibleTo[0] != userID {
		t.Errorf("followup is visible to %v (ephemeral %v), want the caller only", record.VisibleTo, record.Ephemeral)
	}

	for _, message := range g.fake.Visible(g.user, g.guild.ChannelID) {
		if message.ID == shown.ID && message.Content != "a card" {
			t.Errorf("the public card was changed to %q", message.Content)
		}
	}
}

func TestAnAutocompleteErrorAnswersWithNoChoices(t *testing.T) {
	g := newRig(t)
	guild := g.guild
	in := fakediscord.Slash{Guild: &guild, Path: "/t/ac", Options: map[string]any{"q": "x"}}

	choices, err := g.fake.Autocomplete(t.Context(), g.user, in, "q")
	if err != nil {
		t.Fatalf("Autocomplete: %v", err)
	}
	if len(choices) != 0 {
		t.Errorf("choices = %v, want none", choices)
	}

	line := g.waitLine(t, "rejected:test")
	if line["kind"] != "autocomplete" || line["level"] != "DEBUG" {
		t.Errorf("log line = %v, want a DEBUG autocomplete line", line)
	}
}

func TestAPanicIsLoggedWithItsSiteAndNeverItsValue(t *testing.T) {
	g := newRig(t)

	result := g.slash(t, "/t/boom", true)

	if result.Message == nil || !strings.HasPrefix(result.Message.Content, internalPrefix) {
		t.Fatalf("reply = %+v, want the Internal text", result.Message)
	}
	line := g.waitLine(t, "error")
	if strings.Contains(g.logs.String(), panicValue) {
		t.Error("the logs hold the panic value")
	}

	panics := g.logLines(t, "interaction panic")
	if len(panics) != 1 {
		t.Fatalf("%d panic lines, want 1", len(panics))
	}
	reference := strings.TrimPrefix(result.Message.Content, internalPrefix)
	if panics[0]["request_id"] != reference {
		t.Errorf("panic request_id = %v, want %s", panics[0]["request_id"], reference)
	}
	site, _ := panics[0]["panic_site"].(string)
	if !regexp.MustCompile(`^router_test\.go:\d+$`).MatchString(site) {
		t.Errorf("panic_site = %q, want the test file and line", site)
	}

	if line["request_id"] != reference {
		t.Errorf("request line = %v", line)
	}
	if got := len(g.logLines(t, "interaction")); got != 1 {
		t.Errorf("%d request lines for one panicking interaction, want 1", got)
	}
}

func TestAGuildOnlyVerbInTheBotDMAnswersThatAndRunsNoHandler(t *testing.T) {
	g := newRig(t)

	dm := g.slash(t, "/t/guild", false)
	if dm.Message == nil || dm.Message.Content != guildOnlyText {
		t.Fatalf("DM reply = %+v, want the guild-only text", dm.Message)
	}
	if g.handlerRan.Load() != 0 {
		t.Error("the handler ran for a DM")
	}

	inGuild := g.slash(t, "/t/guild", true)
	if inGuild.Message == nil || inGuild.Message.Content != "fine" {
		t.Errorf("guild reply = %+v, want the handler's answer", inGuild.Message)
	}
	if g.handlerRan.Load() != 1 {
		t.Errorf("the handler ran %d times for a guild call, want 1", g.handlerRan.Load())
	}
}

func TestAFailureToIdentifyTheCallerAnswersInternalAndRunsNoHandler(t *testing.T) {
	g := newRig(t)
	g.resolver.fail.Store(true)

	result := g.slash(t, "/t/guild", true)

	if result.Message == nil || !strings.HasPrefix(result.Message.Content, internalPrefix) {
		t.Fatalf("reply = %+v, want the Internal text", result.Message)
	}
	if g.handlerRan.Load() != 0 {
		t.Error("the handler ran without an identified caller")
	}
	g.waitLine(t, "error")
	if strings.Contains(g.logs.String(), userID) {
		t.Error("the logs hold the caller's snowflake")
	}
}

func TestAnUnregisteredPathAnswersInternal(t *testing.T) {
	g := newRig(t)

	result := g.slash(t, "/t/ok", true)
	if result.Message == nil || result.Message.Content != "fine" {
		t.Fatalf("reply = %+v", result.Message)
	}

	unknown, err := g.fake.ClickForged(t.Context(), g.user, *result.Message, "/t/nothing/1")
	if err != nil {
		t.Fatalf("ClickForged: %v", err)
	}
	if unknown.Message == nil || !strings.HasPrefix(unknown.Message.Content, internalPrefix) {
		t.Errorf("reply = %+v, want the Internal text", unknown.Message)
	}
}

func TestNewReportsEveryMissingCollaborator(t *testing.T) {
	t.Parallel()

	_, err := router.New(router.Config{})
	if err == nil {
		t.Fatal("New accepted an empty config")
	}
	for _, want := range []string{"resolver", "mapper", "logger"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}
