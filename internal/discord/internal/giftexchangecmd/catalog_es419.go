package giftexchangecmd

import (
	_ "embed"
	"encoding/json"
)

//go:embed catalog_es419.json
var rawCatalogES419 string

// catalogES419 returns the Chilean / neutral Latin American Spanish message catalog
// (LocaleES419). It uses tu forms (never voseo) and keeps slash command names in English.
func catalogES419() map[MsgID]string {
	var messages map[MsgID]string
	err := json.Unmarshal([]byte(rawCatalogES419), &messages)
	if err != nil {
		panic("giftexchangecmd: invalid embedded es-419 catalog: " + err.Error())
	}

	return messages
}
