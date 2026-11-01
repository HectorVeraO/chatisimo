package capitime

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/capitime/internal/store"
)

const (
	minSnowflakeDigits = 17
	maxSnowflakeDigits = 20
)

// Service coordinates per-guild Capi Time configuration, two-stage arrival detection, randomized
// delay / easter-egg calculation, anti-spam debounce, and banter generation.
type Service struct {
	store    *store.Store
	clock    func() time.Time
	rand     io.Reader
	settings SettingReader
	llm      LLMGenerator
	banter   BanterGenerator

	debounceMu    sync.Mutex
	lastReplyTime map[string]time.Time
}

// New constructs a Capi Time Service from deps.
func New(deps Deps) (*Service, error) {
	if deps.Pool == nil {
		return nil, errors.New("capitime: database pool is nil")
	}

	st, err := store.New(deps.Pool)
	if err != nil {
		return nil, fmt.Errorf("capitime: create store: %w", err)
	}

	clk := deps.Clock
	if clk == nil {
		clk = time.Now
	}

	rnd := deps.Rand
	if rnd == nil {
		rnd = rand.Reader
	}

	banterGen := deps.Banter
	if banterGen == nil && deps.LLM != nil {
		banterGen = NewLLMBanterGenerator(LLMBanterConfig{Generator: deps.LLM})
	}

	svc := &Service{
		store:         st,
		clock:         clk,
		rand:          rnd,
		settings:      deps.Settings,
		llm:           deps.LLM,
		banter:        banterGen,
		lastReplyTime: make(map[string]time.Time),
	}
	return svc, nil
}

// GetGuildConfig returns the stored Capi Time configuration for guildID along with the active
// runtime debounce window.
func (s *Service) GetGuildConfig(ctx context.Context, guildID string) (GuildConfig, error) {
	if err := s.checkReady(ctx); err != nil {
		return GuildConfig{}, err
	}
	if err := validateSnowflake(guildID, "guild_id"); err != nil {
		return GuildConfig{}, err
	}

	row, err := s.store.GetGuildConfig(ctx, guildID)
	if err != nil {
		return GuildConfig{}, fmt.Errorf("capitime: get guild config: %w", err)
	}

	runtimeCfg := resolveRuntimeSettings(ctx, s.settings)
	cfg := rowToGuildConfig(row, runtimeCfg.debounceWindow)
	return cfg, nil
}

// UpdateGuildConfig applies a partial update to a guild's Capi Time configuration. The caller must
// hold ManageGuild or Administrator permission.
func (s *Service) UpdateGuildConfig(
	ctx context.Context,
	scope GuildScope,
	update GuildConfigUpdate,
) (GuildConfig, error) {
	if err := s.checkReady(ctx); err != nil {
		return GuildConfig{}, err
	}
	if err := validateGuildScope(scope); err != nil {
		return GuildConfig{}, err
	}

	var problems []error
	hasField := false
	var patch store.GuildConfigPatch

	if update.TargetUserID != nil {
		hasField = true
		if err := validateSnowflake(*update.TargetUserID, "target_user_id"); err != nil {
			problems = append(problems, err)
		} else {
			patch.SetTargetUserID = true
			patch.TargetUserID = *update.TargetUserID
		}
	}

	if update.ChannelID != nil {
		hasField = true
		if err := validateSnowflake(*update.ChannelID, "channel_id"); err != nil {
			problems = append(problems, err)
		} else {
			patch.AddChannelID = *update.ChannelID
		}
	}

	if update.Locale != nil {
		hasField = true
		if !isValidLocale(*update.Locale) {
			problems = append(problems, newInputError(ErrCodeLocale, "unsupported locale", nil))
		} else {
			patch.SetLocale = true
			patch.Locale = string(*update.Locale)
		}
	}

	if !hasField {
		problems = append(problems, newInputError(ErrCodeEmptyUpdate, "at least one field must be provided", nil))
	}
	if len(problems) > 0 {
		return GuildConfig{}, errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
	}

	if patch.AddChannelID != "" {
		existing, getErr := s.store.GetGuildConfig(ctx, scope.GuildID)
		if getErr != nil {
			return GuildConfig{}, fmt.Errorf("capitime: inspect guild channels: %w", getErr)
		}
		isNewChannel := !slices.Contains(existing.EnabledChannelIDs, patch.AddChannelID)
		if isNewChannel && len(existing.EnabledChannelIDs) >= MaxMonitoredChannels {
			return GuildConfig{}, newInputError(ErrCodeChannelCap, "maximum monitored channels reached", nil)
		}
	}

	now := s.clock().UTC()
	row, err := s.store.ApplyGuildConfigPatch(ctx, scope.GuildID, patch, now)
	if err != nil {
		return GuildConfig{}, fmt.Errorf("capitime: update guild config: %w", err)
	}

	runtimeCfg := resolveRuntimeSettings(ctx, s.settings)
	cfg := rowToGuildConfig(row, runtimeCfg.debounceWindow)
	return cfg, nil
}

// ClearGuildConfig clears the selected portion of a guild's Capi Time configuration. The caller
// must hold ManageGuild or Administrator permission.
func (s *Service) ClearGuildConfig(
	ctx context.Context,
	scope GuildScope,
	target ClearTarget,
) (GuildConfig, error) {
	if err := s.checkReady(ctx); err != nil {
		return GuildConfig{}, err
	}
	if err := validateGuildScope(scope); err != nil {
		return GuildConfig{}, err
	}

	var patch store.GuildConfigPatch
	switch target {
	case ClearTargetAll:
		patch = store.GuildConfigPatch{
			SetTargetUserID: true,
			TargetUserID:    "",
			ClearChannels:   true,
			SetLocale:       true,
			Locale:          "",
		}
	case ClearTargetUser:
		patch = store.GuildConfigPatch{
			SetTargetUserID: true,
			TargetUserID:    "",
		}
	case ClearTargetChannels:
		patch = store.GuildConfigPatch{
			ClearChannels: true,
		}
	case ClearTargetLocale:
		patch = store.GuildConfigPatch{
			SetLocale: true,
			Locale:    "",
		}
	default:
		return GuildConfig{}, newInputError(ErrCodeClearTarget, "unsupported clear target", nil)
	}

	now := s.clock().UTC()
	row, err := s.store.ApplyGuildConfigPatch(ctx, scope.GuildID, patch, now)
	if err != nil {
		return GuildConfig{}, fmt.Errorf("capitime: clear guild config: %w", err)
	}

	runtimeCfg := resolveRuntimeSettings(ctx, s.settings)
	cfg := rowToGuildConfig(row, runtimeCfg.debounceWindow)
	return cfg, nil
}

// HandleMessage evaluates an incoming guild message and returns a Capi Time Reply when the message
// is authored by the guild's configured target user in a monitored channel, expresses arrival
// intent, and falls outside the anti-spam debounce window.
func (s *Service) HandleMessage(ctx context.Context, msg MessageInput) (Reply, error) {
	if err := s.checkReady(ctx); err != nil {
		return Reply{}, err
	}
	if msg.AuthorBot || msg.GuildID == "" || msg.ChannelID == "" || msg.AuthorID == "" || msg.Content == "" {
		return Reply{}, nil
	}
	if !isValidSnowflake(msg.GuildID) ||
		!isValidSnowflake(msg.ChannelID) ||
		!isValidSnowflake(msg.AuthorID) {
		return Reply{}, nil
	}

	row, err := s.store.GetGuildConfig(ctx, msg.GuildID)
	if err != nil {
		return Reply{}, fmt.Errorf("capitime: load guild config for message: %w", err)
	}

	runtimeCfg := resolveRuntimeSettings(ctx, s.settings)
	cfg := rowToGuildConfig(row, runtimeCfg.debounceWindow)

	if cfg.TargetUserID == "" || cfg.TargetUserID != msg.AuthorID {
		return Reply{}, nil
	}
	if !cfg.MonitorsChannel(msg.ChannelID) {
		return Reply{}, nil
	}

	now := s.clock().In(Santiago())
	if s.isDebounced(msg.GuildID, msg.AuthorID, now, runtimeCfg.debounceWindow) {
		return Reply{}, nil
	}

	claim, decided := parseStage1(msg.Content, now, runtimeCfg.compiledRegex)
	if !decided && s.llm != nil {
		claim = classifyStage2WithLLM(ctx, s.llm, msg.Content, now)
	}
	if !claim.Detected {
		return Reply{}, nil
	}

	if !s.claimDebounceSlot(msg.GuildID, msg.AuthorID, now, runtimeCfg.debounceWindow) {
		return Reply{}, nil
	}

	pred := s.computePrediction(claim, runtimeCfg)
	loc := cfg.EffectiveLocale()
	banterText := s.buildBanter(ctx, loc, pred, runtimeCfg)
	content := formatReplyLine(loc, pred, banterText)

	reply := Reply{
		Handled:    true,
		GuildID:    msg.GuildID,
		ChannelID:  msg.ChannelID,
		ReplyToID:  msg.MessageID,
		TargetUser: cfg.TargetUserID,
		Locale:     loc,
		Prediction: pred,
		Banter:     banterText,
		Content:    content,
	}
	return reply, nil
}

func (s *Service) isDebounced(guildID, authorID string, now time.Time, window time.Duration) bool {
	if window <= 0 {
		return false
	}

	key := guildID + ":" + authorID
	s.debounceMu.Lock()
	defer s.debounceMu.Unlock()

	last, exists := s.lastReplyTime[key]
	if !exists {
		return false
	}

	elapsed := now.Sub(last)
	return elapsed >= 0 && elapsed < window
}

func (s *Service) claimDebounceSlot(guildID, authorID string, now time.Time, window time.Duration) bool {
	key := guildID + ":" + authorID
	s.debounceMu.Lock()
	defer s.debounceMu.Unlock()

	if window > 0 {
		if last, exists := s.lastReplyTime[key]; exists {
			elapsed := now.Sub(last)
			if elapsed >= 0 && elapsed < window {
				return false
			}
		}
	}

	s.lastReplyTime[key] = now
	return true
}

func (s *Service) computePrediction(claim ArrivalClaim, cfg runtimeSettings) Prediction {
	roll := s.randomInt(100)
	isEasterEgg := cfg.easterEggPercent > 0 && roll < cfg.easterEggPercent
	if isEasterEgg {
		return Prediction{
			ClaimedAt:    claim.ClaimedAt,
			PredictedAt:  claim.ClaimedAt,
			ClaimSummary: claim.ClaimSummary,
			DelayMinutes: 0,
			Immediate:    claim.Immediate,
			EasterEgg:    true,
		}
	}

	span := cfg.delayMaxMinutes - cfg.delayMinMinutes + 1
	if span < 1 {
		span = 1
	}
	delayMins := cfg.delayMinMinutes + s.randomInt(span)
	predictedAt := claim.ClaimedAt.Add(time.Duration(delayMins) * time.Minute)

	return Prediction{
		ClaimedAt:    claim.ClaimedAt,
		PredictedAt:  predictedAt,
		ClaimSummary: claim.ClaimSummary,
		DelayMinutes: delayMins,
		Immediate:    claim.Immediate,
		EasterEgg:    false,
	}
}

func (s *Service) buildBanter(ctx context.Context, loc Locale, pred Prediction, cfg runtimeSettings) string {
	locSantiago := Santiago()
	claimedClock := pred.ClaimedAt.In(locSantiago).Format("15:04")
	capiClock := pred.PredictedAt.In(locSantiago).Format("15:04")

	prompt := cfg.banterPrompt
	if pred.EasterEgg {
		prompt = cfg.easterEggPrompt
	}

	input := BanterInput{
		Locale:       loc,
		ClaimSummary: pred.ClaimSummary,
		ClaimedClock: claimedClock,
		CapiClock:    capiClock,
		DelayMinutes: pred.DelayMinutes,
		Immediate:    pred.Immediate,
		EasterEgg:    pred.EasterEgg,
		Prompt:       prompt,
	}

	if s.banter != nil {
		generated, err := s.banter.GenerateBanter(ctx, input)
		if err == nil {
			if cleaned, ok := sanitizeBanterOutput(generated); ok {
				return cleaned
			}
		}
	}

	variant := s.randomInt(16)
	return renderFallbackBanter(loc, input, variant)
}

func (s *Service) randomInt(n int) int {
	if n <= 1 {
		return 0
	}

	var buf [8]byte
	if _, err := io.ReadFull(s.rand, buf[:]); err != nil {
		return 0
	}

	val := binary.LittleEndian.Uint64(buf[:])
	//nolint:gosec // n is bounded by caller (<= 1440)
	return int(val % uint64(n))
}

func (s *Service) checkReady(ctx context.Context) error {
	var problems []error
	if ctx == nil {
		problems = append(problems, errors.New("capitime: context is nil"))
	}
	if s == nil || s.store == nil {
		problems = append(problems, errors.New("capitime: service is not initialized"))
	}

	return errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
}

func validateGuildScope(scope GuildScope) error {
	if err := validateSnowflake(scope.GuildID, "guild_id"); err != nil {
		return err
	}
	if !scope.CanManageGuild {
		return ErrForbidden
	}

	return nil
}

func validateSnowflake(raw, field string) error {
	if !isValidSnowflake(raw) {
		return newInputError(ErrCodeSnowflake, fmt.Sprintf("invalid %s snowflake", field), nil)
	}

	return nil
}

func isValidSnowflake(raw string) bool {
	if len(raw) < minSnowflakeDigits || len(raw) > maxSnowflakeDigits {
		return false
	}
	if raw[0] < '1' || raw[0] > '9' {
		return false
	}
	for i := 1; i < len(raw); i++ {
		if raw[i] < '0' || raw[i] > '9' {
			return false
		}
	}

	return true
}

func isValidLocale(loc Locale) bool {
	return loc == LocaleESCL || loc == LocaleENUS
}

func rowToGuildConfig(row store.GuildConfigRow, debounce time.Duration) GuildConfig {
	var loc Locale
	if row.Locale == string(LocaleESCL) || row.Locale == string(LocaleENUS) {
		loc = Locale(row.Locale)
	}

	channels := row.EnabledChannelIDs
	if channels == nil {
		channels = []string{}
	}

	return GuildConfig{
		GuildID:           row.GuildID,
		TargetUserID:      row.TargetUserID,
		EnabledChannelIDs: channels,
		Locale:            loc,
		DebounceWindow:    debounce,
		UpdatedAt:         row.UpdatedAt,
	}
}
