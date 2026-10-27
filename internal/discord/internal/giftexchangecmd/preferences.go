package giftexchangecmd

import (
	"fmt"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"

	"github.com/HectorVeraO/chatisimo/internal/discord/internal/render"
	"github.com/HectorVeraO/chatisimo/internal/discord/internal/router"
	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

const (
	likesOptionName    = "likes"
	dislikesOptionName = "dislikes"
	detailsOptionName  = "details"
	clearOptionName    = "clear"
)

func (h *Handlers) registerPreferences(r *router.Router) {
	h.verb(r, "preferences", h.preferencesCommand, h.autocompleteFor("preferences"))
}

// preferencesCommand handles /ge preferences: sets, views, or clears the caller's own gift
// preferences for their giver. Replies are always ephemeral.
func (h *Handlers) preferencesCommand(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
	req, found := router.FromContext(e.Ctx)
	if !found {
		panic("giftexchangecmd: preferences ran without a request")
	}

	deferErr := req.DeferEphemeral(e.Ctx)
	if deferErr != nil {
		return deferErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	rawExchange, _ := data.OptString(exchangeOptionName)
	candidates, err := h.candidates(e.Ctx, req, filterFor("preferences"))
	if err != nil {
		return err
	}
	id, err := resolveExchange(rawExchange, candidates)
	if err != nil {
		return err
	}
	req.SetExchange(id)

	clear, _ := data.OptBool(clearOptionName)
	if clear {
		return h.clearPreferences(e, req, id, candidates)
	}

	likes, hasLikes := data.OptString(likesOptionName)
	dislikes, hasDislikes := data.OptString(dislikesOptionName)
	details, hasDetails := data.OptString(detailsOptionName)
	isViewOnly := !hasLikes && !hasDislikes && !hasDetails
	if isViewOnly {
		return h.viewPreferences(e, req, id, candidates)
	}

	current, err := h.svc.Preferences(e.Ctx, req.Actor, id)
	if err != nil {
		return fmt.Errorf("read preferences: %w", err)
	}
	if hasLikes {
		current.Likes = likes
	}
	if hasDislikes {
		current.Dislikes = dislikes
	}
	if hasDetails {
		current.Details = details
	}

	saved, err := h.svc.SetPreferences(e.Ctx, req.Actor, id, current)
	if err != nil {
		return fmt.Errorf("save preferences: %w", err)
	}

	name, nameErr := h.questionExchangeName(e.Ctx, req, id, candidates)
	if nameErr != nil {
		return nameErr
	}

	content := fmt.Sprintf(textPreferencesSaved, render.Bold(name))
	embed := withPreferences(render.Embed(fmt.Sprintf(textTitlePreferences, render.Text(name)), ""), saved)
	embeds := []discord.Embed{embed}
	editErr := req.EditOriginal(e.Ctx, discord.MessageUpdate{Content: &content, Embeds: &embeds})
	if editErr != nil {
		return editErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	return nil
}

func (h *Handlers) clearPreferences(
	e *handler.CommandEvent,
	req *router.Request,
	id giftexchange.ExchangeID,
	candidates []giftexchange.ExchangeSummary,
) error {
	_, err := h.svc.SetPreferences(e.Ctx, req.Actor, id, giftexchange.Preferences{})
	if err != nil {
		return fmt.Errorf("clear preferences: %w", err)
	}

	name, nameErr := h.questionExchangeName(e.Ctx, req, id, candidates)
	if nameErr != nil {
		return nameErr
	}

	content := fmt.Sprintf(textPreferencesCleared, render.Bold(name))
	embeds := []discord.Embed{}
	editErr := req.EditOriginal(e.Ctx, discord.MessageUpdate{Content: &content, Embeds: &embeds})
	if editErr != nil {
		return editErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	return nil
}

func (h *Handlers) viewPreferences(
	e *handler.CommandEvent,
	req *router.Request,
	id giftexchange.ExchangeID,
	candidates []giftexchange.ExchangeSummary,
) error {
	prefs, err := h.svc.Preferences(e.Ctx, req.Actor, id)
	if err != nil {
		return fmt.Errorf("read preferences: %w", err)
	}

	name, nameErr := h.questionExchangeName(e.Ctx, req, id, candidates)
	if nameErr != nil {
		return nameErr
	}

	if prefs.IsEmpty() {
		content := fmt.Sprintf(textPreferencesNone, render.Bold(name))
		editErr := req.EditOriginal(e.Ctx, discord.MessageUpdate{Content: &content})
		if editErr != nil {
			return editErr //nolint:wrapcheck // the router reduces REST errors to status and code
		}
		return nil
	}

	embed := withPreferences(render.Embed(fmt.Sprintf(textTitlePreferences, render.Text(name)), ""), prefs)
	embeds := []discord.Embed{embed}
	editErr := req.EditOriginal(e.Ctx, discord.MessageUpdate{Embeds: &embeds})
	if editErr != nil {
		return editErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	return nil
}

// withPreferences appends the non-empty preference fields to embed with their values escaped.
func withPreferences(embed discord.Embed, prefs giftexchange.Preferences) discord.Embed {
	if prefs.Likes != "" {
		embed = render.WithField(embed, textFieldLikes, render.Text(prefs.Likes))
	}
	if prefs.Dislikes != "" {
		embed = render.WithField(embed, textFieldDislikes, render.Text(prefs.Dislikes))
	}
	if prefs.Details != "" {
		embed = render.WithField(embed, textFieldDetails, render.Text(prefs.Details))
	}
	return embed
}
