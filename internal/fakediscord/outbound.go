package fakediscord

import (
	"encoding/json"
	"time"
)

// Record sources.
const (
	sourceDM      = "dm"
	sourceChannel = "channel"
)

// Record is one bot request that created or changed a visible message, kept for tests and
// the control API to assert on who could see it and whom it pinged. It never holds the
// bot token.
type Record struct {
	Seq                      int               `json:"seq"`
	At                       time.Time         `json:"at"`
	Route                    string            `json:"route"`
	Source                   string            `json:"source"`
	Status                   int               `json:"status"`
	Deduplicated             bool              `json:"deduplicated"`
	InteractionID            *string           `json:"interaction_id"`
	MessageID                string            `json:"message_id"`
	ChannelID                string            `json:"channel_id"`
	GuildID                  *string           `json:"guild_id"`
	Ephemeral                bool              `json:"ephemeral"`
	VisibleTo                []string          `json:"visible_to"`
	Content                  string            `json:"content"`
	Embeds                   []json.RawMessage `json:"embeds"`
	Components               []json.RawMessage `json:"components"`
	AllowedMentionsSent      *AllowedMentions  `json:"allowed_mentions_sent"`
	AllowedMentionsEffective EffectiveMentions `json:"allowed_mentions_effective"`
	MentionsRendered         []string          `json:"mentions_rendered"`
	Pings                    []string          `json:"pings"`
}
