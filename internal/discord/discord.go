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
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/rest"

	"github.com/HectorVeraO/chatisimo/internal/capitime"
	"github.com/HectorVeraO/chatisimo/internal/discord/internal/capitimecmd"
	"github.com/HectorVeraO/chatisimo/internal/discord/internal/giftexchangecmd"
	"github.com/HectorVeraO/chatisimo/internal/discord/internal/render"
	"github.com/HectorVeraO/chatisimo/internal/discord/internal/router"
	"github.com/HectorVeraO/chatisimo/internal/discord/internal/uccmd"
	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
	"github.com/HectorVeraO/chatisimo/internal/platform/config"
	"github.com/HectorVeraO/chatisimo/internal/uccalendar"
)

const (
	// timeZoneName is the zone people type times in.
	timeZoneName = "America/Santiago"

	opCreateClient     = "create client"
	opRegisterCommands = "register commands"
)

// Settings are the values read from the database at startup, plus optional runtime template
// reload dependencies.
type Settings struct {
	// Token is the bot token (setting discord.token).
	Token config.Secret
	// APIBaseURL is the REST base URL, such as https://discord.com/api/v10 (setting
	// discord.api_base_url).
	APIBaseURL string
	// Store is an optional runtime settings reader used to live-reload UC message templates.
	// When nil, embedded default templates are used.
	Store SettingReader
	// Clock is an optional time source for UC template cache expiry; defaults to time.Now.
	Clock func() time.Time
	// TemplateTTL overrides the 5-minute UC template cache lifetime when positive.
	TemplateTTL time.Duration
}

// GiftExchange is the part of the giftexchange module the adapter calls. *giftexchange.Service
// satisfies it.
type GiftExchange = giftexchangecmd.Service

// UCCalendar is the part of the uccalendar module the adapter calls. *uccalendar.Service
// satisfies it.
type UCCalendar = uccmd.Service

// CapiTime is the part of the capitime module the adapter calls. *capitime.Service satisfies it.
type CapiTime = capitimecmd.Service

var (
	_ GiftExchange           = (*giftexchange.Service)(nil)
	_ UCCalendar             = (*uccalendar.Service)(nil)
	_ CapiTime               = (*capitime.Service)(nil)
	_ uccalendar.AlertSender = (*Notifier)(nil)
)

// GatewayIntents is the Gateway intent bitmask requested by the adapter on Identify.
const GatewayIntents = gateway.IntentGuilds | gateway.IntentGuildMessages | gateway.IntentMessageContent

// Adapter connects the bot to Discord over the Gateway and routes interactions to handlers.
type Adapter struct {
	notifier *Notifier
	router   *router.Router
	commands []discord.ApplicationCommandCreate
	logger   *slog.Logger

	capiMu   sync.RWMutex
	capiTime CapiTime

	// mu guards the fields below and is never held across a network call, so CloseGateway
	// stays bounded by its ctx while Open is in flight.
	mu            sync.Mutex
	opening       bool
	client        *bot.Client
	gatewayClosed bool
}

// New validates its arguments and builds the handler set. It does no I/O: the Gateway client
// is built by Open. The adapter shares the notifier's REST client and guild cache.
func New(n *Notifier, gx GiftExchange, uc UCCalendar, logger *slog.Logger) (*Adapter, error) {
	var problems []error
	if n == nil {
		problems = append(problems, errors.New("discord adapter: notifier is nil"))
	}
	if gx == nil {
		problems = append(problems, errors.New("discord adapter: giftexchange service is nil"))
	}
	if uc == nil {
		problems = append(problems, errors.New("discord adapter: uccalendar service is nil"))
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
		return n.guildName(giftexchange.Group{Subject: subject})
	}
	handlers, err := giftexchangecmd.New(gx, guildName, time.Now, location, logger)
	if err != nil {
		return nil, fmt.Errorf("discord adapter: %w", err)
	}
	handlers.SetGuildLocale(func(subject string) string {
		return n.guildLocale(giftexchange.Group{Subject: subject})
	})

	ucHandlers, err := uccmd.New(uc, location)
	if err != nil {
		return nil, fmt.Errorf("discord adapter: %w", err)
	}
	ucHandlers.SetTemplates(n.ucTemplates)

	observe := func(req *router.Request) {
		n.recordInteractionLocales(req.UserID(), req.GuildID(), req.Locale(), req.GuildLocale())
	}
	mapError := func(requestID string, err error) router.Rejection {
		if rejection, matched := capitimecmd.TryMapError(requestID, err); matched {
			return rejection
		}
		if rejection, matched := uccmd.TryMapError(requestID, err); matched {
			return rejection
		}

		return giftexchangecmd.MapError(requestID, err)
	}

	adapter := &Adapter{
		notifier: n,
		commands: buildCommands(),
		logger:   logger,
	}

	r, err := router.New(router.Config{
		Resolver:        gx,
		MapError:        mapError,
		Observe:         observe,
		OnMessageCreate: adapter.onMessageCreate,
		Logger:          logger,
	})
	if err != nil {
		return nil, fmt.Errorf("discord adapter: %w", err)
	}
	handlers.Register(r)
	ucHandlers.Register(r)

	capiHandlers, err := capitimecmd.New(&capiAdapterDelegate{adapter: adapter})
	if err != nil {
		return nil, fmt.Errorf("discord adapter: %w", err)
	}
	capiHandlers.Register(r)

	adapter.router = r
	return adapter, nil
}

// SetCapiTime attaches the Capi Time domain service handled by /capitime-config and Gateway
// MESSAGE_CREATE listeners.
func (a *Adapter) SetCapiTime(capi CapiTime) {
	if a == nil {
		return
	}

	a.capiMu.Lock()
	a.capiTime = capi
	a.capiMu.Unlock()
}

func (a *Adapter) getCapiTime() CapiTime {
	a.capiMu.RLock()
	defer a.capiMu.RUnlock()

	return a.capiTime
}

type capiAdapterDelegate struct {
	adapter *Adapter
}

func (d *capiAdapterDelegate) GetGuildConfig(ctx context.Context, guildID string) (capitime.GuildConfig, error) {
	svc := d.adapter.getCapiTime()
	if svc == nil {
		return capitime.GuildConfig{}, errors.New("discord adapter: capitime service is not configured")
	}

	return svc.GetGuildConfig(ctx, guildID) //nolint:wrapcheck // domain errors are mapped by router
}

func (d *capiAdapterDelegate) UpdateGuildConfig(
	ctx context.Context,
	scope capitime.GuildScope,
	update capitime.GuildConfigUpdate,
) (capitime.GuildConfig, error) {
	svc := d.adapter.getCapiTime()
	if svc == nil {
		return capitime.GuildConfig{}, errors.New("discord adapter: capitime service is not configured")
	}

	return svc.UpdateGuildConfig(ctx, scope, update) //nolint:wrapcheck // domain errors are mapped by router
}

func (d *capiAdapterDelegate) ClearGuildConfig(
	ctx context.Context,
	scope capitime.GuildScope,
	target capitime.ClearTarget,
) (capitime.GuildConfig, error) {
	svc := d.adapter.getCapiTime()
	if svc == nil {
		return capitime.GuildConfig{}, errors.New("discord adapter: capitime service is not configured")
	}

	return svc.ClearGuildConfig(ctx, scope, target) //nolint:wrapcheck // domain errors are mapped by router
}

func (d *capiAdapterDelegate) HandleMessage(ctx context.Context, msg capitime.MessageInput) (capitime.Reply, error) {
	svc := d.adapter.getCapiTime()
	if svc == nil {
		return capitime.Reply{}, nil
	}

	return svc.HandleMessage(ctx, msg) //nolint:wrapcheck // handled by onMessageCreate
}

func (a *Adapter) onMessageCreate(ctx context.Context, event *events.MessageCreate) {
	if event == nil || event.GuildID == nil {
		return
	}
	if event.Message.Author.Bot || event.Message.WebhookID != nil {
		return
	}

	svc := a.getCapiTime()
	if svc == nil {
		return
	}

	input := capitime.MessageInput{
		GuildID:   event.GuildID.String(),
		ChannelID: event.ChannelID.String(),
		MessageID: event.Message.ID.String(),
		AuthorID:  event.Message.Author.ID.String(),
		AuthorBot: event.Message.Author.Bot,
		Content:   event.Message.Content,
	}

	reply, err := svc.HandleMessage(ctx, input)
	if err != nil {
		a.logger.ErrorContext(ctx, "capi time handle message failed")
		return
	}
	if !reply.Handled || reply.Content == "" {
		return
	}

	noMentions := render.NoMentions()
	message := discord.NewMessageCreate().
		WithContent(reply.Content).
		WithMessageReferenceByID(event.Message.ID)
	message.AllowedMentions = &noMentions

	_, sendErr := a.notifier.rest.CreateMessage(event.ChannelID, message, rest.WithCtx(ctx))
	if sendErr != nil {
		a.logger.ErrorContext(
			ctx,
			"capi time reply send failed",
			slog.String("error", reduceRESTError(opSendMessage, sendErr).Error()),
		)
	}
}

func buildCommands() []discord.ApplicationCommandCreate {
	gxCommands := giftexchangecmd.Commands()
	commands := make([]discord.ApplicationCommandCreate, 0, len(gxCommands)+3)
	for _, cmd := range gxCommands {
		slash, isSlash := cmd.(discord.SlashCommandCreate)
		if isSlash && slash.Name == "chatisimo" {
			opts := make([]discord.ApplicationCommandOption, 0, len(slash.Options)+1)
			opts = append(opts, slash.Options...)
			opts = append(opts, uccmd.SubCommand())
			slash.Options = opts
			commands = append(commands, slash)
			continue
		}

		commands = append(commands, cmd)
	}
	commands = append(commands, uccmd.Command(), uccmd.ConfigCommand(), capitimecmd.ConfigCommand())
	return commands
}

// Open bulk-overwrites the global commands, then opens the Gateway. Building the Gateway
// client asks Discord for the Gateway URL, so the client is built here and not in New.
func (a *Adapter) Open(ctx context.Context) error {
	err := a.beginOpen()
	if err != nil {
		return err
	}

	client, err := a.openClient(ctx)
	return a.finishOpen(ctx, client, err)
}

// beginOpen marks an Open in flight, refusing a second Open and an Open after the Gateway
// was closed.
func (a *Adapter) beginOpen() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.client != nil || a.opening {
		return errors.New("discord adapter: already open")
	}
	if a.gatewayClosed {
		return errors.New("discord adapter: gateway closed")
	}

	a.opening = true
	return nil
}

// finishOpen keeps the client only once the Gateway is open, so a failed Open can be retried.
// A CloseGateway that arrived during Open closes the new Gateway instead.
func (a *Adapter) finishOpen(ctx context.Context, client *bot.Client, openErr error) error {
	a.mu.Lock()
	a.opening = false
	closedMeanwhile := a.gatewayClosed
	if openErr == nil && !closedMeanwhile {
		a.client = client
	}
	a.mu.Unlock()

	if openErr != nil {
		return openErr
	}
	if closedMeanwhile {
		client.Gateway.Close(ctx)
		return errors.New("discord adapter: gateway closed while opening")
	}
	return nil
}

// openClient builds the client, registers the commands and opens the Gateway.
func (a *Adapter) openClient(ctx context.Context) (*bot.Client, error) {
	// disgo logs request and response bodies, which hold message text and snowflakes, so its
	// own logger is silenced.
	silent := slog.New(slog.NewTextHandler(io.Discard, nil))
	client, err := disgo.New(a.notifier.token.Reveal(),
		bot.WithLogger(silent),
		bot.WithRest(a.notifier.rest),
		bot.WithCaches(a.notifier.caches),
		bot.WithGatewayConfigOpts(
			gateway.WithIntents(GatewayIntents),
			gateway.WithCompression(gateway.CompressionNone),
		),
		bot.WithEventManagerConfigOpts(bot.WithAsyncEventsEnabled()),
		bot.WithEventListeners(a.router.Listener()),
	)
	if err != nil {
		return nil, fmt.Errorf("discord adapter: %w", reduceRESTError(opCreateClient, err))
	}

	_, err = client.Rest.SetGlobalCommands(client.ApplicationID, a.commands, rest.WithCtx(ctx))
	if err != nil {
		return nil, fmt.Errorf("discord adapter: %w", reduceRESTError(opRegisterCommands, err))
	}

	err = client.OpenGateway(ctx)
	if err != nil {
		client.Gateway.Close(ctx)
		return nil, fmt.Errorf("discord adapter: open gateway: %w", err)
	}

	return client, nil
}

// CloseGateway stops taking interactions. REST stays usable, so notifications in flight still
// go out.
func (a *Adapter) CloseGateway(ctx context.Context) {
	a.mu.Lock()
	if a.gatewayClosed {
		a.mu.Unlock()
		return
	}
	a.gatewayClosed = true
	client := a.client
	a.mu.Unlock()

	if client != nil {
		client.Gateway.Close(ctx)
	}
}

// Close stops the Gateway if it is still open, cancels the handlers in flight and releases the
// REST client last. The notifier cannot send afterwards.
func (a *Adapter) Close(ctx context.Context) {
	a.CloseGateway(ctx)
	a.router.Close(ctx)
	a.notifier.Close(ctx)
}
