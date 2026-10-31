package uccalendar_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/uccalendar"
)

func TestAlertsDeduplicationAcrossTwoInstancesAndRetryOnSendFailure(t *testing.T) {
	pool := newIsolatedDB(t)
	loc := santiagoLocation(t)

	// Friday 2026-10-30 at 17:05 Santiago: weekend has confirmed match 8002 on Saturday 2026-10-31 at 18:00.
	clock := newFakeClock(time.Date(2026, 10, 30, 17, 5, 0, 0, loc))

	espnSrv := newFakeESPNServer(
		t,
		readTestdata(t, "completed_schedule.json"),
		readTestdata(t, "upcoming_fixtures.json"),
	)

	settings := newMapSettings(map[string]string{
		uccalendar.SettingAPIBaseURL: espnSrv.BaseURL(),
	})

	alertsA := newFakeAlertSender()
	alertsB := newFakeAlertSender()
	banterA := newFakeBanter("Se nos cayo el panorama cruzado")
	banterB := newFakeBanter("Se nos cayo el panorama cruzado")

	svcA, err := uccalendar.New(uccalendar.Deps{
		Pool:       pool,
		Settings:   settings,
		Clock:      clock,
		Alerts:     alertsA,
		Banter:     banterA,
		HTTPClient: espnSrv.server.Client(),
		Logger:     slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("New svcA: %v", err)
	}
	t.Cleanup(func() {
		_ = svcA.Close()
	})

	svcB, err := uccalendar.New(uccalendar.Deps{
		Pool:       pool,
		Settings:   settings,
		Clock:      clock,
		Alerts:     alertsB,
		Banter:     banterB,
		HTTPClient: espnSrv.server.Client(),
		Logger:     slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("New svcB: %v", err)
	}
	t.Cleanup(func() {
		_ = svcB.Close()
	})

	mustConfigureGuild(t, svcA, fakeGuildID, fakeFriendID, fakeChannelID)

	if err := svcA.Sync(t.Context()); err != nil {
		t.Fatalf("svcA.Sync: %v", err)
	}

	if err := svcB.Sync(t.Context()); err != nil {
		t.Fatalf("svcB.Sync: %v", err)
	}

	// Simulate transient SendUCAlert failure on svcA: claim must be released so svcB can deliver it!
	alertsA.SetFailNext(1, nil)
	if err := svcA.CheckAlerts(t.Context()); err == nil {
		t.Fatal("svcA.CheckAlerts succeeded when SendUCAlert failed, want error")
	}

	if len(alertsA.Delivered()) != 0 {
		t.Fatalf("alertsA delivered = %d after failure, want 0", len(alertsA.Delivered()))
	}

	// svcB now runs CheckAlerts: wins the released claim and sends the Friday weekend summary!
	if err := svcB.CheckAlerts(t.Context()); err != nil {
		t.Fatalf("svcB.CheckAlerts retry after released claim: %v", err)
	}

	gotFriday := alertsB.Delivered()
	if len(gotFriday) != 1 {
		t.Fatalf("alertsB delivered = %d, want 1", len(gotFriday))
	}

	fridayAlert := gotFriday[0].Alert
	if fridayAlert.Kind != uccalendar.AlertFridayWeekend {
		t.Errorf("alert.Kind = %q, want %q", fridayAlert.Kind, uccalendar.AlertFridayWeekend)
	}

	if fridayAlert.GuildID != fakeGuildID || fridayAlert.FriendUserID != fakeFriendID {
		t.Errorf("fridayAlert guild/friend = (%q, %q), want (%q, %q)",
			fridayAlert.GuildID, fridayAlert.FriendUserID, fakeGuildID, fakeFriendID)
	}

	if fridayAlert.SaturdayStatus != uccalendar.StatusBlocked || fridayAlert.SundayStatus != uccalendar.StatusFree {
		t.Errorf("weekend statuses = (Sat=%q, Sun=%q), want (blocked, free)",
			fridayAlert.SaturdayStatus, fridayAlert.SundayStatus)
	}

	if fridayAlert.WeekendStart.String() != "2026-10-30" || fridayAlert.WeekendEnd.String() != "2026-11-01" {
		t.Errorf("weekend span = %s..%s, want 2026-10-30..2026-11-01",
			fridayAlert.WeekendStart, fridayAlert.WeekendEnd)
	}

	if fridayAlert.Banter != "Se nos cayo el panorama cruzado" {
		t.Errorf("fridayAlert.Banter = %q, want synchronous generated banter", fridayAlert.Banter)
	}

	// Subsequent CheckAlerts on BOTH svcA and svcB must be deduplicated via uccalendar.sent_alert!
	if err := svcA.CheckAlerts(t.Context()); err != nil {
		t.Fatalf("svcA duplicate CheckAlerts: %v", err)
	}

	if err := svcB.CheckAlerts(t.Context()); err != nil {
		t.Fatalf("svcB duplicate CheckAlerts: %v", err)
	}

	if len(alertsA.Delivered()) != 0 || len(alertsB.Delivered()) != 1 {
		t.Errorf("deduplicated counts = (A=%d, B=%d), want (0, 1)",
			len(alertsA.Delivered()), len(alertsB.Delivered()))
	}

	// Advance to Saturday 2026-10-31 08:59 (1 minute before 09:00 matchday_alert_time) -> no matchday alert yet.
	clock.Set(time.Date(2026, 10, 31, 8, 59, 0, 0, loc))
	if err := svcA.CheckAlerts(t.Context()); err != nil {
		t.Fatalf("svcA CheckAlerts at 08:59: %v", err)
	}

	if len(alertsA.Delivered()) != 0 {
		t.Fatalf("alertsA delivered at 08:59 = %d, want 0", len(alertsA.Delivered()))
	}

	// Advance to Saturday 2026-10-31 09:00 -> svcA sends Matchday morning reminder, svcB deduplicates!
	clock.Set(time.Date(2026, 10, 31, 9, 0, 0, 0, loc))
	alertsA.SetFailNext(1, nil)
	if err := svcA.CheckAlerts(t.Context()); err == nil {
		t.Fatal("svcA matchday CheckAlerts succeeded on failure, want error")
	}

	if err := svcA.CheckAlerts(t.Context()); err != nil {
		t.Fatalf("svcA matchday CheckAlerts retry: %v", err)
	}

	if err := svcB.CheckAlerts(t.Context()); err != nil {
		t.Fatalf("svcB matchday CheckAlerts deduplicated: %v", err)
	}

	if len(alertsA.Delivered()) != 1 || len(alertsB.Delivered()) != 1 {
		t.Fatalf("matchday delivered counts = (A=%d, B=%d), want (1, 1)",
			len(alertsA.Delivered()), len(alertsB.Delivered()))
	}

	mdAlert := alertsA.Delivered()[0].Alert
	if mdAlert.Kind != uccalendar.AlertMatchdayMorning || len(mdAlert.Matches) != 1 || mdAlert.Matches[0].ID != "8002" {
		t.Errorf("matchday alert = %+v, want AlertMatchdayMorning for match 8002", mdAlert)
	}

	if mdAlert.Banter != "Se nos cayo el panorama cruzado" {
		t.Errorf("mdAlert.Banter = %q, want synchronous generated banter", mdAlert.Banter)
	}

	// Retry on svcA after SendUCAlert failure must reuse cached banter rather than calling Generate twice.
	if banterA.Calls() != 2 {
		t.Errorf("banterA.Calls() = %d, want 2 (1 Friday + 1 Matchday, cached across SendUCAlert retry)", banterA.Calls())
	}
}

func TestAlertsMultiGuildIsolationPartialFailureAndSharedBanter(t *testing.T) {
	pool := newIsolatedDB(t)
	loc := santiagoLocation(t)
	clock := newFakeClock(time.Date(2026, 10, 30, 17, 5, 0, 0, loc))

	espnSrv := newFakeESPNServer(
		t,
		readTestdata(t, "completed_schedule.json"),
		readTestdata(t, "upcoming_fixtures.json"),
	)

	settings := newMapSettings(map[string]string{
		uccalendar.SettingAPIBaseURL: espnSrv.BaseURL(),
	})

	alerts := newFakeAlertSender()
	banter := newFakeBanter("La Cato copa el sabado, organicemos domingo.")

	svc, err := uccalendar.New(uccalendar.Deps{
		Pool:       pool,
		Settings:   settings,
		Clock:      clock,
		Alerts:     alerts,
		Banter:     banter,
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

	// No guilds configured yet -> CheckAlerts is a no-op with 0 LLM calls.
	if err := svc.CheckAlerts(t.Context()); err != nil {
		t.Fatalf("CheckAlerts with no guilds: %v", err)
	}
	if len(alerts.Delivered()) != 0 || banter.Calls() != 0 {
		t.Fatalf("unconfigured CheckAlerts delivered=%d banterCalls=%d, want 0 and 0",
			len(alerts.Delivered()), banter.Calls())
	}

	// Configure three guilds:
	// - fakeGuildID: both friend and channel
	// - otherGuildID: channel only (no friend -> generic fallback)
	// - thirdGuildID: friend only (no channel -> no proactive alerts)
	mustConfigureGuild(t, svc, fakeGuildID, fakeFriendID, fakeChannelID)
	mustConfigureGuild(t, svc, otherGuildID, "", otherChannelID)
	mustConfigureGuild(t, svc, thirdGuildID, otherFriendID, "")

	// Fail delivery to fakeChannelID on the first tick: otherGuildID must still receive its alert,
	// and fakeGuildID's claim must be released for retry on the next tick.
	alerts.SetChannelFailure(fakeChannelID, errors.New("discord channel forbidden"))
	if err := svc.CheckAlerts(t.Context()); err == nil {
		t.Fatal("CheckAlerts succeeded when fakeChannelID failed, want error")
	}

	firstTick := alerts.Delivered()
	if len(firstTick) != 1 {
		t.Fatalf("first tick delivered = %d, want 1 (otherGuildID): %+v", len(firstTick), firstTick)
	}
	if firstTick[0].ChannelID != otherChannelID || firstTick[0].Alert.GuildID != otherGuildID || firstTick[0].Alert.FriendUserID != "" {
		t.Errorf("firstTick[0] = %+v, want channel=%s guild=%s friend=''", firstTick[0], otherChannelID, otherGuildID)
	}
	if banter.Calls() != 1 {
		t.Errorf("banter.Calls() after multi-guild tick = %d, want 1 shared call", banter.Calls())
	}

	// Restore fakeChannelID and retry: fakeGuildID receives its Friday alert, otherGuildID is deduplicated!
	alerts.SetChannelFailure(fakeChannelID, nil)
	if err := svc.CheckAlerts(t.Context()); err != nil {
		t.Fatalf("CheckAlerts retry for fakeGuildID: %v", err)
	}

	secondTick := alerts.Delivered()
	if len(secondTick) != 2 {
		t.Fatalf("second tick total delivered = %d, want 2: %+v", len(secondTick), secondTick)
	}
	if secondTick[1].ChannelID != fakeChannelID || secondTick[1].Alert.GuildID != fakeGuildID || secondTick[1].Alert.FriendUserID != fakeFriendID {
		t.Errorf("secondTick[1] = %+v, want channel=%s guild=%s friend=%s",
			secondTick[1], fakeChannelID, fakeGuildID, fakeFriendID)
	}
	if banter.Calls() != 1 {
		t.Errorf("banter.Calls() after retry = %d, want 1 cached call", banter.Calls())
	}

	// On matchday Saturday 2026-10-31 09:00, simulate banter failure: must invoke Generate only once
	// across both guilds rather than once per guild!
	banter.SetResponse("", errors.New("vertex quota exceeded"))
	clock.Set(time.Date(2026, 10, 31, 9, 0, 0, 0, loc))
	if err := svc.CheckAlerts(t.Context()); err != nil {
		t.Fatalf("CheckAlerts matchday with failing banter: %v", err)
	}

	if len(alerts.Delivered()) != 4 {
		t.Fatalf("total delivered after matchday = %d, want 4", len(alerts.Delivered()))
	}
	if banter.Calls() != 2 {
		t.Errorf("banter.Calls() after failing matchday = %d, want 2 (1 Friday + 1 memoized matchday attempt)", banter.Calls())
	}
}

func TestRunLifecycleRecoversFromInitialSyncErrorAndCancelsCleanly(t *testing.T) {
	pool := newIsolatedDB(t)
	loc := santiagoLocation(t)
	clock := newFakeClock(time.Date(2026, 10, 31, 9, 15, 0, 0, loc))

	espnSrv := newFakeESPNServer(
		t,
		readTestdata(t, "completed_schedule.json"),
		readTestdata(t, "upcoming_fixtures.json"),
	)
	// Start upstream broken so initial Sync inside Run logs Warn without aborting Run!
	espnSrv.SetStatus(http.StatusServiceUnavailable)

	settings := newMapSettings(map[string]string{
		uccalendar.SettingAPIBaseURL: espnSrv.BaseURL(),
	})

	alerts := newFakeAlertSender()
	gate := make(chan struct{})
	banter := newFakeBanter("Hoy juega la franja!")

	svc, err := uccalendar.New(uccalendar.Deps{
		Pool:               pool,
		Settings:           settings,
		Clock:              clock,
		Alerts:             alerts,
		Banter:             banter,
		HTTPClient:         espnSrv.server.Client(),
		Logger:             slog.New(slog.DiscardHandler),
		SyncInterval:       15 * time.Millisecond,
		AlertCheckInterval: 15 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("uccalendar.New: %v", err)
	}

	mustConfigureGuild(t, svc, fakeGuildID, fakeFriendID, fakeChannelID)

	runCtx, cancelRun := context.WithCancel(t.Context())
	runDone := make(chan error, 1)
	go func() {
		runDone <- svc.Run(runCtx)
	}()

	// Wait until at least one failed initial/periodic sync attempt hits espnSrv, then restore 200 OK.
	waitForCondition(t, "initial sync attempt", func() bool {
		return espnSrv.Requests() >= 1
	})
	espnSrv.SetStatus(http.StatusOK)

	// Periodic sync + alert ticks will sync match 8002 and deliver the Saturday matchday alert
	// with synchronous banter!
	select {
	case <-alerts.notifyCh:
		delivered := alerts.Delivered()
		if len(delivered) != 1 || delivered[0].Alert.Banter != "Hoy juega la franja!" {
			t.Errorf("delivered alerts = %+v, want 1 alert with synchronous banter", delivered)
		}
	case <-time.After(5 * time.Second):
		cancelRun()
		t.Fatal("timed out waiting for Run periodic alert delivery")
	}

	// Gate banter and trigger an interactive Check so an async background banter goroutine is
	// blocked inside Generate when Run is canceled!
	banter.SetGate(gate)
	if _, checkErr := svc.Check(t.Context(), uccalendar.Query{Window: uccalendar.WindowTomorrow}); checkErr != nil {
		cancelRun()
		close(gate)
		t.Fatalf("svc.Check(tomorrow): %v", checkErr)
	}

	select {
	case <-banter.startedCh:
	case <-time.After(5 * time.Second):
		cancelRun()
		close(gate)
		t.Fatal("timed out waiting for background banter refresh to start")
	}

	cancelRun()

	select {
	case runErr := <-runDone:
		if runErr != nil {
			t.Fatalf("Run returned error %v, want nil on context cancel", runErr)
		}
	case <-time.After(5 * time.Second):
		close(gate)
		t.Fatal("Run did not cancel in-flight banter goroutine and return promptly")
	}
}
