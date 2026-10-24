// Package router turns Discord interactions into handler calls. It wraps every interaction in
// recovery, request logging and identity resolution, replies to the caller when a handler
// fails, and holds nothing of any feature: features register handlers and supply the mapping
// from their errors to replies.
package router

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

const (
	// interactionTimeout bounds one handler run.
	interactionTimeout = 15 * time.Second
	// replyTimeout bounds the reply that reports a failure.
	replyTimeout = 5 * time.Second

	routeUnmatched = "unmatched"
)

// ErrUnknownRoute is returned for a path that no handler is registered for.
var ErrUnknownRoute = errors.New("router: no handler for the interaction")

// Resolver is the part of the module the router needs to identify the caller.
type Resolver interface {
	ResolveActor(ctx context.Context, ref giftexchange.IdentityRef) (giftexchange.Actor, error)
	ResolveGroup(ctx context.Context, ref giftexchange.GroupRef) (giftexchange.GroupID, error)
}

// Rejection is how a feature wants an error shown and logged.
type Rejection struct {
	// Text is shown to the caller.
	Text string
	// Outcome is the log outcome, such as "rejected:forbidden" or "error".
	Outcome string
	// Internal marks an unexpected failure, which is also logged at Error level.
	Internal bool
}

// ErrorMapper maps an error a handler returned to a Rejection. requestID is the reference
// to show to the caller for unexpected failures.
type ErrorMapper func(requestID string, err error) Rejection

// Config holds the router's collaborators. Every field is required.
type Config struct {
	Resolver Resolver
	MapError ErrorMapper
	Logger   *slog.Logger
}

// Router owns the handler mux. It is safe for concurrent use once handlers are registered.
type Router struct {
	mux      *handler.Mux
	resolver Resolver
	mapError ErrorMapper
	logger   *slog.Logger
	cancel   context.CancelFunc

	mu       sync.Mutex
	closed   bool
	inflight sync.WaitGroup
}

// New validates cfg, reporting every problem together, and builds the router. It does no I/O.
func New(cfg Config) (*Router, error) {
	var problems []error
	if cfg.Resolver == nil {
		problems = append(problems, errors.New("router: resolver is nil"))
	}
	if cfg.MapError == nil {
		problems = append(problems, errors.New("router: error mapper is nil"))
	}
	if cfg.Logger == nil {
		problems = append(problems, errors.New("router: logger is nil"))
	}
	if len(problems) > 0 {
		return nil, errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
	}

	base, cancel := context.WithCancel(context.Background())
	r := &Router{
		mux:      handler.New(),
		resolver: cfg.Resolver,
		mapError: cfg.MapError,
		logger:   cfg.Logger,
		cancel:   cancel,
	}

	r.mux.DefaultContext(func() context.Context { return base })
	r.mux.Use(r.recoverMiddleware, r.requestLog, r.resolve)
	r.mux.Error(r.handleError)
	r.mux.NotFound(func(*handler.InteractionEvent) error { return ErrUnknownRoute })

	return r, nil
}

// Listener returns the event listener to give the Gateway client. Events that arrive after
// Close are dropped.
func (r *Router) Listener() bot.EventListener {
	return r
}

// OnEvent routes one Gateway event, and lets Close wait for it to finish.
func (r *Router) OnEvent(event bot.Event) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.inflight.Add(1)
	r.mu.Unlock()

	defer r.inflight.Done()
	r.mux.OnEvent(event)
}

// Close cancels the context of every handler run and waits for the runs in flight to finish,
// or for ctx to end. Events that arrive afterwards are dropped.
func (r *Router) Close(ctx context.Context) {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()

	r.cancel()

	done := make(chan struct{})
	go func() {
		r.inflight.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
	}
}

// SlashCommand registers h for the command path pattern.
func (r *Router) SlashCommand(pattern string, h handler.SlashCommandHandler) {
	r.mux.SlashCommand(pattern, func(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
		r.markCtx(e.Ctx, pattern)
		return h(data, e)
	})
}

// Autocomplete registers h for the command path pattern.
func (r *Router) Autocomplete(pattern string, h handler.AutocompleteHandler) {
	r.mux.Autocomplete(pattern, func(e *handler.AutocompleteEvent) error {
		r.markCtx(e.Ctx, pattern)
		return h(e)
	})
}

// Component registers h for the custom ID pattern of buttons and selects.
func (r *Router) Component(pattern string, h handler.ComponentHandler) {
	r.mux.Component(pattern, func(e *handler.ComponentEvent) error {
		r.markCtx(e.Ctx, pattern)
		return h(e)
	})
}

// Modal registers h for the custom ID pattern of modal submissions.
func (r *Router) Modal(pattern string, h handler.ModalHandler) {
	r.mux.Modal(pattern, func(e *handler.ModalEvent) error {
		r.markCtx(e.Ctx, pattern)
		return h(e)
	})
}

func (r *Router) markCtx(ctx context.Context, pattern string) {
	req, found := FromContext(ctx)
	if found {
		req.setRoute(pattern)
	}
}

// Verb describes a slash verb that is registered under several command roots.
type Verb struct {
	// Roots are the command path prefixes, such as "/ge".
	Roots []string
	// Name is the verb, such as "join".
	Name string
	// NotInDM is returned, without calling any handler, when the verb is used outside a
	// guild. Nil allows the verb in a DM.
	NotInDM error
	// Command handles the slash command.
	Command handler.SlashCommandHandler
	// Autocomplete handles autocomplete of the verb's options. Nil registers none.
	Autocomplete handler.AutocompleteHandler
}

// RegisterVerb registers the verb's command, and its autocomplete when set, on every root.
func (r *Router) RegisterVerb(v Verb) {
	for _, root := range v.Roots {
		pattern := root + "/" + v.Name

		command := v.Command
		r.SlashCommand(pattern, func(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
			req, found := FromContext(e.Ctx)
			if found && v.NotInDM != nil && !req.InGuild() {
				return v.NotInDM
			}
			return command(data, e)
		})

		if v.Autocomplete == nil {
			continue
		}

		complete := v.Autocomplete
		r.Autocomplete(pattern, func(e *handler.AutocompleteEvent) error {
			req, found := FromContext(e.Ctx)
			if found && v.NotInDM != nil && !req.InGuild() {
				return v.NotInDM
			}
			return complete(e)
		})
	}
}
