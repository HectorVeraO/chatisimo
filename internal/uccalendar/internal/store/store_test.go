package store_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/HectorVeraO/chatisimo/internal/platform/db"
	"github.com/HectorVeraO/chatisimo/internal/platform/pgtest"
	"github.com/HectorVeraO/chatisimo/internal/uccalendar"
	"github.com/HectorVeraO/chatisimo/internal/uccalendar/internal/store"
)

const (
	closeDeadline = 30 * time.Second
	guildAlphaID  = "200000000000000001"
	guildBetaID   = "200000000000000002"
	guildGammaID  = "200000000000000003"
	friendAlphaID = "100000000000000001"
	friendBetaID  = "100000000000000002"
	chanAlphaID   = "300000000000000001"
	chanBetaID    = "300000000000000002"
)

var shared *pgtest.Database

func TestMain(m *testing.M) {
	code := run(m)
	if code == 0 {
		if leakErr := goleak.Find(); leakErr != nil {
			fmt.Fprintln(os.Stderr, leakErr)
			code = 1
		}
	}

	os.Exit(code)
}

func run(m *testing.M) int {
	ctx := context.Background()

	ucSource, err := uccalendar.MigrationSource()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	database, err := pgtest.New(ctx, pgtest.Options{Sources: []db.Source{ucSource}})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	shared = database
	code := m.Run()

	closeCtx, cancel := context.WithTimeout(context.Background(), closeDeadline)
	defer cancel()

	err = database.Close(closeCtx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	return code
}

func TestMigrationsEmbedSQL(t *testing.T) {
	files, err := store.Migrations()
	if err != nil {
		t.Fatalf("Migrations: %v", err)
	}

	for _, name := range []string{"0001_init.sql", "0002_guild_config.sql", "0003_guild_locale.sql"} {
		entry, openErr := files.Open(name)
		if openErr != nil {
			t.Fatalf("open %s: %v", name, openErr)
		}

		closeErr := entry.Close()
		if closeErr != nil {
			t.Fatalf("close %s: %v", name, closeErr)
		}
	}
}

func TestGuildConfigCRUDPartialClearAndAlertList(t *testing.T) {
	s, err := store.New(shared.Pool())
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}

	_, cleanupErr := shared.Pool().Exec(t.Context(), "DELETE FROM uccalendar.guild_config")
	if cleanupErr != nil {
		t.Fatalf("reset guild_config: %v", cleanupErr)
	}

	t0 := time.Date(2026, 10, 30, 14, 0, 0, 0, time.UTC)

	// Unconfigured guild returns empty FriendUserID, ChannelID, and Locale with GuildID populated.
	initial, err := s.GetGuildConfig(t.Context(), guildAlphaID)
	if err != nil {
		t.Fatalf("GetGuildConfig unconfigured: %v", err)
	}
	if initial.GuildID != guildAlphaID || initial.FriendUserID != "" || initial.ChannelID != "" || initial.Locale != "" {
		t.Fatalf("initial config = %+v, want empty fields for guild %s", initial, guildAlphaID)
	}

	// Clearing a single field on an unconfigured guild is a no-op and inserts zero rows.
	clearedUnset, err := s.ApplyGuildConfigPatch(
		t.Context(),
		guildAlphaID,
		store.GuildConfigPatch{SetFriendUserID: true, FriendUserID: ""},
		t0,
	)
	if err != nil {
		t.Fatalf("clear single field on unconfigured guild: %v", err)
	}
	if clearedUnset.FriendUserID != "" || clearedUnset.ChannelID != "" {
		t.Fatalf("clearedUnset = %+v, want empty", clearedUnset)
	}

	// Set friend only on Alpha.
	withFriend, err := s.ApplyGuildConfigPatch(
		t.Context(),
		guildAlphaID,
		store.GuildConfigPatch{SetFriendUserID: true, FriendUserID: friendAlphaID},
		t0,
	)
	if err != nil {
		t.Fatalf("set friend on Alpha: %v", err)
	}
	if withFriend.FriendUserID != friendAlphaID || withFriend.ChannelID != "" {
		t.Fatalf("withFriend = %+v, want friend=%s channel=''", withFriend, friendAlphaID)
	}

	// Add channel on Alpha without touching friend.
	withBoth, err := s.ApplyGuildConfigPatch(
		t.Context(),
		guildAlphaID,
		store.GuildConfigPatch{SetChannelID: true, ChannelID: chanAlphaID},
		t0.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("set channel on Alpha: %v", err)
	}
	if withBoth.FriendUserID != friendAlphaID || withBoth.ChannelID != chanAlphaID {
		t.Fatalf("withBoth = %+v, want friend=%s channel=%s", withBoth, friendAlphaID, chanAlphaID)
	}

	// Set channel + locale on Beta, and friend only on Gamma.
	_, err = s.ApplyGuildConfigPatch(
		t.Context(),
		guildBetaID,
		store.GuildConfigPatch{
			SetChannelID: true,
			ChannelID:    chanBetaID,
			SetLocale:    true,
			Locale:       uccalendar.LocaleENUS,
		},
		t0.Add(2*time.Minute),
	)
	if err != nil {
		t.Fatalf("set channel and locale on Beta: %v", err)
	}

	_, err = s.ApplyGuildConfigPatch(
		t.Context(),
		guildGammaID,
		store.GuildConfigPatch{SetFriendUserID: true, FriendUserID: friendBetaID},
		t0.Add(3*time.Minute),
	)
	if err != nil {
		t.Fatalf("set friend on Gamma: %v", err)
	}

	// ListAlertGuildConfigs returns only guilds with channel_id <> '' (Alpha and Beta, not Gamma).
	alertGuilds, err := s.ListAlertGuildConfigs(t.Context())
	if err != nil {
		t.Fatalf("ListAlertGuildConfigs: %v", err)
	}
	if len(alertGuilds) != 2 {
		t.Fatalf("ListAlertGuildConfigs len = %d, want 2: %+v", len(alertGuilds), alertGuilds)
	}
	if alertGuilds[0].GuildID != guildAlphaID || alertGuilds[0].FriendUserID != friendAlphaID || alertGuilds[0].ChannelID != chanAlphaID || alertGuilds[0].Locale != "" {
		t.Errorf("alertGuilds[0] = %+v, want Alpha", alertGuilds[0])
	}
	if alertGuilds[1].GuildID != guildBetaID || alertGuilds[1].FriendUserID != "" || alertGuilds[1].ChannelID != chanBetaID || alertGuilds[1].Locale != uccalendar.LocaleENUS {
		t.Errorf("alertGuilds[1] = %+v, want Beta with en-US locale", alertGuilds[1])
	}

	// Set locale on Alpha, then clear friend and channel -> locale keeps row alive until locale is cleared too.
	withLocaleAlpha, err := s.ApplyGuildConfigPatch(
		t.Context(),
		guildAlphaID,
		store.GuildConfigPatch{SetLocale: true, Locale: uccalendar.LocaleENUS},
		t0.Add(3*time.Minute+30*time.Second),
	)
	if err != nil {
		t.Fatalf("set locale on Alpha: %v", err)
	}
	if withLocaleAlpha.Locale != uccalendar.LocaleENUS || withLocaleAlpha.FriendUserID != friendAlphaID {
		t.Fatalf("withLocaleAlpha = %+v, want locale=en-US and friend preserved", withLocaleAlpha)
	}

	// Clear friend only on Alpha -> preserves channel and locale.
	afterFriendClear, err := s.ApplyGuildConfigPatch(
		t.Context(),
		guildAlphaID,
		store.GuildConfigPatch{SetFriendUserID: true, FriendUserID: ""},
		t0.Add(4*time.Minute),
	)
	if err != nil {
		t.Fatalf("clear friend on Alpha: %v", err)
	}
	if afterFriendClear.FriendUserID != "" || afterFriendClear.ChannelID != chanAlphaID || afterFriendClear.Locale != uccalendar.LocaleENUS {
		t.Fatalf("afterFriendClear = %+v, want friend='' channel=%s locale=en-US", afterFriendClear, chanAlphaID)
	}

	// Clear channel only on Alpha -> locale is still set, so row remains.
	afterChannelClear, err := s.ApplyGuildConfigPatch(
		t.Context(),
		guildAlphaID,
		store.GuildConfigPatch{SetChannelID: true, ChannelID: ""},
		t0.Add(5*time.Minute),
	)
	if err != nil {
		t.Fatalf("clear channel on Alpha: %v", err)
	}
	if afterChannelClear.FriendUserID != "" || afterChannelClear.ChannelID != "" || afterChannelClear.Locale != uccalendar.LocaleENUS {
		t.Fatalf("afterChannelClear = %+v, want locale=en-US only", afterChannelClear)
	}

	// Clear locale on Alpha -> all three fields now empty, so row is pruned from table.
	afterLocaleClear, err := s.ApplyGuildConfigPatch(
		t.Context(),
		guildAlphaID,
		store.GuildConfigPatch{SetLocale: true, Locale: ""},
		t0.Add(5*time.Minute+30*time.Second),
	)
	if err != nil {
		t.Fatalf("clear locale on Alpha: %v", err)
	}
	if afterLocaleClear.FriendUserID != "" || afterLocaleClear.ChannelID != "" || afterLocaleClear.Locale != "" {
		t.Fatalf("afterLocaleClear = %+v, want empty", afterLocaleClear)
	}

	var alphaRows int
	err = shared.Pool().QueryRow(
		t.Context(),
		"SELECT count(*) FROM uccalendar.guild_config WHERE guild_id = $1",
		guildAlphaID,
	).Scan(&alphaRows)
	if err != nil {
		t.Fatalf("count Alpha rows: %v", err)
	}
	if alphaRows != 0 {
		t.Errorf("Alpha row count after emptying last field = %d, want 0", alphaRows)
	}

	// Clear all three fields at once on Beta -> deletes Beta's row without touching Gamma.
	_, err = s.ApplyGuildConfigPatch(
		t.Context(),
		guildBetaID,
		store.GuildConfigPatch{SetFriendUserID: true, SetChannelID: true, SetLocale: true},
		t0.Add(6*time.Minute),
	)
	if err != nil {
		t.Fatalf("clear all on Beta: %v", err)
	}

	gammaAfter, err := s.GetGuildConfig(t.Context(), guildGammaID)
	if err != nil {
		t.Fatalf("GetGuildConfig Gamma: %v", err)
	}
	if gammaAfter.FriendUserID != friendBetaID {
		t.Errorf("Gamma FriendUserID = %q, want %q untouched", gammaAfter.FriendUserID, friendBetaID)
	}
}

func TestClaimAndReleaseAlertIsolatedPerGuild(t *testing.T) {
	s, err := store.New(shared.Pool())
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}

	_, cleanupErr := shared.Pool().Exec(t.Context(), "DELETE FROM uccalendar.sent_alert")
	if cleanupErr != nil {
		t.Fatalf("reset sent_alert: %v", cleanupErr)
	}

	sentAt := time.Date(2026, 10, 30, 20, 0, 0, 0, time.UTC)

	claimedAlpha, err := s.ClaimAlert(t.Context(), guildAlphaID, "friday:2026-10-30", sentAt)
	if err != nil {
		t.Fatalf("first Alpha ClaimAlert: %v", err)
	}
	if !claimedAlpha {
		t.Fatal("first Alpha ClaimAlert = false, want true")
	}

	// Same alertKey for a second guild must succeed independently.
	claimedBeta, err := s.ClaimAlert(t.Context(), guildBetaID, "friday:2026-10-30", sentAt)
	if err != nil {
		t.Fatalf("first Beta ClaimAlert: %v", err)
	}
	if !claimedBeta {
		t.Fatal("first Beta ClaimAlert = false, want true for distinct guild")
	}

	claimedAlphaAgain, err := s.ClaimAlert(t.Context(), guildAlphaID, "friday:2026-10-30", sentAt.Add(time.Minute))
	if err != nil {
		t.Fatalf("second Alpha ClaimAlert: %v", err)
	}
	if claimedAlphaAgain {
		t.Fatal("second Alpha ClaimAlert = true, want false for duplicate (guild_id, alert_key)")
	}

	otherClaimed, err := s.ClaimAlert(t.Context(), guildAlphaID, "matchday:701001:2026-10-31", sentAt)
	if err != nil {
		t.Fatalf("distinct Alpha ClaimAlert: %v", err)
	}
	if !otherClaimed {
		t.Fatal("distinct Alpha ClaimAlert = false, want true")
	}

	// Releasing Alpha's claim must leave Beta's claim intact.
	err = s.ReleaseAlert(t.Context(), guildAlphaID, "friday:2026-10-30")
	if err != nil {
		t.Fatalf("ReleaseAlert Alpha: %v", err)
	}

	claimedBetaAfterAlphaRelease, err := s.ClaimAlert(t.Context(), guildBetaID, "friday:2026-10-30", sentAt.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("Beta ClaimAlert after Alpha ReleaseAlert: %v", err)
	}
	if claimedBetaAfterAlphaRelease {
		t.Fatal("Beta ClaimAlert after Alpha ReleaseAlert = true, want false (Beta claim must remain)")
	}

	reclaimedAlpha, err := s.ClaimAlert(t.Context(), guildAlphaID, "friday:2026-10-30", sentAt.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("Alpha ClaimAlert after ReleaseAlert: %v", err)
	}
	if !reclaimedAlpha {
		t.Fatal("Alpha ClaimAlert after ReleaseAlert = false, want true")
	}
}

func TestStoreValidationGuardsAndSnowflakeRedaction(t *testing.T) {
	if _, err := store.New(nil); err == nil {
		t.Fatal("store.New(nil) succeeded, want error")
	}

	validStore, err := store.New(shared.Pool())
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}

	nilPoolStore := &store.Store{}
	var nilStore *store.Store
	now := time.Date(2026, 10, 30, 20, 0, 0, 0, time.UTC)

	// GetGuildConfig guards.
	//nolint:staticcheck // testing nil context guard
	if _, err = nilStore.GetGuildConfig(nil, ""); err == nil {
		t.Fatal("nilStore.GetGuildConfig(nil, empty) succeeded, want joined error")
	}
	if _, err = nilPoolStore.GetGuildConfig(t.Context(), guildAlphaID); err == nil {
		t.Fatal("nilPoolStore.GetGuildConfig succeeded, want error")
	}
	if _, err = validStore.GetGuildConfig(t.Context(), ""); err == nil {
		t.Fatal("GetGuildConfig(empty) succeeded, want error")
	}

	// ApplyGuildConfigPatch guards.
	//nolint:staticcheck // testing nil context guard
	if _, err = nilStore.ApplyGuildConfigPatch(nil, "", store.GuildConfigPatch{}, now); err == nil {
		t.Fatal("nilStore.ApplyGuildConfigPatch(nil, empty, empty) succeeded, want joined error")
	}
	if _, err = nilPoolStore.ApplyGuildConfigPatch(t.Context(), guildAlphaID, store.GuildConfigPatch{SetFriendUserID: true}, now); err == nil {
		t.Fatal("nilPoolStore.ApplyGuildConfigPatch succeeded, want error")
	}
	if _, err = validStore.ApplyGuildConfigPatch(t.Context(), guildAlphaID, store.GuildConfigPatch{}, now); err == nil {
		t.Fatal("ApplyGuildConfigPatch with empty patch succeeded, want error")
	}

	// ListAlertGuildConfigs guards.
	//nolint:staticcheck // testing nil context guard
	if _, err = nilStore.ListAlertGuildConfigs(nil); err == nil {
		t.Fatal("nilStore.ListAlertGuildConfigs(nil) succeeded, want joined error")
	}
	if _, err = nilPoolStore.ListAlertGuildConfigs(t.Context()); err == nil {
		t.Fatal("nilPoolStore.ListAlertGuildConfigs succeeded, want error")
	}

	// ClaimAlert & ReleaseAlert guards.
	//nolint:staticcheck // testing nil context guard
	if _, err = validStore.ClaimAlert(nil, guildAlphaID, "key", now); err == nil {
		t.Fatal("ClaimAlert(nil, ...) succeeded, want error")
	}
	if _, err = nilStore.ClaimAlert(t.Context(), guildAlphaID, "key", now); err == nil {
		t.Fatal("nilStore.ClaimAlert succeeded, want error")
	}
	if _, err = nilPoolStore.ClaimAlert(t.Context(), guildAlphaID, "key", now); err == nil {
		t.Fatal("nilPoolStore.ClaimAlert succeeded, want error")
	}
	if _, err = validStore.ClaimAlert(t.Context(), "", "key", now); err == nil {
		t.Fatal("ClaimAlert with empty guildID succeeded, want error")
	}
	if _, err = validStore.ClaimAlert(t.Context(), guildAlphaID, "", now); err == nil {
		t.Fatal("ClaimAlert with empty key succeeded, want error")
	}
	//nolint:staticcheck // testing aggregated validation errors
	if _, err = nilStore.ClaimAlert(nil, "", "", now); err == nil {
		t.Fatal("nilStore.ClaimAlert(nil, empty, empty) succeeded, want joined error")
	}

	//nolint:staticcheck // testing nil context guard
	if err = validStore.ReleaseAlert(nil, guildAlphaID, "key"); err == nil {
		t.Fatal("ReleaseAlert(nil, ...) succeeded, want error")
	}
	if err = nilStore.ReleaseAlert(t.Context(), guildAlphaID, "key"); err == nil {
		t.Fatal("nilStore.ReleaseAlert succeeded, want error")
	}
	if err = nilPoolStore.ReleaseAlert(t.Context(), guildAlphaID, "key"); err == nil {
		t.Fatal("nilPoolStore.ReleaseAlert succeeded, want error")
	}
	if err = validStore.ReleaseAlert(t.Context(), "", "key"); err == nil {
		t.Fatal("ReleaseAlert with empty guildID succeeded, want error")
	}
	if err = validStore.ReleaseAlert(t.Context(), guildAlphaID, ""); err == nil {
		t.Fatal("ReleaseAlert with empty key succeeded, want error")
	}
	//nolint:staticcheck // testing aggregated validation errors
	if err = nilStore.ReleaseAlert(nil, "", ""); err == nil {
		t.Fatal("nilStore.ReleaseAlert(nil, empty, empty) succeeded, want joined error")
	}

	// Canceled context DB errors must never include guild, user, or channel snowflakes.
	canceledCtx, cancel := context.WithCancel(t.Context())
	cancel()

	var dbErrs []error
	if _, e := validStore.GetGuildConfig(canceledCtx, guildAlphaID); e == nil {
		t.Fatal("GetGuildConfig with canceled context succeeded")
	} else {
		dbErrs = append(dbErrs, e)
	}

	if _, e := validStore.ApplyGuildConfigPatch(
		canceledCtx,
		guildAlphaID,
		store.GuildConfigPatch{SetFriendUserID: true, FriendUserID: friendAlphaID, SetChannelID: true, ChannelID: chanAlphaID},
		now,
	); e == nil {
		t.Fatal("ApplyGuildConfigPatch upsert with canceled context succeeded")
	} else {
		dbErrs = append(dbErrs, e)
	}

	if _, e := validStore.ApplyGuildConfigPatch(
		canceledCtx,
		guildAlphaID,
		store.GuildConfigPatch{SetFriendUserID: true, SetChannelID: true, SetLocale: true},
		now,
	); e == nil {
		t.Fatal("ApplyGuildConfigPatch delete with canceled context succeeded")
	} else {
		dbErrs = append(dbErrs, e)
	}

	if _, e := validStore.ApplyGuildConfigPatch(
		canceledCtx,
		guildAlphaID,
		store.GuildConfigPatch{SetFriendUserID: true},
		now,
	); e == nil {
		t.Fatal("ApplyGuildConfigPatch single-clear with canceled context succeeded")
	} else {
		dbErrs = append(dbErrs, e)
	}

	if _, e := validStore.ListAlertGuildConfigs(canceledCtx); e == nil {
		t.Fatal("ListAlertGuildConfigs with canceled context succeeded")
	} else {
		dbErrs = append(dbErrs, e)
	}

	if _, e := validStore.ClaimAlert(canceledCtx, guildAlphaID, "canceled-key", now); e == nil {
		t.Fatal("ClaimAlert with canceled context succeeded")
	} else {
		dbErrs = append(dbErrs, e)
	}

	if e := validStore.ReleaseAlert(canceledCtx, guildAlphaID, "canceled-key"); e == nil {
		t.Fatal("ReleaseAlert with canceled context succeeded")
	} else {
		dbErrs = append(dbErrs, e)
	}

	for _, dbErr := range dbErrs {
		text := dbErr.Error()
		for _, snowflake := range []string{guildAlphaID, friendAlphaID, chanAlphaID} {
			if strings.Contains(text, snowflake) {
				t.Errorf("store error %q leaked snowflake %q", text, snowflake)
			}
		}
	}
}
