package fakediscord

import (
	"encoding/json"
	"slices"
	"time"
)

// Record sources.
const (
	sourceDM       = "dm"
	sourceChannel  = "channel"
	sourceCallback = "callback"
	sourceEdit     = "edit"
	sourceFollowup = "followup"
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

// recordInput describes one bot request for appendRecordLocked.
type recordInput struct {
	route         string
	source        string
	status        int
	deduplicated  bool
	interactionID string
	message       Message
	guildID       string
	// sentContent is the content the request carried; only it can ping.
	sentContent string
	sent        *AllowedMentions
	visibleTo   []string
}

// appendRecordLocked adds the outbound record of a request about message in.message and
// returns it. Callers hold s.mu.
func (s *Server) appendRecordLocked(in recordInput) Record {
	effective := effectiveMentions(in.sent)
	texts := append([]string{in.message.Content}, embedStrings(in.message.Embeds)...)

	var interactionID *string
	if in.interactionID != "" {
		id := in.interactionID
		interactionID = &id
	}
	var guildID *string
	if in.guildID != "" {
		id := in.guildID
		guildID = &id
	}

	s.recordSeq++
	record := Record{
		Seq:                      s.recordSeq,
		At:                       time.Now().UTC(),
		Route:                    in.route,
		Source:                   in.source,
		Status:                   in.status,
		Deduplicated:             in.deduplicated,
		InteractionID:            interactionID,
		MessageID:                in.message.ID,
		ChannelID:                in.message.ChannelID,
		GuildID:                  guildID,
		Ephemeral:                in.message.EphemeralFor != "",
		VisibleTo:                nonNilStrings(slices.Clone(in.visibleTo)),
		Content:                  in.message.Content,
		Embeds:                   nonNilRaw(slices.Clone(in.message.Embeds)),
		Components:               nonNilRaw(slices.Clone(in.message.Components)),
		AllowedMentionsSent:      in.sent,
		AllowedMentionsEffective: effective,
		MentionsRendered:         renderedMentions(texts...),
		Pings:                    pingsFor(in.sentContent, effective),
	}
	s.w.records = append(s.w.records, record)

	return cloneRecord(record)
}
