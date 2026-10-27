package giftexchangecmd

import (
	"github.com/disgoorg/disgo/discord"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

const (
	// groupCommand and groupName are the /chatisimo giftexchange <verb> form.
	groupCommand = "chatisimo"
	groupName    = "giftexchange"
	// shortCommand is the /ge <verb> form.
	shortCommand = "ge"

	maxExchangeOptionRunes = 100
	maxTimeOptionRunes     = 64
	exchangeOptionName     = "exchange"
)

// commandRoots are the command path prefixes every verb is registered under.
var commandRoots = []string{"/" + groupCommand + "/" + groupName, "/" + shortCommand}

// verbSpec describes one verb of the command trees.
type verbSpec struct {
	name        string
	description string
	// guildOnly verbs answer "works only in a server" in a DM.
	guildOnly bool
	options   func() []discord.ApplicationCommandOption
}

func intPtr(n int) *int {
	return &n
}

func exchangeOption() discord.ApplicationCommandOption {
	return discord.ApplicationCommandOptionString{
		Name:         exchangeOptionName,
		Description:  "The exchange. Optional when only one applies",
		Autocomplete: true,
		MaxLength:    intPtr(maxExchangeOptionRunes),
	}
}

func userOption(name, description string) discord.ApplicationCommandOption {
	return discord.ApplicationCommandOptionUser{Name: name, Description: description, Required: true}
}

func textOption(name, description string, minRunes, maxRunes int, required bool) discord.ApplicationCommandOption {
	option := discord.ApplicationCommandOptionString{
		Name:        name,
		Description: description,
		Required:    required,
		MaxLength:   intPtr(maxRunes),
	}
	if minRunes > 0 {
		option.MinLength = intPtr(minRunes)
	}

	return option
}

func justExchange() []discord.ApplicationCommandOption {
	return []discord.ApplicationCommandOption{exchangeOption()}
}

func noOptions() []discord.ApplicationCommandOption {
	return nil
}

// verbs lists the verbs in the order help shows them. The options of each keep required ones
// before optional ones and the exchange option last.
func verbs() []verbSpec {
	specs := []verbSpec{
		{
			name: "create", description: "Create an exchange and become its organizer", guildOnly: true,
			options: func() []discord.ApplicationCommandOption {
				return []discord.ApplicationCommandOption{
					textOption("name", "Name of the exchange", 1, giftexchange.MaxNameRunes, true),
					textOption("draw-at", "When to draw, such as in 3d or 2026-12-20 18:00", 0, maxTimeOptionRunes, false),
					textOption("reveal-at", "When to reveal, such as in 10d", 0, maxTimeOptionRunes, false),
				}
			},
		},
		{name: "list", description: "List the exchanges of this server", guildOnly: true, options: noOptions},
		{name: "join", description: "Join an exchange", guildOnly: true, options: justExchange},
		{name: "leave", description: "Leave an exchange", guildOnly: true, options: justExchange},
		{
			name: "remove", description: "Remove a participant (organizer)", guildOnly: true,
			options: func() []discord.ApplicationCommandOption {
				return []discord.ApplicationCommandOption{
					userOption("participant", "The participant to remove"),
					exchangeOption(),
				}
			},
		},
		{
			name: "rules", description: "See the rules of an exchange, and change them if you organize it",
			guildOnly: true, options: justExchange,
		},
		{
			name: "exclude", description: "Stop one person from drawing another (organizer)", guildOnly: true,
			options: func() []discord.ApplicationCommandOption {
				return []discord.ApplicationCommandOption{
					userOption("giver", "The person who must not draw the recipient"),
					userOption("recipient", "The person the giver must not draw"),
					discord.ApplicationCommandOptionBool{Name: "both", Description: "Also exclude the reverse"},
					exchangeOption(),
				}
			},
		},
		{
			name: "unexclude", description: "Undo an exclusion (organizer)", guildOnly: true,
			options: func() []discord.ApplicationCommandOption {
				return []discord.ApplicationCommandOption{
					userOption("giver", "The giver of the exclusion"),
					userOption("recipient", "The recipient of the exclusion"),
					exchangeOption(),
				}
			},
		},
		{
			name: "rename", description: "Rename an exchange (organizer)", guildOnly: true,
			options: func() []discord.ApplicationCommandOption {
				return []discord.ApplicationCommandOption{
					textOption("name", "The new name", 1, giftexchange.MaxNameRunes, true),
					exchangeOption(),
				}
			},
		},
		{name: "draw", description: "Run the draw now (organizer)", guildOnly: true, options: justExchange},
		{name: "recipient", description: "See who you give a gift to", options: justExchange},
		{name: "giver", description: "See who gave you a gift, after the reveal", options: justExchange},
		{
			name: "ask", description: "Ask your recipient an anonymous question",
			options: func() []discord.ApplicationCommandOption {
				return []discord.ApplicationCommandOption{
					textOption("question", "Your question", 1, giftexchange.MaxQuestionRunes, true),
					exchangeOption(),
				}
			},
		},
		{name: "questions", description: "See the questions you asked and received", options: justExchange},
		{
			name: "preferences", description: "Set or view your gift preferences for your giver",
			options: func() []discord.ApplicationCommandOption {
				return []discord.ApplicationCommandOption{
					textOption(
						"likes",
						"Likes and hobbies, such as coffee, books or board games",
						1,
						giftexchange.MaxPreferenceRunes,
						false,
					),
					textOption(
						"dislikes",
						"Dislikes or things to avoid, such as allergies",
						1,
						giftexchange.MaxPreferenceRunes,
						false,
					),
					textOption(
						"details",
						"Helpful details, such as shirt size or dietary needs",
						1,
						giftexchange.MaxPreferenceRunes,
						false,
					),
					discord.ApplicationCommandOptionBool{
						Name:        "clear",
						Description: "Clear your gift preferences",
					},
					exchangeOption(),
				}
			},
		},
		{name: "reveal", description: "Reveal the givers now (organizer)", guildOnly: true, options: justExchange},
		{name: "cancel", description: "Cancel an exchange (organizer)", guildOnly: true, options: justExchange},
		{name: "help", description: "Show the commands", options: noOptions},
	}

	return specs
}

func subcommands() []discord.ApplicationCommandOptionSubCommand {
	specs := verbs()
	commands := make([]discord.ApplicationCommandOptionSubCommand, 0, len(specs))
	for _, spec := range specs {
		commands = append(commands, discord.ApplicationCommandOptionSubCommand{
			Name:        spec.name,
			Description: spec.description,
			Options:     spec.options(),
		})
	}

	return commands
}

// Commands returns the two command trees, built from one list of verbs so they cannot drift:
// /chatisimo with the giftexchange group, and /ge with the verbs directly. Both work in
// servers and in the bot's DM.
func Commands() []discord.ApplicationCommandCreate {
	contexts := []discord.InteractionContextType{
		discord.InteractionContextTypeGuild,
		discord.InteractionContextTypeBotDM,
	}
	integrations := []discord.ApplicationIntegrationType{discord.ApplicationIntegrationTypeGuildInstall}

	grouped := discord.SlashCommandCreate{
		Name:        groupCommand,
		Description: "Chatisimo commands",
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionSubCommandGroup{
				Name:        groupName,
				Description: "Secret gift exchanges",
				Options:     subcommands(),
			},
		},
		Contexts:         contexts,
		IntegrationTypes: integrations,
	}

	short := discord.SlashCommandCreate{
		Name:             shortCommand,
		Description:      "Secret gift exchanges",
		Contexts:         contexts,
		IntegrationTypes: integrations,
	}
	for _, command := range subcommands() {
		short.Options = append(short.Options, command)
	}

	return []discord.ApplicationCommandCreate{grouped, short}
}
