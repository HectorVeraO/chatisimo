package uccmd

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/disgoorg/disgo/discord"

	"github.com/HectorVeraO/chatisimo/internal/discord/internal/render"
	"github.com/HectorVeraO/chatisimo/internal/uccalendar"
)

const (
	maxReplyRunes = 1900
	clockLayout   = "15:04"
)

// ConfigReplyKind selects the header template rendered by RenderGuildConfigIn.
type ConfigReplyKind int

const (
	// ConfigReplyShow formats the current guild configuration for /uc-config show.
	ConfigReplyShow ConfigReplyKind = iota
	// ConfigReplySaved formats the updated guild configuration for /uc-config set.
	ConfigReplySaved
	// ConfigReplyCleared formats the resulting guild configuration for /uc-config clear.
	ConfigReplyCleared
)

// RenderGuildConfigIn formats a guild's UC calendar configuration in loc for /uc-config replies.
func RenderGuildConfigIn(loc Locale, kind ConfigReplyKind, cfg uccalendar.GuildConfig) string {
	c := CatalogFor(loc)
	friendLabel := resolveConfigUser(c, cfg.FriendUserID)
	channelLabel := resolveConfigChannel(c, cfg.ChannelID)

	msgID := textConfigShow
	switch kind {
	case ConfigReplySaved:
		msgID = textConfigSaved
	case ConfigReplyCleared:
		msgID = textConfigCleared
	case ConfigReplyShow:
		msgID = textConfigShow
	}

	formatted := c.Format(msgID, friendLabel, channelLabel)
	truncated := render.Truncate(formatted, maxReplyRunes, "...")
	return truncated
}

func resolveConfigUser(c Catalog, userID string) string {
	if userID != "" {
		mention, err := render.Mention(userID)
		if err == nil {
			return mention
		}
	}

	unset := c.Get(textConfigUnset)
	return unset
}

func resolveConfigChannel(c Catalog, channelID string) string {
	if channelID != "" {
		mention, err := render.ChannelMention(channelID)
		if err == nil {
			return mention
		}
	}

	unset := c.Get(textConfigUnset)
	return unset
}

// RenderReportIn formats a uccalendar.Report as localized Discord markdown in loc using tz for
// local block clock times and calendar labels. All external and LLM strings are escaped, and the
// final text is truncated to fit under Discord's message limit.
func RenderReportIn(loc Locale, report uccalendar.Report, tz *time.Location) string {
	if tz == nil {
		tz = time.UTC
	}

	c := CatalogFor(loc)
	friend, hasFriendMention := resolveFriend(c, report.FriendUserID)

	header := reportHeader(c, report, tz)

	matchLines := make([]string, 0, len(report.Matches))
	for _, match := range report.Matches {
		matchLines = append(matchLines, renderReportMatch(c, match, friend, tz))
	}

	tailLines := make([]string, 0, 2)
	tailLines = append(tailLines, renderReportBanter(c, report, friend, hasFriendMention))
	if shouldShowNextMatch(report) {
		tailLines = append(tailLines, renderNextMatch(c, *report.NextMatch, tz))
	}

	joined := joinReportWithinBudget(header, matchLines, tailLines)
	truncated := render.Truncate(joined, maxReplyRunes, "...")
	return truncated
}

func joinReportWithinBudget(header string, matchLines, tailLines []string) string {
	all := make([]string, 0, 1+len(matchLines)+len(tailLines))
	all = append(all, header)
	all = append(all, matchLines...)
	all = append(all, tailLines...)

	joined := strings.Join(all, "\n")
	if utf8.RuneCountInString(joined) <= maxReplyRunes {
		return joined
	}

	for keep := len(matchLines) - 1; keep >= 0; keep-- {
		candidate := make([]string, 0, 2+keep+len(tailLines))
		candidate = append(candidate, header)
		candidate = append(candidate, matchLines[:keep]...)
		candidate = append(candidate, "...")
		candidate = append(candidate, tailLines...)

		candidateJoined := strings.Join(candidate, "\n")
		if utf8.RuneCountInString(candidateJoined) <= maxReplyRunes {
			return candidateJoined
		}
	}

	return joined
}

// RenderAlertIn formats a proactive Friday weekend summary or matchday morning reminder in loc
// using tz for local block clock times, with AllowedMentions set to NoMentions.
func RenderAlertIn(loc Locale, alert uccalendar.Alert, tz *time.Location) (discord.MessageCreate, error) {
	if tz == nil {
		tz = time.UTC
	}

	c := CatalogFor(loc)
	friend, _ := resolveFriend(c, alert.FriendUserID)

	var lines []string
	switch alert.Kind {
	case uccalendar.AlertFridayWeekend:
		lines = renderFridayAlertLines(c, alert, friend, tz)
	case uccalendar.AlertMatchdayMorning:
		if len(alert.Matches) == 0 {
			return discord.MessageCreate{}, errors.New("uccmd: matchday alert has no matches")
		}
		var err error
		lines, err = renderMatchdayAlertLines(c, alert, friend, tz)
		if err != nil {
			return discord.MessageCreate{}, err
		}
	default:
		return discord.MessageCreate{}, errors.New("uccmd: unknown alert kind")
	}

	content := render.Truncate(strings.Join(lines, "\n"), maxReplyRunes, "...")
	noMentions := render.NoMentions()
	msg := discord.MessageCreate{
		Content:         content,
		AllowedMentions: &noMentions,
	}

	return msg, nil
}

func renderFridayAlertLines(c Catalog, alert uccalendar.Alert, friend string, tz *time.Location) []string {
	saturday := alert.WeekendStart.AddDays(1)
	sunday := alert.WeekendEnd

	lines := make([]string, 0, len(alert.Matches)+4)
	lines = append(lines, c.Format(textAlertFridayHeader, friend, saturday.DDMM(), sunday.DDMM()))

	for _, match := range alert.Matches {
		if match.TBD {
			line := c.Format(
				textAlertFridayTBDLine,
				weekdayLabel(c, match.Date, tz),
				match.Date.DDMM(),
				matchupLabel(c, match),
				venueLabel(c, match),
			)
			lines = append(lines, line)
			continue
		}

		matchDate := matchLocalDate(match, tz)
		line := c.Format(
			textAlertFridayConfirmedLine,
			weekdayLabel(c, matchDate, tz),
			matchDate.DDMM(),
			match.BlockStart.In(tz).Format(clockLayout),
			match.BlockEnd.In(tz).Format(clockLayout),
			matchupLabel(c, match),
			render.TimestampShortTime(match.Kickoff),
			venueLabel(c, match),
		)
		lines = append(lines, line)
	}

	if alert.SaturdayStatus == uccalendar.StatusFree {
		lines = append(lines, c.Format(textAlertWeekendFreeDay, c.Get(textWeekdaySat), saturday.DDMM()))
	}
	if alert.SundayStatus == uccalendar.StatusFree {
		lines = append(lines, c.Format(textAlertWeekendFreeDay, c.Get(textWeekdaySun), sunday.DDMM()))
	}

	banter := strings.TrimSpace(alert.Banter)
	if banter != "" {
		lines = append(lines, render.Text(banter))
	} else {
		lines = append(lines, c.Get(textAlertFridayFallback))
	}

	return lines
}

func renderMatchdayAlertLines(
	c Catalog,
	alert uccalendar.Alert,
	friend string,
	tz *time.Location,
) ([]string, error) {
	lines := make([]string, 0, len(alert.Matches)+1)
	for _, match := range alert.Matches {
		if match.TBD {
			return nil, errors.New("uccmd: matchday alert has unconfirmed match")
		}

		line := c.Format(
			textAlertMatchdayLine,
			render.TimestampShortTime(match.Kickoff),
			render.TimestampRelative(match.Kickoff),
			matchupLabel(c, match),
			venueLabel(c, match),
			friend,
			match.BlockStart.In(tz).Format(clockLayout),
			match.BlockEnd.In(tz).Format(clockLayout),
		)
		lines = append(lines, line)
	}

	banter := strings.TrimSpace(alert.Banter)
	if banter != "" {
		lines = append(lines, render.Text(banter))
	}

	return lines, nil
}

func resolveFriend(c Catalog, friendUserID string) (string, bool) {
	if friendUserID != "" {
		mention, err := render.Mention(friendUserID)
		if err == nil {
			return mention, true
		}
	}

	fallback := c.Get(textFriendFallback)
	return fallback, false
}

func reportHeader(c Catalog, report uccalendar.Report, tz *time.Location) string {
	period := periodLabel(c, report, tz)
	switch report.Status {
	case uccalendar.StatusBlocked:
		header := c.Format(textHeaderBlocked, period)
		return header
	case uccalendar.StatusPendingANFP:
		header := c.Format(textHeaderPending, period)
		return header
	default:
		header := c.Format(textHeaderFree, period)
		return header
	}
}

func periodLabel(c Catalog, report uccalendar.Report, tz *time.Location) string {
	if report.Mode == uccalendar.ModeDate {
		queried := report.QueriedDate
		if queried.IsZero() && !report.RangeStart.IsZero() {
			queried = localDateFromTime(report.RangeStart, tz)
		}
		formatted := c.Format(textPeriodDate, weekdayLabel(c, queried, tz), queried.DDMM())
		return formatted
	}

	switch report.Window {
	case uccalendar.WindowToday:
		return c.Get(textPeriodToday)
	case uccalendar.WindowTomorrow:
		return c.Get(textPeriodTomorrow)
	case uccalendar.WindowNextWeekend:
		sat, sun := weekendSaturdaySunday(report, tz)
		formatted := c.Format(textPeriodNextWeekend, sat.DDMM(), sun.DDMM())
		return formatted
	case uccalendar.WindowThisMonth:
		return c.Get(textPeriodThisMonth)
	default:
		sat, sun := weekendSaturdaySunday(report, tz)
		formatted := c.Format(textPeriodThisWeekend, sat.DDMM(), sun.DDMM())
		return formatted
	}
}

func weekendSaturdaySunday(report uccalendar.Report, tz *time.Location) (uccalendar.LocalDate, uccalendar.LocalDate) {
	var sat uccalendar.LocalDate
	if !report.RangeStart.IsZero() {
		startDate := localDateFromTime(report.RangeStart, tz)
		if startDate.Weekday(tz) == time.Friday {
			sat = startDate.AddDays(1)
		} else {
			sat = startDate
		}
	}

	var sun uccalendar.LocalDate
	if !report.RangeEnd.IsZero() {
		sun = localDateFromTime(report.RangeEnd, tz)
	} else if !sat.IsZero() {
		sun = sat.AddDays(1)
	}

	return sat, sun
}

func renderReportMatch(c Catalog, m uccalendar.Match, friend string, tz *time.Location) string {
	matchup := matchupLabel(c, m)
	venue := venueLabel(c, m)
	tournament := render.Text(m.Tournament)

	if m.TBD {
		tbdLine := c.Format(
			textMatchTBDLine,
			matchup,
			venue,
			tournament,
			weekdayLabel(c, m.Date, tz),
			m.Date.DDMM(),
			friend,
		)
		return tbdLine
	}

	matchDate := matchLocalDate(m, tz)
	confirmedLine := c.Format(
		textMatchConfirmedLine,
		matchup,
		venue,
		tournament,
		weekdayLabel(c, matchDate, tz),
		matchDate.DDMM(),
		render.TimestampShortTime(m.Kickoff),
		render.TimestampRelative(m.Kickoff),
		m.BlockStart.In(tz).Format(clockLayout),
		m.BlockEnd.In(tz).Format(clockLayout),
		friend,
	)
	return confirmedLine
}

func renderReportBanter(c Catalog, report uccalendar.Report, friend string, hasFriendMention bool) string {
	banter := strings.TrimSpace(report.Banter)
	if banter != "" {
		escaped := render.Text(banter)
		if hasFriendMention {
			withFriend := c.Format(textBanterWithFriend, escaped, friend)
			return withFriend
		}

		return escaped
	}

	switch report.Status {
	case uccalendar.StatusBlocked:
		primary := firstConfirmedMatch(report.Matches)
		if primary.Condition == uccalendar.VenueAway {
			awayText := c.Format(textBanterBlockedAway, friend)
			return awayText
		}

		homeText := c.Format(textBanterBlockedHome, friend)
		return homeText
	case uccalendar.StatusPendingANFP:
		pendingText := c.Format(textBanterPending, friend)
		return pendingText
	default:
		freeText := c.Format(textBanterFree, friend)
		return freeText
	}
}

func shouldShowNextMatch(report uccalendar.Report) bool {
	if report.NextMatch == nil {
		return false
	}

	isFree := report.Status == uccalendar.StatusFree
	isThisWeekend := report.Mode == uccalendar.ModeWindow &&
		(report.Window == uccalendar.WindowThisWeekend || report.Window == uccalendar.WindowDefault)
	shouldShow := isFree || isThisWeekend
	return shouldShow
}

func renderNextMatch(c Catalog, next uccalendar.Match, tz *time.Location) string {
	matchup := matchupLabel(c, next)
	venue := venueLabel(c, next)

	if next.TBD {
		tbdLine := c.Format(
			textNextMatchTBD,
			matchup,
			venue,
			weekdayLabel(c, next.Date, tz),
			next.Date.DDMM(),
		)
		return tbdLine
	}

	nextDate := matchLocalDate(next, tz)
	confirmedLine := c.Format(
		textNextMatchConfirmed,
		matchup,
		venue,
		weekdayLabel(c, nextDate, tz),
		nextDate.DDMM(),
		render.TimestampShortTime(next.Kickoff),
		render.TimestampRelative(next.Kickoff),
		next.BlockStart.In(tz).Format(clockLayout),
		next.BlockEnd.In(tz).Format(clockLayout),
	)
	return confirmedLine
}

func firstConfirmedMatch(matches []uccalendar.Match) uccalendar.Match {
	for _, match := range matches {
		if !match.TBD {
			return match
		}
	}
	if len(matches) > 0 {
		return matches[0]
	}

	return uccalendar.Match{}
}

func matchupLabel(c Catalog, m uccalendar.Match) string {
	opponent := render.Text(m.Opponent)
	if m.Condition == uccalendar.VenueHome {
		home := c.Format(textMatchupHome, opponent)
		return home
	}

	away := c.Format(textMatchupAway, opponent)
	return away
}

func venueLabel(c Catalog, m uccalendar.Match) string {
	venueName := strings.TrimSpace(m.Venue)
	isHome := m.Condition == uccalendar.VenueHome
	if isHome {
		if venueName != "" {
			withName := c.Format(textVenueHomeWithName, render.Text(venueName))
			return withName
		}

		return c.Get(textVenueHome)
	}

	if venueName != "" {
		withName := c.Format(textVenueAwayWithName, render.Text(venueName))
		return withName
	}

	return c.Get(textVenueAway)
}

func weekdayLabel(c Catalog, d uccalendar.LocalDate, tz *time.Location) string {
	switch d.Weekday(tz) {
	case time.Sunday:
		return c.Get(textWeekdaySun)
	case time.Monday:
		return c.Get(textWeekdayMon)
	case time.Tuesday:
		return c.Get(textWeekdayTue)
	case time.Wednesday:
		return c.Get(textWeekdayWed)
	case time.Thursday:
		return c.Get(textWeekdayThu)
	case time.Friday:
		return c.Get(textWeekdayFri)
	default:
		return c.Get(textWeekdaySat)
	}
}

func matchLocalDate(m uccalendar.Match, tz *time.Location) uccalendar.LocalDate {
	if !m.Date.IsZero() {
		return m.Date
	}
	if !m.Kickoff.IsZero() {
		return localDateFromTime(m.Kickoff, tz)
	}

	return uccalendar.LocalDate{}
}

func localDateFromTime(t time.Time, tz *time.Location) uccalendar.LocalDate {
	local := t.In(tz)
	d := uccalendar.LocalDate{
		Year:  local.Year(),
		Month: local.Month(),
		Day:   local.Day(),
	}

	return d
}
