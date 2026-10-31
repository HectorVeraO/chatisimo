package uccmd

import (
	"github.com/disgoorg/disgo/discord"

	"github.com/HectorVeraO/chatisimo/internal/uccalendar"
)

const (
	commandName        = "uc"
	windowOptionName   = "window"
	dateOptionName     = "date"
	maxDateOptionRunes = 10
)

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

func windowChoice(value uccalendar.WindowKind, labelID MsgID) discord.ApplicationCommandOptionChoiceString {
	name, nameLoc := localizedText(labelID)
	choice := discord.ApplicationCommandOptionChoiceString{
		Name:              name,
		NameLocalizations: nameLoc,
		Value:             string(value),
	}

	return choice
}

func windowOption() discord.ApplicationCommandOption {
	_, nameLoc := localizedText(textOptWindowName)
	desc, descLoc := localizedText(textOptWindowDesc)

	opt := discord.ApplicationCommandOptionString{
		Name:                     windowOptionName,
		NameLocalizations:        nameLoc,
		Description:              desc,
		DescriptionLocalizations: descLoc,
		Choices: []discord.ApplicationCommandOptionChoiceString{
			windowChoice(uccalendar.WindowToday, textChoiceToday),
			windowChoice(uccalendar.WindowTomorrow, textChoiceTomorrow),
			windowChoice(uccalendar.WindowThisWeekend, textChoiceThisWeekend),
			windowChoice(uccalendar.WindowNextWeekend, textChoiceNextWeekend),
			windowChoice(uccalendar.WindowThisMonth, textChoiceThisMonth),
		},
	}

	return opt
}

func dateOption() discord.ApplicationCommandOption {
	_, nameLoc := localizedText(textOptDateName)
	desc, descLoc := localizedText(textOptDateDesc)

	opt := discord.ApplicationCommandOptionString{
		Name:                     dateOptionName,
		NameLocalizations:        nameLoc,
		Description:              desc,
		DescriptionLocalizations: descLoc,
		MaxLength:                intPtr(maxDateOptionRunes),
	}

	return opt
}

func commandOptions() []discord.ApplicationCommandOption {
	opts := []discord.ApplicationCommandOption{
		windowOption(),
		dateOption(),
	}

	return opts
}

// Command returns the top-level /uc slash command definition for guild and bot-DM contexts.
func Command() discord.SlashCommandCreate {
	desc, descLoc := localizedText(textCmdUCDesc)
	cmd := discord.SlashCommandCreate{
		Name:                     commandName,
		Description:              desc,
		DescriptionLocalizations: descLoc,
		Options:                  commandOptions(),
		Contexts: []discord.InteractionContextType{
			discord.InteractionContextTypeGuild,
			discord.InteractionContextTypeBotDM,
		},
		IntegrationTypes: []discord.ApplicationIntegrationType{
			discord.ApplicationIntegrationTypeGuildInstall,
		},
	}

	return cmd
}

// SubCommand returns the uc subcommand definition registered under /chatisimo.
func SubCommand() discord.ApplicationCommandOptionSubCommand {
	desc, descLoc := localizedText(textCmdUCDesc)
	sub := discord.ApplicationCommandOptionSubCommand{
		Name:                     commandName,
		Description:              desc,
		DescriptionLocalizations: descLoc,
		Options:                  commandOptions(),
	}

	return sub
}
