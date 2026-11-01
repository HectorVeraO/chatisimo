package store_test

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/HectorVeraO/chatisimo/internal/capitime"
	"github.com/HectorVeraO/chatisimo/internal/capitime/internal/store"
	"github.com/HectorVeraO/chatisimo/internal/platform/db"
	"github.com/HectorVeraO/chatisimo/internal/platform/pgtest"
)

const (
	closeDeadline = 30 * time.Second
	guildAlphaID  = "200000000000000001"
	guildBetaID   = "200000000000000002"
	targetAlphaID = "100000000000000001"
	targetBetaID  = "100000000000000002"
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

	capiSource, err := capitime.MigrationSource()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	database, err := pgtest.New(ctx, pgtest.Options{Sources: []db.Source{capiSource}})
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

	entry, openErr := files.Open("0001_create_capitime_schema.sql")
	if openErr != nil {
		t.Fatalf("open 0001_create_capitime_schema.sql: %v", openErr)
	}

	closeErr := entry.Close()
	if closeErr != nil {
		t.Fatalf("close 0001_create_capitime_schema.sql: %v", closeErr)
	}
}

func TestGuildConfigCRUDChannelDeduplicationAndPartialClear(t *testing.T) {
	s, err := store.New(shared.Pool())
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}

	_, cleanupErr := shared.Pool().Exec(t.Context(), "DELETE FROM capitime.guild_config")
	if cleanupErr != nil {
		t.Fatalf("reset guild_config: %v", cleanupErr)
	}

	t0 := time.Date(2026, 11, 1, 15, 0, 0, 0, time.UTC)

	initial, err := s.GetGuildConfig(t.Context(), guildAlphaID)
	if err != nil {
		t.Fatalf("GetGuildConfig unconfigured: %v", err)
	}
	if initial.GuildID != guildAlphaID || initial.TargetUserID != "" || len(initial.EnabledChannelIDs) != 0 || initial.Locale != "" {
		t.Fatalf("initial = %+v, want empty config", initial)
	}

	// Clearing a single field on an unconfigured guild is a no-op and inserts no row.
	clearedUnset, err := s.ApplyGuildConfigPatch(
		t.Context(),
		guildAlphaID,
		store.GuildConfigPatch{SetTargetUserID: true, TargetUserID: ""},
		t0,
	)
	if err != nil {
		t.Fatalf("clear unset target: %v", err)
	}
	if clearedUnset.TargetUserID != "" || len(clearedUnset.EnabledChannelIDs) != 0 {
		t.Fatalf("clearedUnset = %+v, want empty", clearedUnset)
	}

	// Set target user and first channel on Alpha.
	withTargetAndChan1, err := s.ApplyGuildConfigPatch(
		t.Context(),
		guildAlphaID,
		store.GuildConfigPatch{
			SetTargetUserID: true,
			TargetUserID:    targetAlphaID,
			AddChannelID:    chanAlphaID,
		},
		t0,
	)
	if err != nil {
		t.Fatalf("set target + chan1: %v", err)
	}
	if withTargetAndChan1.TargetUserID != targetAlphaID || !slices.Equal(withTargetAndChan1.EnabledChannelIDs, []string{chanAlphaID}) {
		t.Fatalf("withTargetAndChan1 = %+v, want target=%s channels=[%s]", withTargetAndChan1, targetAlphaID, chanAlphaID)
	}

	// Re-adding the same channel is idempotent; adding a second channel appends in order.
	withDupChan, err := s.ApplyGuildConfigPatch(
		t.Context(),
		guildAlphaID,
		store.GuildConfigPatch{AddChannelID: chanAlphaID},
		t0.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("re-add chan1: %v", err)
	}
	if !slices.Equal(withDupChan.EnabledChannelIDs, []string{chanAlphaID}) {
		t.Fatalf("withDupChan channels = %v, want [%s]", withDupChan.EnabledChannelIDs, chanAlphaID)
	}

	withTwoChans, err := s.ApplyGuildConfigPatch(
		t.Context(),
		guildAlphaID,
		store.GuildConfigPatch{
			AddChannelID: chanBetaID,
			SetLocale:    true,
			Locale:       string(capitime.LocaleENUS),
		},
		t0.Add(2*time.Minute),
	)
	if err != nil {
		t.Fatalf("add chan2 + locale: %v", err)
	}
	if !slices.Equal(withTwoChans.EnabledChannelIDs, []string{chanAlphaID, chanBetaID}) || withTwoChans.Locale != string(capitime.LocaleENUS) {
		t.Fatalf("withTwoChans = %+v, want two channels and en-US", withTwoChans)
	}

	// Configure Beta independently.
	_, err = s.ApplyGuildConfigPatch(
		t.Context(),
		guildBetaID,
		store.GuildConfigPatch{SetTargetUserID: true, TargetUserID: targetBetaID},
		t0.Add(3*time.Minute),
	)
	if err != nil {
		t.Fatalf("set Beta: %v", err)
	}

	// Clear channels only on Alpha -> preserves target and locale.
	afterChanClear, err := s.ApplyGuildConfigPatch(
		t.Context(),
		guildAlphaID,
		store.GuildConfigPatch{ClearChannels: true},
		t0.Add(4*time.Minute),
	)
	if err != nil {
		t.Fatalf("clear channels on Alpha: %v", err)
	}
	if len(afterChanClear.EnabledChannelIDs) != 0 || afterChanClear.TargetUserID != targetAlphaID || afterChanClear.Locale != string(capitime.LocaleENUS) {
		t.Fatalf("afterChanClear = %+v, want channels=[] target=%s locale=en-US", afterChanClear, targetAlphaID)
	}

	// Clear target only on Alpha -> preserves locale.
	afterTargetClear, err := s.ApplyGuildConfigPatch(
		t.Context(),
		guildAlphaID,
		store.GuildConfigPatch{SetTargetUserID: true, TargetUserID: ""},
		t0.Add(5*time.Minute),
	)
	if err != nil {
		t.Fatalf("clear target on Alpha: %v", err)
	}
	if afterTargetClear.TargetUserID != "" || afterTargetClear.Locale != string(capitime.LocaleENUS) {
		t.Fatalf("afterTargetClear = %+v, want target='' locale=en-US", afterTargetClear)
	}

	// Clear locale on Alpha -> all fields now empty, so row is pruned.
	afterLocaleClear, err := s.ApplyGuildConfigPatch(
		t.Context(),
		guildAlphaID,
		store.GuildConfigPatch{SetLocale: true, Locale: ""},
		t0.Add(6*time.Minute),
	)
	if err != nil {
		t.Fatalf("clear locale on Alpha: %v", err)
	}
	if afterLocaleClear.TargetUserID != "" || afterLocaleClear.Locale != "" || len(afterLocaleClear.EnabledChannelIDs) != 0 {
		t.Fatalf("afterLocaleClear = %+v, want empty", afterLocaleClear)
	}

	var alphaRows int
	err = shared.Pool().QueryRow(
		t.Context(),
		"SELECT count(*) FROM capitime.guild_config WHERE guild_id = $1",
		guildAlphaID,
	).Scan(&alphaRows)
	if err != nil {
		t.Fatalf("count Alpha rows: %v", err)
	}
	if alphaRows != 0 {
		t.Errorf("Alpha row count = %d, want 0", alphaRows)
	}

	// Clear all on Beta -> deletes Beta's row.
	_, err = s.ApplyGuildConfigPatch(
		t.Context(),
		guildBetaID,
		store.GuildConfigPatch{
			SetTargetUserID: true,
			ClearChannels:   true,
			SetLocale:       true,
		},
		t0.Add(7*time.Minute),
	)
	if err != nil {
		t.Fatalf("clear all on Beta: %v", err)
	}

	betaAfter, err := s.GetGuildConfig(t.Context(), guildBetaID)
	if err != nil {
		t.Fatalf("GetGuildConfig Beta: %v", err)
	}
	if betaAfter.TargetUserID != "" {
		t.Errorf("Beta TargetUserID = %q, want empty", betaAfter.TargetUserID)
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
	now := time.Date(2026, 11, 1, 15, 0, 0, 0, time.UTC)

	//nolint:staticcheck // testing nil context guard
	if _, err = nilStore.GetGuildConfig(nil, ""); err == nil {
		t.Fatal("nilStore.GetGuildConfig(nil, empty) succeeded, want error")
	}
	if _, err = nilPoolStore.GetGuildConfig(t.Context(), guildAlphaID); err == nil {
		t.Fatal("nilPoolStore.GetGuildConfig succeeded, want error")
	}
	if _, err = validStore.GetGuildConfig(t.Context(), ""); err == nil {
		t.Fatal("GetGuildConfig(empty) succeeded, want error")
	}

	//nolint:staticcheck // testing nil context guard
	if _, err = nilStore.ApplyGuildConfigPatch(nil, "", store.GuildConfigPatch{}, now); err == nil {
		t.Fatal("nilStore.ApplyGuildConfigPatch(nil, empty, empty) succeeded, want error")
	}
	if _, err = nilPoolStore.ApplyGuildConfigPatch(t.Context(), guildAlphaID, store.GuildConfigPatch{SetTargetUserID: true}, now); err == nil {
		t.Fatal("nilPoolStore.ApplyGuildConfigPatch succeeded, want error")
	}
	if _, err = validStore.ApplyGuildConfigPatch(t.Context(), guildAlphaID, store.GuildConfigPatch{}, now); err == nil {
		t.Fatal("ApplyGuildConfigPatch(empty patch) succeeded, want error")
	}

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
		store.GuildConfigPatch{SetTargetUserID: true, TargetUserID: targetAlphaID, AddChannelID: chanAlphaID},
		now,
	); e == nil {
		t.Fatal("ApplyGuildConfigPatch upsert with canceled context succeeded")
	} else {
		dbErrs = append(dbErrs, e)
	}

	if _, e := validStore.ApplyGuildConfigPatch(
		canceledCtx,
		guildAlphaID,
		store.GuildConfigPatch{SetTargetUserID: true, ClearChannels: true, SetLocale: true},
		now,
	); e == nil {
		t.Fatal("ApplyGuildConfigPatch clear-all with canceled context succeeded")
	} else {
		dbErrs = append(dbErrs, e)
	}

	for _, dbErr := range dbErrs {
		text := dbErr.Error()
		for _, snowflake := range []string{guildAlphaID, targetAlphaID, chanAlphaID} {
			if strings.Contains(text, snowflake) {
				t.Errorf("store error %q leaked snowflake %q", text, snowflake)
			}
		}
	}
}
