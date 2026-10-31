package uccalendar

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/singleflight"

	"github.com/HectorVeraO/chatisimo/internal/uccalendar/internal/espn"
	"github.com/HectorVeraO/chatisimo/internal/uccalendar/internal/store"
)

const releaseAlertTimeout = 5 * time.Second

// Deps configures the collaborators and optional intervals for Service. Pool, Settings, Clock,
// and Logger are required; Alerts, Banter, and HTTPClient are optional.
type Deps struct {
	Pool               *pgxpool.Pool
	Settings           SettingReader
	Clock              Clock
	Alerts             AlertSender
	Banter             BanterGenerator
	HTTPClient         *http.Client
	Logger             *slog.Logger
	SyncInterval       time.Duration
	AlertCheckInterval time.Duration
	SettingsTTL        time.Duration
}

// Service manages Universidad Catolica schedule synchronization, availability queries, cached
// banter generation, and proactive alert dispatch. It is safe for concurrent use.
type Service struct {
	store              *store.Store
	espn               *espn.Client
	httpClient         *http.Client
	settings           SettingReader
	clock              Clock
	alerts             AlertSender
	banter             BanterGenerator
	logger             *slog.Logger
	loc                *time.Location
	syncInterval       time.Duration
	alertCheckInterval time.Duration
	settingsTTL        time.Duration

	mu             sync.RWMutex
	schedule       []Match
	hasSynced      bool
	cachedSettings resolvedSettings
	hasSettings    bool
	banterCache    map[string]banterEntry
	banterInFlight map[string]bool
	banterTasks    map[uint64]banterTask
	nextTaskID     uint64
	closed         bool

	sf singleflight.Group
}

// New validates deps and constructs a Service without performing I/O.
func New(deps Deps) (*Service, error) {
	var problems []error

	if deps.Pool == nil {
		problems = append(problems, &InputError{Field: "deps.Pool", Code: InputCodeRequired})
	}

	if deps.Settings == nil {
		problems = append(problems, &InputError{Field: "deps.Settings", Code: InputCodeRequired})
	}

	if deps.Clock == nil {
		problems = append(problems, &InputError{Field: "deps.Clock", Code: InputCodeRequired})
	}

	if deps.Logger == nil {
		problems = append(problems, &InputError{Field: "deps.Logger", Code: InputCodeRequired})
	}

	syncInterval := deps.SyncInterval
	if syncInterval < 0 {
		problems = append(problems, &InputError{Field: "deps.SyncInterval", Code: InputCodeDurationOutOfRange})
	} else if syncInterval == 0 {
		syncInterval = DefaultSyncInterval
	}

	alertInterval := deps.AlertCheckInterval
	if alertInterval < 0 {
		problems = append(problems, &InputError{Field: "deps.AlertCheckInterval", Code: InputCodeDurationOutOfRange})
	} else if alertInterval == 0 {
		alertInterval = DefaultAlertCheckInterval
	}

	settingsTTL := deps.SettingsTTL
	if settingsTTL < 0 {
		problems = append(problems, &InputError{Field: "deps.SettingsTTL", Code: InputCodeDurationOutOfRange})
	} else if settingsTTL == 0 {
		settingsTTL = DefaultSettingsTTL
	}

	if len(problems) > 0 {
		return nil, errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
	}

	loc, err := time.LoadLocation(SantiagoTimezone)
	if err != nil {
		return nil, fmt.Errorf("load timezone %s: %w", SantiagoTimezone, err)
	}

	httpClient := deps.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout:   DefaultHTTPTimeout,
			Transport: &http.Transport{},
		}
	}

	espnClient, err := espn.New(httpClient, loc)
	if err != nil {
		return nil, fmt.Errorf("create espn client: %w", err)
	}

	alertStore, err := store.New(deps.Pool)
	if err != nil {
		return nil, fmt.Errorf("create alert store: %w", err)
	}

	svc := &Service{
		store:              alertStore,
		espn:               espnClient,
		httpClient:         httpClient,
		settings:           deps.Settings,
		clock:              deps.Clock,
		alerts:             deps.Alerts,
		banter:             deps.Banter,
		logger:             deps.Logger,
		loc:                loc,
		syncInterval:       syncInterval,
		alertCheckInterval: alertInterval,
		settingsTTL:        settingsTTL,
		banterCache:        make(map[string]banterEntry, maxBanterCacheEntries),
		banterInFlight:     make(map[string]bool),
		banterTasks:        make(map[uint64]banterTask),
	}

	return svc, nil
}

// Close cancels any in-flight background banter generation goroutines, waits for them to exit, and
// closes idle HTTP connections.
func (s *Service) Close() error {
	if s == nil {
		return nil
	}

	s.cancelBanterAndWait(true)
	if s.httpClient != nil {
		s.httpClient.CloseIdleConnections()
	}

	return nil
}

// Sync fetches the completed and upcoming Universidad Catolica schedules from the configured API
// base URL and atomically replaces the cached schedule. If fetching fails, any previously synced
// schedule remains intact.
func (s *Service) Sync(ctx context.Context) error {
	if s == nil {
		return &InputError{Field: "service", Code: InputCodeRequired}
	}

	if ctx == nil {
		return &InputError{Field: "ctx", Code: InputCodeRequired}
	}

	cfg, err := s.getSettings(ctx)
	if err != nil {
		return fmt.Errorf("load uc settings: %w", err)
	}

	events, err := s.espn.FetchSchedule(ctx, cfg.apiBaseURL)
	if err != nil {
		return fmt.Errorf("sync uc schedule: %w", err)
	}

	matches := make([]Match, 0, len(events))
	for _, ev := range events {
		matches = append(matches, eventToMatch(ev))
	}

	s.mu.Lock()
	s.schedule = matches
	s.hasSynced = true
	s.mu.Unlock()

	s.logger.DebugContext(ctx, "uc schedule synced", slog.Int("matches", len(matches)))

	return nil
}

// Check evaluates friend availability for q against the cached Universidad Catolica schedule.
func (s *Service) Check(ctx context.Context, q Query) (Report, error) {
	if s == nil {
		return Report{}, &InputError{Field: "service", Code: InputCodeRequired}
	}

	now := s.clock.Now().In(s.loc)

	var problems []error
	if ctx == nil {
		problems = append(problems, &InputError{Field: "ctx", Code: InputCodeRequired})
	}

	resolved, err := resolveQueryRange(q, now, s.loc)
	if err != nil {
		problems = append(problems, err)
	}

	if len(problems) > 0 {
		return Report{}, errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
	}

	s.mu.RLock()
	hasSynced := s.hasSynced
	schedule := s.schedule
	s.mu.RUnlock()

	if !hasSynced {
		return Report{}, ErrScheduleUnavailable
	}

	cfg, err := s.getSettings(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("load uc settings: %w", err)
	}

	isDefaultQuery := q.Window == WindowDefault && q.Date == ""
	today := localDateIn(now, s.loc)
	matches := overlappingMatches(schedule, resolved.rangeStart, resolved.rangeEnd, s.loc)
	status := statusForMatches(matches)
	nextMatch := findNextMatch(schedule, now, today, resolved.rangeStart, resolved.rangeEnd, isDefaultQuery, s.loc)

	archetype, fp, scenario := reportBanterParams(status, matches, nextMatch)
	banter := s.resolveBanter(now, cfg.slangCacheTTL, archetype, fp, scenario)

	report := Report{
		Mode:         resolved.mode,
		Window:       resolved.window,
		QueriedDate:  resolved.queriedDate,
		RangeStart:   resolved.rangeStart,
		RangeEnd:     resolved.rangeEnd,
		Status:       status,
		Matches:      cloneMatches(matches),
		NextMatch:    nextMatch,
		FriendUserID: cfg.friendUserID,
		Banter:       banter,
	}

	return report, nil
}

// CheckAlerts evaluates and sends any due Friday weekend summary or matchday morning reminder,
// deduplicating across restarts and instances via Postgres schema uccalendar.
func (s *Service) CheckAlerts(ctx context.Context) error {
	if s == nil {
		return &InputError{Field: "service", Code: InputCodeRequired}
	}

	if ctx == nil {
		return &InputError{Field: "ctx", Code: InputCodeRequired}
	}

	if s.alerts == nil {
		return nil
	}

	s.mu.RLock()
	hasSynced := s.hasSynced
	schedule := s.schedule
	s.mu.RUnlock()

	if !hasSynced {
		return nil
	}

	cfg, err := s.getSettings(ctx)
	if err != nil {
		return fmt.Errorf("load uc settings: %w", err)
	}

	if cfg.channelID == "" {
		return nil
	}

	now := s.clock.Now().In(s.loc)
	today := localDateIn(now, s.loc)

	var problems []error

	if today.Weekday(s.loc) == time.Friday {
		fridayErr := s.checkFridayAlert(ctx, now, today, cfg, schedule)
		if fridayErr != nil {
			problems = append(problems, fridayErr)
		}
	}

	matchdayErr := s.checkMatchdayAlerts(ctx, now, today, cfg, schedule)
	if matchdayErr != nil {
		problems = append(problems, matchdayErr)
	}

	if len(problems) > 0 {
		return errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
	}

	return nil
}

func (s *Service) checkFridayAlert(
	ctx context.Context,
	now time.Time,
	friday LocalDate,
	cfg resolvedSettings,
	schedule []Match,
) error {
	cutoff := time.Date(friday.Year, friday.Month, friday.Day, cfg.fridayHour, cfg.fridayMinute, 0, 0, s.loc)
	if now.Before(cutoff) {
		return nil
	}

	saturday := friday.AddDays(1)
	sunday := friday.AddDays(2)
	weekendStart := fridayEveningStart(friday, s.loc)
	weekendEnd := sunday.EndOfDay(s.loc)

	weekendMatches := overlappingMatches(schedule, weekendStart, weekendEnd, s.loc)
	if len(weekendMatches) == 0 {
		return nil
	}

	alertKey := "friday:" + friday.String()
	claimed, err := s.store.ClaimAlert(ctx, alertKey, now.UTC())
	if err != nil {
		return fmt.Errorf("claim friday alert: %w", err)
	}

	if !claimed {
		return nil
	}

	satMatches := overlappingMatches(schedule, saturday.StartOfDay(s.loc), saturday.EndOfDay(s.loc), s.loc)
	sunMatches := overlappingMatches(schedule, sunday.StartOfDay(s.loc), sunday.EndOfDay(s.loc), s.loc)

	archetype, fp, scenario := fridayBanterParams(friday, weekendMatches)
	banter := s.resolveBanterForAlert(ctx, now, cfg.slangCacheTTL, archetype, fp, scenario)

	alert := Alert{
		Kind:           AlertFridayWeekend,
		FriendUserID:   cfg.friendUserID,
		Banter:         banter,
		WeekendStart:   friday,
		WeekendEnd:     sunday,
		SaturdayStatus: statusForMatches(satMatches),
		SundayStatus:   statusForMatches(sunMatches),
		Matches:        cloneMatches(weekendMatches),
	}

	sendErr := s.alerts.SendUCAlert(ctx, cfg.channelID, alert)
	if sendErr != nil {
		releaseErr := s.releaseAlertClaim(ctx, alertKey)
		return fmt.Errorf("send friday alert: %w", errors.Join(sendErr, releaseErr))
	}

	s.logger.InfoContext(ctx, "uc friday alert sent", slog.String("date", friday.String()))

	return nil
}

func (s *Service) checkMatchdayAlerts(
	ctx context.Context,
	now time.Time,
	today LocalDate,
	cfg resolvedSettings,
	schedule []Match,
) error {
	cutoff := time.Date(today.Year, today.Month, today.Day, cfg.matchdayHour, cfg.matchdayMinute, 0, 0, s.loc)
	if now.Before(cutoff) {
		return nil
	}

	var problems []error

	for _, match := range schedule {
		if match.TBD {
			continue
		}

		kickoffDate := localDateIn(match.Kickoff, s.loc)
		isToday := kickoffDate == today
		isStillActive := now.Before(match.BlockEnd)
		if !isToday || !isStillActive {
			continue
		}

		alertKey := "matchday:" + match.ID + ":" + today.String()
		claimed, err := s.store.ClaimAlert(ctx, alertKey, now.UTC())
		if err != nil {
			problems = append(problems, fmt.Errorf("claim matchday alert %s: %w", match.ID, err))
			continue
		}

		if !claimed {
			continue
		}

		archetype, fp, scenario := matchdayBanterParams(match)
		banter := s.resolveBanterForAlert(ctx, now, cfg.slangCacheTTL, archetype, fp, scenario)

		alert := Alert{
			Kind:         AlertMatchdayMorning,
			FriendUserID: cfg.friendUserID,
			Banter:       banter,
			Matches:      []Match{match},
		}

		sendErr := s.alerts.SendUCAlert(ctx, cfg.channelID, alert)
		if sendErr != nil {
			releaseErr := s.releaseAlertClaim(ctx, alertKey)
			problems = append(problems, fmt.Errorf("send matchday alert %s: %w", match.ID, errors.Join(sendErr, releaseErr)))
			continue
		}

		s.logger.InfoContext(
			ctx,
			"uc matchday alert sent",
			slog.String("match_id", match.ID),
			slog.String("date", today.String()),
		)
	}

	if len(problems) > 0 {
		return errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
	}

	return nil
}

func (s *Service) releaseAlertClaim(ctx context.Context, alertKey string) error {
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseAlertTimeout)
	defer cancel()

	return s.store.ReleaseAlert(releaseCtx, alertKey) //nolint:wrapcheck // caller joins release error with send error
}

func (s *Service) isSynced() bool {
	s.mu.RLock()
	synced := s.hasSynced
	s.mu.RUnlock()

	return synced
}

// Run performs an initial schedule sync, runs periodic sync and proactive alert checks until ctx
// is canceled, cancels any in-flight async banter refreshes, waits for all background goroutines
// to finish, and returns nil.
func (s *Service) Run(ctx context.Context) error {
	if s == nil {
		return &InputError{Field: "service", Code: InputCodeRequired}
	}

	if ctx == nil {
		return &InputError{Field: "ctx", Code: InputCodeRequired}
	}

	defer func() {
		s.cancelBanterAndWait(false)
		if s.httpClient != nil {
			s.httpClient.CloseIdleConnections()
		}
	}()

	if !s.isSynced() {
		if err := s.Sync(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}

			s.logger.WarnContext(ctx, "uc initial schedule sync failed")
		}
	}

	if err := s.CheckAlerts(ctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}

		s.logger.WarnContext(ctx, "uc initial alert check failed")
	}

	syncTicker := time.NewTicker(s.syncInterval)
	defer syncTicker.Stop()

	alertTicker := time.NewTicker(s.alertCheckInterval)
	defer alertTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-syncTicker.C:
			if err := s.Sync(ctx); err != nil {
				if ctx.Err() != nil {
					return nil
				}

				s.logger.WarnContext(ctx, "uc periodic schedule sync failed")
			}
		case <-alertTicker.C:
			if err := s.CheckAlerts(ctx); err != nil {
				if ctx.Err() != nil {
					return nil
				}

				s.logger.WarnContext(ctx, "uc periodic alert check failed")
			}
		}
	}
}
