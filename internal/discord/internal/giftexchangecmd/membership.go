package giftexchangecmd

import (
	"context"
	"fmt"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"

	"github.com/HectorVeraO/chatisimo/internal/discord/internal/render"
	"github.com/HectorVeraO/chatisimo/internal/discord/internal/router"
	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

func (h *Handlers) registerMembership(r *router.Router) {
	h.verb(r, "join", h.joinCommand, h.autocompleteFor("join"))
	r.Component(customIDPattern(actionJoin), h.joinButton)
}

// joinCommand handles /ge join. The reply is ephemeral.
func (h *Handlers) joinCommand(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
	req, found := router.FromContext(e.Ctx)
	if !found {
		panic("giftexchangecmd: join ran without a request")
	}
	if req.Group == nil {
		return errGuildOnly
	}

	deferErr := req.DeferEphemeral(e.Ctx)
	if deferErr != nil {
		return deferErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	raw, _ := data.OptString(exchangeOptionName)
	id, err := h.exchangeFor(e.Ctx, req, "join", raw)
	if err != nil {
		return err
	}

	return h.join(e.Ctx, req, id)
}

// joinButton handles the Join button of an exchange card. It answers with a deferred
// ephemeral reply, so the card stays as it is.
func (h *Handlers) joinButton(e *handler.ComponentEvent) error {
	req, found := router.FromContext(e.Ctx)
	if !found {
		panic("giftexchangecmd: join button ran without a request")
	}

	parsed, err := parseCustomID(e.Data.CustomID())
	if err != nil {
		return err
	}
	if parsed.Action != actionJoin {
		return errBadCustomID
	}
	if req.Group == nil {
		return errGuildOnly
	}

	deferErr := req.DeferEphemeral(e.Ctx)
	if deferErr != nil {
		return deferErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}
	req.SetExchange(parsed.Exchange)

	return h.join(e.Ctx, req, parsed.Exchange)
}

// join adds the caller to the exchange and edits the deferred reply. The exchange is read
// first, which answers not found for an exchange of another guild and gives the name.
func (h *Handlers) join(ctx context.Context, req *router.Request, id giftexchange.ExchangeID) error {
	view, err := h.svc.Exchange(ctx, req.Actor, *req.Group, id)
	if err != nil {
		return fmt.Errorf("read exchange: %w", err)
	}

	joinErr := h.svc.Join(ctx, req.Actor, *req.Group, id)
	if joinErr != nil {
		return fmt.Errorf("join exchange: %w", joinErr)
	}

	text := fmt.Sprintf(textJoined, render.Bold(view.Name))
	editErr := req.EditOriginal(ctx, discord.MessageUpdate{Content: &text})
	if editErr != nil {
		return editErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	return nil
}
