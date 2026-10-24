// Package discord is the Discord adapter of Chatisimo. It delivers module notifications as
// direct messages and turns slash commands and interactions received over the Gateway into
// calls to the feature modules. It holds no domain rules.
package discord

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	// The adapter needs the America/Santiago zone on hosts without a system zone database.
	_ "time/tzdata"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/rest"

	"github.com/HectorVeraO/chatisimo/internal/discord/internal/giftexchangecmd"
	"github.com/HectorVeraO/chatisimo/internal/discord/internal/router"
	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
	"github.com/HectorVeraO/chatisimo/internal/platform/config"
)

const (
	// timeZoneName is the zone people type times in.
	timeZoneName = "America/Santiago"

	opCreateClient     = "create client"
	opRegisterCommands = "register commands"
)

// Settings are the values read from the database at startup.
type Settings struct {
	// Token is the bot token (setting discord.token).
	Token config.Secret
	// APIBaseURL is the REST base URL, such as https://discord.com/api/v10 (setting
	// discord.api_base_url).
	APIBaseURL string
}

// GiftExchange is the part of the giftexchange module the adapter calls. *giftexchange.Service
// satisfies it.
type GiftExchange = giftexchangecmd.Service

var _ GiftExchange = (*giftexchange.Service)(nil)

// Adapter connects the bot to Discord over the Gateway and routes interactions to handlers.
type Adapter struct {
	notifier *Notifier
	router   *router.Router
	commands []discord.ApplicationCommandCreate

	mu            sync.Mutex
	client        *bot.Client
	gatewayClosed bool
}

// New validates its arguments and builds the handler set. It does no I/O: the Gateway client
// is built by Open. The adapter shares the notifier's REST client and guild cache.
func New(n *Notifier, gx GiftExchange, logger *slog.Logger) (*Adapter, error) {
	var problems []error
	if n == nil {
		problems = append(problems, errors.New("discord adapter: notifier is nil"))
	}
	if gx == nil {
		problems = append(problems, errors.New("discord adapter: giftexchange service is nil"))
	}
	if logger == nil {
		problems = append(problems, errors.New("discord adapter: logger is nil"))
	}
	if len(problems) > 0 {
		return nil, errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
	}

	location, err := time.LoadLocation(timeZoneName)
	if err != nil {
		return nil, fmt.Errorf("discord adapter: load time zone: %w", err)
	}

	guildName := func(subject string) string {
		name := n.guildName(giftexchange.Group{Subject: subject})
		return giftexchangecmd.GuildNameOrUnknown(name)
	}
	handlers, err := giftexchangecmd.New(gx, guildName, time.Now, location, logger)
	if err != nil {
		return nil, fmt.Errorf("discord adapter: %w", err)
	}

	r, err := router.New(router.Config{Resolver: gx, MapError: giftexchangecmd.MapError, Logger: logger})
	if err != nil {
		return nil, fmt.Errorf("discord adapter: %w", err)
	}
	handlers.Register(r)

	adapter := &Adapter{notifier: n, router: r, commands: giftexchangecmd.Commands()}
	return adapter, nil
}

// Open bulk-overwrites the global commands, then opens the Gateway. Building the Gateway
// client asks Discord for the Gateway URL, so the client is built here and not in New.
func (a *Adapter) Open(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.client != nil {
		return errors.New("discord adapter: already open")
	}

	// disgo logs request and response bodies, which hold message text and snowflakes, so its
	// own logger is silenced.
	silent := slog.New(slog.NewTextHandler(io.Discard, nil))
	client, err := disgo.New(a.notifier.token.Reveal(),
		bot.WithLogger(silent),
		bot.WithRest(a.notifier.rest),
		bot.WithCaches(a.notifier.caches),
		bot.WithGatewayConfigOpts(
			gateway.WithIntents(gateway.IntentGuilds),
			gateway.WithCompression(gateway.CompressionNone),
		),
		bot.WithEventManagerConfigOpts(bot.WithAsyncEventsEnabled()),
		bot.WithEventListeners(a.router.Listener()),
	)
	if err != nil {
		return fmt.Errorf("discord adapter: %w", reduceRESTError(opCreateClient, err))
	}

	_, err = client.Rest.SetGlobalCommands(client.ApplicationID, a.commands, rest.WithCtx(ctx))
	if err != nil {
		return fmt.Errorf("discord adapter: %w", reduceRESTError(opRegisterCommands, err))
	}

	err = client.OpenGateway(ctx)
	if err != nil {
		client.Gateway.Close(ctx)
		return fmt.Errorf("discord adapter: open gateway: %w", err)
	}

	// The client is kept only once the Gateway is open, so a failed Open can be retried.
	a.client = client
	return nil
}

// CloseGateway stops taking interactions. REST stays usable, so notifications in flight still
// go out.
func (a *Adapter) CloseGateway(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.client == nil || a.gatewayClosed {
		return
	}

	a.gatewayClosed = true
	a.client.Gateway.Close(ctx)
}

// Close stops the Gateway if it is still open, cancels the handlers in flight and releases the
// REST client last. The notifier cannot send afterwards.
func (a *Adapter) Close(ctx context.Context) {
	a.CloseGateway(ctx)
	a.router.Close(ctx)
	a.notifier.Close(ctx)
}
