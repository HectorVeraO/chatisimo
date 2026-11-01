package capitime_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/HectorVeraO/chatisimo/internal/capitime"
	"github.com/HectorVeraO/chatisimo/internal/platform/config"
	"github.com/HectorVeraO/chatisimo/internal/platform/db"
	"github.com/HectorVeraO/chatisimo/internal/platform/pgtest"
)

const (
	closeDeadline = 30 * time.Second
	guildOneID    = "200000000000000001"
	guildTwoID    = "200000000000000002"
	capiUserID    = "100000000000000001"
	otherUserID   = "100000000000000002"
	chanGeneralID = "300000000000000001"
	chanGamingID  = "300000000000000002"
	chanOfftopic  = "300000000000000003"
	msgOneID      = "400000000000000001"
	msgTwoID      = "400000000000000002"
	msgThreeID    = "400000000000000003"
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

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(initial time.Time) *fakeClock {
	return &fakeClock{now: initial}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)
}

type fakeSettings struct {
	mu     sync.RWMutex
	values map[string]string
}

func newFakeSettings(initial map[string]string) *fakeSettings {
	cloned := make(map[string]string, len(initial))
	for k, v := range initial {
		cloned[k] = v
	}

	return &fakeSettings{values: cloned}
}

func (s *fakeSettings) Get(_ context.Context, name string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	val, ok := s.values[name]
	if !ok {
		return "", config.ErrNotSet
	}

	return val, nil
}

func (s *fakeSettings) Set(name, val string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.values[name] = val
}

type fakeLLM struct {
	mu        sync.Mutex
	responses []string
	err       error
	calls     int
}

func (l *fakeLLM) GenerateWithPrompt(_ context.Context, _, _ string) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.calls++
	if l.err != nil {
		return "", l.err
	}
	if len(l.responses) == 0 {
		return "", errors.New("no response queued")
	}

	out := l.responses[0]
	if len(l.responses) > 1 {
		l.responses = l.responses[1:]
	}

	return out, nil
}

func uint64Stream(values ...uint64) *bytes.Reader {
	buf := make([]byte, 0, len(values)*8)
	for _, v := range values {
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], v)
		buf = append(buf, b[:]...)
	}

	return bytes.NewReader(buf)
}

func resetGuildConfigs(t *testing.T) {
	t.Helper()

	_, err := shared.Pool().Exec(t.Context(), "DELETE FROM capitime.guild_config")
	if err != nil {
		t.Fatalf("reset capitime.guild_config: %v", err)
	}
}

func TestGuildConfigLifecycleAuthorizationAndChannelFiltering(t *testing.T) {
	resetGuildConfigs(t)

	clk := newFakeClock(time.Date(2026, 11, 1, 21, 0, 0, 0, capitime.Santiago()))
	svc, err := capitime.New(capitime.Deps{
		Pool:  shared.Pool(),
		Clock: clk.Now,
	})
	if err != nil {
		t.Fatalf("capitime.New: %v", err)
	}

	// Non-manager cannot update or clear config.
	nonManager := capitime.GuildScope{GuildID: guildOneID, CanManageGuild: false}
	target := capiUserID
	if _, err = svc.UpdateGuildConfig(t.Context(), nonManager, capitime.GuildConfigUpdate{TargetUserID: &target}); !errors.Is(err, capitime.ErrForbidden) {
		t.Fatalf("UpdateGuildConfig by non-manager = %v, want ErrForbidden", err)
	}
	if _, err = svc.ClearGuildConfig(t.Context(), nonManager, capitime.ClearTargetAll); !errors.Is(err, capitime.ErrForbidden) {
		t.Fatalf("ClearGuildConfig by non-manager = %v, want ErrForbidden", err)
	}

	manager := capitime.GuildScope{GuildID: guildOneID, CanManageGuild: true}

	// Empty update fails with ErrCodeEmptyUpdate.
	if _, err = svc.UpdateGuildConfig(t.Context(), manager, capitime.GuildConfigUpdate{}); !errors.Is(err, capitime.ErrInvalidInput) {
		t.Fatalf("empty UpdateGuildConfig = %v, want ErrInvalidInput", err)
	}

	// Configure target user and two channels.
	ch1 := chanGeneralID
	ch2 := chanGamingID
	locEN := capitime.LocaleENUS
	cfg, err := svc.UpdateGuildConfig(t.Context(), manager, capitime.GuildConfigUpdate{
		TargetUserID: &target,
		ChannelID:    &ch1,
		Locale:       &locEN,
	})
	if err != nil {
		t.Fatalf("UpdateGuildConfig first: %v", err)
	}
	if !cfg.Configured() || cfg.TargetUserID != capiUserID || cfg.EffectiveLocale() != capitime.LocaleENUS {
		t.Fatalf("cfg after first update = %+v", cfg)
	}

	cfg, err = svc.UpdateGuildConfig(t.Context(), manager, capitime.GuildConfigUpdate{
		ChannelID: &ch2,
	})
	if err != nil {
		t.Fatalf("UpdateGuildConfig second channel: %v", err)
	}
	if !slices.Equal(cfg.EnabledChannelIDs, []string{chanGeneralID, chanGamingID}) {
		t.Fatalf("EnabledChannelIDs = %v, want [%s %s]", cfg.EnabledChannelIDs, chanGeneralID, chanGamingID)
	}
	if !cfg.MonitorsChannel(chanGeneralID) || !cfg.MonitorsChannel(chanGamingID) || cfg.MonitorsChannel(chanOfftopic) {
		t.Errorf("MonitorsChannel mismatch on restricted guild: %+v", cfg)
	}

	gotCfg, err := svc.GetGuildConfig(t.Context(), guildOneID)
	if err != nil {
		t.Fatalf("GetGuildConfig: %v", err)
	}
	if gotCfg.TargetUserID != capiUserID || !slices.Equal(gotCfg.EnabledChannelIDs, []string{chanGeneralID, chanGamingID}) {
		t.Fatalf("GetGuildConfig = %+v", gotCfg)
	}
	if _, err = svc.GetGuildConfig(t.Context(), "bad-snowflake"); !errors.Is(err, capitime.ErrInvalidInput) {
		t.Errorf("GetGuildConfig invalid snowflake = %v, want ErrInvalidInput", err)
	}

	// Clear channel restrictions -> all channels become monitored while target user remains set.
	cfg, err = svc.ClearGuildConfig(t.Context(), manager, capitime.ClearTargetChannels)
	if err != nil {
		t.Fatalf("ClearGuildConfig channels: %v", err)
	}
	if len(cfg.EnabledChannelIDs) != 0 || !cfg.MonitorsChannel(chanOfftopic) {
		t.Fatalf("after ClearTargetChannels: %+v, want unconstrained channels", cfg)
	}

	// Clear locale -> reverts EffectiveLocale to es-CL.
	cfg, err = svc.ClearGuildConfig(t.Context(), manager, capitime.ClearTargetLocale)
	if err != nil {
		t.Fatalf("ClearGuildConfig locale: %v", err)
	}
	if cfg.EffectiveLocale() != capitime.LocaleESCL {
		t.Fatalf("EffectiveLocale after clear = %q, want es-CL", cfg.EffectiveLocale())
	}

	// Clear target user only.
	cfg, err = svc.ClearGuildConfig(t.Context(), manager, capitime.ClearTargetUser)
	if err != nil {
		t.Fatalf("ClearGuildConfig user: %v", err)
	}
	if cfg.TargetUserID != "" {
		t.Fatalf("TargetUserID after ClearTargetUser = %q, want empty", cfg.TargetUserID)
	}

	// Invalid clear target.
	if _, err = svc.ClearGuildConfig(t.Context(), manager, "invalid"); !errors.Is(err, capitime.ErrInvalidInput) {
		t.Errorf("ClearGuildConfig invalid target = %v, want ErrInvalidInput", err)
	}

	// Clear all -> guild becomes unconfigured.
	cfg, err = svc.ClearGuildConfig(t.Context(), manager, capitime.ClearTargetAll)
	if err != nil {
		t.Fatalf("ClearGuildConfig all: %v", err)
	}
	if cfg.Configured() || cfg.MonitorsChannel(chanGeneralID) {
		t.Fatalf("cfg after ClearTargetAll = %+v, want unconfigured", cfg)
	}
}

func TestHandleMessageStandardDelayEasterEggDebounceAndSilence(t *testing.T) {
	resetGuildConfigs(t)

	loc := capitime.Santiago()
	t0 := time.Date(2026, 11, 1, 22, 0, 0, 0, loc)
	clk := newFakeClock(t0)
	settings := newFakeSettings(map[string]string{
		capitime.SettingEasterEggPercent: "5",
		capitime.SettingDelayMinMinutes:  "45",
		capitime.SettingDelayMaxMinutes:  "180",
		capitime.SettingDebounceWindow:   "2m",
	})

	// Roll 1: roll=50 (>= 5, standard delay), delay offset=15 -> 45+15=60 mins, variant=0
	// Roll 2: roll=2 (< 5, easter egg!), variant=1
	rnd := uint64Stream(50, 15, 0, 2, 1)

	svc, err := capitime.New(capitime.Deps{
		Pool:     shared.Pool(),
		Clock:    clk.Now,
		Rand:     rnd,
		Settings: settings,
	})
	if err != nil {
		t.Fatalf("capitime.New: %v", err)
	}

	manager := capitime.GuildScope{GuildID: guildOneID, CanManageGuild: true}
	target := capiUserID
	ch := chanGeneralID
	if _, err = svc.UpdateGuildConfig(t.Context(), manager, capitime.GuildConfigUpdate{
		TargetUserID: &target,
		ChannelID:    &ch,
	}); err != nil {
		t.Fatalf("UpdateGuildConfig: %v", err)
	}

	// 1. Message from another user in monitored channel -> silent.
	repOther, err := svc.HandleMessage(t.Context(), capitime.MessageInput{
		GuildID:   guildOneID,
		ChannelID: chanGeneralID,
		MessageID: msgOneID,
		AuthorID:  otherUserID,
		Content:   "22:30 llego",
	})
	if err != nil || repOther.Handled {
		t.Fatalf("other user message: rep=%+v err=%v, want silent", repOther, err)
	}

	// 2. Message from target user in unmonitored channel -> silent.
	repWrongChan, err := svc.HandleMessage(t.Context(), capitime.MessageInput{
		GuildID:   guildOneID,
		ChannelID: chanOfftopic,
		MessageID: msgOneID,
		AuthorID:  capiUserID,
		Content:   "22:30 llego",
	})
	if err != nil || repWrongChan.Handled {
		t.Fatalf("unmonitored channel message: rep=%+v err=%v, want silent", repWrongChan, err)
	}

	// 3. Message from bot -> silent.
	repBot, err := svc.HandleMessage(t.Context(), capitime.MessageInput{
		GuildID:   guildOneID,
		ChannelID: chanGeneralID,
		MessageID: msgOneID,
		AuthorID:  capiUserID,
		AuthorBot: true,
		Content:   "voy altiro",
	})
	if err != nil || repBot.Handled {
		t.Fatalf("bot message: rep=%+v err=%v, want silent", repBot, err)
	}

	// 4. Non-arrival message from target user does NOT trigger reply and does NOT consume debounce.
	repChat, err := svc.HandleMessage(t.Context(), capitime.MessageInput{
		GuildID:   guildOneID,
		ChannelID: chanGeneralID,
		MessageID: msgOneID,
		AuthorID:  capiUserID,
		Content:   "buena cabros como estan",
	})
	if err != nil || repChat.Handled {
		t.Fatalf("non-arrival message: rep=%+v err=%v, want silent", repChat, err)
	}

	// 5. Arrival message "22:30 llego" triggers standard Capi Time (+60 min -> 23:30).
	rep1, err := svc.HandleMessage(t.Context(), capitime.MessageInput{
		GuildID:   guildOneID,
		ChannelID: chanGeneralID,
		MessageID: msgOneID,
		AuthorID:  capiUserID,
		Content:   "22:30 llego",
	})
	if err != nil {
		t.Fatalf("HandleMessage 1: %v", err)
	}
	if !rep1.Handled || rep1.Prediction.EasterEgg || rep1.Prediction.DelayMinutes != 60 {
		t.Fatalf("rep1 = %+v, want Handled=true EasterEgg=false DelayMinutes=60", rep1)
	}
	wantPred1 := time.Date(2026, 11, 1, 23, 30, 0, 0, loc)
	if !rep1.Prediction.PredictedAt.Equal(wantPred1) {
		t.Errorf("rep1 PredictedAt = %v, want %v", rep1.Prediction.PredictedAt, wantPred1)
	}
	wantUnix1 := fmt.Sprintf("<t:%d:t>", wantPred1.Unix())
	wantRel1 := fmt.Sprintf("<t:%d:R>", wantPred1.Unix())
	if !strings.Contains(rep1.Content, wantUnix1) || !strings.Contains(rep1.Content, wantRel1) {
		t.Errorf("rep1.Content %q missing %s or %s", rep1.Content, wantUnix1, wantRel1)
	}
	if strings.Contains(rep1.Content, "\n") {
		t.Errorf("rep1.Content %q is not single-line", rep1.Content)
	}

	// 6. Second arrival message 30 seconds later (inside 2m debounce window) is suppressed.
	clk.Advance(30 * time.Second)
	repDebounced, err := svc.HandleMessage(t.Context(), capitime.MessageInput{
		GuildID:   guildOneID,
		ChannelID: chanGeneralID,
		MessageID: msgTwoID,
		AuthorID:  capiUserID,
		Content:   "voy altiro",
	})
	if err != nil || repDebounced.Handled {
		t.Fatalf("debounced message: rep=%+v err=%v, want silent", repDebounced, err)
	}

	// 7. Advance past 2m debounce window -> next message triggers the 5% Easter Egg (roll=2 < 5).
	clk.Advance(2 * time.Minute)
	nowAfterAdvance := clk.Now().In(loc)
	repEasterEgg, err := svc.HandleMessage(t.Context(), capitime.MessageInput{
		GuildID:   guildOneID,
		ChannelID: chanGeneralID,
		MessageID: msgThreeID,
		AuthorID:  capiUserID,
		Content:   "voy altiro",
	})
	if err != nil {
		t.Fatalf("HandleMessage easter egg: %v", err)
	}
	if !repEasterEgg.Handled || !repEasterEgg.Prediction.EasterEgg || repEasterEgg.Prediction.DelayMinutes != 0 {
		t.Fatalf("repEasterEgg = %+v, want EasterEgg=true DelayMinutes=0", repEasterEgg)
	}
	if !repEasterEgg.Prediction.PredictedAt.Equal(nowAfterAdvance) {
		t.Errorf("repEasterEgg PredictedAt = %v, want %v", repEasterEgg.Prediction.PredictedAt, nowAfterAdvance)
	}

	// 8. Switch guild locale to en-US and verify English standard + Easter-egg fallback templates.
	locEN := capitime.LocaleENUS
	if _, err = svc.UpdateGuildConfig(t.Context(), manager, capitime.GuildConfigUpdate{Locale: &locEN}); err != nil {
		t.Fatalf("UpdateGuildConfig en-US: %v", err)
	}
	settings.Set(capitime.SettingDebounceWindow, "0s")
	settings.Set(capitime.SettingBanterPrompt, "Custom banter prompt")
	settings.Set(capitime.SettingEasterEggPrompt, "Custom easter egg prompt")
	settings.Set(capitime.SettingRegexPattern, capitime.DefaultRegexPattern)

	repEN, err := svc.HandleMessage(t.Context(), capitime.MessageInput{
		GuildID:   guildOneID,
		ChannelID: chanGeneralID,
		MessageID: msgThreeID,
		AuthorID:  capiUserID,
		Content:   "23:15 llego",
	})
	if err != nil || !repEN.Handled || !strings.Contains(repEN.Content, "Capi Time") {
		t.Fatalf("repEN = %+v, err=%v", repEN, err)
	}

	settings.Set(capitime.SettingEasterEggPercent, "100")
	repENEaster, err := svc.HandleMessage(t.Context(), capitime.MessageInput{
		GuildID:   guildOneID,
		ChannelID: chanGeneralID,
		MessageID: msgThreeID,
		AuthorID:  capiUserID,
		Content:   "23:15 llego",
	})
	if err != nil || !repENEaster.Handled || !strings.Contains(repENEaster.Content, "Capi Time Miracle") {
		t.Fatalf("repENEaster = %+v, err=%v", repENEaster, err)
	}
}

func TestStage2LLMDisambiguationAndDynamicBanterWithFallback(t *testing.T) {
	resetGuildConfigs(t)

	loc := capitime.Santiago()
	t0 := time.Date(2026, 11, 1, 21, 0, 0, 0, loc)
	clk := newFakeClock(t0)
	settings := newFakeSettings(map[string]string{
		capitime.SettingDebounceWindow:   "0s",
		capitime.SettingEasterEggPercent: "0",
		capitime.SettingDelayMinMinutes:  "90",
		capitime.SettingDelayMaxMinutes:  "90",
	})

	llmFake := &fakeLLM{
		responses: []string{
			`{"arrival":true,"immediate":false,"offset_minutes":20,"clock":""}`,
			"Dice que en 20 min mi king, pero con Capi Time sumenle 90 min mis chanchitos mientras prende el PC.",
		},
	}

	svc, err := capitime.New(capitime.Deps{
		Pool:     shared.Pool(),
		Clock:    clk.Now,
		Settings: settings,
		LLM:      llmFake,
	})
	if err != nil {
		t.Fatalf("capitime.New: %v", err)
	}

	manager := capitime.GuildScope{GuildID: guildTwoID, CanManageGuild: true}
	target := capiUserID
	if _, err = svc.UpdateGuildConfig(t.Context(), manager, capitime.GuildConfigUpdate{
		TargetUserID: &target,
	}); err != nil {
		t.Fatalf("UpdateGuildConfig: %v", err)
	}

	// Phrasing that bypasses Stage 1 regex tokens and is classified by Stage 2 LLM.
	rep, err := svc.HandleMessage(t.Context(), capitime.MessageInput{
		GuildID:   guildTwoID,
		ChannelID: chanGeneralID,
		MessageID: msgOneID,
		AuthorID:  capiUserID,
		Content:   "terminando de comer y me sumo al discord",
	})
	if err != nil {
		t.Fatalf("HandleMessage Stage 2: %v", err)
	}
	if !rep.Handled || rep.Prediction.DelayMinutes != 90 {
		t.Fatalf("rep = %+v, want Handled=true with 90m delay", rep)
	}
	if !strings.Contains(rep.Content, "mis chanchitos") {
		t.Errorf("rep.Content %q did not use dynamic LLM banter", rep.Content)
	}

	// If LLM returns Argentine voseo or fails, service falls back to static Chilean template.
	llmFake.responses = []string{"Vos tenes que esperar sentado pibe"}
	repFallback, err := svc.HandleMessage(t.Context(), capitime.MessageInput{
		GuildID:   guildTwoID,
		ChannelID: chanGeneralID,
		MessageID: msgTwoID,
		AuthorID:  capiUserID,
		Content:   "voy altiro",
	})
	if err != nil || !repFallback.Handled {
		t.Fatalf("repFallback = %+v, err=%v", repFallback, err)
	}
	if strings.Contains(strings.ToLower(repFallback.Content), "tenes") {
		t.Errorf("repFallback.Content %q leaked voseo output", repFallback.Content)
	}
}

func TestValidateSettingsReportsInvalidKeysWithoutEchoingValues(t *testing.T) {
	const probeMarker = "bad-value-marker"

	bad := newFakeSettings(map[string]string{
		capitime.SettingBanterPrompt:     probeMarker + "\x00bad",
		capitime.SettingEasterEggPrompt:  "   ",
		capitime.SettingEasterEggPercent: probeMarker,
		capitime.SettingDelayMinMinutes:  "200",
		capitime.SettingDelayMaxMinutes:  "100",
		capitime.SettingDebounceWindow:   probeMarker,
		capitime.SettingRegexPattern:     "[" + probeMarker,
	})

	err := capitime.ValidateSettings(t.Context(), bad)
	if !errors.Is(err, capitime.ErrInvalidInput) {
		t.Fatalf("ValidateSettings = %v, want ErrInvalidInput", err)
	}

	msg := err.Error()
	for _, key := range []string{
		capitime.SettingBanterPrompt,
		capitime.SettingEasterEggPrompt,
		capitime.SettingEasterEggPercent,
		capitime.SettingDelayMinMinutes,
		capitime.SettingDebounceWindow,
		capitime.SettingRegexPattern,
	} {
		if !strings.Contains(msg, key) {
			t.Errorf("error %q missing key %q", msg, key)
		}
	}
	if strings.Contains(msg, probeMarker) {
		t.Errorf("error %q leaked value marker %q", msg, probeMarker)
	}
}
