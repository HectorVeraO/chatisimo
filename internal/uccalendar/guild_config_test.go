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
	_, aggErr := svc.UpdateGuildConfig(t.Context(), managerA, uccalendar.GuildConfigUpdate{
		FriendUserID: &badFriend,
		ChannelID:    &badChannel,
	})
	if !errors.Is(aggErr, uccalendar.ErrInvalidInput) {
		t.Fatalf("UpdateGuildConfig with two invalid fields = %v, want ErrInvalidInput", aggErr)
	}
	if !strings.Contains(aggErr.Error(), "friend_user_id") || !strings.Contains(aggErr.Error(), "channel_id") {
		t.Errorf("aggregated UpdateGuildConfig error %q missing friend_user_id or channel_id", aggErr.Error())
	}
	if strings.Contains(aggErr.Error(), badFriend) {
		t.Errorf("UpdateGuildConfig error %q echoed rejected input %q", aggErr.Error(), badFriend)
	}

	if _, err := svc.ClearGuildConfig(t.Context(), managerA, uccalendar.ClearTarget("everything")); !errors.Is(err, uccalendar.ErrInvalidInput) {
		t.Errorf("ClearGuildConfig unknown target = %v, want ErrInvalidInput", err)
	}

	// 3. Configure Guild A (friendA + channelA) and Guild B (friendB only).
	friendA := fakeFriendID
	chanA := fakeChannelID
	friendB := otherFriendID

	cfgA, err := svc.UpdateGuildConfig(t.Context(), managerA, uccalendar.GuildConfigUpdate{
		FriendUserID: &friendA,
	})
	if err != nil {
		t.Fatalf("set friend on Guild A: %v", err)
	}
	if cfgA.GuildID != fakeGuildID || cfgA.FriendUserID != friendA || cfgA.ChannelID != "" {
		t.Fatalf("cfgA after friend set = %+v, want friend=%s channel=''", cfgA, friendA)
	}

	cfgA, err = svc.UpdateGuildConfig(t.Context(), managerA, uccalendar.GuildConfigUpdate{
		ChannelID: &chanA,
	})
	if err != nil {
		t.Fatalf("set channel on Guild A: %v", err)
	}
	if cfgA.FriendUserID != friendA || cfgA.ChannelID != chanA {
		t.Fatalf("cfgA after channel set = %+v, want friend=%s channel=%s", cfgA, friendA, chanA)
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
	if readA.FriendUserID != friendA || readA.ChannelID != chanA {
		t.Fatalf("Guild A config = %+v, want intact after unauthorized Clear attempt", readA)
	}

	// 4. Cross-guild isolation on Check:
	// - Guild A resolves friendA
	// - Guild B resolves friendB
	// - Unconfigured Guild C resolves ""
	// - Direct message (GuildID: "") resolves ""
	repA, err := svc.Check(t.Context(), uccalendar.Query{GuildID: fakeGuildID})
	if err != nil {
		t.Fatalf("Check Guild A: %v", err)
	}
	if repA.FriendUserID != friendA {
		t.Errorf("Guild A Report.FriendUserID = %q, want %q", repA.FriendUserID, friendA)
	}

	repB, err := svc.Check(t.Context(), uccalendar.Query{GuildID: otherGuildID})
	if err != nil {
		t.Fatalf("Check Guild B: %v", err)
	}
	if repB.FriendUserID != friendB {
		t.Errorf("Guild B Report.FriendUserID = %q, want %q", repB.FriendUserID, friendB)
	}

	repUnconfigured, err := svc.Check(t.Context(), uccalendar.Query{GuildID: thirdGuildID})
	if err != nil {
		t.Fatalf("Check unconfigured Guild C: %v", err)
	}
	if repUnconfigured.FriendUserID != "" {
		t.Errorf("unconfigured Guild C Report.FriendUserID = %q, want empty", repUnconfigured.FriendUserID)
	}

	repDM, err := svc.Check(t.Context(), uccalendar.Query{})
	if err != nil {
		t.Fatalf("Check DM: %v", err)
	}
	if repDM.FriendUserID != "" {
		t.Errorf("DM Report.FriendUserID = %q, want empty", repDM.FriendUserID)
	}

	// 5. Partial and full clears on Guild A leave Guild B completely untouched.
	afterClearFriend, err := svc.ClearGuildConfig(t.Context(), managerA, uccalendar.ClearTargetFriend)
	if err != nil {
		t.Fatalf("ClearTargetFriend Guild A: %v", err)
	}
	if afterClearFriend.FriendUserID != "" || afterClearFriend.ChannelID != chanA {
		t.Errorf("after ClearTargetFriend = %+v, want friend='' channel=%s", afterClearFriend, chanA)
	}

	// Restore friendA, clear channelA, then clear default ("") -> ClearTargetAll.
	_, err = svc.UpdateGuildConfig(t.Context(), managerA, uccalendar.GuildConfigUpdate{FriendUserID: &friendA})
	if err != nil {
		t.Fatalf("restore friendA: %v", err)
	}

	afterClearChan, err := svc.ClearGuildConfig(t.Context(), managerA, uccalendar.ClearTargetChannel)
	if err != nil {
		t.Fatalf("ClearTargetChannel Guild A: %v", err)
	}
	if afterClearChan.FriendUserID != friendA || afterClearChan.ChannelID != "" {
		t.Errorf("after ClearTargetChannel = %+v, want friend=%s channel=''", afterClearChan, friendA)
	}

	afterClearAll, err := svc.ClearGuildConfig(t.Context(), managerA, "")
	if err != nil {
		t.Fatalf("ClearGuildConfig default target Guild A: %v", err)
	}
	if afterClearAll.FriendUserID != "" || afterClearAll.ChannelID != "" {
		t.Errorf("after ClearGuildConfig('') = %+v, want both empty", afterClearAll)
	}

	readB, err := svc.GetGuildConfig(t.Context(), managerB)
	if err != nil {
		t.Fatalf("GetGuildConfig Guild B after clearing Guild A: %v", err)
	}
	if readB.FriendUserID != friendB {
		t.Errorf("Guild B FriendUserID = %q, want %q untouched", readB.FriendUserID, friendB)
	}
}
