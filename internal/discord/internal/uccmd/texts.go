// Package uccmd implements the /uc, /chatisimo uc, and /uc-config slash commands and renders
// Universidad Catolica schedule reports, guild configuration replies, and proactive channel alerts.
package uccmd

import (
	_ "embed"
	"sync"

	"github.com/HectorVeraO/chatisimo/internal/platform/i18n"
)

//go:embed catalog_en_us.json
var rawCatalogENUS []byte

//go:embed catalog_es_cl.json
var rawCatalogESCL []byte

var defaultBundle = sync.OnceValue(func() *i18n.Bundle {
	en, err := i18n.ParseJSONCatalog(rawCatalogENUS)
	if err != nil {
		panic("uccmd: invalid embedded en-US catalog: " + err.Error())
	}
	es, err := i18n.ParseJSONCatalog(rawCatalogESCL)
	if err != nil {
		panic("uccmd: invalid embedded es-CL catalog: " + err.Error())
	}

	b, err := i18n.NewBundle(i18n.LocaleENUS, map[i18n.Locale]map[string]string{
		i18n.LocaleENUS: en,
		i18n.LocaleESCL: es,
	})
	if err != nil {
		panic("uccmd: build i18n bundle: " + err.Error())
	}

	return b
})

// catalogENUS returns the English (United States) message catalog (LocaleENUS).
func catalogENUS() map[MsgID]string {
	return toMsgIDMap(rawCatalogENUS, "en-US")
}

// catalogESCL returns the Chilean Spanish message catalog (LocaleESCL).
func catalogESCL() map[MsgID]string {
	return toMsgIDMap(rawCatalogESCL, "es-CL")
}

func toMsgIDMap(raw []byte, label string) map[MsgID]string {
	parsed, err := i18n.ParseJSONCatalog(raw)
	if err != nil {
		panic("uccmd: invalid embedded " + label + " catalog: " + err.Error())
	}

	out := make(map[MsgID]string, len(parsed))
	for k, v := range parsed {
		out[MsgID(k)] = v
	}

	return out
}

// Locale identifies a BCP 47 language catalog of the Discord adapter.
type Locale = i18n.Locale

// Supported locales of the Discord adapter. LocaleENUS is the default and fallback for Discord UI
// strings; UC schedule reports and proactive alerts default to LocaleESCL unless a guild configures
// LocaleENUS.
const (
	LocaleENUS Locale = i18n.LocaleENUS
	LocaleESCL Locale = i18n.LocaleESCL
)

// LocaleFromDiscord maps a Discord locale tag (e.g. "es-419", "es-CL", "es-ES", "en-US")
// to a supported Locale, falling back to LocaleENUS.
func LocaleFromDiscord(raw string) Locale {
	return i18n.MatchLocale(raw)
}

// ResolveLocale picks the best supported Locale from candidates in priority order, preferring
// a non-default supported locale when an earlier candidate is the default fallback.
func ResolveLocale(candidates ...string) Locale {
	return i18n.ResolveLocale(candidates...)
}

// MsgID identifies one user-facing message template in a Catalog.
type MsgID string

// Catalog holds the user-facing message templates for one Locale.
type Catalog struct {
	inner     i18n.Catalog
	hasBundle bool
}

// CatalogFor returns the message catalog for loc, falling back to LocaleENUS when loc is
// unsupported or empty.
func CatalogFor(loc Locale) Catalog {
	cat := Catalog{inner: defaultBundle().Catalog(loc), hasBundle: true}
	return cat
}

// Locale returns the resolved canonical locale of the catalog.
func (c Catalog) Locale() Locale {
	if !c.hasBundle {
		return LocaleENUS
	}

	return c.inner.Locale()
}

// Get returns the raw message template for id, falling back to LocaleENUS if missing.
func (c Catalog) Get(id MsgID) string {
	if !c.hasBundle {
		return defaultBundle().Catalog(LocaleENUS).Get(string(id))
	}

	return c.inner.Get(string(id))
}

// Format formats the message template for id with args.
func (c Catalog) Format(id MsgID, args ...any) string {
	if !c.hasBundle {
		return defaultBundle().Catalog(LocaleENUS).Format(string(id), args...)
	}

	return c.inner.Format(string(id), args...)
}

// Slash command, option, and choice localization keys.
const (
	textCmdUCDesc          MsgID = "textCmdUCDesc"
	textCmdUCConfigDesc    MsgID = "textCmdUCConfigDesc"
	textSubConfigSetName   MsgID = "textSubConfigSetName"
	textSubConfigSetDesc   MsgID = "textSubConfigSetDesc"
	textSubConfigShowName  MsgID = "textSubConfigShowName"
	textSubConfigShowDesc  MsgID = "textSubConfigShowDesc"
	textSubConfigClearName MsgID = "textSubConfigClearName"
	textSubConfigClearDesc MsgID = "textSubConfigClearDesc"
	textOptWindowName      MsgID = "textOptWindowName"
	textOptWindowDesc      MsgID = "textOptWindowDesc"
	textOptDateName        MsgID = "textOptDateName"
	textOptDateDesc        MsgID = "textOptDateDesc"
	textOptFriendName      MsgID = "textOptFriendName"
	textOptFriendDesc      MsgID = "textOptFriendDesc"
	textOptChannelName     MsgID = "textOptChannelName"
	textOptChannelDesc     MsgID = "textOptChannelDesc"
	textOptLocaleName      MsgID = "textOptLocaleName"
	textOptLocaleDesc      MsgID = "textOptLocaleDesc"
	textOptClearTargetName MsgID = "textOptClearTargetName"
	textOptClearTargetDesc MsgID = "textOptClearTargetDesc"
	textChoiceToday        MsgID = "textChoiceToday"
	textChoiceTomorrow     MsgID = "textChoiceTomorrow"
	textChoiceThisWeekend  MsgID = "textChoiceThisWeekend"
	textChoiceNextWeekend  MsgID = "textChoiceNextWeekend"
	textChoiceThisMonth    MsgID = "textChoiceThisMonth"
	textChoiceLocaleESCL   MsgID = "textChoiceLocaleESCL"
	textChoiceLocaleENUS   MsgID = "textChoiceLocaleENUS"
	textChoiceClearAll     MsgID = "textChoiceClearAll"
	textChoiceClearFriend  MsgID = "textChoiceClearFriend"
	textChoiceClearChannel MsgID = "textChoiceClearChannel"
	textChoiceClearLocale  MsgID = "textChoiceClearLocale"
)

// Weekday, period, template field, fallback banter, configuration, and error keys.
const (
	textWeekdaySun               MsgID = "textWeekdaySun"
	textWeekdayMon               MsgID = "textWeekdayMon"
	textWeekdayTue               MsgID = "textWeekdayTue"
	textWeekdayWed               MsgID = "textWeekdayWed"
	textWeekdayThu               MsgID = "textWeekdayThu"
	textWeekdayFri               MsgID = "textWeekdayFri"
	textWeekdaySat               MsgID = "textWeekdaySat"
	textPeriodToday              MsgID = "textPeriodToday"
	textPeriodTomorrow           MsgID = "textPeriodTomorrow"
	textPeriodThisWeekend        MsgID = "textPeriodThisWeekend"
	textPeriodNextWeekend        MsgID = "textPeriodNextWeekend"
	textPeriodThisMonth          MsgID = "textPeriodThisMonth"
	textPeriodDate               MsgID = "textPeriodDate"
	textFriendFallback           MsgID = "textFriendFallback"
	textVenueHomeWithName        MsgID = "textVenueHomeWithName"
	textVenueHome                MsgID = "textVenueHome"
	textVenueAwayWithName        MsgID = "textVenueAwayWithName"
	textVenueAway                MsgID = "textVenueAway"
	textMatchupHome              MsgID = "textMatchupHome"
	textMatchupAway              MsgID = "textMatchupAway"
	textBanterBlockedHome        MsgID = "textBanterBlockedHome"
	textBanterBlockedAway        MsgID = "textBanterBlockedAway"
	textBanterPending            MsgID = "textBanterPending"
	textBanterFree               MsgID = "textBanterFree"
	textAlertFridayFallback      MsgID = "textAlertFridayFallback"
	textConfigUnset              MsgID = "textConfigUnset"
	textConfigLocaleDefault      MsgID = "textConfigLocaleDefault"
	textConfigShow               MsgID = "textConfigShow"
	textConfigSaved              MsgID = "textConfigSaved"
	textConfigCleared            MsgID = "textConfigCleared"
	textConfigGuildOnly          MsgID = "textConfigGuildOnly"
	textConfigForbidden          MsgID = "textConfigForbidden"
	textConfigEmptyUpdate        MsgID = "textConfigEmptyUpdate"
	textConfigFriendBot          MsgID = "textConfigFriendBot"
	textConfigInvalidFriend      MsgID = "textConfigInvalidFriend"
	textConfigInvalidChannel     MsgID = "textConfigInvalidChannel"
	textConfigInvalidLocale      MsgID = "textConfigInvalidLocale"
	textConfigInvalidClearTarget MsgID = "textConfigInvalidClearTarget"
	textScheduleUnavailable      MsgID = "textScheduleUnavailable"
	textMutuallyExclusiveOptions MsgID = "textMutuallyExclusiveOptions"
	textInvalidDate              MsgID = "textInvalidDate"
	textInvalidWindow            MsgID = "textInvalidWindow"
	textInvalidInput             MsgID = "textInvalidInput"
	textInternal                 MsgID = "textInternal"
)
