package uccmd_test

import (
	"fmt"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"
	"unicode/utf8"

	"github.com/disgoorg/disgo/discord"

	"github.com/HectorVeraO/chatisimo/internal/discord/internal/uccmd"
	"github.com/HectorVeraO/chatisimo/internal/uccalendar"
)

const testFriendID = "100000000000000001"

func santiagoTZ(t *testing.T) *time.Location {
	t.Helper()

	tz, err := time.LoadLocation(uccalendar.SantiagoTimezone)
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}

	return tz
}

func sampleConfirmedHomeMatch(tz *time.Location) uccalendar.Match {
	kickoff := time.Date(2026, time.November, 7, 18, 0, 0, 0, tz)
	m := uccalendar.Match{
		ID:         "401001",
		Opponent:   "Colo-Colo",
		Tournament: "Liga de Primera",
		Venue:      "Claro Arena",
		Condition:  uccalendar.VenueHome,
		TBD:        false,
		Date:       uccalendar.LocalDate{Year: 2026, Month: time.November, Day: 7},
		Kickoff:    kickoff,
		BlockStart: kickoff.Add(-2 * time.Hour),
		BlockEnd:   kickoff.Add(3*time.Hour + 30*time.Minute),
	}

	return m
}

func sampleConfirmedAwayMatch(tz *time.Location) uccalendar.Match {
	kickoff := time.Date(2026, time.November, 8, 20, 30, 0, 0, tz)
	m := uccalendar.Match{
		ID:         "401002",
		Opponent:   "Palestino",
		Tournament: "Copa Chile",
		Venue:      "",
		Condition:  uccalendar.VenueAway,
		TBD:        false,
		Date:       uccalendar.LocalDate{Year: 2026, Month: time.November, Day: 8},
		Kickoff:    kickoff,
		BlockStart: kickoff.Add(-30 * time.Minute),
		BlockEnd:   kickoff.Add(2*time.Hour + 30*time.Minute),
	}

	return m
}

func sampleTBDMatch() uccalendar.Match {
	m := uccalendar.Match{
		ID:         "401003",
		Opponent:   "Cobreloa",
		Tournament: "Liga de Primera",
		Venue:      "Claro Arena",
		Condition:  uccalendar.VenueHome,
		TBD:        true,
		Date:       uccalendar.LocalDate{Year: 2026, Month: time.November, Day: 15},
	}

	return m
}

func TestRenderReportInStatusesPeriodsAndLocales(t *testing.T) {
	t.Parallel()

	tz := santiagoTZ(t)
	home := sampleConfirmedHomeMatch(tz)
	away := sampleConfirmedAwayMatch(tz)
	tbd := sampleTBDMatch()

	friStart := time.Date(2026, time.November, 6, 18, 0, 0, 0, tz)
	sunEnd := time.Date(2026, time.November, 8, 23, 59, 59, 0, tz)

	tests := []struct {
		name     string
		loc      uccmd.Locale
		report   uccalendar.Report
		wantSubs []string
		omitSubs []string
	}{
		{
			name: "blocked home this weekend in Chilean Spanish with friend and next match",
			loc:  uccmd.LocaleESCL,
			report: uccalendar.Report{
				Mode:         uccalendar.ModeWindow,
				Window:       uccalendar.WindowThisWeekend,
				RangeStart:   friStart,
				RangeEnd:     sunEnd,
				Status:       uccalendar.StatusBlocked,
				Matches:      []uccalendar.Match{home},
				NextMatch:    &tbd,
				FriendUserID: testFriendID,
			},
			wantSubs: []string{
				"\u26aa\U0001f535 \U0001f534 **Bloqueado \u00b7 este finde (07/11-08/11)**",
				"\u26bd **Universidad Cat\u00f3lica vs Colo-Colo**",
				"- Local \U0001f3df\ufe0f Claro Arena \u00b7 Liga de Primera",
				"- **Inicio:** s\u00e1bado 07/11 a las " + fmt.Sprintf("<t:%d:t>", home.Kickoff.Unix()),
				fmt.Sprintf("(<t:%d:R>)", home.Kickoff.Unix()),
				"- **Bloqueo:** 16:00 a 21:30 hrs para <@" + testFriendID + ">",
				"> Olv\u00eddense de mi rey en esa ventana, kings: va rumbo a la cancha a ver a la Cato.",
				"-# Pr\u00f3ximo partido: Universidad Cat\u00f3lica vs Cobreloa",
				"domingo 15/11 \u00b7 horario pendiente ANFP",
			},
			omitSubs: []string{
				"(<@" + testFriendID + ">)",
				"((",
			},
		},
		{
			name: "blocked away today in English omits next match and uses friend fallback",
			loc:  uccmd.LocaleENUS,
			report: uccalendar.Report{
				Mode:      uccalendar.ModeWindow,
				Window:    uccalendar.WindowToday,
				Status:    uccalendar.StatusBlocked,
				Matches:   []uccalendar.Match{away},
				NextMatch: &tbd,
			},
			wantSubs: []string{
				"\u26aa\U0001f535 \U0001f534 **Blocked \u00b7 today**",
				"\u26bd **Palestino vs Universidad Catolica**",
				"- Away \U0001f4fa \u00b7 Copa Chile",
				"- **Kickoff:** Sunday 08/11",
				"- **Unavailable:** 20:00 to 23:00 for the UC fan",
				"> Count our king out during that window: glued to the screen for UC.",
			},
			omitSubs: []string{
				"Next match:",
			},
		},
		{
			name: "pending ANFP on explicit date in Spanish",
			loc:  uccmd.LocaleESCL,
			report: uccalendar.Report{
				Mode:        uccalendar.ModeDate,
				QueriedDate: tbd.Date,
				Status:      uccalendar.StatusPendingANFP,
				Matches:     []uccalendar.Match{tbd},
				NextMatch:   &home,
			},
			wantSubs: []string{
				"\u26aa\U0001f535 \U0001f7e1 **Pendiente ANFP \u00b7 domingo 15/11**",
				"\u26bd **Universidad Cat\u00f3lica vs Cobreloa**",
				"- **Fecha:** domingo 15/11 \u00b7 horario pendiente ANFP para el hincha cruzado",
				"> La ANFP todav\u00eda no suelta la hora exacta, kings; armar junta con mi chanchito es deporte extremo.",
			},
			omitSubs: []string{
				"Pr\u00f3ximo partido:",
			},
		},
		{
			name: "free tomorrow in English shows single friend mention in availability bullet and no duplicate on banter",
			loc:  uccmd.LocaleENUS,
			report: uccalendar.Report{
				Mode:         uccalendar.ModeWindow,
				Window:       uccalendar.WindowTomorrow,
				Status:       uccalendar.StatusFree,
				NextMatch:    &home,
				FriendUserID: testFriendID,
				Banter:       "Free to fire up the grill",
			},
			wantSubs: []string{
				"\u26aa\U0001f535 \U0001f7e2 **All clear \u00b7 tomorrow**",
				"- **Availability:** <@" + testFriendID + "> has no UC match",
				"> Free to fire up the grill",
				"-# Next match: Universidad Catolica vs Colo-Colo",
				"unavailable 16:00 to 21:30",
			},
			omitSubs: []string{
				"Free to fire up the grill (<@" + testFriendID + ">)",
			},
		},
		{
			name: "free next weekend period label without nested parentheses",
			loc:  uccmd.LocaleENUS,
			report: uccalendar.Report{
				Mode:       uccalendar.ModeWindow,
				Window:     uccalendar.WindowNextWeekend,
				RangeStart: friStart.AddDate(0, 0, 7),
				RangeEnd:   sunEnd.AddDate(0, 0, 7),
				Status:     uccalendar.StatusFree,
			},
			wantSubs: []string{
				"\u26aa\U0001f535 \U0001f7e2 **All clear \u00b7 next weekend (14/11-15/11)**",
			},
			omitSubs: []string{
				"((",
			},
		},
		{
			name: "free this month in Spanish with invalid friend snowflake falls back cleanly",
			loc:  uccmd.LocaleESCL,
			report: uccalendar.Report{
				Mode:         uccalendar.ModeWindow,
				Window:       uccalendar.WindowThisMonth,
				Status:       uccalendar.StatusFree,
				FriendUserID: "@everyone",
				Banter:       "Todo despejado por ahora, mi rey",
			},
			wantSubs: []string{
				"\u26aa\U0001f535 \U0001f7e2 **V\u00eda libre \u00b7 este mes**",
				"- **Disponibilidad:** el hincha cruzado sin partido de la UC",
				"> Todo despejado por ahora, mi rey",
			},
			omitSubs: []string{
				"@everyone",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := uccmd.RenderReportIn(tt.loc, tt.report, tz)
			for _, want := range tt.wantSubs {
				if !strings.Contains(got, want) {
					t.Errorf("RenderReportIn missing %q in:\n%s", want, got)
				}
			}
			for _, omit := range tt.omitSubs {
				if strings.Contains(got, omit) {
					t.Errorf("RenderReportIn unexpectedly contains %q in:\n%s", omit, got)
				}
			}
		})
	}
}

func TestRenderReportInEscapesHostileStringsAndTruncatesTo1900Runes(t *testing.T) {
	t.Parallel()

	tz := santiagoTZ(t)
	hostile := sampleConfirmedHomeMatch(tz)
	hostile.Opponent = "@everyone <@999>"
	hostile.Tournament = "**Cup** [x](https://example.com)"
	hostile.Venue = "`Stadium` ||secret||"

	matches := make([]uccalendar.Match, 0, 20)
	for range 20 {
		matches = append(matches, hostile)
	}

	report := uccalendar.Report{
		Mode:         uccalendar.ModeWindow,
		Window:       uccalendar.WindowThisMonth,
		Status:       uccalendar.StatusBlocked,
		Matches:      matches,
		FriendUserID: testFriendID,
		Banter:       "@here <@888> " + strings.Repeat("x", 120),
	}

	got := uccmd.RenderReportIn(uccmd.LocaleENUS, report, tz)
	if count := utf8.RuneCountInString(got); count > 1900 {
		t.Errorf("rune count = %d, want <= 1900", count)
	}
	if !strings.Contains(got, "\n...\n") {
		t.Errorf("overflow report missing line-boundary '...' marker: %q", got)
	}
	if !strings.Contains(got, "<@"+testFriendID+">") {
		t.Errorf("overflow report dropped friend mention <@%s>: %q", testFriendID, got)
	}
	for _, wantEscaped := range []string{`\@everyone \<\@999\>`, `\*\*Cup\*\*`, `\[x\]`, `\@here \<\@888\>`} {
		if !strings.Contains(got, wantEscaped) {
			t.Errorf("expected escaped %q in:\n%s", wantEscaped, got)
		}
	}
}

func TestRenderAlertInFormatsFridayAndMatchdayWithNoMentions(t *testing.T) {
	t.Parallel()

	tz := santiagoTZ(t)
	home := sampleConfirmedHomeMatch(tz)
	tbd := sampleTBDMatch()
	tbd.Date = uccalendar.LocalDate{Year: 2026, Month: time.November, Day: 8}

	friAlert := uccalendar.Alert{
		Kind:           uccalendar.AlertFridayWeekend,
		FriendUserID:   testFriendID,
		Banter:         "@everyone ojo con el s\u00e1bado",
		WeekendStart:   uccalendar.LocalDate{Year: 2026, Month: time.November, Day: 6},
		WeekendEnd:     uccalendar.LocalDate{Year: 2026, Month: time.November, Day: 8},
		SaturdayStatus: uccalendar.StatusBlocked,
		SundayStatus:   uccalendar.StatusFree,
		Matches:        []uccalendar.Match{home, tbd},
	}

	friMsg, err := uccmd.RenderAlertIn(uccmd.LocaleESCL, friAlert, tz)
	if err != nil {
		t.Fatalf("RenderAlertIn(friday): %v", err)
	}
	assertNoMentionsOnCreate(t, friMsg.AllowedMentions)
	for _, want := range []string{
		"\u26aa\U0001f535 **Reporte cruzado de finde \u00b7 07/11-08/11**",
		"- **Hincha:** <@" + testFriendID + ">",
		"- \U0001f534 **s\u00e1bado 07/11** (16:00 a 21:30 hrs)",
		"- \U0001f7e1 **domingo 08/11** (pendiente ANFP)",
		"- \U0001f7e2 **domingo 08/11** libre para armar panorama",
		`> \@everyone ojo con el s` + "\u00e1" + `bado`,
	} {
		if !strings.Contains(friMsg.Content, want) {
			t.Errorf("friday alert missing %q in:\n%s", want, friMsg.Content)
		}
	}

	matchdayAlert := uccalendar.Alert{
		Kind:         uccalendar.AlertMatchdayMorning,
		FriendUserID: testFriendID,
		Banter:       "Hoy juega la Cato, kings",
		Matches:      []uccalendar.Match{home},
	}
	matchMsg, err := uccmd.RenderAlertIn(uccmd.LocaleESCL, matchdayAlert, tz)
	if err != nil {
		t.Fatalf("RenderAlertIn(matchday): %v", err)
	}
	assertNoMentionsOnCreate(t, matchMsg.AllowedMentions)
	for _, want := range []string{
		"\u26aa\U0001f535 **Hoy juega la franja**",
		"\u26bd **Universidad Cat\u00f3lica vs Colo-Colo**",
		fmt.Sprintf("<t:%d:t>", home.Kickoff.Unix()),
		fmt.Sprintf("<t:%d:R>", home.Kickoff.Unix()),
		"- **Bloqueo:** <@" + testFriendID + "> no disponible de **16:00 a 21:30 hrs**",
		"\n> Hoy juega la Cato, kings",
	} {
		if !strings.Contains(matchMsg.Content, want) {
			t.Errorf("matchday alert missing %q in:\n%s", want, matchMsg.Content)
		}
	}
}

func TestRenderAlertInRejectsInvalidPayloads(t *testing.T) {
	t.Parallel()

	tz := santiagoTZ(t)
	tests := []struct {
		name  string
		alert uccalendar.Alert
	}{
		{
			name:  "unknown alert kind",
			alert: uccalendar.Alert{Kind: uccalendar.AlertKind("mystery")},
		},
		{
			name:  "matchday with empty matches",
			alert: uccalendar.Alert{Kind: uccalendar.AlertMatchdayMorning},
		},
		{
			name: "matchday with TBD match",
			alert: uccalendar.Alert{
				Kind:    uccalendar.AlertMatchdayMorning,
				Matches: []uccalendar.Match{sampleTBDMatch()},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := uccmd.RenderAlertIn(uccmd.LocaleESCL, tt.alert, tz)
			if err == nil {
				t.Fatal("RenderAlertIn succeeded, want error")
			}
		})
	}
}

func assertNoMentionsOnCreate(t *testing.T, allowed *discord.AllowedMentions) {
	t.Helper()

	if allowed == nil {
		t.Fatal("AllowedMentions is nil, want non-nil NoMentions")
	}

	hasEmptySlices := allowed.Parse != nil && allowed.Users != nil && allowed.Roles != nil &&
		len(allowed.Parse) == 0 && len(allowed.Users) == 0 && len(allowed.Roles) == 0
	if !hasEmptySlices || allowed.RepliedUser {
		t.Fatalf("AllowedMentions = %+v, want empty non-nil slices and RepliedUser=false", allowed)
	}
}

func TestRenderReportAndAlertCoverAllWeekdaysEmptyVenuesAndNilLocation(t *testing.T) {
	t.Parallel()

	weekdayDates := []struct {
		day  int
		want string
	}{
		{day: 2, want: "Monday 02/11"},
		{day: 3, want: "Tuesday 03/11"},
		{day: 4, want: "Wednesday 04/11"},
		{day: 5, want: "Thursday 05/11"},
		{day: 6, want: "Friday 06/11"},
	}
	for _, wd := range weekdayDates {
		got := uccmd.RenderReportIn(uccmd.LocaleENUS, uccalendar.Report{
			Mode:        uccalendar.ModeDate,
			QueriedDate: uccalendar.LocalDate{Year: 2026, Month: time.November, Day: wd.day},
			Status:      uccalendar.StatusFree,
		}, nil)
		if !strings.Contains(got, wd.want) {
			t.Errorf("day %d: missing %q in %q", wd.day, wd.want, got)
		}
	}

	// Empty venue names on home/away matches and zero Date falling back to Kickoff.
	kickoff := time.Date(2026, time.November, 8, 19, 0, 0, 0, time.UTC)
	homeNoVenue := uccalendar.Match{
		ID:         "101",
		Kickoff:    kickoff,
		Opponent:   "Palestino",
		Condition:  uccalendar.VenueHome,
		Tournament: "Copa Chile",
		BlockStart: kickoff.Add(-2 * time.Hour),
		BlockEnd:   kickoff.Add(3 * time.Hour),
	}
	awayNoVenue := uccalendar.Match{
		ID:         "102",
		Date:       uccalendar.LocalDate{Year: 2026, Month: time.November, Day: 14},
		Opponent:   "Cobresal",
		Condition:  uccalendar.VenueAway,
		Tournament: "Liga de Primera",
		TBD:        true,
	}

	reportOut := uccmd.RenderReportIn(uccmd.LocaleENUS, uccalendar.Report{
		Mode:    uccalendar.ModeWindow,
		Window:  uccalendar.WindowThisWeekend,
		Status:  uccalendar.StatusBlocked,
		Matches: []uccalendar.Match{homeNoVenue, awayNoVenue},
	}, nil)
	for _, want := range []string{"- Home \U0001f3df\ufe0f \u00b7 Copa Chile", "- Away \U0001f4fa \u00b7 Liga de Primera"} {
		if !strings.Contains(reportOut, want) {
			t.Errorf("empty-venue report missing %q in:\n%s", want, reportOut)
		}
	}

	friMsg, err := uccmd.RenderAlertIn(uccmd.LocaleENUS, uccalendar.Alert{
		Kind:           uccalendar.AlertFridayWeekend,
		WeekendStart:   uccalendar.LocalDate{Year: 2026, Month: time.November, Day: 6},
		WeekendEnd:     uccalendar.LocalDate{Year: 2026, Month: time.November, Day: 8},
		SaturdayStatus: uccalendar.StatusFree,
		SundayStatus:   uccalendar.StatusPendingANFP,
		Matches:        []uccalendar.Match{awayNoVenue},
	}, nil)
	if err != nil {
		t.Fatalf("RenderAlertIn(nil tz): %v", err)
	}
	for _, want := range []string{
		"- \U0001f7e2 **Saturday 07/11** free for plans",
		"> Keep those UC windows in mind before locking in weekend plans.",
	} {
		if !strings.Contains(friMsg.Content, want) {
			t.Errorf("friday fallback alert missing %q in:\n%s", want, friMsg.Content)
		}
	}
}

func TestRenderGuildConfigInFormatsShowSavedClearedAndUnsetInBothLocales(t *testing.T) {
	t.Parallel()

	const testChannelID = "300000000000000001"

	tests := []struct {
		name     string
		loc      uccmd.Locale
		kind     uccmd.ConfigReplyKind
		cfg      uccalendar.GuildConfig
		wantSubs []string
		omitSubs []string
	}{
		{
			name: "show with all unset in English displays es-CL default",
			loc:  uccmd.LocaleENUS,
			kind: uccmd.ConfigReplyShow,
			cfg:  uccalendar.GuildConfig{},
			wantSubs: []string{
				"**UC calendar configuration for this server**",
				"- **UC-fan friend:** not configured",
				"- **Proactive alert channel:** not configured",
				"- **Message language:** es-CL (default)",
			},
		},
		{
			name: "saved with friend channel and explicit en-US locale in Chilean Spanish",
			loc:  uccmd.LocaleESCL,
			kind: uccmd.ConfigReplySaved,
			cfg: uccalendar.GuildConfig{
				FriendUserID: testFriendID,
				ChannelID:    testChannelID,
				Locale:       uccalendar.LocaleENUS,
			},
			wantSubs: []string{
				"**Configuraci\u00f3n del calendario UC actualizada**",
				"- **Hincha cruzado:** <@" + testFriendID + ">",
				"- **Canal de alertas:** <#" + testChannelID + ">",
				"- **Idioma de mensajes:** en-US",
			},
		},
		{
			name: "cleared friend only in English keeps channel link and default locale",
			loc:  uccmd.LocaleENUS,
			kind: uccmd.ConfigReplyCleared,
			cfg: uccalendar.GuildConfig{
				ChannelID: testChannelID,
			},
			wantSubs: []string{
				"**UC calendar configuration cleared**",
				"- **UC-fan friend:** not configured",
				"- **Proactive alert channel:** <#" + testChannelID + ">",
				"- **Message language:** es-CL (default)",
			},
		},
		{
			name: "invalid non-snowflake IDs and locale fall back to unset/default without injecting raw text",
			loc:  uccmd.LocaleESCL,
			kind: uccmd.ConfigReplyShow,
			cfg: uccalendar.GuildConfig{
				FriendUserID: "@everyone",
				ChannelID:    "<#bad>",
				Locale:       "@here-bad-locale",
			},
			wantSubs: []string{
				"- **Hincha cruzado:** sin configurar",
				"- **Canal de alertas:** sin configurar",
				"- **Idioma de mensajes:** es-CL (por defecto)",
			},
			omitSubs: []string{
				"@everyone",
				"<#bad>",
				"@here-bad-locale",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := uccmd.RenderGuildConfigIn(tt.loc, tt.kind, tt.cfg)
			for _, want := range tt.wantSubs {
				if !strings.Contains(got, want) {
					t.Errorf("RenderGuildConfigIn missing %q in:\n%s", want, got)
				}
			}
			for _, omit := range tt.omitSubs {
				if strings.Contains(got, omit) {
					t.Errorf("RenderGuildConfigIn unexpectedly contains %q in:\n%s", omit, got)
				}
			}
		})
	}
}
