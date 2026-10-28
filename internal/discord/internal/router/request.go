package router

import (
	"context"
	"sync"
	"time"
	"uuid"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"
	"github.com/disgoorg/snowflake/v2"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
	"github.com/HectorVeraO/chatisimo/internal/platform/i18n"
)

// Interaction kinds, as RequestLog logs them.
const (
	kindCommand      = "command"
	kindAutocomplete = "autocomplete"
	kindComponent    = "component"
	kindModal        = "modal"
)

// responseStage tells which response the bot already sent for an interaction, so a failure
// is shown on the right channel.
type responseStage int

const (
	stageNone responseStage = iota
	stageDeferredEphemeral
	stageDeferredUpdate
	stageMessage
	stageModal
	stageAutocomplete
)

type requestKey struct{}

// Request is what the router knows about one interaction. Handlers read it from the event
// context with FromContext. Actor and Group are set before any handler runs.
type Request struct {
	// ID is a uuid v7 that names the interaction in logs and in the Internal reply.
	ID uuid.UUID
	// Actor is the caller, resolved from the Discord user.
	Actor giftexchange.Actor
	// Group is the guild the interaction came from, or nil in a DM.
	Group *giftexchange.GroupID

	// guildID and userID are used only to render mentions and to name the caller and the
	// group to the module. They are never logged.
	guildID snowflake.ID
	userID  snowflake.ID

	event *handler.InteractionEvent
	kind  string
	start time.Time

	mu              sync.Mutex
	route           string
	stage           responseStage
	sourceEphemeral bool
	exchange        giftexchange.ExchangeID
	locale          string
	finished        bool
}

func newRequest(e *handler.InteractionEvent) *Request {
	req := &Request{
		ID:    uuid.NewV7(),
		event: e,
		kind:  kindOf(e.Interaction),
		start: time.Now(),
		route: routeUnmatched,
	}

	guild := e.GuildID()
	if guild != nil {
		req.guildID = *guild
	}
	req.userID = e.User().ID

	component, isComponent := e.Interaction.(discord.ComponentInteraction)
	if isComponent {
		req.sourceEphemeral = component.Message.Flags.Has(discord.MessageFlagEphemeral)
	}

	return req
}

func kindOf(i discord.Interaction) string {
	switch i.(type) {
	case discord.AutocompleteInteraction:
		return kindAutocomplete
	case discord.ComponentInteraction:
		return kindComponent
	case discord.ModalSubmitInteraction:
		return kindModal
	default:
		return kindCommand
	}
}

// FromContext returns the request stored in the context of an interaction event.
func FromContext(ctx context.Context) (*Request, bool) {
	req, found := ctx.Value(requestKey{}).(*Request)
	return req, found && req != nil
}

// GuildID returns the guild snowflake, zero in a DM. It is for rendering and for naming the
// group to the module; it is never logged.
func (r *Request) GuildID() snowflake.ID {
	return r.guildID
}

// UserID returns the caller's snowflake. It is for rendering and for naming the caller to the
// module; it is never logged.
func (r *Request) UserID() snowflake.ID {
	return r.userID
}

// InGuild reports whether the interaction came from a guild.
func (r *Request) InGuild() bool {
	isGuild := r.guildID != 0
	return isGuild
}

// Locale returns the interacting user's Discord locale tag (e.g. "en-US", "es-419").
func (r *Request) Locale() string {
	if r.event == nil || r.event.Interaction == nil {
		return ""
	}
	return string(r.event.Locale())
}

// GuildLocale returns the guild's preferred Discord locale tag, or empty in a DM.
func (r *Request) GuildLocale() string {
	if r.event == nil || r.event.Interaction == nil {
		return ""
	}
	guildLocale := r.event.GuildLocale()
	if guildLocale == nil {
		return ""
	}
	return string(*guildLocale)
}

// SetLocale overrides the locale used for error replies on this request (e.g. once an exchange's
// language is resolved).
func (r *Request) SetLocale(locale string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.locale = locale
}

// EffectiveLocale returns the locale override set by SetLocale when non-empty, or the best
// supported locale resolved from Locale() and GuildLocale().
func (r *Request) EffectiveLocale() string {
	r.mu.Lock()
	override := r.locale
	r.mu.Unlock()
	if override != "" {
		return override
	}
	return string(i18n.ResolveLocale(r.Locale(), r.GuildLocale()))
}

// SetExchange records the exchange the interaction is about, so its ID appears in the log.
func (r *Request) SetExchange(id giftexchange.ExchangeID) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.exchange = id
}

func (r *Request) setRoute(pattern string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.route = pattern
}

func (r *Request) setStage(s responseStage) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.stage = s
}

func (r *Request) snapshot() (responseStage, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.stage, r.sourceEphemeral
}
