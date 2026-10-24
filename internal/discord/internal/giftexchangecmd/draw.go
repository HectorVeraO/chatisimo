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

// drawStep describes one organizer action that asks for a confirmation: the draw and the
// reveal share every step but these fields.
type drawStep struct {
	verb    string
	confirm action
	// needState is the only state the action starts from.
	needState giftexchange.State
	// prompt returns the confirmation text for the exchange.
	prompt func(view giftexchange.ExchangeView) string
	run    func(
		ctx context.Context, a giftexchange.Actor, g giftexchange.GroupID, id giftexchange.ExchangeID,
	) error
	// doneText and publicText take the exchange name in bold.
	doneText   string
	publicText string
}

func (h *Handlers) registerDraw(r *router.Router) {
	h.verb(r, "draw", h.drawCommand, h.autocompleteFor("draw"))
	h.verb(r, "reveal", h.revealCommand, h.autocompleteFor("reveal"))
	r.Component(customIDPattern(actionDrawConfirm), h.drawConfirmButton)
	r.Component(customIDPattern(actionRevealConfirm), h.revealConfirmButton)
	h.registerAssignment(r)
}

func (h *Handlers) drawStepOf(verb string) drawStep {
	switch verb {
	case "draw":
		return drawStep{
			verb:      "draw",
			confirm:   actionDrawConfirm,
			needState: giftexchange.StateOpen,
			prompt: func(view giftexchange.ExchangeView) string {
				return fmt.Sprintf(textConfirmDraw, render.Bold(view.Name), view.ParticipantCount)
			},
			run:        h.svc.Draw,
			doneText:   textDrawn,
			publicText: textDrawnPublic,
		}
	case "reveal":
		return drawStep{
			verb:      "reveal",
			confirm:   actionRevealConfirm,
			needState: giftexchange.StateDrawn,
			prompt: func(view giftexchange.ExchangeView) string {
				return fmt.Sprintf(textConfirmReveal, render.Bold(view.Name))
			},
			run:        h.svc.Reveal,
			doneText:   textRevealed,
			publicText: textRevealedPublic,
		}
	default:
		panic("giftexchangecmd: unknown draw step " + verb)
	}
}

// drawCommand handles /ge draw.
func (h *Handlers) drawCommand(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
	return h.drawAsk(data, e, h.drawStepOf("draw"))
}

// revealCommand handles /ge reveal.
func (h *Handlers) revealCommand(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
	return h.drawAsk(data, e, h.drawStepOf("reveal"))
}

// drawConfirmButton handles the Confirm button of the draw prompt.
func (h *Handlers) drawConfirmButton(e *handler.ComponentEvent) error {
	return h.drawConfirm(e, h.drawStepOf("draw"))
}

// revealConfirmButton handles the Confirm button of the reveal prompt.
func (h *Handlers) revealConfirmButton(e *handler.ComponentEvent) error {
	return h.drawConfirm(e, h.drawStepOf("reveal"))
}

// drawAsk replies, ephemeral, with the confirmation prompt of the step. It answers the error
// of the module for a caller who is not the organizer and for an exchange in the wrong state,
// so nobody is asked to confirm what cannot happen.
func (h *Handlers) drawAsk(
	data discord.SlashCommandInteractionData, e *handler.CommandEvent, step drawStep,
) error {
	req, found := router.FromContext(e.Ctx)
	if !found {
		panic("giftexchangecmd: " + step.verb + " ran without a request")
	}
	if req.Group == nil {
		return errGuildOnly
	}

	deferErr := req.DeferEphemeral(e.Ctx)
	if deferErr != nil {
		return deferErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	raw, _ := data.OptString(exchangeOptionName)
	id, err := h.exchangeFor(e.Ctx, req, step.verb, raw)
	if err != nil {
		return err
	}

	view, err := h.svc.Exchange(e.Ctx, req.Actor, *req.Group, id)
	if err != nil {
		return fmt.Errorf("read exchange: %w", err)
	}

	guardErr := drawGuard(view, step)
	if guardErr != nil {
		return guardErr
	}

	text := step.prompt(view)
	confirmID := formatCustomID(customID{Action: step.confirm, Exchange: id})
	backID := formatCustomID(customID{Action: actionDismiss, Exchange: id})
	row := discord.NewActionRow(
		discord.NewPrimaryButton(textButtonConfirm, confirmID),
		discord.NewSecondaryButton(textButtonBack, backID),
	)
	components := []discord.LayoutComponent{row}

	editErr := req.EditOriginal(e.Ctx, discord.MessageUpdate{Content: &text, Components: &components})
	if editErr != nil {
		return editErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	return nil
}

// drawGuard returns the error the module would answer to the step, from what the view says.
func drawGuard(view giftexchange.ExchangeView, step drawStep) error {
	if !view.CallerIsOrganizer {
		return fmt.Errorf("%s exchange: %w", step.verb, giftexchange.ErrForbidden)
	}
	if view.State != step.needState {
		return fmt.Errorf("%s exchange: %w", step.verb, &giftexchange.StateError{Op: step.verb, State: view.State})
	}

	return nil
}

// drawConfirm runs the step the Confirm button stands for. The module checks the role and
// the state again, so a replayed or stale click gets its error and changes nothing.
func (h *Handlers) drawConfirm(e *handler.ComponentEvent, step drawStep) error {
	req, found := router.FromContext(e.Ctx)
	if !found {
		panic("giftexchangecmd: " + step.verb + " confirm ran without a request")
	}

	parsed, err := parseCustomID(e.Data.CustomID())
	if err != nil {
		return err
	}
	if parsed.Action != step.confirm {
		return errBadCustomID
	}
	if req.Group == nil {
		return errGuildOnly
	}

	deferErr := req.DeferUpdate(e.Ctx)
	if deferErr != nil {
		return deferErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}
	req.SetExchange(parsed.Exchange)

	view, err := h.svc.Exchange(e.Ctx, req.Actor, *req.Group, parsed.Exchange)
	if err != nil {
		return fmt.Errorf("read exchange: %w", err)
	}

	runErr := step.run(e.Ctx, req.Actor, *req.Group, parsed.Exchange)
	if runErr != nil {
		return fmt.Errorf("%s exchange: %w", step.verb, runErr)
	}

	return drawAnnounce(e.Ctx, req, step, view.Name)
}

// drawAnnounce edits the prompt into the done text and posts the public followup. Both are
// tried, because the change already happened.
func drawAnnounce(ctx context.Context, req *router.Request, step drawStep, name string) error {
	bold := render.Bold(name)
	done := fmt.Sprintf(step.doneText, bold)
	embeds := []discord.Embed{}
	components := []discord.LayoutComponent{}

	editErr := req.EditOriginal(ctx, discord.MessageUpdate{Content: &done, Embeds: &embeds, Components: &components})
	followupErr := req.FollowupPublic(ctx, discord.MessageCreate{Content: fmt.Sprintf(step.publicText, bold)})

	joined := errors.Join(editErr, followupErr)
	return joined //nolint:wrapcheck // the router reduces REST errors to status and code
}
