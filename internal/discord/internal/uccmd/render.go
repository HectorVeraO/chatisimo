package uccmd

import (
	"context"
	"errors"
	"strings"
	"text/template"
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

var newlineReplacer = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ")

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

// MessageLocale resolves a stored UC guild locale ("" when unset, "es-CL", or "en-US") to the
// Discord adapter Locale used for UC schedule reports and proactive alerts, defaulting to Chilean
// Spanish (LocaleESCL).
func MessageLocale(raw string) Locale {
	if strings.TrimSpace(raw) == "" {
		return LocaleESCL
	}

	parsed, err := uccalendar.ParseGuildLocale(raw)
	if err != nil {
		return LocaleESCL
	}

	resolved := LocaleFromDiscord(parsed)
	return resolved
}

// RenderGuildConfigIn formats a guild's UC calendar configuration in loc for /uc-config replies.
func RenderGuildConfigIn(loc Locale, kind ConfigReplyKind, cfg uccalendar.GuildConfig) string {
	c := CatalogFor(loc)
	friendLabel := resolveConfigUser(c, cfg.FriendUserID)
	channelLabel := resolveConfigChannel(c, cfg.ChannelID)
	localeLabel := resolveConfigLocale(c, cfg.Locale)

	msgID := textConfigShow
	switch kind {
	case ConfigReplySaved:
		msgID = textConfigSaved
	case ConfigReplyCleared:
		msgID = textConfigCleared
	case ConfigReplyShow:
		msgID = textConfigShow
	}

	formatted := c.Format(msgID, friendLabel, channelLabel, localeLabel)
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

func resolveConfigLocale(c Catalog, rawLocale string) string {
	if rawLocale != "" {
		parsed, err := uccalendar.ParseGuildLocale(rawLocale)
		if err == nil {
			return parsed
		}
	}

	defaultLabel := c.Get(textConfigLocaleDefault)
	return defaultLabel
}

// RenderReportIn formats a uccalendar.Report as localized Discord markdown in loc using the
// embedded default report template and tz for local block clock times and calendar labels.
func RenderReportIn(loc Locale, report uccalendar.Report, tz *time.Location) string {
	return RenderReportWith(context.Background(), nil, loc, report, tz)
}

// RenderReportWith formats a uccalendar.Report as localized Discord markdown in loc using cache
// (falling back to the embedded default template when cache is nil or unset) and tz for local
// block clock times and calendar labels. All external and LLM strings are pre-escaped, and the
// final text is truncated to fit under Discord's message limit.
func RenderReportWith(
	ctx context.Context,
	cache *TemplateCache,
	loc Locale,
	report uccalendar.Report,
	tz *time.Location,
) string {
	if tz == nil {
		tz = time.UTC
	}

	c := CatalogFor(loc)
	effectiveLoc := c.Locale()
	view := buildReportView(c, report, tz)

	templates := cache.resolve(ctx)
	primaryTmpl := templates.reportFor(effectiveLoc)
	fallbackTmpl := defaultTemplates().reportFor(effectiveLoc)

	rendered := renderReportWithinBudget(primaryTmpl, fallbackTmpl, view)
	truncated := render.Truncate(rendered, maxReplyRunes, "...")
	return truncated
}

func buildReportView(c Catalog, report uccalendar.Report, tz *time.Location) ReportView {
	friend, hasFriend := resolveFriend(c, report.FriendUserID)

	matches := make([]MatchView, 0, len(report.Matches))
	for _, match := range report.Matches {
		matches = append(matches, buildMatchView(c, match, tz))
	}

	var nextView *MatchView
	if shouldShowNextMatch(report) {
		builtNext := buildMatchView(c, *report.NextMatch, tz)
		nextView = &builtNext
	}

	status := report.Status
	isBlocked := status == uccalendar.StatusBlocked
	isPending := status == uccalendar.StatusPendingANFP
	isFree := !isBlocked && !isPending

	view := ReportView{
		Status:      string(status),
		Blocked:     isBlocked,
		PendingANFP: isPending,
		Free:        isFree,
		Period:      periodLabel(c, report, tz),
		Friend:      friend,
		HasFriend:   hasFriend,
		Matches:     matches,
		Banter:      resolveReportBanter(c, report),
		NextMatch:   nextView,
	}
	return view
}

func renderReportWithinBudget(primary, fallback *template.Template, view ReportView) string {
	rendered := executeWithFallback(primary, fallback, view)
	if utf8.RuneCountInString(rendered) <= maxReplyRunes || len(view.Matches) == 0 {
		return rendered
	}

	allMatches := view.Matches
	for keep := len(allMatches) - 1; keep >= 0; keep-- {
		candidateView := view
		candidateView.Matches = allMatches[:keep]
		candidateView.Overflowed = true
		candidateView.OmittedMatches = len(allMatches) - keep

		candidate := executeWithFallback(primary, fallback, candidateView)
		if utf8.RuneCountInString(candidate) <= maxReplyRunes {
			return candidate
		}
	}

	return rendered
}

func executeWithFallback(primary, fallback *template.Template, data any) string {
	out, err := executeTemplate(primary, data)
	if err == nil && out != "" {
		return out
	}

	fallbackOut, fallbackErr := executeTemplate(fallback, data)
	if fallbackErr == nil {
		return fallbackOut
	}

	return ""
}

// RenderAlertIn formats a proactive Friday weekend summary or matchday morning reminder in loc
// using the embedded default alert template and tz for local block clock times, with
// AllowedMentions set to NoMentions.
func RenderAlertIn(loc Locale, alert uccalendar.Alert, tz *time.Location) (discord.MessageCreate, error) {
	return RenderAlertWith(context.Background(), nil, loc, alert, tz)
}

// RenderAlertWith formats a proactive Friday weekend summary or matchday morning reminder in loc
// using cache (falling back to the embedded default template when cache is nil or unset) and tz
// for local block clock times, with AllowedMentions set to NoMentions.
func RenderAlertWith(
	ctx context.Context,
	cache *TemplateCache,
	loc Locale,
	alert uccalendar.Alert,
	tz *time.Location,
) (discord.MessageCreate, error) {
	if tz == nil {
		tz = time.UTC
	}

	c := CatalogFor(loc)
	effectiveLoc := c.Locale()
	templates := cache.resolve(ctx)
	defaults := defaultTemplates()

	var rendered string
	switch alert.Kind {
	case uccalendar.AlertFridayWeekend:
		view := buildFridayAlertView(c, alert, tz)
		rendered = executeWithFallback(
			templates.fridayFor(effectiveLoc),
			defaults.fridayFor(effectiveLoc),
			view,
		)
	case uccalendar.AlertMatchdayMorning:
		view, err := buildMatchdayAlertView(c, alert, tz)
		if err != nil {
			return discord.MessageCreate{}, err
		}
		rendered = executeWithFallback(
			templates.matchdayFor(effectiveLoc),
			defaults.matchdayFor(effectiveLoc),
			view,
		)
	default:
		return discord.MessageCreate{}, errors.New("uccmd: unknown alert kind")
	}

	content := render.Truncate(rendered, maxReplyRunes, "...")
	noMentions := render.NoMentions()
	msg := discord.MessageCreate{
		Content:         content,
		AllowedMentions: &noMentions,
	}

	return msg, nil
}

func buildFridayAlertView(c Catalog, alert uccalendar.Alert, tz *time.Location) FridayAlertView {
	friend, hasFriend := resolveFriend(c, alert.FriendUserID)
	saturday := alert.WeekendStart.AddDays(1)
	sunday := alert.WeekendEnd

	matches := make([]MatchView, 0, len(alert.Matches))
	for _, match := range alert.Matches {
		matches = append(matches, buildMatchView(c, match, tz))
	}

	banter := cleanInlineText(alert.Banter)
	if banter == "" {
		banter = c.Get(textAlertFridayFallback)
	}

	view := FridayAlertView{
		Friend:          friend,
		HasFriend:       hasFriend,
		SaturdayWeekday: c.Get(textWeekdaySat),
		SaturdayDate:    saturday.DDMM(),
		SaturdayStatus:  string(alert.SaturdayStatus),
		SaturdayFree:    alert.SaturdayStatus == uccalendar.StatusFree,
		SundayWeekday:   c.Get(textWeekdaySun),
		SundayDate:      sunday.DDMM(),
		SundayStatus:    string(alert.SundayStatus),
		SundayFree:      alert.SundayStatus == uccalendar.StatusFree,
		Matches:         matches,
		Banter:          banter,
	}
	return view
}

func buildMatchdayAlertView(
	c Catalog,
	alert uccalendar.Alert,
	tz *time.Location,
) (MatchdayAlertView, error) {
	if len(alert.Matches) == 0 {
		return MatchdayAlertView{}, errors.New("uccmd: matchday alert has no matches")
	}

	friend, hasFriend := resolveFriend(c, alert.FriendUserID)
	matches := make([]MatchView, 0, len(alert.Matches))
	for _, match := range alert.Matches {
		if match.TBD {
			return MatchdayAlertView{}, errors.New("uccmd: matchday alert has unconfirmed match")
		}
		matches = append(matches, buildMatchView(c, match, tz))
	}

	view := MatchdayAlertView{
		Friend:    friend,
		HasFriend: hasFriend,
		Matches:   matches,
		Banter:    cleanInlineText(alert.Banter),
	}
	return view, nil
}

func buildMatchView(c Catalog, m uccalendar.Match, tz *time.Location) MatchView {
	opponent := cleanInlineText(m.Opponent)
	tournament := cleanInlineText(m.Tournament)
	isHome := m.Condition == uccalendar.VenueHome

	if m.TBD {
		tbdView := MatchView{
			Matchup:    matchupLabel(c, m),
			Opponent:   opponent,
			Tournament: tournament,
			Venue:      venueLabel(c, m),
			Home:       isHome,
			TBD:        true,
			Weekday:    weekdayLabel(c, m.Date, tz),
			Date:       m.Date.DDMM(),
		}
		return tbdView
	}

	matchDate := matchLocalDate(m, tz)
	confirmedView := MatchView{
		Matchup:         matchupLabel(c, m),
		Opponent:        opponent,
		Tournament:      tournament,
		Venue:           venueLabel(c, m),
		Home:            isHome,
		TBD:             false,
		Weekday:         weekdayLabel(c, matchDate, tz),
		Date:            matchDate.DDMM(),
		KickoffTime:     render.TimestampShortTime(m.Kickoff),
		KickoffRelative: render.TimestampRelative(m.Kickoff),
		BlockStart:      m.BlockStart.In(tz).Format(clockLayout),
		BlockEnd:        m.BlockEnd.In(tz).Format(clockLayout),
	}
	return confirmedView
}

func cleanInlineText(raw string) string {
	singleLine := strings.TrimSpace(newlineReplacer.Replace(raw))
	if singleLine == "" {
		return ""
	}

	escaped := render.Text(singleLine)
	return escaped
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

func resolveReportBanter(c Catalog, report uccalendar.Report) string {
	escaped := cleanInlineText(report.Banter)
	if escaped != "" {
		return escaped
	}

	switch report.Status {
	case uccalendar.StatusBlocked:
		primary := firstConfirmedMatch(report.Matches)
		if primary.Condition == uccalendar.VenueAway {
			return c.Get(textBanterBlockedAway)
		}

		return c.Get(textBanterBlockedHome)
	case uccalendar.StatusPendingANFP:
		return c.Get(textBanterPending)
	default:
		return c.Get(textBanterFree)
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
	opponent := cleanInlineText(m.Opponent)
	if m.Condition == uccalendar.VenueHome {
		home := c.Format(textMatchupHome, opponent)
		return home
	}

	away := c.Format(textMatchupAway, opponent)
	return away
}

func venueLabel(c Catalog, m uccalendar.Match) string {
	venueName := cleanInlineText(m.Venue)
	isHome := m.Condition == uccalendar.VenueHome
	if isHome {
		if venueName != "" {
			withName := c.Format(textVenueHomeWithName, venueName)
			return withName
		}

		return c.Get(textVenueHome)
	}

	if venueName != "" {
		withName := c.Format(textVenueAwayWithName, venueName)
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
