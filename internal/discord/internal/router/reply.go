package router

import (
	"context"
	"errors"
	"fmt"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"

	"github.com/HectorVeraO/chatisimo/internal/discord/internal/render"
)

// RESTError is Discord's answer to a reply, reduced to the HTTP status and the Discord error
// code, or to the operation when Discord did not answer. The REST error itself holds the
// request, with the bot token, so it never leaves this file.
type RESTError struct {
	Op     string
	Status int
	Code   int
}

// Error returns the status and code, or the operation, and nothing else.
func (e RESTError) Error() string {
	if e.Status != 0 {
		text := fmt.Sprintf("discord status %d code %d", e.Status, e.Code)
		return text
	}

	text := e.Op + " failed"
	return text
}

func reduce(op string, err error) error {
	var restErr *rest.Error
	if errors.As(err, &restErr) {
		status := 0
		if restErr.Response != nil {
			status = restErr.Response.StatusCode
		}

		return RESTError{Op: op, Status: status, Code: int(restErr.Code)}
	}

	return RESTError{Op: op}
}

func (r *Request) respond(
	ctx context.Context,
	op string,
	kind discord.InteractionResponseType,
	data discord.InteractionResponseData,
	next responseStage,
) error {
	err := r.event.Respond(kind, data, rest.WithCtx(ctx))
	if err != nil {
		return reduce(op, err)
	}

	r.setStage(next)
	return nil
}

// DeferEphemeral answers with a deferred ephemeral callback (type 5, flags 64). Edit the
// result with EditOriginal.
func (r *Request) DeferEphemeral(ctx context.Context) error {
	data := discord.MessageCreate{Flags: discord.MessageFlagEphemeral}
	return r.respond(ctx, "defer ephemeral", discord.InteractionResponseTypeDeferredCreateMessage, data, stageDeferredEphemeral)
}

// DeferUpdate answers a component with a deferred update (type 6). Edit the message with
// EditOriginal.
func (r *Request) DeferUpdate(ctx context.Context) error {
	return r.respond(ctx, "defer update", discord.InteractionResponseTypeDeferredUpdateMessage, nil, stageDeferredUpdate)
}

// Ephemeral answers directly with an ephemeral message (type 4, flags 64).
func (r *Request) Ephemeral(ctx context.Context, msg discord.MessageCreate) error {
	none := render.NoMentions()
	msg.AllowedMentions = &none
	msg.Flags = msg.Flags.Add(discord.MessageFlagEphemeral)

	return r.respond(ctx, "reply", discord.InteractionResponseTypeCreateMessage, msg, stageMessage)
}

// UpdateMessage answers a component by replacing its message (type 7).
func (r *Request) UpdateMessage(ctx context.Context, msg discord.MessageUpdate) error {
	none := render.NoMentions()
	msg.AllowedMentions = &none

	return r.respond(ctx, "update message", discord.InteractionResponseTypeUpdateMessage, msg, stageMessage)
}

// Modal answers with a modal (type 9), which must be the first response.
func (r *Request) Modal(ctx context.Context, modal discord.ModalCreate) error {
	return r.respond(ctx, "open modal", discord.InteractionResponseTypeModal, modal, stageModal)
}

// Autocomplete answers an autocomplete interaction (type 8).
func (r *Request) Autocomplete(ctx context.Context, choices []discord.AutocompleteChoice) error {
	data := discord.AutocompleteResult{Choices: choices}
	return r.respond(ctx, "autocomplete", discord.InteractionResponseTypeAutocompleteResult, data, stageAutocomplete)
}

// EditOriginal edits the message the interaction's first response created or deferred.
func (r *Request) EditOriginal(ctx context.Context, msg discord.MessageUpdate) error {
	none := render.NoMentions()
	msg.AllowedMentions = &none

	_, err := r.event.Client().Rest.UpdateInteractionResponse(
		r.event.ApplicationID(), r.event.Token(), msg, rest.WithCtx(ctx))
	if err != nil {
		return reduce("edit original", err)
	}

	return nil
}

// FollowupPublic posts a followup message visible to the channel (no flags).
func (r *Request) FollowupPublic(ctx context.Context, msg discord.MessageCreate) error {
	msg.Flags = msg.Flags.Remove(discord.MessageFlagEphemeral)
	return r.followup(ctx, msg)
}

// FollowupEphemeral posts a followup message visible to the caller only.
func (r *Request) FollowupEphemeral(ctx context.Context, msg discord.MessageCreate) error {
	msg.Flags = msg.Flags.Add(discord.MessageFlagEphemeral)
	return r.followup(ctx, msg)
}

func (r *Request) followup(ctx context.Context, msg discord.MessageCreate) error {
	none := render.NoMentions()
	msg.AllowedMentions = &none

	_, err := r.event.Client().Rest.CreateFollowupMessage(
		r.event.ApplicationID(), r.event.Token(), msg, rest.WithCtx(ctx))
	if err != nil {
		return reduce("followup", err)
	}

	return nil
}

// Reject shows text to the caller on the channel the interaction's state allows: a new
// ephemeral message before any response, an edit of @original after a deferred ephemeral
// response or after an update of an ephemeral message, and an ephemeral followup otherwise.
// An autocomplete gets no choices and a modal cannot show anything.
func (r *Request) Reject(ctx context.Context, text string) error {
	stage, sourceEphemeral := r.snapshot()

	switch stage {
	case stageNone:
		if r.kind == kindAutocomplete {
			return r.Autocomplete(ctx, []discord.AutocompleteChoice{})
		}
		return r.Ephemeral(ctx, discord.MessageCreate{Content: text})
	case stageDeferredEphemeral:
		return r.replaceOriginal(ctx, text)
	case stageDeferredUpdate:
		if sourceEphemeral {
			return r.replaceOriginal(ctx, text)
		}
		return r.FollowupEphemeral(ctx, discord.MessageCreate{Content: text})
	case stageMessage:
		return r.FollowupEphemeral(ctx, discord.MessageCreate{Content: text})
	case stageModal, stageAutocomplete:
		return nil
	default:
		panic(fmt.Sprintf("router: unknown response stage %d", stage))
	}
}

func (r *Request) replaceOriginal(ctx context.Context, text string) error {
	embeds := []discord.Embed{}
	components := []discord.LayoutComponent{}

	return r.EditOriginal(ctx, discord.MessageUpdate{
		Content:    &text,
		Embeds:     &embeds,
		Components: &components,
	})
}
