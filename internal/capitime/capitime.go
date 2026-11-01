// Package capitime detects arrival promises from a configured target member in Discord guild
// channels, translates the claimed arrival into an exaggerated "Capi Time" prediction (with an
// occasional on-time miracle easter egg), and generates single-line Chilean Spanish banter.
package capitime

import (
	"context"
	"io"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Timezone is the IANA timezone name used for all Capi Time arrival calculations.
const Timezone = "America/Santiago"

// MaxMonitoredChannels is the maximum number of restricted channels a single guild can configure.
const MaxMonitoredChannels = 50

// Locale identifies the language used for Capi Time banter and configuration views in a guild.
type Locale string

// Supported guild locales for Capi Time.
const (
	LocaleESCL Locale = "es-CL"
	LocaleENUS Locale = "en-US"
)

// ClearTarget selects which subset of a guild's Capi Time configuration to reset.
type ClearTarget string

// Supported targets for ClearGuildConfig.
const (
	ClearTargetAll      ClearTarget = "all"
	ClearTargetUser     ClearTarget = "target"
	ClearTargetChannels ClearTarget = "channels"
	ClearTargetLocale   ClearTarget = "locale"
)

// GuildScope carries the guild snowflake and caller authorization bit for guild configuration
// commands.
type GuildScope struct {
	GuildID        string
	CanManageGuild bool
}

// GuildConfig holds the persisted Capi Time settings for a single Discord guild.
type GuildConfig struct {
	GuildID           string
	TargetUserID      string
	EnabledChannelIDs []string
	Locale            Locale
	DebounceWindow    time.Duration
	UpdatedAt         time.Time
}

// Configured reports whether the guild has any stored Capi Time setting.
func (c GuildConfig) Configured() bool {
	return c.TargetUserID != "" || len(c.EnabledChannelIDs) > 0 || c.Locale != ""
}

// EffectiveLocale returns the guild's configured locale, defaulting to LocaleESCL ("es-CL") when
// none is stored.
func (c GuildConfig) EffectiveLocale() Locale {
	if c.Locale == LocaleENUS {
		return LocaleENUS
	}

	return LocaleESCL
}

// MonitorsChannel reports whether channelID is eligible for Capi Time monitoring in this guild.
// When TargetUserID is empty, monitoring is inactive. When EnabledChannelIDs is empty, all guild
// channels are monitored.
func (c GuildConfig) MonitorsChannel(channelID string) bool {
	if c.TargetUserID == "" || channelID == "" {
		return false
	}
	if len(c.EnabledChannelIDs) == 0 {
		return true
	}

	return slices.Contains(c.EnabledChannelIDs, channelID)
}

// GuildConfigUpdate describes a partial update to a guild's Capi Time configuration. Nil fields are
// left unchanged.
type GuildConfigUpdate struct {
	TargetUserID *string
	ChannelID    *string
	Locale       *Locale
}

// MessageInput describes an incoming guild channel message evaluated by the Capi Time detector.
type MessageInput struct {
	GuildID   string
	ChannelID string
	MessageID string
	AuthorID  string
	AuthorBot bool
	Content   string
}

// Prediction holds the detected arrival claim and the computed Capi Time target instant.
type Prediction struct {
	ClaimedAt    time.Time
	PredictedAt  time.Time
	ClaimSummary string
	DelayMinutes int
	Immediate    bool
	EasterEgg    bool
}

// Reply is the result of evaluating a guild message with HandleMessage. When Handled is false, the
// bot remains silent.
type Reply struct {
	Handled    bool
	GuildID    string
	ChannelID  string
	ReplyToID  string
	TargetUser string
	Locale     Locale
	Prediction Prediction
	Banter     string
	Content    string
}

// BanterInput carries the non-sensitive arrival context passed to a BanterGenerator.
type BanterInput struct {
	Locale       Locale
	ClaimSummary string
	ClaimedClock string
	CapiClock    string
	DelayMinutes int
	Immediate    bool
	EasterEgg    bool
	Prompt       string
}

// LLMGenerator generates short single-turn text completions from a system instruction and user
// prompt. *llm.Client satisfies this interface.
type LLMGenerator interface {
	GenerateWithPrompt(ctx context.Context, systemInstruction, userPrompt string) (string, error)
}

// BanterGenerator produces a short single-line banter clause for a Capi Time reply.
type BanterGenerator interface {
	GenerateBanter(ctx context.Context, input BanterInput) (string, error)
}

// Deps bundles the dependencies required to construct a Capi Time Service.
type Deps struct {
	Pool     *pgxpool.Pool
	Clock    func() time.Time
	Rand     io.Reader
	Settings SettingReader
	LLM      LLMGenerator
	Banter   BanterGenerator
}
