// Package capitimecmd implements the /capitime-config Discord slash command and renders Capi Time
// configuration replies in Chilean Spanish (es-CL) and English (en-US).
package capitimecmd

import (
	_ "embed"
	"strings"
	"sync"

	"github.com/disgoorg/disgo/discord"

	"github.com/HectorVeraO/chatisimo/internal/platform/i18n"
)

//go:embed catalog_es_cl.json
var rawCatalogESCL []byte

//go:embed catalog_en_us.json
var rawCatalogENUS []byte

// Locale identifies a supported UI language for Capi Time Discord messages.
type Locale = i18n.Locale

// Supported locales for Capi Time Discord messages.
const (
	LocaleESCL Locale = i18n.LocaleESCL
	LocaleENUS Locale = i18n.LocaleENUS
)

// MsgID is a typed message catalog key for Capi Time copy.
type MsgID string

const (
	textCmdConfigDesc     MsgID = "cmd.capitime_config.desc"
	textCmdConfigSetDesc  MsgID = "cmd.capitime_config.set.desc"
	textCmdConfigShowDesc MsgID = "cmd.capitime_config.show.desc"
	textCmdConfigClear    MsgID = "cmd.capitime_config.clear.desc"

	textOptTargetDesc      MsgID = "opt.target_user.desc"
	textOptChannelDesc     MsgID = "opt.channel.desc"
	textOptLocaleDesc      MsgID = "opt.locale.desc"
	textOptClearTargetDesc MsgID = "opt.clear_target.desc"

	textChoiceLocaleESCL MsgID = "choice.locale.es_cl"
	textChoiceLocaleENUS MsgID = "choice.locale.en_us"

	textChoiceClearAll      MsgID = "choice.clear.all"
	textChoiceClearTarget   MsgID = "choice.clear.target"
	textChoiceClearChannels MsgID = "choice.clear.channels"
	textChoiceClearLocale   MsgID = "choice.clear.locale"

	textConfigTitle               MsgID = "config.title"
	textConfigSaved               MsgID = "config.saved"
	textConfigCleared             MsgID = "config.cleared"
	textConfigFieldTarget         MsgID = "config.field.target"
	textConfigFieldChannels       MsgID = "config.field.channels"
	textConfigFieldLocale         MsgID = "config.field.locale"
	textConfigFieldDebounce       MsgID = "config.field.debounce"
	textConfigUnset               MsgID = "config.unset"
	textConfigChannelsAll         MsgID = "config.channels.all"
	textConfigLocaleDefaultSuffix MsgID = "config.locale.default_suffix"

	textErrGuildOnly          MsgID = "err.guild_only"
	textErrForbidden          MsgID = "err.forbidden"
	textErrEmptySet           MsgID = "err.empty_set"
	textErrInvalidSnowflake   MsgID = "err.invalid_snowflake"
	textErrInvalidLocale      MsgID = "err.invalid_locale"
	textErrInvalidClearTarget MsgID = "err.invalid_clear_target"
	textErrChannelCap         MsgID = "err.channel_cap"
	textErrInvalidInput       MsgID = "err.invalid_input"
)

var defaultBundle = sync.OnceValue(func() *i18n.Bundle {
	es, err := i18n.ParseJSONCatalog(rawCatalogESCL)
	if err != nil {
		panic("capitimecmd: invalid embedded es-CL catalog: " + err.Error())
	}

	en, err := i18n.ParseJSONCatalog(rawCatalogENUS)
	if err != nil {
		panic("capitimecmd: invalid embedded en-US catalog: " + err.Error())
	}

	b, err := i18n.NewBundle(LocaleESCL, map[Locale]map[string]string{
		LocaleESCL: es,
		LocaleENUS: en,
	})
	if err != nil {
		panic("capitimecmd: invalid i18n catalogs: " + err.Error())
	}

	return b
})

// Bundle returns the validated Capi Time message catalog bundle with es-CL as the default locale.
func Bundle() *i18n.Bundle {
	return defaultBundle()
}

// ResolveLocale maps a guild's configured locale or Discord's interaction/guild locale tag to a
// supported Capi Time Locale, defaulting to LocaleESCL ("es-CL").
func ResolveLocale(candidates ...string) Locale {
	for _, raw := range candidates {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}

		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "es") {
			return LocaleESCL
		}
		if strings.HasPrefix(lower, "en") {
			return LocaleENUS
		}
	}

	return LocaleESCL
}

// T formats message id for locale loc with optional fmt.Sprintf args.
func T(loc Locale, id MsgID, args ...any) string {
	cat := Bundle().Catalog(loc)
	if len(args) == 0 {
		return cat.Get(string(id))
	}

	return cat.Format(string(id), args...)
}

// LocalizeDesc returns the localized description map (es-419, es-ES, and en-US) for a command,
// subcommand, or option description key.
func LocalizeDesc(id MsgID) map[discord.Locale]string {
	return map[discord.Locale]string{
		discord.LocaleSpanishLATAM: T(LocaleESCL, id),
		discord.LocaleSpanishES:    T(LocaleESCL, id),
		discord.LocaleEnglishUS:    T(LocaleENUS, id),
	}
}
