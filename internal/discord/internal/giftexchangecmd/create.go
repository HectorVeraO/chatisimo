package giftexchangecmd

import (
	"errors"
	"fmt"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"

	"github.com/HectorVeraO/chatisimo/internal/discord/internal/render"
	"github.com/HectorVeraO/chatisimo/internal/discord/internal/router"
	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

const (
	createNameOption     = "name"
	createDrawAtOption   = "draw-at"
	createRevealAtOption = "reveal-at"
)

func (h *Handlers) registerCreate(r *router.Router) {
	h.verb(r, "create", h.createCommand, nil)
}

// createCommand handles /ge create. The organizer gets an ephemeral confirmation and the
// channel gets the public card of the exchange.
func (h *Handlers) createCommand(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
	req := membershipRequestFrom(e.Ctx, "create")
	if req.Group == nil {
		return errGuildOnly
	}

	deferErr := req.DeferEphemeral(e.Ctx)
	if deferErr != nil {
		return deferErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	rules, err := h.membershipTimeRules(data)
	if err != nil {
		return err
	}

	view, err := h.svc.CreateExchange(e.Ctx, req.Actor, giftexchange.CreateExchangeRequest{
		Group: *req.Group,
		Name:  data.String(createNameOption),
		Rules: rules,
	})
	if err != nil {
		return fmt.Errorf("create exchange: %w", err)
	}
	req.SetExchange(view.ID)

	// The exchange exists from here on, so a failed confirmation must not skip the public card.
	editErr := membershipEdit(e.Ctx, req, fmt.Sprintf(textCreated, render.Bold(view.Name)))

	card, err := membershipCard(req, view)
	if err != nil {
		return err
	}
	followErr := req.FollowupPublic(e.Ctx, card)
	if followErr != nil {
		return followErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	return editErr
}

// membershipTimeRules turns the draw-at and reveal-at options into rule changes. An empty
// option sets no rule. Every unreadable time returns errBadTime and creates nothing.
func (h *Handlers) membershipTimeRules(data discord.SlashCommandInteractionData) ([]giftexchange.RuleChange, error) {
	options := []struct {
		option string
		rule   giftexchange.RuleName
	}{
		{createDrawAtOption, giftexchange.RuleDrawAt},
		{createRevealAtOption, giftexchange.RuleRevealAt},
	}

	var rules []giftexchange.RuleChange
	var problems []error
	for _, item := range options {
		raw, _ := data.OptString(item.option)
		when, isEmpty, err := parseWhen(raw, h.now(), h.loc)
		if err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", item.option, err))

			continue
		}
		if isEmpty {
			continue
		}

		rules = append(rules, giftexchange.RuleChange{Name: item.rule, Raw: when.UTC().Format(time.RFC3339)})
	}

	if len(problems) > 0 {
		return nil, fmt.Errorf("read times: %w", errors.Join(problems...))
	}

	return rules, nil
}

// membershipCard builds the public card of a new exchange.
func membershipCard(req *router.Request, view giftexchange.ExchangeView) (discord.MessageCreate, error) {
	organizer, err := render.Mention(req.UserID().String())
	if err != nil {
		return discord.MessageCreate{}, fmt.Errorf("mention organizer: %w", err)
	}

	embed := render.Embed(view.Name, fmt.Sprintf(textCardBody, organizer))
	embed = render.WithField(embed, textFieldDraw, membershipWhenText(view.DrawAt))
	embed = render.WithField(embed, textFieldReveal, membershipWhenText(view.RevealAt))

	row := discord.NewActionRow(
		discord.NewPrimaryButton(textButtonJoin, formatCustomID(customID{Action: actionJoin, Exchange: view.ID})),
		discord.NewSecondaryButton(textButtonLeave, formatCustomID(customID{Action: actionLeave, Exchange: view.ID})),
		discord.NewSecondaryButton(textButtonRules, formatCustomID(customID{Action: actionRules, Exchange: view.ID})),
	)

	none := render.NoMentions()
	card := discord.MessageCreate{
		Embeds:          []discord.Embed{embed},
		Components:      []discord.LayoutComponent{row},
		AllowedMentions: &none,
	}
	return card, nil
}

func membershipWhenText(t *time.Time) string {
	if t == nil {
		return textManual
	}

	return render.TimestampFull(*t)
}
