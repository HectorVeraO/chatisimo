package capitimecmd

import (
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/omit"

	"github.com/HectorVeraO/chatisimo/internal/capitime"
)

// Command names and router paths for Capi Time configuration.
const (
	CommandName = "capitime-config"

	SubcommandSet   = "set"
	SubcommandShow  = "show"
	SubcommandClear = "clear"

	PathSet   = "/capitime-config/set"
	PathShow  = "/capitime-config/show"
	PathClear = "/capitime-config/clear"

	OptionUserTarget  = "target"
	OptionChannel     = "channel"
	OptionLocale      = "locale"
	OptionClearTarget = "target"
)

// ConfigCommand builds the top-level /capitime-config guild-only slash command restricted to
// members with ManageGuild.
func ConfigCommand() discord.SlashCommandCreate {
	manageGuild := discord.PermissionManageGuild

	return discord.SlashCommandCreate{
		Name:                     CommandName,
		Description:              T(LocaleESCL, textCmdConfigDesc),
		DescriptionLocalizations: LocalizeDesc(textCmdConfigDesc),
		DefaultMemberPermissions: omit.NewPtr(manageGuild),
		Contexts: []discord.InteractionContextType{
			discord.InteractionContextTypeGuild,
		},
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionSubCommand{
				Name:                     SubcommandSet,
				Description:              T(LocaleESCL, textCmdConfigSetDesc),
				DescriptionLocalizations: LocalizeDesc(textCmdConfigSetDesc),
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionUser{
						Name:                     OptionUserTarget,
						Description:              T(LocaleESCL, textOptTargetDesc),
						DescriptionLocalizations: LocalizeDesc(textOptTargetDesc),
						Required:                 false,
					},
					discord.ApplicationCommandOptionChannel{
						Name:                     OptionChannel,
						Description:              T(LocaleESCL, textOptChannelDesc),
						DescriptionLocalizations: LocalizeDesc(textOptChannelDesc),
						Required:                 false,
						ChannelTypes: []discord.ChannelType{
							discord.ChannelTypeGuildText,
						},
					},
					discord.ApplicationCommandOptionString{
						Name:                     OptionLocale,
						Description:              T(LocaleESCL, textOptLocaleDesc),
						DescriptionLocalizations: LocalizeDesc(textOptLocaleDesc),
						Required:                 false,
						Choices: []discord.ApplicationCommandOptionChoiceString{
							localeChoice(capitime.LocaleESCL, textChoiceLocaleESCL),
							localeChoice(capitime.LocaleENUS, textChoiceLocaleENUS),
						},
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:                     SubcommandShow,
				Description:              T(LocaleESCL, textCmdConfigShowDesc),
				DescriptionLocalizations: LocalizeDesc(textCmdConfigShowDesc),
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:                     SubcommandClear,
				Description:              T(LocaleESCL, textCmdConfigClear),
				DescriptionLocalizations: LocalizeDesc(textCmdConfigClear),
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionString{
						Name:                     OptionClearTarget,
						Description:              T(LocaleESCL, textOptClearTargetDesc),
						DescriptionLocalizations: LocalizeDesc(textOptClearTargetDesc),
						Required:                 false,
						Choices: []discord.ApplicationCommandOptionChoiceString{
							clearTargetChoice(capitime.ClearTargetAll, textChoiceClearAll),
							clearTargetChoice(capitime.ClearTargetUser, textChoiceClearTarget),
							clearTargetChoice(capitime.ClearTargetChannels, textChoiceClearChannels),
							clearTargetChoice(capitime.ClearTargetLocale, textChoiceClearLocale),
						},
					},
				},
			},
		},
	}
}

func localeChoice(value capitime.Locale, labelID MsgID) discord.ApplicationCommandOptionChoiceString {
	return discord.ApplicationCommandOptionChoiceString{
		Name:              T(LocaleESCL, labelID),
		NameLocalizations: LocalizeDesc(labelID),
		Value:             string(value),
	}
}

func clearTargetChoice(value capitime.ClearTarget, labelID MsgID) discord.ApplicationCommandOptionChoiceString {
	return discord.ApplicationCommandOptionChoiceString{
		Name:              T(LocaleESCL, labelID),
		NameLocalizations: LocalizeDesc(labelID),
		Value:             string(value),
	}
}
