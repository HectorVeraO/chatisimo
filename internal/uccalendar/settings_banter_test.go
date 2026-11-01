package uccalendar_test

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/HectorVeraO/chatisimo/internal/uccalendar"
)

func TestSettings5MinuteTTLLiveUpdatesAndKillSwitch(t *testing.T) {
	pool := newIsolatedDB(t)
	cfgStore := newRealConfigStore(t, pool)
	loc := santiagoLocation(t)

	// Start on Friday 2026-10-30 at 15:10 Santiago (default friday_alert_time is 17:00).
	clock := newFakeClock(time.Date(2026, 10, 30, 15, 10, 0, 0, loc))

	espnSrv := newFakeESPNServer(
		t,
		readTestdata(t, "completed_schedule.json"),
		readTestdata(t, "upcoming_fixtures.json"),
	)

	if err := cfgStore.Set(t.Context(), uccalendar.SettingAPIBaseURL, espnSrv.BaseURL()); err != nil {
		t.Fatalf("Set api_base_url: %v", err)
	}

	banter := newFakeBanter("La franja manda este sabado, imposible armar junta.")
	alerts := newFakeAlertSender()

	svc, err := uccalendar.New(uccalendar.Deps{
		Pool:        pool,
		Settings:    cfgStore,
		Clock:       clock,
		Alerts:      alerts,
		Banter:      banter,
		HTTPClient:  espnSrv.server.Client(),
		Logger:      slog.New(slog.DiscardHandler),
		SettingsTTL: 5 * time.Minute,
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

	// Unconfigured guild defaults FriendUserID to "".
	initialRep, err := svc.Check(t.Context(), uccalendar.Query{GuildID: fakeGuildID})
	if err != nil {
		t.Fatalf("initial Check: %v", err)
	}

	if initialRep.FriendUserID != "" {
		t.Errorf("initial FriendUserID = %q, want empty when guild is unconfigured", initialRep.FriendUserID)
	}

	// Wait for initial cold async banter refresh to populate the cache at T = 15:10.
	waitForCondition(t, "initial banter cache warm", func() bool {
		rep, checkErr := svc.Check(t.Context(), uccalendar.Query{GuildID: fakeGuildID})
		return checkErr == nil && rep.Banter != ""
	})

	if banter.Calls() != 1 {
		t.Fatalf("banter.Calls() = %d, want 1", banter.Calls())
	}

	// Configure guild friend and alert channel immediately (no TTL delay on guild_config!) and
	// change friday_alert_time from 17:00 -> 15:00 in platform.setting (subject to 5m TTL).
	mustConfigureGuild(t, svc, fakeGuildID, otherFriendID, fakeChannelID)

	if err := cfgStore.Set(t.Context(), uccalendar.SettingFridayAlertTime, "15:00"); err != nil {
		t.Fatalf("Set friday_alert_time: %v", err)
	}

	// At T + 2m (15:12), platform settings cache is still within 5m TTL -> alert does NOT fire yet.
	clock.Advance(2 * time.Minute)
	if err := svc.CheckAlerts(t.Context()); err != nil {
		t.Fatalf("CheckAlerts before 5m TTL: %v", err)
	}

	if len(alerts.Delivered()) != 0 {
		t.Fatalf("alerts delivered before 5m settings TTL = %d, want 0", len(alerts.Delivered()))
	}

	// Advance past 5m settings TTL (T = 15:16) -> reloads 15:00 and fires Friday alert for fakeGuildID!
	clock.Advance(4 * time.Minute)
	if err := svc.CheckAlerts(t.Context()); err != nil {
		t.Fatalf("CheckAlerts after 5m TTL: %v", err)
	}

	delivered := alerts.Delivered()
	if len(delivered) != 1 || delivered[0].Alert.Kind != uccalendar.AlertFridayWeekend {
		t.Fatalf("delivered after 5m TTL = %+v, want 1 Friday alert", delivered)
	}

	if delivered[0].Alert.FriendUserID != otherFriendID {
		t.Errorf("Friday alert FriendUserID = %q, want %q", delivered[0].Alert.FriendUserID, otherFriendID)
	}

	// Advance 2 hours (T = 17:16): banter entry generated at 15:10 is 2h06m old, still fresh under 24h TTL.
	clock.Advance(2 * time.Hour)
	banter.SetResponse("Frase renovada tras bajar el TTL a una hora.", nil)

	repUnder24h, err := svc.Check(t.Context(), uccalendar.Query{GuildID: fakeGuildID})
	if err != nil {
		t.Fatalf("Check under 24h TTL: %v", err)
	}

	if repUnder24h.Banter != "La franja manda este sabado, imposible armar junta." {
		t.Errorf("Banter under 24h TTL = %q, want original cached phrase", repUnder24h.Banter)
	}

	// Shrink uc.slang_cache_ttl from 24h -> 1h and advance 5m so settings cache reloads.
	if err := cfgStore.Set(t.Context(), uccalendar.SettingSlangCacheTTL, "1h"); err != nil {
		t.Fatalf("Set slang_cache_ttl=1h: %v", err)
	}

	clock.Advance(5 * time.Minute)

	// First Check sees 2h11m >= 1h immediately: returns stale phrase in <1ms and triggers async refresh!
	staleRep, err := svc.Check(t.Context(), uccalendar.Query{GuildID: fakeGuildID})
	if err != nil {
		t.Fatalf("Check after shrinking TTL to 1h: %v", err)
	}

	if staleRep.Banter != "La franja manda este sabado, imposible armar junta." {
		t.Errorf("stale-while-revalidate returned %q, want previous stale phrase immediately", staleRep.Banter)
	}

	waitForCondition(t, "revalidated banter after TTL shrink", func() bool {
		rep, checkErr := svc.Check(t.Context(), uccalendar.Query{GuildID: fakeGuildID})
		return checkErr == nil && rep.Banter == "Frase renovada tras bajar el TTL a una hora."
	})

	// Update uc.banter_prompt in platform.setting and advance 5m -> new prompt digest invalidates
	// banter cache immediately (even though the 1h banter entry is only 5m old!) and passes the new
	// system prompt to GenerateWithPrompt.
	customPrompt := "Write one playful Chilean Spanish line using mi chanchito and kingcito."
	if err := cfgStore.Set(t.Context(), uccalendar.SettingBanterPrompt, customPrompt); err != nil {
		t.Fatalf("Set banter_prompt: %v", err)
	}

	banter.SetResponse("Hoy juega la franja, mi chanchito kingcito esta bloqueado.", nil)
	clock.Advance(5 * time.Minute)

	waitForCondition(t, "banter cache invalidated on prompt change", func() bool {
		rep, checkErr := svc.Check(t.Context(), uccalendar.Query{GuildID: fakeGuildID})
		return checkErr == nil && rep.Banter == "Hoy juega la franja, mi chanchito kingcito esta bloqueado."
	})

	sysPrompts := banter.SystemPrompts()
	if len(sysPrompts) == 0 || sysPrompts[len(sysPrompts)-1] != customPrompt {
		t.Errorf("latest system prompt = %v, want %q", sysPrompts, customPrompt)
	}

	// Activate 0s kill-switch and advance 5m -> immediately returns "" and makes zero new LLM calls.
	if err := cfgStore.Set(t.Context(), uccalendar.SettingSlangCacheTTL, "0s"); err != nil {
		t.Fatalf("Set slang_cache_ttl=0s: %v", err)
	}

	clock.Advance(5 * time.Minute)
	callsBeforeKillSwitch := banter.Calls()

	disabledRep, err := svc.Check(t.Context(), uccalendar.Query{GuildID: fakeGuildID})
	if err != nil {
		t.Fatalf("Check with 0s kill-switch: %v", err)
	}

	if disabledRep.Banter != "" {
		t.Errorf("Banter with 0s kill-switch = %q, want empty string", disabledRep.Banter)
	}

	if banter.Calls() != callsBeforeKillSwitch {
		t.Errorf("banter.Calls() changed from %d to %d under 0s kill-switch", callsBeforeKillSwitch, banter.Calls())
	}
}

func TestStaleWhileRevalidateSingleflightSanitizationEvictionAndLogSafety(t *testing.T) {
	loc := santiagoLocation(t)
	clock := newFakeClock(time.Date(2026, 10, 30, 18, 0, 0, 0, loc))

	espnSrv := newFakeESPNServer(
		t,
		readTestdata(t, "completed_schedule.json"),
		readTestdata(t, "upcoming_fixtures.json"),
	)

	settings := newMapSettings(map[string]string{
		uccalendar.SettingAPIBaseURL: espnSrv.BaseURL(),
	})

	var logBuf syncBuffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	gate := make(chan struct{})
	longDirtyBanter := "  \"Oye @everyone <@100000000000000001> la Cato juega en Claro Arena\x07\n" +
		strings.Repeat("cruzados ", 25) + "\"  "
	banter := newFakeBanter(longDirtyBanter)
	banter.SetGate(gate)

	alerts := newFakeAlertSender()

	svc, err := uccalendar.New(uccalendar.Deps{
		Pool:       shared.Pool(),
		Settings:   settings,
		Clock:      clock,
		Alerts:     alerts,
		Banter:     banter,
		HTTPClient: espnSrv.server.Client(),
		Logger:     logger,
	})
	if err != nil {
		t.Fatalf("uccalendar.New: %v", err)
	}
	t.Cleanup(func() {
		_ = svc.Close()
	})

	cleanGuildConfigs(t, shared.Pool())
	mustConfigureGuild(t, svc, fakeGuildID, fakeFriendID, fakeChannelID)

	if err := svc.Sync(t.Context()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	// Fire 12 concurrent Check calls while gate holds the single in-flight LLM call.
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()

			rep, checkErr := svc.Check(t.Context(), uccalendar.Query{GuildID: fakeGuildID})
			if checkErr != nil {
				t.Errorf("concurrent Check: %v", checkErr)
				return
			}

			if rep.Banter != "" {
				t.Errorf("cold miss Banter = %q, want empty immediately", rep.Banter)
			}
		}()
	}
	wg.Wait()

	// Wait until the single background goroutine has entered Generate, then release the gate.
	<-banter.startedCh
	if banter.Calls() != 1 {
		t.Fatalf("singleflight coalesced calls = %d, want exactly 1", banter.Calls())
	}
	close(gate)

	var sanitized string
	waitForCondition(t, "sanitized banter stored", func() bool {
		rep, checkErr := svc.Check(t.Context(), uccalendar.Query{GuildID: fakeGuildID})
		if checkErr == nil && rep.Banter != "" {
			sanitized = rep.Banter
			return true
		}

		return false
	})

	if utf8.RuneCountInString(sanitized) > 160 {
		t.Errorf("sanitized rune count = %d, want <= 160", utf8.RuneCountInString(sanitized))
	}

	if strings.ContainsAny(sanitized, "@<\n\r\t\x07") {
		t.Errorf("sanitized banter %q still contains forbidden characters", sanitized)
	}

	// Verify prompts sent to LLM never include guild, user, or channel IDs.
	for _, p := range banter.Prompts() {
		if strings.Contains(p, fakeGuildID) || strings.Contains(p, fakeFriendID) || strings.Contains(p, fakeChannelID) {
			t.Errorf("LLM prompt %q leaked guild, user, or channel snowflake", p)
		}
	}

	// Exercise bounded 32-entry cache eviction by syncing 36 distinct fixtures on distinct dates.
	banter.SetGate(nil)
	banter.SetResponse("Panorama libre o cruzado para el grupo.", nil)

	var eventsBuilder strings.Builder
	eventsBuilder.WriteString(`{"events":[`)
	for i := range 36 {
		if i > 0 {
			eventsBuilder.WriteByte(',')
		}

		day := time.Date(2027, 1, 1, 20, 0, 0, 0, time.UTC).AddDate(0, 0, i)
		fmt.Fprintf(&eventsBuilder, `{
			"id": %q,
			"date": %q,
			"timeValid": true,
			"competitions": [{
				"id": %q,
				"date": %q,
				"timeValid": true,
				"competitors": [
					{"id": "885", "homeAway": "home", "team": {"id": "885", "displayName": "Universidad Catolica"}},
					{"id": "999", "homeAway": "away", "team": {"id": "999", "displayName": "Rival Sintetico"}}
				]
			}]
		}`, fmt.Sprintf("95%02d", i), day.Format(time.RFC3339), fmt.Sprintf("95%02d", i), day.Format(time.RFC3339))
	}
	eventsBuilder.WriteString(`]}`)

	espnSrv.SetFixtures([]byte(`{"events":[]}`), []byte(eventsBuilder.String()))
	if err := svc.Sync(t.Context()); err != nil {
		t.Fatalf("Sync 36 fixtures: %v", err)
	}

	for i := range 36 {
		day := time.Date(2027, 1, 1, 12, 0, 0, 0, loc).AddDate(0, 0, i)
		clock.Advance(time.Second)
		queryDate := fmt.Sprintf("%04d-%02d-%02d", day.Year(), int(day.Month()), day.Day())

		waitForCondition(t, "populate slot "+queryDate, func() bool {
			rep, checkErr := svc.Check(t.Context(), uccalendar.Query{Date: queryDate})
			return checkErr == nil && rep.Banter != ""
		})
	}

	// Entry 0 (2027-01-01) was the oldest generatedAt and must have been evicted once 36 > 32 entries loaded.
	callsBeforeEvictedCheck := banter.Calls()
	evictedRep, err := svc.Check(t.Context(), uccalendar.Query{Date: "2027-01-01"})
	if err != nil {
		t.Fatalf("Check evicted 2027-01-01: %v", err)
	}

	if evictedRep.Banter != "" {
		t.Errorf("oldest entry 2027-01-01 Banter = %q, want empty after 32-entry cap eviction", evictedRep.Banter)
	}

	waitForCondition(t, "evicted slot refresh finishes", func() bool {
		return banter.Calls() > callsBeforeEvictedCheck
	})

	// Also exercise LLM error, guild config clear, and AlertSender failure to verify logs never leak snowflakes or banter!
	banter.SetResponse("", errors.New("upstream vertex failure"))
	clock.Advance(25 * time.Hour)
	_, _ = svc.Check(t.Context(), uccalendar.Query{Date: "2027-01-02"})
	waitForCondition(t, "failed banter call logged", func() bool {
		return strings.Contains(logBuf.String(), "uc banter generation failed")
	})

	cleanSentAlerts(t, shared.Pool())
	alerts.SetFailNext(1, errors.New("POST /channels/"+fakeChannelID+"/messages failed"))
	clock.Set(time.Date(2027, 1, 1, 18, 0, 0, 0, loc)) // Friday 2027-01-01 18:00
	_ = svc.CheckAlerts(t.Context())

	_, clearErr := svc.ClearGuildConfig(
		t.Context(),
		uccalendar.GuildScope{GuildID: fakeGuildID, CanManageGuild: true},
		uccalendar.ClearTargetFriend,
	)
	if clearErr != nil {
		t.Fatalf("ClearGuildConfig: %v", clearErr)
	}

	if err := svc.Close(); err != nil {
		t.Fatalf("svc.Close: %v", err)
	}

	logs := logBuf.String()
	for _, forbidden := range []string{
		fakeGuildID,
		fakeFriendID,
		fakeChannelID,
		"Panorama libre o cruzado",
		"cruzados",
	} {
		if strings.Contains(logs, forbidden) {
			t.Errorf("log output %q leaked forbidden token %q", logs, forbidden)
		}
	}
}

func TestInvalidSettingsAggregationAndDiscardedBanter(t *testing.T) {
	loc := santiagoLocation(t)
	clock := newFakeClock(time.Date(2026, 11, 4, 10, 0, 0, 0, loc))

	espnSrv := newFakeESPNServer(
		t,
		readTestdata(t, "completed_schedule.json"),
		readTestdata(t, "upcoming_fixtures.json"),
	)

	badSettings := newMapSettings(map[string]string{
		uccalendar.SettingFridayAlertTime:   "25:00",
		uccalendar.SettingMatchdayAlertTime: "9:00",
		uccalendar.SettingSlangCacheTTL:     "10s",
		uccalendar.SettingAPIBaseURL:        "ftp://user@bad host/api?x=1",
		uccalendar.SettingBanterPrompt:      "bad\x00prompt",
	})

	banter := newFakeBanter("   @\x00<\n   ")
	alerts := newFakeAlertSender()

	svc, err := uccalendar.New(uccalendar.Deps{
		Pool:       shared.Pool(),
		Settings:   badSettings,
		Clock:      clock,
		Alerts:     alerts,
		Banter:     legacyOnlyBanterGenerator{inner: banter},
		HTTPClient: espnSrv.server.Client(),
		Logger:     slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("uccalendar.New: %v", err)
	}
	t.Cleanup(func() {
		_ = svc.Close()
	})

	syncErr := svc.Sync(t.Context())
	if !errors.Is(syncErr, uccalendar.ErrInvalidInput) {
		t.Fatalf("Sync with invalid settings = %v, want ErrInvalidInput", syncErr)
	}

	for _, key := range []string{
		uccalendar.SettingFridayAlertTime,
		uccalendar.SettingMatchdayAlertTime,
		uccalendar.SettingSlangCacheTTL,
		uccalendar.SettingAPIBaseURL,
		uccalendar.SettingBanterPrompt,
	} {
		if !strings.Contains(syncErr.Error(), key) {
			t.Errorf("aggregated settings error %q missing key %q", syncErr.Error(), key)
		}
	}

	// Fix settings and sync away match 8004 on Wednesday 2026-11-04 to test away matchday alert & sanitizer discard.
	cleanSentAlerts(t, shared.Pool())
	cleanGuildConfigs(t, shared.Pool())
	mustConfigureGuild(t, svc, fakeGuildID, fakeFriendID, fakeChannelID)

	goodSettings := newMapSettings(map[string]string{
		uccalendar.SettingAPIBaseURL: espnSrv.BaseURL(),
	})
	badSettings.mu.Lock()
	badSettings.values = goodSettings.values
	badSettings.mu.Unlock()

	if err := svc.Sync(t.Context()); err != nil {
		t.Fatalf("Sync after fixing settings: %v", err)
	}

	if err := svc.CheckAlerts(t.Context()); err != nil {
		t.Fatalf("CheckAlerts on away matchday: %v", err)
	}

	waitForCondition(t, "discarded banter attempt completes", func() bool {
		return banter.Calls() >= 1
	})

	gotAlerts := alerts.Delivered()
	if len(gotAlerts) != 1 || gotAlerts[0].Alert.Kind != uccalendar.AlertMatchdayMorning {
		t.Fatalf("away matchday alerts = %+v, want 1 matchday alert", gotAlerts)
	}
}
