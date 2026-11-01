package uccalendar_test

import (
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/uccalendar"
)

func TestGuildConfigAuthorizationValidationAndCrossGuildIsolation(t *testing.T) {
	pool := newIsolatedDB(t)
	loc := santiagoLocation(t)
	clock := newFakeClock(time.Date(2026, 10, 27, 11, 0, 0, 0, loc))

	espnSrv := newFakeESPNServer(
		t,
		readTestdata(t, "completed_schedule.json"),
		readTestdata(t, "upcoming_fixtures.json"),
	)

	settings := newMapSettings(map[string]string{
		uccalendar.SettingAPIBaseURL: espnSrv.BaseURL(),
	})

	svc, err := uccalendar.New(uccalendar.Deps{
		Pool:       pool,
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

	managerA := uccalendar.GuildScope{GuildID: fakeGuildID, CanManageGuild: true}
	memberA := uccalendar.GuildScope{GuildID: fakeGuildID, CanManageGuild: false}
	managerB := uccalendar.GuildScope{GuildID: otherGuildID, CanManageGuild: true}

	// 1. Authorization: callers without ManageGuild are rejected with ErrForbidden on Get, Update, and Clear,
	// even if their update or target payload would otherwise fail validation.
	if _, err := svc.GetGuildConfig(t.Context(), memberA); !errors.Is(err, uccalendar.ErrForbidden) {
		t.Fatalf("GetGuildConfig without ManageGuild = %v, want ErrForbidden", err)
	}

	badSnow := "not-a-snowflake"
	if _, err := svc.UpdateGuildConfig(t.Context(), memberA, uccalendar.GuildConfigUpdate{FriendUserID: &badSnow}); !errors.Is(err, uccalendar.ErrForbidden) {
		t.Fatalf("UpdateGuildConfig without ManageGuild = %v, want ErrForbidden before payload inspection", err)
	}

	if _, err := svc.ClearGuildConfig(t.Context(), memberA, uccalendar.ClearTarget("bogus")); !errors.Is(err, uccalendar.ErrForbidden) {
		t.Fatalf("ClearGuildConfig without ManageGuild = %v, want ErrForbidden before target inspection", err)
	}

	// 2. Input validation & error aggregation for managers (never echoing raw invalid values).
	if _, err := svc.GetGuildConfig(t.Context(), uccalendar.GuildScope{GuildID: "", CanManageGuild: true}); !errors.Is(err, uccalendar.ErrInvalidInput) {
		t.Errorf("GetGuildConfig empty guild_id = %v, want ErrInvalidInput", err)
	}

	if _, err := svc.UpdateGuildConfig(t.Context(), managerA, uccalendar.GuildConfigUpdate{}); !errors.Is(err, uccalendar.ErrInvalidInput) {
		t.Errorf("UpdateGuildConfig empty update = %v, want ErrInvalidInput", err)
	}

	badFriend := "bad-friend-value"
	badChannel := "0"
	badLocale := "es-AR-voseo"
	_, aggErr := svc.UpdateGuildConfig(t.Context(), managerA, uccalendar.GuildConfigUpdate{
		FriendUserID: &badFriend,
		ChannelID:    &badChannel,
		Locale:       &badLocale,
	})
	if !errors.Is(aggErr, uccalendar.ErrInvalidInput) {
		t.Fatalf("UpdateGuildConfig with three invalid fields = %v, want ErrInvalidInput", aggErr)
	}
	for _, field := range []string{"friend_user_id", "channel_id", "locale"} {
		if !strings.Contains(aggErr.Error(), field) {
			t.Errorf("aggregated UpdateGuildConfig error %q missing %q", aggErr.Error(), field)
		}
	}
	for _, leaked := range []string{badFriend, badLocale} {
		if strings.Contains(aggErr.Error(), leaked) {
			t.Errorf("UpdateGuildConfig error %q echoed rejected input %q", aggErr.Error(), leaked)
		}
	}

	if _, err := svc.ClearGuildConfig(t.Context(), managerA, uccalendar.ClearTarget("everything")); !errors.Is(err, uccalendar.ErrInvalidInput) {
		t.Errorf("ClearGuildConfig unknown target = %v, want ErrInvalidInput", err)
	}

	// 3. Configure Guild A (friendA + channelA + en-US) and Guild B (friendB only, default es-CL).
	friendA := fakeFriendID
	chanA := fakeChannelID
	localeA := uccalendar.LocaleENUS
	friendB := otherFriendID

	cfgA, err := svc.UpdateGuildConfig(t.Context(), managerA, uccalendar.GuildConfigUpdate{
		FriendUserID: &friendA,
	})
	if err != nil {
		t.Fatalf("set friend on Guild A: %v", err)
	}
	if cfgA.GuildID != fakeGuildID || cfgA.FriendUserID != friendA || cfgA.ChannelID != "" || cfgA.Locale != "" {
		t.Fatalf("cfgA after friend set = %+v, want friend=%s channel='' locale=''", cfgA, friendA)
	}

	cfgA, err = svc.UpdateGuildConfig(t.Context(), managerA, uccalendar.GuildConfigUpdate{
		ChannelID: &chanA,
		Locale:    &localeA,
	})
	if err != nil {
		t.Fatalf("set channel and locale on Guild A: %v", err)
	}
	if cfgA.FriendUserID != friendA || cfgA.ChannelID != chanA || cfgA.Locale != uccalendar.LocaleENUS {
		t.Fatalf("cfgA after channel+locale set = %+v, want friend=%s channel=%s locale=en-US", cfgA, friendA, chanA)
	}

	_, err = svc.UpdateGuildConfig(t.Context(), managerB, uccalendar.GuildConfigUpdate{
		FriendUserID: &friendB,
	})
	if err != nil {
		t.Fatalf("set friend on Guild B: %v", err)
	}

	// Unauthorized member still cannot read or clear Guild A's config.
	if _, err := svc.ClearGuildConfig(t.Context(), memberA, uccalendar.ClearTargetAll); !errors.Is(err, uccalendar.ErrForbidden) {
		t.Fatalf("ClearGuildConfig by non-manager = %v, want ErrForbidden", err)
	}

	readA, err := svc.GetGuildConfig(t.Context(), managerA)
	if err != nil {
		t.Fatalf("GetGuildConfig Guild A: %v", err)
	}
	if readA.FriendUserID != friendA || readA.ChannelID != chanA || readA.Locale != uccalendar.LocaleENUS {
		t.Fatalf("Guild A config = %+v, want intact after unauthorized Clear attempt", readA)
	}

	// 4. Cross-guild isolation on Check:
	// - Guild A resolves friendA and Locale "en-US"
	// - Guild B resolves friendB and default Locale "es-CL"
	// - Unconfigured Guild C resolves "" and default Locale "es-CL"
	// - Direct message (GuildID: "") resolves "" and default Locale "es-CL"
	repA, err := svc.Check(t.Context(), uccalendar.Query{GuildID: fakeGuildID})
	if err != nil {
		t.Fatalf("Check Guild A: %v", err)
	}
	if repA.FriendUserID != friendA || repA.Locale != uccalendar.LocaleENUS {
		t.Errorf("Guild A Report = (friend=%q, locale=%q), want (%q, %q)", repA.FriendUserID, repA.Locale, friendA, uccalendar.LocaleENUS)
	}

	repB, err := svc.Check(t.Context(), uccalendar.Query{GuildID: otherGuildID})
	if err != nil {
		t.Fatalf("Check Guild B: %v", err)
	}
	if repB.FriendUserID != friendB || repB.Locale != uccalendar.LocaleESCL {
		t.Errorf("Guild B Report = (friend=%q, locale=%q), want (%q, %q)", repB.FriendUserID, repB.Locale, friendB, uccalendar.LocaleESCL)
	}

	repUnconfigured, err := svc.Check(t.Context(), uccalendar.Query{GuildID: thirdGuildID})
	if err != nil {
		t.Fatalf("Check unconfigured Guild C: %v", err)
	}
	if repUnconfigured.FriendUserID != "" || repUnconfigured.Locale != uccalendar.LocaleESCL {
		t.Errorf("unconfigured Guild C Report = (friend=%q, locale=%q), want ('', es-CL)", repUnconfigured.FriendUserID, repUnconfigured.Locale)
	}

	repDM, err := svc.Check(t.Context(), uccalendar.Query{})
	if err != nil {
		t.Fatalf("Check DM: %v", err)
	}
	if repDM.FriendUserID != "" || repDM.Locale != uccalendar.LocaleESCL {
		t.Errorf("DM Report = (friend=%q, locale=%q), want ('', es-CL)", repDM.FriendUserID, repDM.Locale)
	}

	// 5. Partial and full clears on Guild A leave Guild B completely untouched.
	afterClearFriend, err := svc.ClearGuildConfig(t.Context(), managerA, uccalendar.ClearTargetFriend)
	if err != nil {
		t.Fatalf("ClearTargetFriend Guild A: %v", err)
	}
	if afterClearFriend.FriendUserID != "" || afterClearFriend.ChannelID != chanA || afterClearFriend.Locale != uccalendar.LocaleENUS {
		t.Errorf("after ClearTargetFriend = %+v, want friend='' channel=%s locale=en-US", afterClearFriend, chanA)
	}

	afterClearLocale, err := svc.ClearGuildConfig(t.Context(), managerA, uccalendar.ClearTargetLocale)
	if err != nil {
		t.Fatalf("ClearTargetLocale Guild A: %v", err)
	}
	if afterClearLocale.Locale != "" || afterClearLocale.ChannelID != chanA {
		t.Errorf("after ClearTargetLocale = %+v, want locale='' channel=%s", afterClearLocale, chanA)
	}

	repAAfterLocaleClear, err := svc.Check(t.Context(), uccalendar.Query{GuildID: fakeGuildID})
	if err != nil {
		t.Fatalf("Check Guild A after ClearTargetLocale: %v", err)
	}
	if repAAfterLocaleClear.Locale != uccalendar.LocaleESCL {
		t.Errorf("Guild A Report.Locale after ClearTargetLocale = %q, want %q", repAAfterLocaleClear.Locale, uccalendar.LocaleESCL)
	}

	// Restore friendA and localeA, clear channelA, then clear default ("") -> ClearTargetAll.
	_, err = svc.UpdateGuildConfig(t.Context(), managerA, uccalendar.GuildConfigUpdate{
		FriendUserID: &friendA,
		Locale:       &localeA,
	})
	if err != nil {
		t.Fatalf("restore friendA and localeA: %v", err)
	}

	afterClearChan, err := svc.ClearGuildConfig(t.Context(), managerA, uccalendar.ClearTargetChannel)
	if err != nil {
		t.Fatalf("ClearTargetChannel Guild A: %v", err)
	}
	if afterClearChan.FriendUserID != friendA || afterClearChan.ChannelID != "" || afterClearChan.Locale != uccalendar.LocaleENUS {
		t.Errorf("after ClearTargetChannel = %+v, want friend=%s channel='' locale=en-US", afterClearChan, friendA)
	}

	afterClearAll, err := svc.ClearGuildConfig(t.Context(), managerA, "")
	if err != nil {
		t.Fatalf("ClearGuildConfig default target Guild A: %v", err)
	}
	if afterClearAll.FriendUserID != "" || afterClearAll.ChannelID != "" || afterClearAll.Locale != "" {
		t.Errorf("after ClearGuildConfig('') = %+v, want all empty", afterClearAll)
	}

	readB, err := svc.GetGuildConfig(t.Context(), managerB)
	if err != nil {
		t.Fatalf("GetGuildConfig Guild B after clearing Guild A: %v", err)
	}
	if readB.FriendUserID != friendB {
		t.Errorf("Guild B FriendUserID = %q, want %q untouched", readB.FriendUserID, friendB)
	}
}
