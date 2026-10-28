package giftexchangecmd

import (
	"context"
	"errors"
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

	h.verb(r, "leave", h.leaveCommand, h.autocompleteFor("leave"))
	r.Component(customIDPattern(actionLeave), h.leaveButton)
	r.Component(customIDPattern(actionLeaveConfirm), h.leaveConfirmButton)

	h.verb(r, "remove", h.removeCommand, h.autocompleteFor("remove"))
	r.Component(customIDPattern(actionRemoveConfirm), h.removeConfirmButton)

	r.Component(customIDPattern(actionDismiss), h.dismissButton)

	h.registerCreate(r)
	h.registerLifecycle(r)
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

	c := h.catalogFor(req)
	text := c.Format(textJoined, render.Bold(view.Name))
	editErr := req.EditOriginal(ctx, discord.MessageUpdate{Content: &text})
	if editErr != nil {
		return editErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	return nil
}

// membershipRequestFrom returns the request of the interaction. It panics when there is none,
// which only a missing router middleware can cause.
func membershipRequestFrom(ctx context.Context, what string) *router.Request {
	req, found := router.FromContext(ctx)
	if !found {
		panic("giftexchangecmd: " + what + " ran without a request")
	}

	return req
}

// membershipEdit replaces the deferred reply with text and no embeds or components.
func membershipEdit(ctx context.Context, req *router.Request, text string) error {
	embeds := []discord.Embed{}
	components := []discord.LayoutComponent{}
	err := req.EditOriginal(ctx, discord.MessageUpdate{Content: &text, Embeds: &embeds, Components: &components})
	if err != nil {
		return err //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	return nil
}

// membershipEditWithButtons replaces the deferred reply with text and a Confirm and a Back
// button; confirm is the custom ID of Confirm.
func membershipEditWithButtons(
	ctx context.Context, req *router.Request, c Catalog, text string, confirm customID,
) error {
	back := customID{Action: actionDismiss, Exchange: confirm.Exchange}
	row := discord.NewActionRow(
		discord.NewDangerButton(c.Get(textButtonConfirm), formatCustomID(confirm)),
		discord.NewSecondaryButton(c.Get(textButtonBack), formatCustomID(back)),
	)
	components := []discord.LayoutComponent{row}
	embeds := []discord.Embed{}
	err := req.EditOriginal(ctx, discord.MessageUpdate{Content: &text, Embeds: &embeds, Components: &components})
	if err != nil {
		return err //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	return nil
}

// membershipButton checks a component's custom ID against the wanted action, answers with a
// deferred ephemeral reply (a new message) or a deferred update (the message of the button),
// and records the exchange on the request.
func membershipButton(
	ctx context.Context, rawID string, want action, req *router.Request, update bool,
) (customID, error) {
	parsed, err := parseCustomID(rawID)
	if err != nil {
		return customID{}, err
	}
	if parsed.Action != want {
		return customID{}, errBadCustomID
	}
	if req.Group == nil {
		return customID{}, errGuildOnly
	}

	var deferErr error
	if update {
		deferErr = req.DeferUpdate(ctx)
	} else {
		deferErr = req.DeferEphemeral(ctx)
	}
	if deferErr != nil {
		return customID{}, deferErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}
	req.SetExchange(parsed.Exchange)

	return parsed, nil
}

// membershipIsParticipant reports whether the person is an active participant of the view.
func membershipIsParticipant(view giftexchange.ExchangeView, person giftexchange.PersonID) bool {
	for _, participant := range view.Participants {
		if participant.Person.ID == person {
			return true
		}
	}

	return false
}

// leaveCommand handles /ge leave. In Open it acts; in Drawn it asks first.
func (h *Handlers) leaveCommand(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
	req := membershipRequestFrom(e.Ctx, "leave")
	if req.Group == nil {
		return errGuildOnly
	}

	deferErr := req.DeferEphemeral(e.Ctx)
	if deferErr != nil {
		return deferErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	raw, _ := data.OptString(exchangeOptionName)
	id, err := h.exchangeFor(e.Ctx, req, "leave", raw)
	if err != nil {
		return err
	}

	return h.membershipLeave(e.Ctx, req, id)
}

// leaveButton handles the Leave button of an exchange card like the command.
func (h *Handlers) leaveButton(e *handler.ComponentEvent) error {
	req := membershipRequestFrom(e.Ctx, "leave button")
	parsed, err := membershipButton(e.Ctx, e.Data.CustomID(), actionLeave, req, false)
	if err != nil {
		return err
	}

	return h.membershipLeave(e.Ctx, req, parsed.Exchange)
}

func (h *Handlers) membershipLeave(ctx context.Context, req *router.Request, id giftexchange.ExchangeID) error {
	view, err := h.svc.Exchange(ctx, req.Actor, *req.Group, id)
	if err != nil {
		return fmt.Errorf("read exchange: %w", err)
	}

	isDrawn := view.State == giftexchange.StateDrawn
	if !isDrawn {
		return h.membershipLeaveNow(ctx, req, view)
	}
	if !membershipIsParticipant(view, req.Actor.Person()) {
		return giftexchange.ErrNotParticipant
	}

	c := h.catalogFor(req)
	text := c.Format(textConfirmLeave, render.Bold(view.Name))
	return membershipEditWithButtons(ctx, req, c, text, customID{Action: actionLeaveConfirm, Exchange: id})
}

// leaveConfirmButton handles Confirm of the leave question. The module checks the caller and
// the state again, so a stale or replayed click gets an error and changes nothing.
func (h *Handlers) leaveConfirmButton(e *handler.ComponentEvent) error {
	req := membershipRequestFrom(e.Ctx, "leave confirm")
	parsed, err := membershipButton(e.Ctx, e.Data.CustomID(), actionLeaveConfirm, req, true)
	if err != nil {
		return err
	}

	view, err := h.svc.Exchange(e.Ctx, req.Actor, *req.Group, parsed.Exchange)
	if err != nil {
		return fmt.Errorf("read exchange: %w", err)
	}

	return h.membershipLeaveNow(e.Ctx, req, view)
}

// membershipLeaveNow leaves and tells the caller nothing about the outcome.
func (h *Handlers) membershipLeaveNow(ctx context.Context, req *router.Request, view giftexchange.ExchangeView) error {
	_, err := h.svc.Leave(ctx, req.Actor, *req.Group, view.ID)
	if err != nil {
		return fmt.Errorf("leave exchange: %w", err)
	}

	c := h.catalogFor(req)
	return membershipEdit(ctx, req, c.Format(textLeft, render.Bold(view.Name)))
}

// removeCommand handles /ge remove. In Open it acts; in Drawn it asks first.
func (h *Handlers) removeCommand(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
	req := membershipRequestFrom(e.Ctx, "remove")
	if req.Group == nil {
		return errGuildOnly
	}

	deferErr := req.DeferEphemeral(e.Ctx)
	if deferErr != nil {
		return deferErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	raw, _ := data.OptString(exchangeOptionName)
	id, err := h.exchangeFor(e.Ctx, req, "remove", raw)
	if err != nil {
		return err
	}

	user, hasUser := data.OptUser("participant")
	if !hasUser {
		return giftexchange.ErrNotParticipant
	}
	mention, mentionErr := render.Mention(user.ID.String())
	if mentionErr != nil {
		return fmt.Errorf("mention participant: %w", mentionErr)
	}
	target, err := h.svc.ResolvePerson(e.Ctx, giftexchange.IdentityRef{
		Provider: giftexchange.ProviderDiscord,
		Subject:  user.ID.String(),
	})
	if err != nil {
		return fmt.Errorf("resolve participant: %w", err)
	}

	view, err := h.svc.Exchange(e.Ctx, req.Actor, *req.Group, id)
	if err != nil {
		return fmt.Errorf("read exchange: %w", err)
	}

	isDrawn := view.State == giftexchange.StateDrawn
	if isDrawn {
		return h.membershipAskRemove(e.Ctx, req, view, target, mention)
	}

	_, removeErr := h.svc.RemoveParticipant(e.Ctx, req.Actor, *req.Group, id, target)
	if removeErr != nil {
		return fmt.Errorf("remove participant: %w", removeErr)
	}

	c := h.catalogFor(req)
	return membershipEdit(e.Ctx, req, c.Format(textRemoved, mention, render.Bold(view.Name)))
}

// membershipAskRemove shows the confirmation of a removal after the draw. Only the organizer
// is asked, and only about a participant.
func (h *Handlers) membershipAskRemove(
	ctx context.Context, req *router.Request, view giftexchange.ExchangeView, target giftexchange.PersonID, mention string,
) error {
	if view.Organizer.ID != req.Actor.Person() {
		return giftexchange.ErrForbidden
	}
	if !membershipIsParticipant(view, target) {
		return giftexchange.ErrNotParticipant
	}

	c := h.catalogFor(req)
	text := c.Format(textConfirmRemove, render.Bold(view.Name), mention)
	confirm := customID{Action: actionRemoveConfirm, Exchange: view.ID, Person: target}
	return membershipEditWithButtons(ctx, req, c, text, confirm)
}

// removeConfirmButton handles Confirm of the removal question. An organizer who is a
// participant cannot remove their own recipient; the module refuses with ErrForbidden, which
// is told apart from a missing permission only for the organizer.
func (h *Handlers) removeConfirmButton(e *handler.ComponentEvent) error {
	req := membershipRequestFrom(e.Ctx, "remove confirm")
	parsed, err := membershipButton(e.Ctx, e.Data.CustomID(), actionRemoveConfirm, req, true)
	if err != nil {
		return err
	}

	view, err := h.svc.Exchange(e.Ctx, req.Actor, *req.Group, parsed.Exchange)
	if err != nil {
		return fmt.Errorf("read exchange: %w", err)
	}

	outcome, err := h.svc.RemoveParticipant(e.Ctx, req.Actor, *req.Group, parsed.Exchange, parsed.Person)
	if err != nil {
		isOrganizer := view.Organizer.ID == req.Actor.Person()
		if isOrganizer && errors.Is(err, giftexchange.ErrForbidden) {
			return errOwnRecipient
		}

		return fmt.Errorf("remove participant: %w", err)
	}

	c := h.catalogFor(req)
	return membershipEdit(e.Ctx, req, membershipOutcomeText(c, outcome, view.Name))
}

// membershipOutcomeText is the organizer's reply after a removal in Drawn. It names no one.
func membershipOutcomeText(c Catalog, outcome giftexchange.DropoutOutcome, name string) string {
	switch outcome {
	case giftexchange.OutcomeRemoved:
		return c.Get(textOutcomeRemoved)
	case giftexchange.OutcomePatched:
		return c.Get(textOutcomePatched)
	case giftexchange.OutcomeRedrawn:
		return c.Get(textOutcomeRedrawn)
	case giftexchange.OutcomeReopened:
		return c.Format(textOutcomeReopened, render.Bold(name))
	default:
		panic(fmt.Sprintf("giftexchangecmd: RemoveParticipant returned the unknown dropout outcome %q", outcome))
	}
}

// dismissButton handles Back: it replaces the question and removes its buttons.
func (h *Handlers) dismissButton(e *handler.ComponentEvent) error {
	req := membershipRequestFrom(e.Ctx, "dismiss")
	parsed, err := parseCustomID(e.Data.CustomID())
	if err != nil {
		return err
	}
	if parsed.Action != actionDismiss {
		return errBadCustomID
	}
	req.SetExchange(parsed.Exchange)

	c := h.catalogFor(req)
	text := c.Get(textNothingChanged)
	embeds := []discord.Embed{}
	components := []discord.LayoutComponent{}
	replyErr := req.UpdateMessage(e.Ctx, discord.MessageUpdate{Content: &text, Embeds: &embeds, Components: &components})
	if replyErr != nil {
		return replyErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	return nil
}
