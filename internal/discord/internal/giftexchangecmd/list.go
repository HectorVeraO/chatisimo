package giftexchangecmd

import (
	"fmt"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"

	"github.com/HectorVeraO/chatisimo/internal/discord/internal/render"
	"github.com/HectorVeraO/chatisimo/internal/discord/internal/router"
	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

const (
	textListTitle = "Exchanges in this server"
	textListEmpty = "No exchanges yet. Create one with /ge create."
	textListLine  = "- %s - %s - participants: %d - you are %s"

	// maxListDescriptionRunes is below Discord's limit on an embed description.
	maxListDescriptionRunes = 4000
)

func (h *Handlers) registerList(r *router.Router) {
	h.verb(r, "list", h.list, nil)
}

// list shows one line per exchange of the guild: name, state, participant count and the
// caller's role. The reply is ephemeral.
func (h *Handlers) list(_ discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
	req, found := router.FromContext(e.Ctx)
	if !found {
		panic("giftexchangecmd: list ran without a request")
	}
	if req.Group == nil {
		return errGuildOnly
	}

	deferErr := req.DeferEphemeral(e.Ctx)
	if deferErr != nil {
		return deferErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	listed, err := h.svc.ExchangesFor(e.Ctx, req.Actor, req.Group)
	if err != nil {
		return fmt.Errorf("list exchanges: %w", err)
	}
	sortByName(listed)

	embeds := []discord.Embed{render.Embed(textListTitle, listDescription(listed))}
	editErr := req.EditOriginal(e.Ctx, discord.MessageUpdate{Embeds: &embeds})
	if editErr != nil {
		return editErr //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	return nil
}

func listDescription(listed []giftexchange.ExchangeSummary) string {
	if len(listed) == 0 {
		return textListEmpty
	}

	lines := make([]string, 0, len(listed))
	for i, summary := range listed {
		if i == maxListedLines {
			lines = append(lines, fmt.Sprintf(textAndMore, len(listed)-maxListedLines))
			break
		}
		lines = append(lines, fmt.Sprintf(textListLine,
			render.Bold(summary.Name), summary.State, summary.ParticipantCount, roleText(summary)))
	}

	text := render.Truncate(strings.Join(lines, "\n"), maxListDescriptionRunes, truncationMark)
	return text
}

func roleText(s giftexchange.ExchangeSummary) string {
	switch {
	case s.CallerIsOrganizer && s.CallerIsParticipant:
		return textRoleBoth
	case s.CallerIsOrganizer:
		return textRoleOrganizer
	case s.CallerIsParticipant:
		return textRoleParticipant
	default:
		return textRoleNone
	}
}
