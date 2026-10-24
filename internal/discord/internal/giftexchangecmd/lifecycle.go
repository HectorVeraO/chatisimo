package giftexchangecmd

import (
	"fmt"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"

	"github.com/HectorVeraO/chatisimo/internal/discord/internal/render"
	"github.com/HectorVeraO/chatisimo/internal/discord/internal/router"
	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

const renameNameOption = "name"

func (h *Handlers) registerLifecycle(r *router.Router) {
	h.verb(r, "rename", h.renameCommand, h.autocompleteFor("rename"))
	h.verb(r, "cancel", h.cancelCommand, h.autocompleteFor("cancel"))
	r.Component(customIDPattern(actionCancelConfirm), h.cancelConfirmButton)
}

// renameCommand handles /ge rename. The reply is ephemeral.
func (h *Handlers) renameCommand(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
	req := membershipRequestFrom(e.Ctx, "rename")
	if req.Group == nil {
		return errGuildOnly
	}

	deferErr := req.DeferEphemeral(e.Ctx)
	if deferErr != nil {
		return deferErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	raw, _ := data.OptString(exchangeOptionName)
	id, err := h.exchangeFor(e.Ctx, req, "rename", raw)
	if err != nil {
		return err
	}

	typed := data.String(renameNameOption)
	renameErr := h.svc.Rename(e.Ctx, req.Actor, *req.Group, id, typed)
	if renameErr != nil {
		return fmt.Errorf("rename exchange: %w", renameErr)
	}

	// The module accepted the name, so it parses; the parsed form is what it stored.
	stored, parseErr := giftexchange.ParseExchangeName(typed)
	if parseErr != nil {
		return fmt.Errorf("read stored name: %w", parseErr)
	}

	return membershipEdit(e.Ctx, req, fmt.Sprintf(textRenamed, render.Bold(stored)))
}

// cancelCommand handles /ge cancel: it asks the organizer to confirm.
func (h *Handlers) cancelCommand(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
	req := membershipRequestFrom(e.Ctx, "cancel")
	if req.Group == nil {
		return errGuildOnly
	}

	deferErr := req.DeferEphemeral(e.Ctx)
	if deferErr != nil {
		return deferErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	raw, _ := data.OptString(exchangeOptionName)
	id, err := h.exchangeFor(e.Ctx, req, "cancel", raw)
	if err != nil {
		return err
	}

	view, err := h.svc.Exchange(e.Ctx, req.Actor, *req.Group, id)
	if err != nil {
		return fmt.Errorf("read exchange: %w", err)
	}
	if view.Organizer.ID != req.Actor.Person() {
		return giftexchange.ErrForbidden
	}

	text := fmt.Sprintf(textConfirmCancel, render.Bold(view.Name))
	return membershipEditWithButtons(e.Ctx, req, text, customID{Action: actionCancelConfirm, Exchange: id})
}

// cancelConfirmButton handles Confirm of the cancel question. The module checks the role and
// the state again; the participants get their direct message from it.
func (h *Handlers) cancelConfirmButton(e *handler.ComponentEvent) error {
	req := membershipRequestFrom(e.Ctx, "cancel confirm")
	parsed, err := membershipButton(e.Ctx, e.Data.CustomID(), actionCancelConfirm, req, true)
	if err != nil {
		return err
	}

	view, err := h.svc.Exchange(e.Ctx, req.Actor, *req.Group, parsed.Exchange)
	if err != nil {
		return fmt.Errorf("read exchange: %w", err)
	}

	cancelErr := h.svc.Cancel(e.Ctx, req.Actor, *req.Group, parsed.Exchange)
	if cancelErr != nil {
		return fmt.Errorf("cancel exchange: %w", cancelErr)
	}

	editErr := membershipEdit(e.Ctx, req, fmt.Sprintf(textCancelled, render.Bold(view.Name)))

	// The exchange is cancelled whether or not the confirmation edit worked, so the
	// announcement goes out regardless.
	public := discord.MessageCreate{Content: fmt.Sprintf(textCancelledPublic, render.Bold(view.Name))}
	followErr := req.FollowupPublic(e.Ctx, public)
	if followErr != nil {
		h.logger.WarnContext(e.Ctx, "cancel announcement failed",
			"request_id", req.ID.String(), "error", followErr.Error())
	}

	return editErr
}
