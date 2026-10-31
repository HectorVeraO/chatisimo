// Package uccalendar tracks Universidad Catolica's football schedule in America/Santiago time,
// evaluates friend availability around match traffic windows, and dispatches deduplicated
// proactive weekend and matchday alerts.
package uccalendar

import (
	"context"
	"fmt"
	"time"
	_ "time/tzdata"
)

const (
	// SettingFridayAlertTime is the HH:MM America/Santiago time for Friday weekend summaries.
	SettingFridayAlertTime = "uc.friday_alert_time"
	// SettingMatchdayAlertTime is the HH:MM America/Santiago time for matchday morning reminders.
	SettingMatchdayAlertTime = "uc.matchday_alert_time"
	// SettingSlangCacheTTL is the cache lifetime for generated Chilean football banter ("0s" disables it).
	SettingSlangCacheTTL = "uc.slang_cache_ttl"
	// SettingAPIBaseURL is the ESPN soccer API base URL.
	SettingAPIBaseURL = "uc.api_base_url"

	// DefaultAPIBaseURL is the production ESPN soccer site API base URL.
	DefaultAPIBaseURL = "https://site.api.espn.com/apis/site/v2/sports/soccer"
	// DefaultFridayAlertTime is the default Friday weekend summary time in America/Santiago.
	DefaultFridayAlertTime = "17:00"
	// DefaultMatchdayAlertTime is the default matchday morning reminder time in America/Santiago.
	DefaultMatchdayAlertTime = "09:00"
	// DefaultSlangCacheTTL is the default cache lifetime for LLM banter entries.
	DefaultSlangCacheTTL = 24 * time.Hour
	// DefaultSyncInterval is the default interval between background ESPN schedule syncs.
	DefaultSyncInterval = 6 * time.Hour
	// DefaultAlertCheckInterval is the default interval between proactive alert checks.
	DefaultAlertCheckInterval = 1 * time.Minute
	// DefaultSettingsTTL is the default in-memory cache lifetime for UC settings reads.
	DefaultSettingsTTL = 5 * time.Minute
	// DefaultHTTPTimeout is the default HTTP timeout for ESPN schedule requests.
	DefaultHTTPTimeout = 10 * time.Second

	// MinSlangCacheTTL is the shortest non-zero banter cache lifetime accepted by ParseSlangCacheTTL.
	MinSlangCacheTTL = 1 * time.Minute
	// MaxSlangCacheTTL is the longest banter cache lifetime accepted by ParseSlangCacheTTL.
	MaxSlangCacheTTL = 168 * time.Hour

	// SantiagoTimezone is the canonical IANA timezone identifier for all UC schedule calculations.
	SantiagoTimezone = "America/Santiago"

	// DefaultBanterPrompt is the system instruction sent to Vertex AI for Chilean football banter.
	DefaultBanterPrompt = "Write ONE short, witty Chilean Spanish football-fan banter sentence " +
		"for a group of friends checking whether their Universidad Catolica fan friend is free to hang out. " +
		"Use natural Chilean Spanish tuteo and light local football slang such as la franja, la Cato, cruzados, " +
		"armar junta, or panorama. Strictly forbid Argentinian voseo (vos, tenes, sos) and vosotros. " +
		"Do not include emojis, dates, clock times, scores, hashtags, @ mentions, angle brackets, or quotes. " +
		"Output ONLY a single line of plain text under 140 characters matching the scenario described by the user."

	homePreBlock  = 2 * time.Hour
	homePostBlock = 3*time.Hour + 30*time.Minute
	awayPreBlock  = 30 * time.Minute
	awayPostBlock = 2*time.Hour + 30*time.Minute
)

// LocalDate represents a calendar date without a time of day or timezone offset.
type LocalDate struct {
	Year  int
	Month time.Month
	Day   int
}

// IsZero reports whether d is the zero LocalDate.
func (d LocalDate) IsZero() bool {
	isZero := d.Year == 0 && d.Month == 0 && d.Day == 0
	return isZero
}

// String formats d as YYYY-MM-DD, or returns "" when d is zero.
func (d LocalDate) String() string {
	if d.IsZero() {
		return ""
	}

	formatted := fmt.Sprintf("%04d-%02d-%02d", d.Year, int(d.Month), d.Day)
	return formatted
}

// DDMM formats d as DD/MM, or returns "" when d is zero.
func (d LocalDate) DDMM() string {
	if d.IsZero() {
		return ""
	}

	formatted := fmt.Sprintf("%02d/%02d", d.Day, int(d.Month))
	return formatted
}

// StartOfDay returns the first valid local instant on calendar date d in loc (handling Chile's
// midnight spring-forward DST transition where 00:00 jumps directly to 01:00).
func (d LocalDate) StartOfDay(loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}

	start := time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, loc)
	shiftedToPrevDay := start.Year() != d.Year || start.Month() != d.Month || start.Day() != d.Day
	if shiftedToPrevDay {
		start = time.Date(d.Year, d.Month, d.Day, 1, 0, 0, 0, loc)
	}

	return start
}

// EndOfDay returns the final nanosecond of calendar date d in loc, derived from the next calendar
// day's StartOfDay so Chilean midnight fall-back DST transitions include the repeated 23:00-23:59
// hour.
func (d LocalDate) EndOfDay(loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}

	end := d.AddDays(1).StartOfDay(loc).Add(-time.Nanosecond)
	return end
}

// Weekday returns the day of the week for calendar date d, evaluated at midday to avoid midnight
// DST transitions.
func (d LocalDate) Weekday(loc *time.Location) time.Weekday {
	if loc == nil {
		loc = time.UTC
	}

	noon := time.Date(d.Year, d.Month, d.Day, 12, 0, 0, 0, loc)
	return noon.Weekday()
}

// AddDays returns the calendar date offset from d by days.
func (d LocalDate) AddDays(days int) LocalDate {
	noon := time.Date(d.Year, d.Month, d.Day, 12, 0, 0, 0, time.UTC).AddDate(0, 0, days)
	shifted := LocalDate{
		Year:  noon.Year(),
		Month: noon.Month(),
		Day:   noon.Day(),
	}

	return shifted
}

// Compare returns -1 when d is before other, 0 when equal, and +1 when d is after other.
func (d LocalDate) Compare(other LocalDate) int {
	if d.Year != other.Year {
		if d.Year < other.Year {
			return -1
		}

		return 1
	}

	if d.Month != other.Month {
		if d.Month < other.Month {
			return -1
		}

		return 1
	}

	if d.Day != other.Day {
		if d.Day < other.Day {
			return -1
		}

		return 1
	}

	return 0
}

// VenueCondition describes whether Universidad Catolica plays at home or away.
type VenueCondition string

const (
	// VenueHome indicates a home fixture (-2h to +3h30m availability block).
	VenueHome VenueCondition = "home"
	// VenueAway indicates an away fixture (-30m to +2h30m availability block).
	VenueAway VenueCondition = "away"
)

// AvailabilityStatus summarizes friend availability across a queried range or calendar day.
type AvailabilityStatus string

const (
	// StatusFree indicates no UC match overlaps the queried range.
	StatusFree AvailabilityStatus = "free"
	// StatusBlocked indicates at least one confirmed UC match block overlaps the queried range.
	StatusBlocked AvailabilityStatus = "blocked"
	// StatusPendingANFP indicates an unconfirmed TBD match falls in the range with no confirmed block.
	StatusPendingANFP AvailabilityStatus = "pending_anfp"
)

// WindowKind identifies a named time window accepted by Service.Check.
type WindowKind string

const (
	// WindowDefault selects the default weekend check when neither window nor date is specified.
	WindowDefault WindowKind = ""
	// WindowToday checks the current calendar day in America/Santiago.
	WindowToday WindowKind = "today"
	// WindowTomorrow checks the next calendar day in America/Santiago.
	WindowTomorrow WindowKind = "tomorrow"
	// WindowThisWeekend checks Friday 18:00 through Sunday 23:59:59 of the current Mon-Sun week.
	WindowThisWeekend WindowKind = "this_weekend"
	// WindowNextWeekend checks Friday 18:00 through Sunday 23:59:59 of the following Mon-Sun week.
	WindowNextWeekend WindowKind = "next_weekend"
	// WindowThisMonth checks the entire current calendar month in America/Santiago.
	WindowThisMonth WindowKind = "this_month"
)

// QueryMode identifies whether a Report answers a named window or an explicit calendar date.
type QueryMode string

const (
	// ModeWindow indicates Report answers a WindowKind query.
	ModeWindow QueryMode = "window"
	// ModeDate indicates Report answers an explicit calendar date query.
	ModeDate QueryMode = "date"
)

// Query specifies an optional GuildID (empty in direct messages) and either a named Window or a
// raw Date ("DD/MM" or "YYYY-MM-DD"), never both.
type Query struct {
	GuildID string
	Window  WindowKind
	Date    string
}

// GuildScope identifies the calling Discord guild and whether the caller holds server-management
// permission (ManageGuild or Administrator) for guild configuration operations.
type GuildScope struct {
	GuildID        string
	CanManageGuild bool
}

// GuildConfig holds one Discord guild's configured UC-fan friend and proactive alert channel.
// Empty FriendUserID or ChannelID indicates that field is not configured for the guild.
type GuildConfig struct {
	GuildID      string
	FriendUserID string
	ChannelID    string
}

// GuildConfigUpdate specifies fields to set on a guild's UC configuration. Nil fields are left
// unchanged; at least one field must be non-nil and hold a valid Discord snowflake.
type GuildConfigUpdate struct {
	FriendUserID *string
	ChannelID    *string
}

// ClearTarget selects which guild configuration fields Service.ClearGuildConfig resets.
type ClearTarget string

const (
	// ClearTargetAll clears both the guild's target friend and proactive alert channel.
	ClearTargetAll ClearTarget = "all"
	// ClearTargetFriend clears only the guild's target friend, keeping its alert channel.
	ClearTargetFriend ClearTarget = "friend"
	// ClearTargetChannel clears only the guild's proactive alert channel, keeping its target friend.
	ClearTargetChannel ClearTarget = "channel"
)

// Match represents one Universidad Catolica fixture and its computed availability block in
// America/Santiago time. When TBD is true, Kickoff, BlockStart, and BlockEnd are zero.
type Match struct {
	ID         string
	Opponent   string
	Tournament string
	Venue      string
	Condition  VenueCondition
	TBD        bool
	Date       LocalDate
	Kickoff    time.Time
	BlockStart time.Time
	BlockEnd   time.Time
}

// Report is the result of evaluating friend availability for a Query.
type Report struct {
	Mode         QueryMode
	Window       WindowKind
	QueriedDate  LocalDate
	RangeStart   time.Time
	RangeEnd     time.Time
	Status       AvailabilityStatus
	Matches      []Match
	NextMatch    *Match
	FriendUserID string
	Banter       string
}

// AlertKind distinguishes Friday weekend summaries from matchday morning reminders.
type AlertKind string

const (
	// AlertFridayWeekend is sent on Fridays when at least one UC match overlaps the weekend.
	AlertFridayWeekend AlertKind = "friday_weekend"
	// AlertMatchdayMorning is sent on matchday mornings for confirmed fixtures kicking off that day.
	AlertMatchdayMorning AlertKind = "matchday_morning"
)

// Alert is the domain payload delivered to AlertSender for one proactive channel notification.
type Alert struct {
	Kind           AlertKind
	GuildID        string
	FriendUserID   string
	Banter         string
	WeekendStart   LocalDate
	WeekendEnd     LocalDate
	SaturdayStatus AvailabilityStatus
	SundayStatus   AvailabilityStatus
	Matches        []Match
}

// Clock supplies the current instant.
type Clock interface {
	Now() time.Time
}

// SettingReader reads plain configuration values by setting key.
type SettingReader interface {
	Get(ctx context.Context, name string) (string, error)
}

// AlertSender delivers a proactive UC alert to a configured Discord channel.
type AlertSender interface {
	SendUCAlert(ctx context.Context, channelID string, alert Alert) error
}

// BanterGenerator generates one short Chilean Spanish football banter line for a scenario prompt.
type BanterGenerator interface {
	Generate(ctx context.Context, prompt string) (string, error)
}
