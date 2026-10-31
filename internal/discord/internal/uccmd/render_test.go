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
				"\U0001f534 **Bloqueado (este finde (07/11-08/11)): Juega la franja**",
				"**Universidad Cat\u00f3lica vs Colo-Colo** (Local \U0001f3df\ufe0f Claro Arena - Liga de Primera)",
				"s\u00e1bado 07/11",
				fmt.Sprintf("<t:%d:t>", home.Kickoff.Unix()),
				fmt.Sprintf("<t:%d:R>", home.Kickoff.Unix()),
				"de 16:00 a 21:30 hrs para <@" + testFriendID + ">",
				"rumbo a la cancha a ver a la Cato",
				"*(Ojo - pr\u00f3ximo partido: **Universidad Cat\u00f3lica vs Cobreloa**",
				"domingo 15/11",
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
				"\U0001f534 **Blocked (today): UC plays**",
				"**Palestino vs Universidad Catolica** (Away \U0001f4fa - Copa Chile)",
				"Sunday 08/11",
				"20:00 to 23:00 for the UC fan",
				"glued to the screen for UC",
			},
			omitSubs: []string{
				"Heads up - next match",
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
				"\U0001f7e1 **Pendiente ANFP (el domingo 15/11): Horario por confirmar**",
				"\U0001f7e1 **Universidad Cat\u00f3lica vs Cobreloa**",
				"el hincha cruzado",
				"bajo su propio riesgo",
			},
			omitSubs: []string{
				"pr\u00f3ximo partido",
			},
		},
		{
			name: "free tomorrow in English shows confirmed next match and custom banter with friend",
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
				"\U0001f7e2 **All clear (tomorrow): Plans can go ahead**",
				"Free to fire up the grill (<@" + testFriendID + ">)",
				"*(Heads up - next match: **Universidad Catolica vs Colo-Colo**",
				"blocking **16:00 to 21:30**.)*",
			},
		},
		{
			name: "free next weekend and this month period labels",
			loc:  uccmd.LocaleENUS,
			report: uccalendar.Report{
				Mode:       uccalendar.ModeWindow,
				Window:     uccalendar.WindowNextWeekend,
				RangeStart: friStart.AddDate(0, 0, 7),
				RangeEnd:   sunEnd.AddDate(0, 0, 7),
				Status:     uccalendar.StatusFree,
			},
			wantSubs: []string{
				"\U0001f7e2 **All clear (next weekend (14/11-15/11)): Plans can go ahead**",
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
				Banter:       "Todo despejado por ahora",
			},
			wantSubs: []string{
				"\U0001f7e2 **V\u00eda libre (este mes): Se puede armar junta**",
				"Todo despejado por ahora",
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
		t.Errorf("overflow report dropped bottom friend mention <@%s>: %q", testFriendID, got)
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

	friday := uccmd.LocaleESCL
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

	friMsg, err := uccmd.RenderAlertIn(friday, friAlert, tz)
	if err != nil {
		t.Fatalf("RenderAlertIn(friday): %v", err)
	}
	assertNoMentionsOnCreate(t, friMsg.AllowedMentions)
	for _, want := range []string{
		"\U0001f4e2 **Reporte de disponibilidad de <@" + testFriendID + "> para el finde (07/11-08/11):**",
		"\U0001f534 **s\u00e1bado 07/11 bloqueado de 16:00 a 21:30 hrs**",
		"\U0001f7e1 **domingo 08/11 pendiente ANFP**",
		"\U0001f7e2 **domingo 08/11 libre** para armar panorama.",
		`\@everyone ojo con el s` + "\u00e1" + `bado`,
	} {
		if !strings.Contains(friMsg.Content, want) {
			t.Errorf("friday alert missing %q in:\n%s", want, friMsg.Content)
		}
	}

	matchdayAlert := uccalendar.Alert{
		Kind:         uccalendar.AlertMatchdayMorning,
		FriendUserID: testFriendID,
		Banter:       "Hoy juega la Cato",
		Matches:      []uccalendar.Match{home},
	}
	matchMsg, err := uccmd.RenderAlertIn(uccmd.LocaleESCL, matchdayAlert, tz)
	if err != nil {
		t.Fatalf("RenderAlertIn(matchday): %v", err)
	}
	assertNoMentionsOnCreate(t, matchMsg.AllowedMentions)
	for _, want := range []string{
		"\u26a0\ufe0f **Aviso de utilidad p\u00fablica:**",
		fmt.Sprintf("<t:%d:t>", home.Kickoff.Unix()),
		fmt.Sprintf("<t:%d:R>", home.Kickoff.Unix()),
		"**Universidad Cat\u00f3lica vs Colo-Colo**",
		"<@" + testFriendID + "> desaparece del mapa entre las **16:00 y las 21:30 hrs**.",
		"\nHoy juega la Cato",
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
	for _, want := range []string{"Home \U0001f3df\ufe0f - Copa Chile", "Away \U0001f4fa - Liga de Primera"} {
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
		"Saturday 07/11 free",
		"Plan the weekend around those windows before committing.",
	} {
		if !strings.Contains(friMsg.Content, want) {
			t.Errorf("friday fallback alert missing %q in:\n%s", want, friMsg.Content)
		}
	}
}
