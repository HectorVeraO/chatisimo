package uccmd

import (
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/omit"

	"github.com/HectorVeraO/chatisimo/internal/uccalendar"
)

const (
	commandName           = "uc"
	configCommandName     = "uc-config"
	subConfigSetName      = "set"
	subConfigShowName     = "show"
	subConfigClearName    = "clear"
	windowOptionName      = "window"
	dateOptionName        = "date"
	friendOptionName      = "friend"
	channelOptionName     = "channel"
	localeOptionName      = "locale"
	clearTargetOptionName = "target"
	maxDateOptionRunes    = 10
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

func clearTargetChoice(value uccalendar.ClearTarget, labelID MsgID) discord.ApplicationCommandOptionChoiceString {
	name, nameLoc := localizedText(labelID)
	choice := discord.ApplicationCommandOptionChoiceString{
		Name:              name,
		NameLocalizations: nameLoc,
		Value:             string(value),
	}

	return choice
}

func localeChoice(value string, labelID MsgID) discord.ApplicationCommandOptionChoiceString {
	name, nameLoc := localizedText(labelID)
	choice := discord.ApplicationCommandOptionChoiceString{
		Name:              name,
		NameLocalizations: nameLoc,
		Value:             value,
	}

	return choice
}

func configSetSubCommand() discord.ApplicationCommandOptionSubCommand {
	_, setLoc := localizedText(textSubConfigSetName)
	setDesc, setDescLoc := localizedText(textSubConfigSetDesc)
	_, friendLoc := localizedText(textOptFriendName)
	friendDesc, friendDescLoc := localizedText(textOptFriendDesc)
	_, channelLoc := localizedText(textOptChannelName)
	channelDesc, channelDescLoc := localizedText(textOptChannelDesc)
	_, localeLoc := localizedText(textOptLocaleName)
	localeDesc, localeDescLoc := localizedText(textOptLocaleDesc)

	sub := discord.ApplicationCommandOptionSubCommand{
		Name:                     subConfigSetName,
		NameLocalizations:        setLoc,
		Description:              setDesc,
		DescriptionLocalizations: setDescLoc,
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionUser{
				Name:                     friendOptionName,
				NameLocalizations:        friendLoc,
				Description:              friendDesc,
				DescriptionLocalizations: friendDescLoc,
			},
			discord.ApplicationCommandOptionChannel{
				Name:                     channelOptionName,
				NameLocalizations:        channelLoc,
				Description:              channelDesc,
				DescriptionLocalizations: channelDescLoc,
				ChannelTypes: []discord.ChannelType{
					discord.ChannelTypeGuildText,
					discord.ChannelTypeGuildNews,
				},
			},
			discord.ApplicationCommandOptionString{
				Name:                     localeOptionName,
				NameLocalizations:        localeLoc,
				Description:              localeDesc,
				DescriptionLocalizations: localeDescLoc,
				Choices: []discord.ApplicationCommandOptionChoiceString{
					localeChoice(uccalendar.LocaleESCL, textChoiceLocaleESCL),
					localeChoice(uccalendar.LocaleENUS, textChoiceLocaleENUS),
				},
			},
		},
	}

	return sub
}

func configShowSubCommand() discord.ApplicationCommandOptionSubCommand {
	_, showLoc := localizedText(textSubConfigShowName)
	showDesc, showDescLoc := localizedText(textSubConfigShowDesc)

	sub := discord.ApplicationCommandOptionSubCommand{
		Name:                     subConfigShowName,
		NameLocalizations:        showLoc,
		Description:              showDesc,
		DescriptionLocalizations: showDescLoc,
	}

	return sub
}

func configClearSubCommand() discord.ApplicationCommandOptionSubCommand {
	_, clearLoc := localizedText(textSubConfigClearName)
	clearDesc, clearDescLoc := localizedText(textSubConfigClearDesc)
	_, targetLoc := localizedText(textOptClearTargetName)
	targetDesc, targetDescLoc := localizedText(textOptClearTargetDesc)

	sub := discord.ApplicationCommandOptionSubCommand{
		Name:                     subConfigClearName,
		NameLocalizations:        clearLoc,
		Description:              clearDesc,
		DescriptionLocalizations: clearDescLoc,
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionString{
				Name:                     clearTargetOptionName,
				NameLocalizations:        targetLoc,
				Description:              targetDesc,
				DescriptionLocalizations: targetDescLoc,
				Choices: []discord.ApplicationCommandOptionChoiceString{
					clearTargetChoice(uccalendar.ClearTargetAll, textChoiceClearAll),
					clearTargetChoice(uccalendar.ClearTargetFriend, textChoiceClearFriend),
					clearTargetChoice(uccalendar.ClearTargetChannel, textChoiceClearChannel),
					clearTargetChoice(uccalendar.ClearTargetLocale, textChoiceClearLocale),
				},
			},
		},
	}

	return sub
}

// ConfigCommand returns the guild-only /uc-config slash command definition restricted by default
// to server managers (ManageGuild).
func ConfigCommand() discord.SlashCommandCreate {
	desc, descLoc := localizedText(textCmdUCConfigDesc)
	cmd := discord.SlashCommandCreate{
		Name:                     configCommandName,
		Description:              desc,
		DescriptionLocalizations: descLoc,
		DefaultMemberPermissions: omit.NewPtr(discord.PermissionManageGuild),
		Options: []discord.ApplicationCommandOption{
			configSetSubCommand(),
			configShowSubCommand(),
			configClearSubCommand(),
		},
		Contexts: []discord.InteractionContextType{
			discord.InteractionContextTypeGuild,
		},
		IntegrationTypes: []discord.ApplicationIntegrationType{
			discord.ApplicationIntegrationTypeGuildInstall,
		},
	}

	return cmd
}
