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

const (
	rulesGiverOption     = "giver"
	rulesRecipientOption = "recipient"
	rulesBothOption      = "both"

	rulesNotAPersonReason   = "must be a person in this exchange"
	rulesNotParticipantText = "giver and recipient must both be participants"
)

func (h *Handlers) registerExclusions(r *router.Router) {
	h.verb(r, "exclude", h.rulesExclude, h.autocompleteFor("exclude"))
	h.verb(r, "unexclude", h.rulesUnexclude, h.autocompleteFor("unexclude"))
}

// rulesPair is the two people an exclusion command names, as module IDs and as snowflakes
// for the mentions of the reply.
type rulesPair struct {
	giver, recipient               giftexchange.PersonID
	giverMention, recipientMention string
}

// rulesExclude handles /ge exclude. The reply is ephemeral.
func (h *Handlers) rulesExclude(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
	req, found := router.FromContext(e.Ctx)
	if !found {
		panic("giftexchangecmd: exclude ran without a request")
	}
	if req.Group == nil {
		return errGuildOnly
	}

	deferErr := req.DeferEphemeral(e.Ctx)
	if deferErr != nil {
		return deferErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	raw, _ := data.OptString(exchangeOptionName)
	id, err := h.exchangeFor(e.Ctx, req, "exclude", raw)
	if err != nil {
		return err
	}

	pair, err := h.rulesResolvePair(e, data)
	if err != nil {
		return err
	}

	both := data.Bool(rulesBothOption)
	addErr := h.svc.AddExclusion(e.Ctx, req.Actor, *req.Group, id, pair.giver, pair.recipient, both)
	if addErr != nil {
		return rulesTranslate(addErr)
	}

	text := fmt.Sprintf(textExcluded, pair.giverMention, pair.recipientMention)
	if both {
		text += textExcludedBoth
	}

	return rulesReply(e, req, text)
}

// rulesUnexclude handles /ge unexclude. The reply is ephemeral.
func (h *Handlers) rulesUnexclude(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
	req, found := router.FromContext(e.Ctx)
	if !found {
		panic("giftexchangecmd: unexclude ran without a request")
	}
	if req.Group == nil {
		return errGuildOnly
	}

	deferErr := req.DeferEphemeral(e.Ctx)
	if deferErr != nil {
		return deferErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	raw, _ := data.OptString(exchangeOptionName)
	id, err := h.exchangeFor(e.Ctx, req, "unexclude", raw)
	if err != nil {
		return err
	}

	pair, err := h.rulesResolvePair(e, data)
	if err != nil {
		return err
	}

	removeErr := h.svc.RemoveExclusion(e.Ctx, req.Actor, *req.Group, id, pair.giver, pair.recipient)
	if removeErr != nil {
		return rulesTranslate(removeErr)
	}

	text := fmt.Sprintf(textUnexcluded, pair.giverMention, pair.recipientMention)
	return rulesReply(e, req, text)
}

func rulesReply(e *handler.CommandEvent, req *router.Request, text string) error {
	editErr := req.EditOriginal(e.Ctx, discord.MessageUpdate{Content: &text})
	if editErr != nil {
		return editErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	return nil
}

// rulesResolvePair resolves the giver and recipient options to people, reporting every
// problem with them together. A bot, or a user Discord did not resolve, is never a participant.
func (h *Handlers) rulesResolvePair(
	e *handler.CommandEvent, data discord.SlashCommandInteractionData,
) (rulesPair, error) {
	var pair rulesPair
	var problems []error

	giver, giverErr := h.rulesResolveUser(e.Ctx, data, rulesGiverOption)
	if giverErr != nil {
		problems = append(problems, giverErr)
	}
	recipient, recipientErr := h.rulesResolveUser(e.Ctx, data, rulesRecipientOption)
	if recipientErr != nil {
		problems = append(problems, recipientErr)
	}
	if len(problems) > 0 {
		return rulesPair{}, errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
	}

	pair = rulesPair{
		giver:            giver.id,
		recipient:        recipient.id,
		giverMention:     giver.mention,
		recipientMention: recipient.mention,
	}
	return pair, nil
}

type rulesUser struct {
	id      giftexchange.PersonID
	mention string
}

func (h *Handlers) rulesResolveUser(
	ctx context.Context, data discord.SlashCommandInteractionData, option string,
) (rulesUser, error) {
	user, found := data.OptUser(option)
	if !found || user.Bot {
		return rulesUser{}, &giftexchange.InputError{Field: option, Reason: rulesNotAPersonReason}
	}

	subject := user.ID.String()
	mention, err := render.Mention(subject)
	if err != nil {
		return rulesUser{}, &giftexchange.InputError{Field: option, Reason: rulesNotAPersonReason}
	}

	person, err := h.svc.ResolvePerson(ctx, giftexchange.IdentityRef{
		Provider: giftexchange.ProviderDiscord,
		Subject:  subject,
	})
	if err != nil {
		return rulesUser{}, fmt.Errorf("resolve %s: %w", option, err)
	}

	return rulesUser{id: person, mention: mention}, nil
}

// rulesTranslate words the module's not-a-participant refusal for the people the command
// named; the module's text is about the caller.
func rulesTranslate(err error) error {
	if errors.Is(err, giftexchange.ErrNotParticipant) {
		return &giftexchange.InputError{Field: "exclusion", Reason: rulesNotParticipantText}
	}

	return fmt.Errorf("change exclusion: %w", err)
}
