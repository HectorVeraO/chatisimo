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
	name          string
	descriptionID MsgID
	// guildOnly verbs answer "works only in a server" in a DM.
	guildOnly bool
	options   func() []discord.ApplicationCommandOption
}

func intPtr(n int) *int {
	return &n
}

func localizedText(id MsgID) (string, map[discord.Locale]string) {
	en := CatalogFor(LocaleENUS).Get(id)
	es := CatalogFor(LocaleESCL).Get(id)
	localizations := map[discord.Locale]string{
		discord.LocaleEnglishUS:    en,
		discord.LocaleSpanishLATAM: es,
		discord.LocaleSpanishES:    es,
	}

	return en, localizations
}

func exchangeOption() discord.ApplicationCommandOption {
	desc, loc := localizedText(textOptExchangeDesc)

	return discord.ApplicationCommandOptionString{
		Name:                     exchangeOptionName,
		Description:              desc,
		DescriptionLocalizations: loc,
		Autocomplete:             true,
		MaxLength:                intPtr(maxExchangeOptionRunes),
	}
}

func userOption(name string, descriptionID MsgID) discord.ApplicationCommandOption {
	desc, loc := localizedText(descriptionID)

	return discord.ApplicationCommandOptionUser{
		Name:                     name,
		Description:              desc,
		DescriptionLocalizations: loc,
		Required:                 true,
	}
}

func boolOption(name string, descriptionID MsgID) discord.ApplicationCommandOption {
	desc, loc := localizedText(descriptionID)

	return discord.ApplicationCommandOptionBool{
		Name:                     name,
		Description:              desc,
		DescriptionLocalizations: loc,
	}
}

func textOption(name string, descriptionID MsgID, minRunes, maxRunes int, required bool) discord.ApplicationCommandOption {
	desc, loc := localizedText(descriptionID)
	option := discord.ApplicationCommandOptionString{
		Name:                     name,
		Description:              desc,
		DescriptionLocalizations: loc,
		Required:                 required,
		MaxLength:                intPtr(maxRunes),
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
			name: "create", descriptionID: textCmdCreateDesc, guildOnly: true,
			options: func() []discord.ApplicationCommandOption {
				return []discord.ApplicationCommandOption{
					textOption("name", textOptCreateNameDesc, 1, giftexchange.MaxNameRunes, true),
					textOption("draw-at", textOptCreateDrawAtDesc, 0, maxTimeOptionRunes, false),
					textOption("reveal-at", textOptCreateRevealAtDesc, 0, maxTimeOptionRunes, false),
				}
			},
		},
		{name: "list", descriptionID: textCmdListDesc, guildOnly: true, options: noOptions},
		{name: "join", descriptionID: textCmdJoinDesc, guildOnly: true, options: justExchange},
		{name: "leave", descriptionID: textCmdLeaveDesc, guildOnly: true, options: justExchange},
		{
			name: "remove", descriptionID: textCmdRemoveDesc, guildOnly: true,
			options: func() []discord.ApplicationCommandOption {
				return []discord.ApplicationCommandOption{
					userOption("participant", textOptRemoveParticipantDesc),
					exchangeOption(),
				}
			},
		},
		{
			name: "rules", descriptionID: textCmdRulesDesc,
			guildOnly: true, options: justExchange,
		},
		{
			name: "exclude", descriptionID: textCmdExcludeDesc, guildOnly: true,
			options: func() []discord.ApplicationCommandOption {
				return []discord.ApplicationCommandOption{
					userOption("giver", textOptExcludeGiverDesc),
					userOption("recipient", textOptExcludeRecipientDesc),
					boolOption("both", textOptExcludeBothDesc),
					exchangeOption(),
				}
			},
		},
		{
			name: "unexclude", descriptionID: textCmdUnexcludeDesc, guildOnly: true,
			options: func() []discord.ApplicationCommandOption {
				return []discord.ApplicationCommandOption{
					userOption("giver", textOptUnexcludeGiverDesc),
					userOption("recipient", textOptUnexcludeRecipientDesc),
					exchangeOption(),
				}
			},
		},
		{
			name: "rename", descriptionID: textCmdRenameDesc, guildOnly: true,
			options: func() []discord.ApplicationCommandOption {
				return []discord.ApplicationCommandOption{
					textOption("name", textOptRenameNameDesc, 1, giftexchange.MaxNameRunes, true),
					exchangeOption(),
				}
			},
		},
		{name: "draw", descriptionID: textCmdDrawDesc, guildOnly: true, options: justExchange},
		{name: "recipient", descriptionID: textCmdRecipientDesc, options: justExchange},
		{name: "giver", descriptionID: textCmdGiverDesc, options: justExchange},
		{
			name: "ask", descriptionID: textCmdAskDesc,
			options: func() []discord.ApplicationCommandOption {
				return []discord.ApplicationCommandOption{
					textOption("question", textOptAskQuestionDesc, 1, giftexchange.MaxQuestionRunes, true),
					exchangeOption(),
				}
			},
		},
		{name: "questions", descriptionID: textCmdQuestionsDesc, options: justExchange},
		{
			name: "preferences", descriptionID: textCmdPreferencesDesc,
			options: func() []discord.ApplicationCommandOption {
				return []discord.ApplicationCommandOption{
					boolOption("clear", textOptPreferencesClearDesc),
					exchangeOption(),
				}
			},
		},
		{name: "reveal", descriptionID: textCmdRevealDesc, guildOnly: true, options: justExchange},
		{name: "cancel", descriptionID: textCmdCancelDesc, guildOnly: true, options: justExchange},
		{name: "help", descriptionID: textCmdHelpDesc, options: noOptions},
	}

	return specs
}

func subcommands() []discord.ApplicationCommandOptionSubCommand {
	specs := verbs()
	commands := make([]discord.ApplicationCommandOptionSubCommand, 0, len(specs))
	for _, spec := range specs {
		desc, loc := localizedText(spec.descriptionID)
		commands = append(commands, discord.ApplicationCommandOptionSubCommand{
			Name:                     spec.name,
			Description:              desc,
			DescriptionLocalizations: loc,
			Options:                  spec.options(),
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

	chatisimoDesc, chatisimoLoc := localizedText(textCmdChatisimoDesc)
	geDesc, geLoc := localizedText(textCmdGiftexchangeDesc)

	grouped := discord.SlashCommandCreate{
		Name:                     groupCommand,
		Description:              chatisimoDesc,
		DescriptionLocalizations: chatisimoLoc,
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionSubCommandGroup{
				Name:                     groupName,
				Description:              geDesc,
				DescriptionLocalizations: geLoc,
				Options:                  subcommands(),
			},
		},
		Contexts:         contexts,
		IntegrationTypes: integrations,
	}

	short := discord.SlashCommandCreate{
		Name:                     shortCommand,
		Description:              geDesc,
		DescriptionLocalizations: geLoc,
		Contexts:                 contexts,
		IntegrationTypes:         integrations,
	}
	for _, command := range subcommands() {
		short.Options = append(short.Options, command)
	}

	return []discord.ApplicationCommandCreate{grouped, short}
}
