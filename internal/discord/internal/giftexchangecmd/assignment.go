package giftexchangecmd

import (
	"fmt"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"

	"github.com/HectorVeraO/chatisimo/internal/discord/internal/render"
	"github.com/HectorVeraO/chatisimo/internal/discord/internal/router"
)

func (h *Handlers) registerAssignment(r *router.Router) {
	h.verb(r, "recipient", h.recipientCommand, h.autocompleteFor("recipient"))
	h.verb(r, "giver", h.giverCommand, h.autocompleteFor("giver"))
}

// recipientCommand handles /ge recipient: it shows the caller their own recipient, ephemeral,
// in a server or in the bot DM.
func (h *Handlers) recipientCommand(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
	req, found := router.FromContext(e.Ctx)
	if !found {
		panic("giftexchangecmd: recipient ran without a request")
	}

	deferErr := req.DeferEphemeral(e.Ctx)
	if deferErr != nil {
		return deferErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	raw, _ := data.OptString(exchangeOptionName)
	id, err := h.exchangeFor(e.Ctx, req, "recipient", raw)
	if err != nil {
		return err
	}

	assignment, err := h.svc.MyAssignment(e.Ctx, req.Actor, id)
	if err != nil {
		return fmt.Errorf("read own recipient: %w", err)
	}

	mention, err := discordMention(assignment.Recipient)
	if err != nil {
		return fmt.Errorf("render own recipient: %w", err)
	}

	return drawShowPair(e, req, textTitleRecipient, mention)
}

// giverCommand handles /ge giver: it shows the caller their own giver, ephemeral, after the
// reveal.
func (h *Handlers) giverCommand(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
	req, found := router.FromContext(e.Ctx)
	if !found {
		panic("giftexchangecmd: giver ran without a request")
	}

	deferErr := req.DeferEphemeral(e.Ctx)
	if deferErr != nil {
		return deferErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	raw, _ := data.OptString(exchangeOptionName)
	id, err := h.exchangeFor(e.Ctx, req, "giver", raw)
	if err != nil {
		return err
	}

	revealed, err := h.svc.MyGiver(e.Ctx, req.Actor, id)
	if err != nil {
		return fmt.Errorf("read own giver: %w", err)
	}

	mention, err := discordMention(revealed.Giver)
	if err != nil {
		return fmt.Errorf("render own giver: %w", err)
	}

	return drawShowPair(e, req, textTitleGiver, mention)
}

// drawShowPair edits the deferred reply into an embed that holds the counterpart's mention.
func drawShowPair(e *handler.CommandEvent, req *router.Request, title, mention string) error {
	embeds := []discord.Embed{render.Embed(title, mention)}

	editErr := req.EditOriginal(e.Ctx, discord.MessageUpdate{Embeds: &embeds})
	if editErr != nil {
		return editErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	return nil
}
