package router

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/disgoorg/disgo/handler"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

var errPanic = errors.New("router: handler panicked")

const outcomeOK = "ok"

// outcomePanic is the log outcome of an interaction whose handler panicked.
const outcomePanic = "error"

// resolveError reports a failure to identify the caller. It keeps the cause as text only, so
// no sentinel of the cause makes the failure look like a rejection.
type resolveError struct {
	op    string
	cause string
}

func (e resolveError) Error() string {
	text := "router: " + e.op + ": " + e.cause
	return text
}

func withRequest(ctx context.Context, req *Request) context.Context {
	return context.WithValue(ctx, requestKey{}, req)
}

// recoverMiddleware is the outermost layer. A panic is logged with the place it came from,
// never with its value, and the caller gets the Internal reply.
func (r *Router) recoverMiddleware(next handler.Handler) handler.Handler {
	return func(e *handler.InteractionEvent) error {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}

			r.onPanic(e)
		}()

		return next(e)
	}
}

func (r *Router) onPanic(e *handler.InteractionEvent) {
	site := panicSite()
	req, found := FromContext(e.Ctx)
	if !found {
		r.logger.Error("interaction panic", slog.String("panic_site", site))
		return
	}

	r.logger.Error("interaction panic",
		slog.String("request_id", req.ID.String()),
		slog.String("panic_site", site))

	ctx, cancel := context.WithTimeout(context.WithoutCancel(e.Ctx), replyTimeout)
	defer cancel()

	rejection := r.mapError(req.ID.String(), errPanic)
	replyErr := req.Reject(ctx, rejection.Text)
	r.logReplyFailure(ctx, req, replyErr)
	r.finish(ctx, req, outcomePanic)
}

// panicSite returns file:line of the first frame outside the runtime package below the panic.
func panicSite() string {
	pcs := make([]uintptr, 32)
	n := runtime.Callers(2, pcs)
	frames := runtime.CallersFrames(pcs[:n])

	seenPanic := false
	for {
		frame, more := frames.Next()
		isRuntime := strings.HasPrefix(frame.Function, "runtime.")
		if isRuntime && strings.HasPrefix(frame.Function, "runtime.gopanic") {
			seenPanic = true
		}
		if seenPanic && !isRuntime {
			site := fmt.Sprintf("%s:%d", filepath.Base(frame.File), frame.Line)
			return site
		}
		if !more {
			return "unknown"
		}
	}
}

// requestLog creates the request, bounds the handler run and logs one line per interaction.
func (r *Router) requestLog(next handler.Handler) handler.Handler {
	return func(e *handler.InteractionEvent) error {
		req := newRequest(e)
		ctx, cancel := context.WithTimeout(withRequest(e.Ctx, req), interactionTimeout)
		defer cancel()
		e.Ctx = ctx

		err := next(e)
		if err == nil {
			r.finish(ctx, req, outcomeOK)
		}

		return err
	}
}

// resolve identifies the caller and the guild before any handler runs.
func (r *Router) resolve(next handler.Handler) handler.Handler {
	return func(e *handler.InteractionEvent) error {
		req, found := FromContext(e.Ctx)
		if !found {
			panic("router: resolve ran before the request was created")
		}

		actor, err := r.resolver.ResolveActor(e.Ctx, giftexchange.IdentityRef{
			Provider: giftexchange.ProviderDiscord,
			Subject:  req.userID.String(),
		})
		if err != nil {
			return resolveError{op: "resolve actor", cause: err.Error()}
		}
		req.Actor = actor

		if req.InGuild() {
			group, groupErr := r.resolver.ResolveGroup(e.Ctx, giftexchange.GroupRef{
				Provider: giftexchange.ProviderDiscord,
				Subject:  req.guildID.String(),
			})
			if groupErr != nil {
				return resolveError{op: "resolve group", cause: groupErr.Error()}
			}
			req.Group = &group
		}

		return next(e)
	}
}

// handleError is the mux error hook: it replies to the caller and logs the interaction.
func (r *Router) handleError(e *handler.InteractionEvent, err error) {
	req, found := FromContext(e.Ctx)
	if !found {
		r.logger.Error("interaction failed before it had a request")
		return
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(e.Ctx), replyTimeout)
	defer cancel()

	rejection := r.mapError(req.ID.String(), err)
	if rejection.Internal {
		r.logger.ErrorContext(ctx, "interaction failed",
			slog.String("request_id", req.ID.String()),
			slog.String("error", err.Error()))
	}

	replyErr := req.Reject(ctx, rejection.Text)
	r.logReplyFailure(ctx, req, replyErr)
	r.finish(ctx, req, rejection.Outcome)
}

func (r *Router) logReplyFailure(ctx context.Context, req *Request, err error) {
	if err == nil {
		return
	}

	r.logger.WarnContext(ctx, "reply failed",
		slog.String("request_id", req.ID.String()),
		slog.String("error", err.Error()))
}

// finish logs the one line of the interaction. Only the first call logs.
func (r *Router) finish(ctx context.Context, req *Request, outcome string) {
	req.mu.Lock()
	if req.finished {
		req.mu.Unlock()
		return
	}
	req.finished = true
	route := req.route
	exchange := req.exchange
	req.mu.Unlock()

	scope := "dm"
	if req.InGuild() {
		scope = "guild"
	}

	attrs := []slog.Attr{
		slog.String("request_id", req.ID.String()),
		slog.String("kind", req.kind),
		slog.String("route", route),
		slog.String("context", scope),
	}
	if !req.Actor.Person().IsZero() {
		attrs = append(attrs, slog.String("person_id", req.Actor.Person().String()))
	}
	if req.Group != nil {
		attrs = append(attrs, slog.String("group_id", req.Group.String()))
	}
	if !exchange.IsZero() {
		attrs = append(attrs, slog.String("exchange_id", exchange.String()))
	}
	attrs = append(attrs,
		slog.String("outcome", outcome),
		slog.Int64("duration_ms", time.Since(req.start).Milliseconds()))

	level := slog.LevelInfo
	if req.kind == kindAutocomplete {
		level = slog.LevelDebug
	}
	r.logger.LogAttrs(ctx, level, "interaction", attrs...)
}
