package giftexchangecmd

import (
	"context"
	"fmt"
	"strings"

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
	r.Component(customIDPattern(actionPreferencesEdit), h.preferencesEditButton)
	r.Modal(customIDPattern(actionPreferencesModal), h.preferencesModal)
}

// preferencesCommand handles /ge preferences: opens a prefilled modal to edit gift
// preferences, or clears them directly when clear is true. Replies are always ephemeral.
func (h *Handlers) preferencesCommand(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
	req, found := router.FromContext(e.Ctx)
	if !found {
		panic("giftexchangecmd: preferences ran without a request")
	}

	clear, _ := data.OptBool(clearOptionName)
	if clear {
		deferErr := req.DeferEphemeral(e.Ctx)
		if deferErr != nil {
			return deferErr //nolint:wrapcheck // the router reduces REST errors to status and code
		}

		rawExchange, _ := data.OptString(exchangeOptionName)
		filter := filterFor("preferences")
		candidates, listed, err := h.listCandidates(e.Ctx, req, filter)
		if err != nil {
			return err
		}
		id, err := resolveExchange(rawExchange, candidates, filter.preferState)
		if err != nil {
			return err
		}
		req.SetExchange(id)
		h.setExchangeLocale(e.Ctx, req, id, listed)

		return h.clearPreferences(e, req, id, candidates)
	}

	rawExchange, _ := data.OptString(exchangeOptionName)
	filter := filterFor("preferences")
	candidates, listed, err := h.listCandidates(e.Ctx, req, filter)
	if err != nil {
		return err
	}
	id, err := resolveExchange(rawExchange, candidates, filter.preferState)
	if err != nil {
		return err
	}
	req.SetExchange(id)
	h.setExchangeLocale(e.Ctx, req, id, listed)

	current, err := h.svc.Preferences(e.Ctx, req.Actor, id)
	if err != nil {
		return fmt.Errorf("read preferences: %w", err)
	}

	if editableErr := h.ensureEditable(e.Ctx, req, id, candidates); editableErr != nil {
		return editableErr
	}

	c := h.catalogFor(req)
	modal := h.buildPreferencesModal(c, id, current)
	modalErr := req.Modal(e.Ctx, modal)
	if modalErr != nil {
		return modalErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	return nil
}

func (h *Handlers) preferencesEditButton(e *handler.ComponentEvent) error {
	req, found := router.FromContext(e.Ctx)
	if !found {
		panic("giftexchangecmd: preferences edit ran without a request")
	}

	parsed, err := parseCustomID(e.Data.CustomID())
	if err != nil {
		return err
	}
	if parsed.Action != actionPreferencesEdit {
		return errBadCustomID
	}
	id := parsed.Exchange
	req.SetExchange(id)

	filter := filterFor("preferences")
	candidates, listed, err := h.listCandidates(e.Ctx, req, filter)
	if err != nil {
		return err
	}
	h.setExchangeLocale(e.Ctx, req, id, listed)

	current, err := h.svc.Preferences(e.Ctx, req.Actor, id)
	if err != nil {
		return fmt.Errorf("read preferences: %w", err)
	}

	if editableErr := h.ensureEditable(e.Ctx, req, id, candidates); editableErr != nil {
		return editableErr
	}

	c := h.catalogFor(req)
	modal := h.buildPreferencesModal(c, id, current)
	modalErr := req.Modal(e.Ctx, modal)
	if modalErr != nil {
		return modalErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	return nil
}

func (h *Handlers) ensureEditable(
	ctx context.Context,
	req *router.Request,
	id giftexchange.ExchangeID,
	candidates []giftexchange.ExchangeSummary,
) error {
	for _, c := range candidates {
		if c.ID == id {
			return nil
		}
	}

	if req.Group != nil {
		view, err := h.svc.Exchange(ctx, req.Actor, *req.Group, id)
		if err == nil {
			isEditable := view.State == giftexchange.StateOpen || view.State == giftexchange.StateDrawn
			if !isEditable {
				return &giftexchange.StateError{Op: "set_preferences", State: view.State}
			}
			return nil
		}
	}

	listed, err := h.svc.ExchangesFor(ctx, req.Actor, nil)
	if err != nil {
		return fmt.Errorf("list exchanges: %w", err)
	}
	for _, s := range listed {
		isMatching := s.ID == id
		if !isMatching {
			continue
		}
		isEditable := s.State == giftexchange.StateOpen || s.State == giftexchange.StateDrawn
		if !isEditable {
			return &giftexchange.StateError{Op: "set_preferences", State: s.State}
		}
		return nil
	}

	// ExchangesFor(nil) omits cancelled exchanges; since h.svc.Preferences already confirmed
	// the caller is an active participant of id, an omitted exchange is in StateCancelled.
	return &giftexchange.StateError{Op: "set_preferences", State: giftexchange.StateCancelled}
}

func (h *Handlers) preferencesModal(e *handler.ModalEvent) error {
	req, found := router.FromContext(e.Ctx)
	if !found {
		panic("giftexchangecmd: preferences modal ran without a request")
	}

	parsed, err := parseCustomID(e.Data.CustomID)
	if err != nil {
		return err
	}
	if parsed.Action != actionPreferencesModal {
		return errBadCustomID
	}
	id := parsed.Exchange
	req.SetExchange(id)

	deferErr := req.DeferEphemeral(e.Ctx)
	if deferErr != nil {
		return deferErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	filter := filterFor("preferences")
	candidates, listed, err := h.listCandidates(e.Ctx, req, filter)
	if err != nil {
		return err
	}
	h.setExchangeLocale(e.Ctx, req, id, listed)

	rawLikes := e.Data.Text(likesOptionName)
	rawDislikes := e.Data.Text(dislikesOptionName)
	rawDetails := e.Data.Text(detailsOptionName)

	allTrimmedEmpty := strings.TrimSpace(rawLikes) == "" &&
		strings.TrimSpace(rawDislikes) == "" &&
		strings.TrimSpace(rawDetails) == ""

	var toSave giftexchange.Preferences
	if !allTrimmedEmpty {
		toSave = giftexchange.Preferences{
			Likes:    rawLikes,
			Dislikes: rawDislikes,
			Details:  rawDetails,
		}
	}

	saved, err := h.svc.SetPreferences(e.Ctx, req.Actor, id, toSave)
	if err != nil {
		return fmt.Errorf("save preferences: %w", err)
	}

	summary, nameErr := h.questionExchangeSummary(e.Ctx, req, id, candidates)
	if nameErr != nil {
		return nameErr
	}

	c := h.catalogFor(req)
	editBtn := discord.NewSecondaryButton(
		c.Get(textButtonEditPreferences),
		formatCustomID(customID{Action: actionPreferencesEdit, Exchange: id}),
	)
	components := []discord.LayoutComponent{discord.NewActionRow(editBtn)}

	if saved.IsEmpty() {
		content := c.Format(textPreferencesCleared, render.Bold(summary.Name))
		embeds := []discord.Embed{}
		editErr := req.EditOriginal(e.Ctx, discord.MessageUpdate{
			Content:    &content,
			Embeds:     &embeds,
			Components: &components,
		})
		if editErr != nil {
			return editErr //nolint:wrapcheck // the router reduces REST errors to status and code
		}
		return nil
	}

	content := c.Format(textPreferencesSaved, render.Bold(summary.Name))
	embed := withPreferences(c, render.Embed(c.Format(textTitlePreferences, render.Text(summary.Name)), ""), saved)
	embeds := []discord.Embed{embed}
	editErr := req.EditOriginal(e.Ctx, discord.MessageUpdate{
		Content:    &content,
		Embeds:     &embeds,
		Components: &components,
	})
	if editErr != nil {
		return editErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	return nil
}

func (h *Handlers) buildPreferencesModal(
	c Catalog, id giftexchange.ExchangeID, current giftexchange.Preferences,
) discord.ModalCreate {
	likesInput := discord.TextInputComponent{
		CustomID:  likesOptionName,
		Style:     discord.TextInputStyleParagraph,
		MaxLength: giftexchange.MaxPreferenceRunes,
		Required:  false,
		Value:     current.Likes,
	}
	dislikesInput := discord.TextInputComponent{
		CustomID:  dislikesOptionName,
		Style:     discord.TextInputStyleParagraph,
		MaxLength: giftexchange.MaxPreferenceRunes,
		Required:  false,
		Value:     current.Dislikes,
	}
	detailsInput := discord.TextInputComponent{
		CustomID:  detailsOptionName,
		Style:     discord.TextInputStyleParagraph,
		MaxLength: giftexchange.MaxPreferenceRunes,
		Required:  false,
		Value:     current.Details,
	}

	return discord.ModalCreate{
		CustomID: formatCustomID(customID{Action: actionPreferencesModal, Exchange: id}),
		Title:    c.Get(textPreferencesModalTitle),
	}.AddLabel(c.Get(textFieldLikes), likesInput).
		AddLabel(c.Get(textFieldDislikes), dislikesInput).
		AddLabel(c.Get(textFieldDetails), detailsInput)
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

	summary, nameErr := h.questionExchangeSummary(e.Ctx, req, id, candidates)
	if nameErr != nil {
		return nameErr
	}

	c := h.catalogFor(req)
	content := c.Format(textPreferencesCleared, render.Bold(summary.Name))
	embeds := []discord.Embed{}
	editErr := req.EditOriginal(e.Ctx, discord.MessageUpdate{Content: &content, Embeds: &embeds})
	if editErr != nil {
		return editErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	return nil
}

// withPreferences appends the non-empty preference fields to embed with their values escaped.
func withPreferences(c Catalog, embed discord.Embed, prefs giftexchange.Preferences) discord.Embed {
	if prefs.Likes != "" {
		embed = render.WithField(embed, c.Get(textFieldLikes), render.Text(prefs.Likes))
	}
	if prefs.Dislikes != "" {
		embed = render.WithField(embed, c.Get(textFieldDislikes), render.Text(prefs.Dislikes))
	}
	if prefs.Details != "" {
		embed = render.WithField(embed, c.Get(textFieldDetails), render.Text(prefs.Details))
	}
	return embed
}
