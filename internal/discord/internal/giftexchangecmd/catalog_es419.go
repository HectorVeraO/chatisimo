package giftexchangecmd

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
		panic("giftexchangecmd: invalid embedded en-US catalog: " + err.Error())
	}
	es, err := i18n.ParseJSONCatalog(rawCatalogESCL)
	if err != nil {
		panic("giftexchangecmd: invalid embedded es-CL catalog: " + err.Error())
	}

	b, err := i18n.NewBundle(i18n.LocaleENUS, map[i18n.Locale]map[string]string{
		i18n.LocaleENUS: en,
		i18n.LocaleESCL: es,
	})
	if err != nil {
		panic("giftexchangecmd: build i18n bundle: " + err.Error())
	}

	return b
})

// catalogENUS returns the English (United States) message catalog (LocaleENUS).
func catalogENUS() map[MsgID]string {
	return toMsgIDMap(rawCatalogENUS, "en-US")
}

// catalogESCL returns the Chilean / neutral Latin American Spanish message catalog
// (LocaleESCL). It uses tu forms (never voseo) and keeps slash command names in English.
func catalogESCL() map[MsgID]string {
	return toMsgIDMap(rawCatalogESCL, "es-CL")
}

func toMsgIDMap(raw []byte, label string) map[MsgID]string {
	parsed, err := i18n.ParseJSONCatalog(raw)
	if err != nil {
		panic("giftexchangecmd: invalid embedded " + label + " catalog: " + err.Error())
	}

	out := make(map[MsgID]string, len(parsed))
	for k, v := range parsed {
		out[MsgID(k)] = v
	}

	return out
}
