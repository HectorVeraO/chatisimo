package uccalendar_test

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/uccalendar"
)

func TestNewAggregatesValidationErrors(t *testing.T) {
	t.Parallel()

	_, err := uccalendar.New(uccalendar.Deps{
		SyncInterval:       -time.Second,
		AlertCheckInterval: -time.Second,
		SettingsTTL:        -time.Second,
	})
	if !errors.Is(err, uccalendar.ErrInvalidInput) {
		t.Fatalf("New error = %v, want ErrInvalidInput", err)
	}

	msg := err.Error()
	for _, field := range []string{
		"deps.Pool",
		"deps.Settings",
		"deps.Clock",
		"deps.Logger",
		"deps.SyncInterval",
		"deps.AlertCheckInterval",
		"deps.SettingsTTL",
	} {
		if !strings.Contains(msg, field) {
			t.Errorf("error %q missing field %q", msg, field)
		}
	}
}

func TestNilReceiverAndNilContextGuards(t *testing.T) {
	t.Parallel()

	var nilSvc *uccalendar.Service
	if err := nilSvc.Close(); err != nil {
		t.Errorf("nilSvc.Close() = %v, want nil", err)
	}

	if err := nilSvc.Sync(t.Context()); !errors.Is(err, uccalendar.ErrInvalidInput) {
		t.Errorf("nilSvc.Sync = %v, want ErrInvalidInput", err)
	}

	if _, err := nilSvc.Check(t.Context(), uccalendar.Query{}); !errors.Is(err, uccalendar.ErrInvalidInput) {
		t.Errorf("nilSvc.Check = %v, want ErrInvalidInput", err)
	}

	if err := nilSvc.CheckAlerts(t.Context()); !errors.Is(err, uccalendar.ErrInvalidInput) {
		t.Errorf("nilSvc.CheckAlerts = %v, want ErrInvalidInput", err)
	}

	if err := nilSvc.Run(t.Context()); !errors.Is(err, uccalendar.ErrInvalidInput) {
		t.Errorf("nilSvc.Run = %v, want ErrInvalidInput", err)
	}

	scope := uccalendar.GuildScope{GuildID: fakeGuildID, CanManageGuild: true}
	friend := fakeFriendID
	if _, err := nilSvc.GetGuildConfig(t.Context(), scope); !errors.Is(err, uccalendar.ErrInvalidInput) {
		t.Errorf("nilSvc.GetGuildConfig = %v, want ErrInvalidInput", err)
	}

	if _, err := nilSvc.UpdateGuildConfig(t.Context(), scope, uccalendar.GuildConfigUpdate{FriendUserID: &friend}); !errors.Is(err, uccalendar.ErrInvalidInput) {
		t.Errorf("nilSvc.UpdateGuildConfig = %v, want ErrInvalidInput", err)
	}

	if _, err := nilSvc.ClearGuildConfig(t.Context(), scope, uccalendar.ClearTargetAll); !errors.Is(err, uccalendar.ErrInvalidInput) {
		t.Errorf("nilSvc.ClearGuildConfig = %v, want ErrInvalidInput", err)
	}

	svc, err := uccalendar.New(uccalendar.Deps{
		Pool:     shared.Pool(),
		Settings: newMapSettings(nil),
		Clock:    newFakeClock(time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)),
		Logger:   slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("uccalendar.New: %v", err)
	}
	t.Cleanup(func() {
		_ = svc.Close()
	})

	//nolint:staticcheck // testing nil context guard
	if err := svc.Sync(nil); !errors.Is(err, uccalendar.ErrInvalidInput) {
		t.Errorf("svc.Sync(nil) = %v, want ErrInvalidInput", err)
	}

	//nolint:staticcheck // testing nil context guard
	if _, err := svc.Check(nil, uccalendar.Query{}); !errors.Is(err, uccalendar.ErrInvalidInput) {
		t.Errorf("svc.Check(nil) = %v, want ErrInvalidInput", err)
	}

	//nolint:staticcheck // testing aggregated nil context + invalid query validation
	_, joinedErr := svc.Check(nil, uccalendar.Query{GuildID: "bad-guild", Window: "bad"})
	if joinedErr == nil || !strings.Contains(joinedErr.Error(), "ctx") || !strings.Contains(joinedErr.Error(), "guild_id") || !strings.Contains(joinedErr.Error(), "window") {
		t.Errorf("svc.Check(nil, bad query) = %v, want joined ctx, guild_id, and window errors", joinedErr)
	}

	//nolint:staticcheck // testing nil context guard
	if err := svc.CheckAlerts(nil); !errors.Is(err, uccalendar.ErrInvalidInput) {
		t.Errorf("svc.CheckAlerts(nil) = %v, want ErrInvalidInput", err)
	}

	//nolint:staticcheck // testing nil context guard
	if err := svc.Run(nil); !errors.Is(err, uccalendar.ErrInvalidInput) {
		t.Errorf("svc.Run(nil) = %v, want ErrInvalidInput", err)
	}

	//nolint:staticcheck // testing nil context + invalid guild_id aggregation
	if _, err := svc.GetGuildConfig(nil, uccalendar.GuildScope{GuildID: "bad"}); !errors.Is(err, uccalendar.ErrInvalidInput) {
		t.Errorf("svc.GetGuildConfig(nil, bad) = %v, want ErrInvalidInput", err)
	}

	//nolint:staticcheck // testing nil context guard
	if _, err := svc.UpdateGuildConfig(nil, scope, uccalendar.GuildConfigUpdate{FriendUserID: &friend}); !errors.Is(err, uccalendar.ErrInvalidInput) {
		t.Errorf("svc.UpdateGuildConfig(nil) = %v, want ErrInvalidInput", err)
	}

	//nolint:staticcheck // testing nil context guard
	if _, err := svc.ClearGuildConfig(nil, scope, uccalendar.ClearTargetAll); !errors.Is(err, uccalendar.ErrInvalidInput) {
		t.Errorf("svc.ClearGuildConfig(nil) = %v, want ErrInvalidInput", err)
	}
}

func TestColdBootUnavailableAndUpstreamFailureRetainsLastValidSchedule(t *testing.T) {
	loc := santiagoLocation(t)
	clock := newFakeClock(time.Date(2026, 10, 27, 15, 0, 0, 0, loc))

	espnSrv := newFakeESPNServer(
		t,
		readTestdata(t, "completed_schedule.json"),
		readTestdata(t, "upcoming_fixtures.json"),
	)
	espnSrv.SetStatus(http.StatusServiceUnavailable)

	settings := newMapSettings(map[string]string{
		uccalendar.SettingAPIBaseURL: espnSrv.BaseURL(),
	})

	svc, err := uccalendar.New(uccalendar.Deps{
		Pool:       shared.Pool(),
		Settings:   settings,
		Clock:      clock,
		HTTPClient: espnSrv.server.Client(),
		Logger:     slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("uccalendar.New: %v", err)
	}
	t.Cleanup(func() {
		_ = svc.Close()
	})

	// Query validation runs even before first sync.
	_, badErr := svc.Check(t.Context(), uccalendar.Query{
		Window: uccalendar.WindowToday,
		Date:   "31/10",
	})
	if !errors.Is(badErr, uccalendar.ErrInvalidInput) {
		t.Fatalf("Check with both Window and Date = %v, want ErrInvalidInput", badErr)
	}

	// Cold boot before any sync returns ErrScheduleUnavailable.
	_, err = svc.Check(t.Context(), uccalendar.Query{})
	if !errors.Is(err, uccalendar.ErrScheduleUnavailable) {
		t.Fatalf("Check before Sync = %v, want ErrScheduleUnavailable", err)
	}

	// First Sync fails against HTTP 503 -> still ErrScheduleUnavailable.
	if syncErr := svc.Sync(t.Context()); syncErr == nil {
		t.Fatal("Sync succeeded against HTTP 503, want error")
	}

	_, err = svc.Check(t.Context(), uccalendar.Query{})
	if !errors.Is(err, uccalendar.ErrScheduleUnavailable) {
		t.Fatalf("Check after failed initial Sync = %v, want ErrScheduleUnavailable", err)
	}

	// Recover upstream -> Sync succeeds.
	espnSrv.SetStatus(http.StatusOK)
	if syncErr := svc.Sync(t.Context()); syncErr != nil {
		t.Fatalf("Sync after recovery: %v", syncErr)
	}

	report, err := svc.Check(t.Context(), uccalendar.Query{})
	if err != nil {
		t.Fatalf("Check after Sync: %v", err)
	}

	if report.Status != uccalendar.StatusBlocked || len(report.Matches) != 1 {
		t.Fatalf("Report = %+v, want StatusBlocked with 1 match", report)
	}

	// Break upstream again -> failed Sync retains the previously synced schedule!
	espnSrv.SetStatus(http.StatusBadGateway)
	if syncErr := svc.Sync(t.Context()); syncErr == nil {
		t.Fatal("Sync succeeded against HTTP 502, want error")
	}

	retained, err := svc.Check(t.Context(), uccalendar.Query{})
	if err != nil {
		t.Fatalf("Check after subsequent failed Sync: %v", err)
	}

	if retained.Status != uccalendar.StatusBlocked || len(retained.Matches) != 1 || retained.Matches[0].ID != "8002" {
		t.Fatalf("retained Report = %+v, want previously cached match 8002", retained)
	}
}

func TestHomeAwayBlockMathPostMatchWindowAndWindows(t *testing.T) {
	loc := santiagoLocation(t)
	// Start on Saturday 2026-10-24 at 19:20 Santiago: 20 minutes after full-time of match 8001
	// (kickoff 17:00, full-time ~19:00, home post-match traffic block ends at 20:30).
	clock := newFakeClock(time.Date(2026, 10, 24, 19, 20, 0, 0, loc))

	espnSrv := newFakeESPNServer(
		t,
		readTestdata(t, "completed_schedule.json"),
		readTestdata(t, "upcoming_fixtures.json"),
	)

	settings := newMapSettings(map[string]string{
		uccalendar.SettingAPIBaseURL: espnSrv.BaseURL(),
	})

	svc, err := uccalendar.New(uccalendar.Deps{
		Pool:       shared.Pool(),
		Settings:   settings,
		Clock:      clock,
		HTTPClient: espnSrv.server.Client(),
		Logger:     slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("uccalendar.New: %v", err)
	}
	t.Cleanup(func() {
		_ = svc.Close()
	})

	cleanGuildConfigs(t, shared.Pool())
	mustConfigureGuild(t, svc, fakeGuildID, fakeFriendID, "")

	if err := svc.Sync(t.Context()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	// 1. Post-match +3h30m window still active on Saturday 2026-10-24 at 19:20 for STATUS_FINAL match 8001.
	postMatchToday, err := svc.Check(t.Context(), uccalendar.Query{Window: uccalendar.WindowToday})
	if err != nil {
		t.Fatalf("Check(WindowToday): %v", err)
	}

	if postMatchToday.Status != uccalendar.StatusBlocked || len(postMatchToday.Matches) != 1 {
		t.Fatalf("post-match today report = %+v, want StatusBlocked with match 8001", postMatchToday)
	}

	m8001 := postMatchToday.Matches[0]
	wantKickoff := time.Date(2026, 10, 24, 17, 0, 0, 0, loc)
	wantStart := time.Date(2026, 10, 24, 15, 0, 0, 0, loc)
	wantEnd := time.Date(2026, 10, 24, 20, 30, 0, 0, loc)
	if !m8001.Kickoff.Equal(wantKickoff) || !m8001.BlockStart.Equal(wantStart) || !m8001.BlockEnd.Equal(wantEnd) {
		t.Errorf("home match 8001 window = [%v .. %v] (kickoff %v), want [%v .. %v]",
			m8001.BlockStart, m8001.BlockEnd, m8001.Kickoff, wantStart, wantEnd)
	}

	if !clock.Now().Before(m8001.BlockEnd) {
		t.Fatalf("expected 19:20 to be inside active +3h30m block ending at %v", m8001.BlockEnd)
	}

	// 2. Move to Tuesday 2026-10-27 11:00 Santiago and check all 5 WindowKinds + default + dates.
	clock.Set(time.Date(2026, 10, 27, 11, 0, 0, 0, loc))

	todayRep, err := svc.Check(t.Context(), uccalendar.Query{
		GuildID: fakeGuildID,
		Window:  uccalendar.WindowToday,
	})
	if err != nil {
		t.Fatalf("Check(today): %v", err)
	}

	if todayRep.Status != uccalendar.StatusFree || todayRep.FriendUserID != fakeFriendID {
		t.Errorf("Tuesday today report = %+v, want StatusFree with FriendUserID", todayRep)
	}

	if todayRep.NextMatch == nil || todayRep.NextMatch.ID != "8002" {
		t.Errorf("Tuesday NextMatch = %+v, want match 8002 on 2026-10-31", todayRep.NextMatch)
	}

	tomorrowRep, err := svc.Check(t.Context(), uccalendar.Query{Window: uccalendar.WindowTomorrow})
	if err != nil {
		t.Fatalf("Check(tomorrow): %v", err)
	}

	if tomorrowRep.Status != uccalendar.StatusFree {
		t.Errorf("Wednesday tomorrow report = %+v, want StatusFree (8003 canceled)", tomorrowRep)
	}

	defaultRep, err := svc.Check(t.Context(), uccalendar.Query{})
	if err != nil {
		t.Fatalf("Check(default): %v", err)
	}

	thisWeekendRep, err := svc.Check(t.Context(), uccalendar.Query{Window: uccalendar.WindowThisWeekend})
	if err != nil {
		t.Fatalf("Check(this_weekend): %v", err)
	}

	if defaultRep.Mode != uccalendar.ModeWindow || defaultRep.Window != uccalendar.WindowThisWeekend {
		t.Errorf("default Mode/Window = (%q, %q), want (window, this_weekend)", defaultRep.Mode, defaultRep.Window)
	}

	if defaultRep.Status != uccalendar.StatusBlocked || thisWeekendRep.Status != uccalendar.StatusBlocked {
		t.Errorf("this_weekend status = (%q, %q), want StatusBlocked", defaultRep.Status, thisWeekendRep.Status)
	}

	if defaultRep.NextMatch == nil || defaultRep.NextMatch.ID != "8004" {
		t.Errorf("this_weekend NextMatch = %+v, want away match 8004 on 2026-11-04", defaultRep.NextMatch)
	}

	// Next weekend (Fri 06/11 18:00 .. Sun 08/11 23:59) has TBD match 8005 on 2026-11-08 (no day shift!).
	nextWeekendRep, err := svc.Check(t.Context(), uccalendar.Query{Window: uccalendar.WindowNextWeekend})
	if err != nil {
		t.Fatalf("Check(next_weekend): %v", err)
	}

	if nextWeekendRep.Status != uccalendar.StatusPendingANFP || len(nextWeekendRep.Matches) != 1 {
		t.Fatalf("next_weekend report = %+v, want StatusPendingANFP with match 8005", nextWeekendRep)
	}

	tbdMatch := nextWeekendRep.Matches[0]
	if !tbdMatch.TBD || tbdMatch.Date.String() != "2026-11-08" {
		t.Errorf("TBD match = %+v, want TBD on 2026-11-08 without -03 day shift", tbdMatch)
	}

	// Verify 07/11 is FREE (proving 2026-11-08T00:00Z TBD did NOT shift to Saturday 07/11!).
	nov7Rep, err := svc.Check(t.Context(), uccalendar.Query{Date: "07/11"})
	if err != nil {
		t.Fatalf("Check(07/11): %v", err)
	}

	if nov7Rep.Status != uccalendar.StatusFree {
		t.Errorf("07/11 status = %q, want StatusFree (TBD 08/11 must not shift into 07/11)", nov7Rep.Status)
	}

	// Away match 8004 on 2026-11-04 at 20:00 Santiago -> BlockStart 19:30 (-30m), BlockEnd 22:30 (+2h30m).
	nov4Rep, err := svc.Check(t.Context(), uccalendar.Query{Date: "2026-11-04"})
	if err != nil {
		t.Fatalf("Check(2026-11-04): %v", err)
	}

	if nov4Rep.Mode != uccalendar.ModeDate || nov4Rep.Status != uccalendar.StatusBlocked || len(nov4Rep.Matches) != 1 {
		t.Fatalf("2026-11-04 report = %+v, want ModeDate StatusBlocked", nov4Rep)
	}

	away := nov4Rep.Matches[0]
	if away.Condition != uccalendar.VenueAway {
		t.Errorf("away.Condition = %q, want away", away.Condition)
	}

	wantAwayStart := time.Date(2026, 11, 4, 19, 30, 0, 0, loc)
	wantAwayEnd := time.Date(2026, 11, 4, 22, 30, 0, 0, loc)
	if !away.BlockStart.Equal(wantAwayStart) || !away.BlockEnd.Equal(wantAwayEnd) {
		t.Errorf("away block = [%v .. %v], want [%v .. %v]", away.BlockStart, away.BlockEnd, wantAwayStart, wantAwayEnd)
	}

	if nov4Rep.NextMatch == nil || nov4Rep.NextMatch.ID != "8005" {
		t.Errorf("NextMatch after 2026-11-04 = %+v, want TBD match 8005", nov4Rep.NextMatch)
	}

	// this_month in October 2026 includes 8001 (24/10) and 8002 (31/10).
	octMonthRep, err := svc.Check(t.Context(), uccalendar.Query{Window: uccalendar.WindowThisMonth})
	if err != nil {
		t.Fatalf("Check(this_month): %v", err)
	}

	if octMonthRep.Status != uccalendar.StatusBlocked || len(octMonthRep.Matches) != 2 {
		t.Errorf("October this_month matches = %d (status %q), want 2 blocked matches",
			len(octMonthRep.Matches), octMonthRep.Status)
	}
}

func TestFridayNightMatchIncludedInWeekendAndValidationErrors(t *testing.T) {
	loc := santiagoLocation(t)
	clock := newFakeClock(time.Date(2026, 11, 13, 12, 0, 0, 0, loc))

	// Friday 2026-11-13 at 20:00 Santiago = 2026-11-13T23:00Z (away match -> 19:30..22:30),
	// plus Friday 2026-11-20 at 12:00 Santiago = 2026-11-20T15:00Z (home match -> 10:00..15:30, before 18:00!).
	fridayFixtures := []byte(`{
		"events": [
			{
				"id": "8100",
				"date": "2026-11-13T23:00Z",
				"timeValid": true,
				"competitions": [{
					"id": "8100",
					"date": "2026-11-13T23:00Z",
					"timeValid": true,
					"competitors": [
						{"id": "885", "homeAway": "away", "team": {"id": "885", "displayName": "Universidad Catolica"}},
						{"id": "9200", "homeAway": "home", "team": {"id": "9200", "displayName": "Rival Viernes"}}
					]
				}]
			},
			{
				"id": "8101",
				"date": "2026-11-20T15:00Z",
				"timeValid": true,
				"competitions": [{
					"id": "8101",
					"date": "2026-11-20T15:00Z",
					"timeValid": true,
					"competitors": [
						{"id": "885", "homeAway": "home", "team": {"id": "885", "displayName": "Universidad Catolica"}},
						{"id": "9201", "homeAway": "away", "team": {"id": "9201", "displayName": "Rival Mediodia"}}
					]
				}]
			}
		]
	}`)

	espnSrv := newFakeESPNServer(t, []byte(`{"events":[]}`), fridayFixtures)
	settings := newMapSettings(map[string]string{
		uccalendar.SettingAPIBaseURL: espnSrv.BaseURL(),
	})

	svc, err := uccalendar.New(uccalendar.Deps{
		Pool:       shared.Pool(),
		Settings:   settings,
		Clock:      clock,
		HTTPClient: espnSrv.server.Client(),
		Logger:     slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("uccalendar.New: %v", err)
	}
	t.Cleanup(func() {
		_ = svc.Close()
	})

	cleanGuildConfigs(t, shared.Pool())
	mustConfigureGuild(t, svc, fakeGuildID, fakeFriendID, "")

	if err := svc.Sync(t.Context()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	// Friday 20:00 match is included in this_weekend [Fri 18:00, Sun 23:59].
	thisWk, err := svc.Check(t.Context(), uccalendar.Query{Window: uccalendar.WindowThisWeekend})
	if err != nil {
		t.Fatalf("Check(this_weekend): %v", err)
	}

	if thisWk.Status != uccalendar.StatusBlocked || len(thisWk.Matches) != 1 || thisWk.Matches[0].ID != "8100" {
		t.Errorf("Friday 20:00 match weekend report = %+v, want StatusBlocked with 8100", thisWk)
	}

	// Next weekend's Friday 12:00 match ends at 15:30 (< 18:00), so next_weekend is StatusFree!
	nextWk, err := svc.Check(t.Context(), uccalendar.Query{Window: uccalendar.WindowNextWeekend})
	if err != nil {
		t.Fatalf("Check(next_weekend): %v", err)
	}

	if nextWk.Status != uccalendar.StatusFree {
		t.Errorf("Friday 12:00 match weekend report = %+v, want StatusFree", nextWk)
	}

	// Validation errors: unknown window, invalid date, mutual exclusion.
	_, err = svc.Check(t.Context(), uccalendar.Query{Window: uccalendar.WindowKind("next_year")})
	var inputErr *uccalendar.InputError
	if !errors.As(err, &inputErr) || inputErr.Code != uccalendar.InputCodeUnknown {
		t.Errorf("unknown window error = %v, want InputCodeUnknown", err)
	}

	_, err = svc.Check(t.Context(), uccalendar.Query{Date: "31/02"})
	if !errors.As(err, &inputErr) || inputErr.Code != uccalendar.InputCodeInvalidDate {
		t.Errorf("invalid date error = %v, want InputCodeInvalidDate", err)
	}

	_, err = svc.Check(t.Context(), uccalendar.Query{Window: uccalendar.WindowToday, Date: "13/11"})
	if !errors.As(err, &inputErr) || inputErr.Code != uccalendar.InputCodeMutuallyExclusive {
		t.Errorf("mutually exclusive error = %v, want InputCodeMutuallyExclusive", err)
	}
}

func TestMidweekNextMatchDefaultVsExplicitThisWeekend(t *testing.T) {
	loc := santiagoLocation(t)
	// Tuesday 2026-10-27 11:00 Santiago: precedes Thursday 2026-10-29 midweek match and Fri..Sun weekend.
	clock := newFakeClock(time.Date(2026, 10, 27, 11, 0, 0, 0, loc))

	confirmedMidweekPayload := []byte(`{
		"events": [
			{
				"id": "8201",
				"date": "2026-10-29T23:00Z",
				"timeValid": true,
				"competitions": [{
					"id": "8201",
					"date": "2026-10-29T23:00Z",
					"timeValid": true,
					"competitors": [
						{"id": "885", "homeAway": "home", "team": {"id": "885", "displayName": "Universidad Catolica"}},
						{"id": "9301", "homeAway": "away", "team": {"id": "9301", "displayName": "Rival Jueves"}}
					]
				}]
			},
			{
				"id": "8202",
				"date": "2026-11-01T20:00Z",
				"timeValid": true,
				"competitions": [{
					"id": "8202",
					"date": "2026-11-01T20:00Z",
					"timeValid": true,
					"competitors": [
						{"id": "885", "homeAway": "home", "team": {"id": "885", "displayName": "Universidad Catolica"}},
						{"id": "9302", "homeAway": "away", "team": {"id": "9302", "displayName": "Rival Domingo"}}
					]
				}]
			},
			{
				"id": "8203",
				"date": "2026-11-04T23:00Z",
				"timeValid": true,
				"competitions": [{
					"id": "8203",
					"date": "2026-11-04T23:00Z",
					"timeValid": true,
					"competitors": [
						{"id": "885", "homeAway": "away", "team": {"id": "885", "displayName": "Universidad Catolica"}},
						{"id": "9303", "homeAway": "home", "team": {"id": "9303", "displayName": "Rival PostFinde"}}
					]
				}]
			}
		]
	}`)

	tbdMidweekPayload := []byte(`{
		"events": [
			{
				"id": "8204",
				"date": "2026-10-29T00:00Z",
				"timeValid": false,
				"competitions": [{
					"id": "8204",
					"date": "2026-10-29T00:00Z",
					"timeValid": false,
					"competitors": [
						{"id": "885", "homeAway": "home", "team": {"id": "885", "displayName": "Universidad Catolica"}},
						{"id": "9304", "homeAway": "away", "team": {"id": "9304", "displayName": "Rival Jueves TBD"}}
					]
				}]
			},
			{
				"id": "8202",
				"date": "2026-11-01T20:00Z",
				"timeValid": true,
				"competitions": [{
					"id": "8202",
					"date": "2026-11-01T20:00Z",
					"timeValid": true,
					"competitors": [
						{"id": "885", "homeAway": "home", "team": {"id": "885", "displayName": "Universidad Catolica"}},
						{"id": "9302", "homeAway": "away", "team": {"id": "9302", "displayName": "Rival Domingo"}}
					]
				}]
			},
			{
				"id": "8203",
				"date": "2026-11-04T23:00Z",
				"timeValid": true,
				"competitions": [{
					"id": "8203",
					"date": "2026-11-04T23:00Z",
					"timeValid": true,
					"competitors": [
						{"id": "885", "homeAway": "away", "team": {"id": "885", "displayName": "Universidad Catolica"}},
						{"id": "9303", "homeAway": "home", "team": {"id": "9303", "displayName": "Rival PostFinde"}}
					]
				}]
			}
		]
	}`)

	for _, tc := range []struct {
		name          string
		payload       []byte
		wantDefaultID string
	}{
		{name: "confirmed midweek match", payload: confirmedMidweekPayload, wantDefaultID: "8201"},
		{name: "TBD midweek match", payload: tbdMidweekPayload, wantDefaultID: "8204"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			espnSrv := newFakeESPNServer(t, []byte(`{"events":[]}`), tc.payload)
			settings := newMapSettings(map[string]string{
				uccalendar.SettingAPIBaseURL: espnSrv.BaseURL(),
			})

			svc, err := uccalendar.New(uccalendar.Deps{
				Pool:       shared.Pool(),
				Settings:   settings,
				Clock:      clock,
				HTTPClient: espnSrv.server.Client(),
				Logger:     slog.New(slog.DiscardHandler),
			})
			if err != nil {
				t.Fatalf("uccalendar.New: %v", err)
			}
			t.Cleanup(func() {
				_ = svc.Close()
			})

			if err := svc.Sync(t.Context()); err != nil {
				t.Fatalf("Sync: %v", err)
			}

			defaultRep, err := svc.Check(t.Context(), uccalendar.Query{})
			if err != nil {
				t.Fatalf("Check(Query{}): %v", err)
			}
			if defaultRep.NextMatch == nil || defaultRep.NextMatch.ID != tc.wantDefaultID {
				t.Errorf("Query{} NextMatch = %+v, want midweek fixture %s", defaultRep.NextMatch, tc.wantDefaultID)
			}

			explicitRep, err := svc.Check(t.Context(), uccalendar.Query{Window: uccalendar.WindowThisWeekend})
			if err != nil {
				t.Fatalf("Check(WindowThisWeekend): %v", err)
			}
			if explicitRep.NextMatch == nil || explicitRep.NextMatch.ID != "8203" {
				t.Errorf("WindowThisWeekend NextMatch = %+v, want post-weekend fixture 8203", explicitRep.NextMatch)
			}
		})
	}
}

func TestWeekendWindowWeekdayBoundariesAndChileFallBackDST(t *testing.T) {
	loc := santiagoLocation(t)
	clock := newFakeClock(time.Date(2026, 10, 26, 10, 0, 0, 0, loc))

	// Seed a match during the repeated 23:30 -04 hour on Chile's fall-back Saturday 2026-04-04
	// (2026-04-05T03:30:00Z = 2026-04-04 23:30:00 -04).
	dstPayload := []byte(`{
		"events": [{
			"id": "8301",
			"date": "2026-04-05T03:30:00Z",
			"timeValid": true,
			"competitions": [{
				"id": "8301",
				"date": "2026-04-05T03:30:00Z",
				"timeValid": true,
				"competitors": [
					{"id": "885", "homeAway": "away", "team": {"id": "885", "displayName": "Universidad Catolica"}},
					{"id": "9401", "homeAway": "home", "team": {"id": "9401", "displayName": "Rival CambioHora"}}
				]
			}]
		}]
	}`)

	espnSrv := newFakeESPNServer(t, []byte(`{"events":[]}`), dstPayload)
	settings := newMapSettings(map[string]string{
		uccalendar.SettingAPIBaseURL: espnSrv.BaseURL(),
	})

	svc, err := uccalendar.New(uccalendar.Deps{
		Pool:       shared.Pool(),
		Settings:   settings,
		Clock:      clock,
		HTTPClient: espnSrv.server.Client(),
		Logger:     slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("uccalendar.New: %v", err)
	}
	t.Cleanup(func() {
		_ = svc.Close()
	})

	cleanGuildConfigs(t, shared.Pool())
	mustConfigureGuild(t, svc, fakeGuildID, fakeFriendID, "")

	if err := svc.Sync(t.Context()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	wantThisStart := time.Date(2026, 10, 30, 18, 0, 0, 0, loc)
	wantThisEnd := (uccalendar.LocalDate{Year: 2026, Month: time.November, Day: 1}).EndOfDay(loc)
	wantNextStart := time.Date(2026, 11, 6, 18, 0, 0, 0, loc)
	wantNextEnd := (uccalendar.LocalDate{Year: 2026, Month: time.November, Day: 8}).EndOfDay(loc)

	nowCases := []struct {
		name string
		now  time.Time
	}{
		{name: "Monday morning", now: time.Date(2026, 10, 26, 10, 0, 0, 0, loc)},
		{name: "Friday 17:59", now: time.Date(2026, 10, 30, 17, 59, 0, 0, loc)},
		{name: "Friday 18:00", now: time.Date(2026, 10, 30, 18, 0, 0, 0, loc)},
		{name: "Saturday noon", now: time.Date(2026, 10, 31, 12, 0, 0, 0, loc)},
		{name: "Sunday 23:00 (time.Sunday==0)", now: time.Date(2026, 11, 1, 23, 0, 0, 0, loc)},
	}

	for _, tc := range nowCases {
		t.Run(tc.name, func(t *testing.T) {
			clock.Set(tc.now)

			defRep, defErr := svc.Check(t.Context(), uccalendar.Query{})
			if defErr != nil {
				t.Fatalf("Check(Query{}): %v", defErr)
			}
			if !defRep.RangeStart.Equal(wantThisStart) || !defRep.RangeEnd.Equal(wantThisEnd) {
				t.Errorf("Query{} range = [%v .. %v], want [%v .. %v]",
					defRep.RangeStart, defRep.RangeEnd, wantThisStart, wantThisEnd)
			}

			thisRep, thisErr := svc.Check(t.Context(), uccalendar.Query{Window: uccalendar.WindowThisWeekend})
			if thisErr != nil {
				t.Fatalf("Check(WindowThisWeekend): %v", thisErr)
			}
			if !thisRep.RangeStart.Equal(wantThisStart) || !thisRep.RangeEnd.Equal(wantThisEnd) {
				t.Errorf("WindowThisWeekend range = [%v .. %v], want [%v .. %v]",
					thisRep.RangeStart, thisRep.RangeEnd, wantThisStart, wantThisEnd)
			}

			nextRep, nextErr := svc.Check(t.Context(), uccalendar.Query{Window: uccalendar.WindowNextWeekend})
			if nextErr != nil {
				t.Fatalf("Check(WindowNextWeekend): %v", nextErr)
			}
			if !nextRep.RangeStart.Equal(wantNextStart) || !nextRep.RangeEnd.Equal(wantNextEnd) {
				t.Errorf("WindowNextWeekend range = [%v .. %v], want [%v .. %v]",
					nextRep.RangeStart, nextRep.RangeEnd, wantNextStart, wantNextEnd)
			}
		})
	}

	// Verify away match kicking off at 2026-04-04 23:30 -04 (BlockStart 23:00 -04) overlaps 2026-04-04!
	dstRep, dstErr := svc.Check(t.Context(), uccalendar.Query{Date: "2026-04-04"})
	if dstErr != nil {
		t.Fatalf("Check(2026-04-04): %v", dstErr)
	}
	if dstRep.Status != uccalendar.StatusBlocked || len(dstRep.Matches) != 1 || dstRep.Matches[0].ID != "8301" {
		t.Errorf("2026-04-04 fall-back Saturday report = %+v, want StatusBlocked with match 8301", dstRep)
	}
}
