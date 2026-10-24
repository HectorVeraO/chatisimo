package discord

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"strings"

	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/HectorVeraO/chatisimo/internal/discord/internal/giftexchangecmd"
	"github.com/HectorVeraO/chatisimo/internal/discord/internal/render"
	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

const (
	opCreateDM    = "create dm"
	opSendMessage = "send message"
)

// Notifier delivers giftexchange notifications as Discord direct messages. It implements
// giftexchange.Notifier and is safe for concurrent use.
type Notifier struct {
	rest   rest.Rest
	caches cache.Caches
	logger *slog.Logger
}

// NewNotifier validates s and builds the REST client and the guild cache. It does no I/O. It
// reports every invalid field together.
func NewNotifier(s Settings, logger *slog.Logger) (*Notifier, error) {
	var problems []error
	if s.Token.Reveal() == "" {
		problems = append(problems, errors.New("discord notifier: token is empty"))
	}
	if !isAbsoluteHTTPURL(s.APIBaseURL) {
		problems = append(problems, errors.New("discord notifier: API base URL is not an absolute http(s) URL"))
	}
	if logger == nil {
		problems = append(problems, errors.New("discord notifier: logger is nil"))
	}
	if len(problems) > 0 {
		return nil, errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
	}

	// disgo logs request and response bodies, which hold message text and snowflakes, so its
	// own logger is silenced; the notifier logs what is safe.
	silent := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := rest.NewClient(
		s.Token.Reveal(),
		rest.WithURL(strings.TrimRight(s.APIBaseURL, "/")),
		rest.WithLogger(silent),
	)
	noMentions := render.NoMentions()

	notifier := &Notifier{
		rest:   rest.New(client, rest.WithDefaultAllowedMentions(noMentions)),
		caches: cache.New(cache.WithCaches(cache.FlagGuilds)),
		logger: logger,
	}
	return notifier, nil
}

func isAbsoluteHTTPURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}

	isHTTP := parsed.Scheme == "http" || parsed.Scheme == "https"
	hasHost := parsed.Host != ""
	return isHTTP && hasHost
}

// Notify sends msg as one DM. Delivery is at least once: the nonce, the base64url form of
// msg.ID with enforce_nonce, deduplicates retries inside Discord's window only. A person whose
// DMs are closed or who shares no server with the bot gives an error that matches
// giftexchange.ErrUndeliverable. Errors carry the notification ID and kind, an HTTP status and
// a Discord code, nothing else.
func (n *Notifier) Notify(ctx context.Context, msg giftexchange.Notification) error {
	if msg.ID.IsZero() {
		return fmt.Errorf("notify %s %s: zero notification ID", msg.ID, msg.Kind)
	}

	userID, err := discordUserID(msg.To)
	if err != nil {
		return fmt.Errorf("notify %s %s: %w: %w", msg.ID, msg.Kind, err, giftexchange.ErrUndeliverable)
	}

	message, err := giftexchangecmd.RenderNotification(msg, n.guildName(msg.Exchange.Group))
	if err != nil {
		return fmt.Errorf("notify %s %s: %w", msg.ID, msg.Kind, err)
	}

	nonce := base64.RawURLEncoding.EncodeToString(msg.ID[:])
	message = message.WithNonce(nonce).WithEnforceNonce(true)

	err = n.deliver(ctx, userID, message)
	if err != nil {
		return fmt.Errorf("notify %s %s: %w", msg.ID, msg.Kind, err)
	}

	n.logger.DebugContext(ctx, "notification sent", slog.String("notification_id", msg.ID.String()))
	return nil
}

// deliver opens the DM channel and sends message. It returns only deliveryError and
// transportError values.
func (n *Notifier) deliver(ctx context.Context, userID snowflake.ID, message discord.MessageCreate) error {
	channel, err := n.rest.CreateDMChannel(userID, rest.WithCtx(ctx))
	if err != nil {
		return reduceRESTError(opCreateDM, err)
	}

	_, err = n.rest.CreateMessage(channel.ID(), message, rest.WithCtx(ctx))
	if err != nil {
		return reduceRESTError(opSendMessage, err)
	}

	return nil
}

// reduceRESTError drops everything of err but the status and the Discord code, or the
// operation when there was no answer from Discord. The *rest.Error is not wrapped.
func reduceRESTError(op string, err error) error {
	var restErr *rest.Error
	if errors.As(err, &restErr) {
		status := 0
		if restErr.Response != nil {
			status = restErr.Response.StatusCode
		}

		return deliveryError{Status: status, Code: int(restErr.Code)}
	}

	return transportError{Op: op}
}

// discordUserID returns the Discord snowflake of the person.
func discordUserID(p giftexchange.PersonRef) (snowflake.ID, error) {
	for _, identity := range p.Identities {
		if identity.Provider != giftexchange.ProviderDiscord {
			continue
		}

		id, err := snowflake.Parse(identity.Subject)
		if err != nil {
			return 0, errors.New("invalid discord identity")
		}
		return id, nil
	}

	return 0, errors.New("no discord identity")
}

// guildName returns the name of the guild from the cache the Gateway fills, or "" when the
// Gateway has not seen it yet.
func (n *Notifier) guildName(g giftexchange.Group) string {
	id, err := snowflake.Parse(g.Subject)
	if err != nil {
		return ""
	}

	guild, found := n.caches.Guild(id)
	if !found {
		return ""
	}

	return guild.Name
}

// Caches returns the guild cache the notifier reads guild names from. The Gateway client is
// built over it, so the Gateway fills the cache the notifier reads.
func (n *Notifier) Caches() cache.Caches {
	return n.caches
}

// Close waits for the requests in flight and closes the idle connections. The notifier cannot
// send afterwards. Call it once, when nothing will send through it again.
func (n *Notifier) Close(ctx context.Context) {
	n.rest.Close(ctx)
}
