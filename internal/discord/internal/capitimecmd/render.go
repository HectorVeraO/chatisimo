package capitimecmd

import (
	"fmt"
	"strings"

	"github.com/HectorVeraO/chatisimo/internal/capitime"
)

// RenderConfigShow formats the ephemeral reply text for /capitime-config show.
func RenderConfigShow(loc Locale, cfg capitime.GuildConfig) string {
	return formatConfigBlock(loc, T(loc, textConfigTitle), cfg)
}

// RenderConfigSaved formats the ephemeral confirmation reply text for /capitime-config set.
func RenderConfigSaved(loc Locale, cfg capitime.GuildConfig) string {
	return formatConfigBlock(loc, T(loc, textConfigSaved), cfg)
}

// RenderConfigCleared formats the ephemeral confirmation reply text for /capitime-config clear.
func RenderConfigCleared(loc Locale, cfg capitime.GuildConfig) string {
	return formatConfigBlock(loc, T(loc, textConfigCleared), cfg)
}

func formatConfigBlock(loc Locale, heading string, cfg capitime.GuildConfig) string {
	targetValue := T(loc, textConfigUnset)
	if cfg.TargetUserID != "" {
		targetValue = "<@" + cfg.TargetUserID + ">"
	}

	channelsValue := T(loc, textConfigChannelsAll)
	if len(cfg.EnabledChannelIDs) > 0 {
		mentions := make([]string, 0, len(cfg.EnabledChannelIDs))
		for _, chID := range cfg.EnabledChannelIDs {
			mentions = append(mentions, "<#"+chID+">")
		}
		channelsValue = strings.Join(mentions, ", ")
	}

	localeValue := fmt.Sprintf("`%s`", cfg.EffectiveLocale())
	if cfg.Locale == "" {
		localeValue += T(loc, textConfigLocaleDefaultSuffix)
	}

	debounceValue := fmt.Sprintf("`%s`", cfg.DebounceWindow)

	var b strings.Builder
	b.WriteString(heading)
	b.WriteString("\n- **")
	b.WriteString(T(loc, textConfigFieldTarget))
	b.WriteString(":** ")
	b.WriteString(targetValue)
	b.WriteString("\n- **")
	b.WriteString(T(loc, textConfigFieldChannels))
	b.WriteString(":** ")
	b.WriteString(channelsValue)
	b.WriteString("\n- **")
	b.WriteString(T(loc, textConfigFieldLocale))
	b.WriteString(":** ")
	b.WriteString(localeValue)
	b.WriteString("\n- **")
	b.WriteString(T(loc, textConfigFieldDebounce))
	b.WriteString(":** ")
	b.WriteString(debounceValue)

	return b.String()
}
