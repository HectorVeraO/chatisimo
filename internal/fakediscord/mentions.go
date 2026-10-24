package fakediscord

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

const (
	parseUsers    = "users"
	parseRoles    = "roles"
	parseEveryone = "everyone"

	pingEveryone = "everyone"
	pingHere     = "here"

	// maxEmbedDepth bounds the walk over an embed's JSON.
	maxEmbedDepth = 64
)

// userMention matches <@id> and <@!id>.
var userMention = regexp.MustCompile(`<@!?([0-9]{1,20})>`)

// AllowedMentions is the allowed_mentions object a bot sent with a message.
type AllowedMentions struct {
	Parse       []string `json:"parse"`
	Roles       []string `json:"roles"`
	Users       []string `json:"users"`
	RepliedUser bool     `json:"replied_user"`
}

// EffectiveMentions is the part of allowed_mentions that decides who is pinged once
// Discord's defaults are applied.
type EffectiveMentions struct {
	Parse []string `json:"parse"`
	Roles []string `json:"roles"`
	Users []string `json:"users"`
}

// parseAllowedMentions decodes the raw allowed_mentions field of a request body. An absent
// field and a JSON null both mean "not sent" and return nil. Null members count as empty.
func parseAllowedMentions(raw json.RawMessage) (*AllowedMentions, error) {
	trimmed := bytes.TrimSpace(raw)
	isAbsent := len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
	if isAbsent {
		return nil, nil
	}

	var sent AllowedMentions
	err := json.Unmarshal(trimmed, &sent)
	if err != nil {
		return nil, fmt.Errorf("allowed_mentions: %w", err)
	}

	sent.Parse = nonNilStrings(sent.Parse)
	sent.Roles = nonNilStrings(sent.Roles)
	sent.Users = nonNilStrings(sent.Users)

	return &sent, nil
}

// effectiveMentions returns the object Discord applies: the one sent, or the default that
// parses users, roles and everyone when none was sent.
func effectiveMentions(sent *AllowedMentions) EffectiveMentions {
	if sent == nil {
		return EffectiveMentions{
			Parse: []string{parseUsers, parseRoles, parseEveryone},
			Roles: []string{},
			Users: []string{},
		}
	}

	return EffectiveMentions{
		Parse: slices.Clone(sent.Parse),
		Roles: slices.Clone(sent.Roles),
		Users: slices.Clone(sent.Users),
	}
}

// renderedMentions lists, without duplicates and in order of appearance, the user IDs that
// <@id> and <@!id> mention in the given texts.
func renderedMentions(texts ...string) []string {
	rendered := []string{}
	for _, text := range texts {
		for _, match := range userMention.FindAllStringSubmatch(text, -1) {
			id := match[1]
			if !slices.Contains(rendered, id) {
				rendered = append(rendered, id)
			}
		}
	}

	return rendered
}

// pingsFor lists who a message is allowed to notify: user IDs, then "everyone", then "here".
// Only the content pings; embeds never do.
func pingsFor(content string, effective EffectiveMentions) []string {
	pings := []string{}

	parsesUsers := slices.Contains(effective.Parse, parseUsers)
	for _, id := range renderedMentions(content) {
		isAllowed := parsesUsers || slices.Contains(effective.Users, id)
		if isAllowed {
			pings = append(pings, id)
		}
	}

	parsesEveryone := slices.Contains(effective.Parse, parseEveryone)
	if parsesEveryone && strings.Contains(content, "@everyone") {
		pings = append(pings, pingEveryone)
	}
	if parsesEveryone && strings.Contains(content, "@here") {
		pings = append(pings, pingHere)
	}

	return pings
}

// embedStrings returns every string value found in the embeds, so mentions in titles,
// descriptions and fields can be rendered. Undecodable embeds contribute nothing.
func embedStrings(embeds []json.RawMessage) []string {
	var found []string
	for _, embed := range embeds {
		var decoded any
		err := json.Unmarshal(embed, &decoded)
		if err != nil {
			continue
		}
		found = collectStrings(decoded, 0, found)
	}

	return found
}

func collectStrings(value any, depth int, found []string) []string {
	if depth > maxEmbedDepth {
		return found
	}

	switch typed := value.(type) {
	case string:
		return append(found, typed)
	case []any:
		for _, item := range typed {
			found = collectStrings(item, depth+1, found)
		}
	case map[string]any:
		for _, key := range slices.Sorted(maps.Keys(typed)) {
			found = collectStrings(typed[key], depth+1, found)
		}
	}

	return found
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}

	return values
}
