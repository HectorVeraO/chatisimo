package giftexchangecmd

import (
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"

	"github.com/HectorVeraO/chatisimo/internal/discord/internal/router"
)

// helpText is the lead line followed by one line per verb, in the order of the trees.
func helpText() string {
	lines := []string{
		textHelpLead,
		textHelpCreate,
		textHelpList,
		textHelpJoin,
		textHelpLeave,
		textHelpRemove,
		textHelpRules,
		textHelpExclude,
		textHelpUnexclude,
		textHelpRename,
		textHelpDraw,
		textHelpRecipient,
		textHelpGiver,
		textHelpAsk,
		textHelpQuestions,
		textHelpPreferences,
		textHelpReveal,
		textHelpCancel,
		textHelpHelp,
	}

	text := strings.Join(lines, "\n")
	return text
}

func (h *Handlers) registerHelp(r *router.Router) {
	h.verb(r, "help", h.help, nil)
}

// help answers directly with an ephemeral message: it calls the module for nothing.
func (h *Handlers) help(_ discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
	req, found := router.FromContext(e.Ctx)
	if !found {
		panic("giftexchangecmd: help ran without a request")
	}

	err := req.Ephemeral(e.Ctx, discord.MessageCreate{Content: helpText()})
	if err != nil {
		return err //nolint:wrapcheck // the router reduces REST errors to status and code
	}

	return nil
}
