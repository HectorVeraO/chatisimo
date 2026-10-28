package fakediscord

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"text/template"
)

//go:embed testdata/payloads/*.json
var payloadFiles embed.FS

const payloadDir = "testdata/payloads/"

// Template names, one per payload file.
const (
	tplSlashGuild        = "interaction_slash_guild.json"
	tplSlashDM           = "interaction_slash_dm.json"
	tplAutocompleteGuild = "interaction_autocomplete_guild.json"
	tplAutocompleteDM    = "interaction_autocomplete_dm.json"
	tplButtonGuild       = "interaction_button_guild.json"
	tplButtonDM          = "interaction_button_dm.json"
	tplSelectGuild       = "interaction_select_guild.json"
	tplSelectDM          = "interaction_select_dm.json"
	tplModalGuild        = "interaction_modal_guild.json"
	tplModalDM           = "interaction_modal_dm.json"
)

// templateData holds every value a payload template can fill. A template reads only the
// fields it needs, and each value reaches the output through the json function, so any
// string stays a valid JSON string.
type templateData struct {
	AppID string
	Bot   userJSON
	User  userJSON

	HeartbeatInterval int64
	Seq               int
	SessionID         string
	ResumeURL         string
	UnavailableGuilds []map[string]any

	InteractionID string
	Token         string
	GuildID       string
	GuildName     string
	GuildLocale   string
	OwnerID       string
	ChannelID     string
	Locale        string

	CommandID          string
	CommandName        string
	CommandDescription string
	CommandVersion     string
	Options            any
	Resolved           any

	Message           any
	MessageID         string
	Content           string
	Timestamp         string
	Flags             int
	Embeds            any
	MessageComponents any

	CustomID        string
	Values          []string
	ModalComponents any
}

// loadTemplates parses every embedded payload template.
func loadTemplates() (*template.Template, error) {
	funcs := template.FuncMap{"json": quoteJSON}
	parsed, err := template.New("payloads").Funcs(funcs).ParseFS(payloadFiles, payloadDir+"*.json")
	if err != nil {
		return nil, fmt.Errorf("parse payload templates: %w", err)
	}

	return parsed, nil
}

// quoteJSON is the only way a template inserts a value: it encodes the value as JSON.
func quoteJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode template value: %w", err)
	}

	return string(encoded), nil
}

// fillTemplate renders one payload template with data and checks the result is valid JSON.
func fillTemplate(templates *template.Template, name string, data templateData) ([]byte, error) {
	var out bytes.Buffer
	err := templates.ExecuteTemplate(&out, name, data)
	if err != nil {
		return nil, fmt.Errorf("fill template %s: %w", name, err)
	}
	if !json.Valid(out.Bytes()) {
		return nil, fmt.Errorf("fill template %s: result is not valid JSON", name)
	}

	return out.Bytes(), nil
}
