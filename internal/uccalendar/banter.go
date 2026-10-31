package uccalendar

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode"
)

const (
	maxBanterCacheEntries = 32
	maxBanterRunes        = 160

	archetypeBlockedHome   = "blocked_home"
	archetypeBlockedAway   = "blocked_away"
	archetypeFree          = "free"
	archetypePendingANFP   = "pending_anfp"
	archetypeFridayAlert   = "friday_alert"
	archetypeMatchdayAlert = "matchday_alert"
)

type banterEntry struct {
	text        string
	generatedAt time.Time
}

type banterTask struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func (s *Service) resolveBanter(
	now time.Time,
	ttl time.Duration,
	archetype string,
	fingerprint string,
	scenario string,
) string {
	if s.banter == nil || ttl <= 0 {
		return ""
	}

	cacheKey := archetype + "|" + fingerprint

	s.mu.Lock()
	entry, found := s.banterCache[cacheKey]
	if found {
		age := now.Sub(entry.generatedAt)
		isFresh := age >= 0 && age < ttl
		if isFresh {
			text := entry.text
			s.mu.Unlock()

			return text
		}
	}

	staleText := ""
	if found {
		staleText = entry.text
	}

	shouldLaunch := !s.closed && !s.banterInFlight[cacheKey]
	if shouldLaunch {
		s.banterInFlight[cacheKey] = true
		refreshCtx, cancel := context.WithTimeout(context.Background(), DefaultHTTPTimeout)
		done := make(chan struct{})
		taskID := s.nextTaskID
		s.nextTaskID++
		s.banterTasks[taskID] = banterTask{cancel: cancel, done: done}

		go s.runBanterRefresh(refreshCtx, cancel, done, taskID, cacheKey, archetype, scenario)
	}
	s.mu.Unlock()

	return staleText
}

func (s *Service) resolveBanterForAlert(
	ctx context.Context,
	now time.Time,
	ttl time.Duration,
	archetype string,
	fingerprint string,
	scenario string,
) string {
	if s.banter == nil || ttl <= 0 {
		return ""
	}

	cacheKey := archetype + "|" + fingerprint

	s.mu.Lock()
	entry, found := s.banterCache[cacheKey]
	if found {
		age := now.Sub(entry.generatedAt)
		isFresh := age >= 0 && age < ttl
		if isFresh {
			text := entry.text
			s.mu.Unlock()

			return text
		}
	}

	staleText := ""
	if found {
		staleText = entry.text
	}

	isClosed := s.closed
	s.mu.Unlock()

	if isClosed {
		return staleText
	}

	val, err, _ := s.sf.Do(cacheKey, func() (any, error) {
		callCtx, cancel := context.WithTimeout(ctx, DefaultHTTPTimeout)
		defer cancel()

		return s.generateAndStoreBanter(callCtx, cacheKey, archetype, scenario)
	})
	if err != nil {
		return staleText
	}

	clean, ok := val.(string)
	if !ok || clean == "" {
		return staleText
	}

	return clean
}

func (s *Service) runBanterRefresh(
	refreshCtx context.Context,
	cancel context.CancelFunc,
	done chan struct{},
	taskID uint64,
	cacheKey string,
	archetype string,
	scenario string,
) {
	defer close(done)
	defer func() {
		cancel()

		s.mu.Lock()
		delete(s.banterTasks, taskID)
		delete(s.banterInFlight, cacheKey)
		s.mu.Unlock()
	}()

	// Background banter refresh is best-effort; generation failures are logged inside the closure.
	_, _, _ = s.sf.Do(cacheKey, func() (any, error) {
		return s.generateAndStoreBanter(refreshCtx, cacheKey, archetype, scenario)
	})
}

func (s *Service) generateAndStoreBanter(
	callCtx context.Context,
	cacheKey string,
	archetype string,
	scenario string,
) (string, error) {
	raw, err := s.banter.Generate(callCtx, scenario)
	if err != nil {
		if callCtx.Err() == nil {
			s.logger.WarnContext(
				callCtx,
				"uc banter generation failed",
				slog.String("archetype", archetype),
			)
		}

		return "", fmt.Errorf("generate banter: %w", err)
	}

	clean, ok := sanitizeBanter(raw)
	if !ok {
		s.logger.WarnContext(
			callCtx,
			"uc banter output discarded by sanitizer",
			slog.String("archetype", archetype),
		)

		return "", nil
	}

	generatedAt := s.clock.Now()

	s.mu.Lock()
	_, exists := s.banterCache[cacheKey]
	if !exists && len(s.banterCache) >= maxBanterCacheEntries {
		s.evictOldestBanterLocked()
	}

	s.banterCache[cacheKey] = banterEntry{
		text:        clean,
		generatedAt: generatedAt,
	}
	s.mu.Unlock()

	return clean, nil
}

func (s *Service) evictOldestBanterLocked() {
	var (
		oldestKey  string
		oldestTime time.Time
		hasOldest  bool
	)

	for key, entry := range s.banterCache {
		isOlder := !hasOldest ||
			entry.generatedAt.Before(oldestTime) ||
			(entry.generatedAt.Equal(oldestTime) && key < oldestKey)
		if isOlder {
			oldestKey = key
			oldestTime = entry.generatedAt
			hasOldest = true
		}
	}

	if hasOldest {
		delete(s.banterCache, oldestKey)
	}
}

func (s *Service) cancelBanterAndWait(permanentlyClose bool) {
	s.mu.Lock()
	if permanentlyClose {
		s.closed = true
	}

	tasks := make([]banterTask, 0, len(s.banterTasks))
	for _, task := range s.banterTasks {
		tasks = append(tasks, task)
	}
	s.mu.Unlock()

	for _, task := range tasks {
		task.cancel()
	}

	for _, task := range tasks {
		<-task.done
	}
}

func sanitizeBanter(raw string) (string, bool) {
	valid := strings.ToValidUTF8(raw, "")
	if valid == "" {
		return "", false
	}

	var b strings.Builder
	b.Grow(len(valid))

	for _, r := range valid {
		switch {
		case r == '\n' || r == '\r' || r == '\t' || unicode.IsSpace(r):
			b.WriteByte(' ')
		case r == '@' || r == '<' || unicode.IsControl(r) || isBidiRune(r):
			continue
		default:
			b.WriteRune(r)
		}
	}

	collapsed := strings.Join(strings.Fields(b.String()), " ")
	collapsed = strings.TrimSpace(strings.Trim(collapsed, "\""))
	collapsed = strings.Join(strings.Fields(collapsed), " ")

	runes := []rune(collapsed)
	if len(runes) > maxBanterRunes {
		collapsed = strings.TrimSpace(string(runes[:maxBanterRunes]))
	}

	if collapsed == "" {
		return "", false
	}

	return collapsed, true
}

func isBidiRune(r rune) bool {
	isEmbeddingOrOverride := r >= 0x202A && r <= 0x202E
	isIsolate := r >= 0x2066 && r <= 0x2069

	return isEmbeddingOrOverride || isIsolate
}

func reportBanterParams(status AvailabilityStatus, matches []Match, nextMatch *Match) (string, string, string) {
	switch status {
	case StatusBlocked:
		primary := firstConfirmedMatch(matches)
		archetype := archetypeBlockedHome
		if primary.Condition == VenueAway {
			archetype = archetypeBlockedAway
		}

		fp := matchesFingerprint(matches)
		scenario := buildBlockedScenario(archetype, primary)

		return archetype, fp, scenario
	case StatusPendingANFP:
		fp := matchesFingerprint(matches)
		primary := matches[0]
		scenario := "Scenario: pending_anfp. Universidad Catolica has a scheduled match against " +
			sanitizeScenarioToken(primary.Opponent) + " in " + sanitizeScenarioToken(primary.Tournament) +
			", waiting for ANFP to confirm exact kickoff time."

		return archetypePendingANFP, fp, scenario
	default:
		fp := "none"
		scenario := "Scenario: free. Universidad Catolica does not play during this window, so the UC fan friend is free to hang out."
		if nextMatch != nil {
			fp = "next:" + matchFingerprint(*nextMatch)
			scenario = "Scenario: free. Universidad Catolica has no match in this window so the friend is free; next fixture is against " +
				sanitizeScenarioToken(nextMatch.Opponent) + " in " + sanitizeScenarioToken(nextMatch.Tournament) + "."
		}

		return archetypeFree, fp, scenario
	}
}

func fridayBanterParams(friday LocalDate, matches []Match) (string, string, string) {
	fp := friday.String() + ":" + matchesFingerprint(matches)
	primary := matches[0]
	scenario := "Scenario: friday_alert. It is Friday afternoon and Universidad Catolica plays this weekend against " +
		sanitizeScenarioToken(primary.Opponent) + " in " + sanitizeScenarioToken(primary.Tournament) +
		". Warn the group before making weekend plans."

	return archetypeFridayAlert, fp, scenario
}

func matchdayBanterParams(match Match) (string, string, string) {
	fp := matchFingerprint(match)
	venueWord := "at home"
	if match.Condition == VenueAway {
		venueWord = "away"
	}

	scenario := "Scenario: matchday_alert. Today is matchday and Universidad Catolica plays " +
		venueWord + " against " + sanitizeScenarioToken(match.Opponent) + " in " +
		sanitizeScenarioToken(match.Tournament) + ". Remind the group that their UC friend is unavailable during the match block."

	return archetypeMatchdayAlert, fp, scenario
}

func buildBlockedScenario(archetype string, match Match) string {
	if archetype == archetypeBlockedAway {
		text := "Scenario: blocked_away. Universidad Catolica plays away against " +
			sanitizeScenarioToken(match.Opponent) + " in " + sanitizeScenarioToken(match.Tournament) +
			". The UC fan friend will be glued to the screen during the match."

		return text
	}

	venueClause := ""
	if match.Venue != "" {
		venueClause = " at " + sanitizeScenarioToken(match.Venue)
	}

	text := "Scenario: blocked_home. Universidad Catolica plays at home against " +
		sanitizeScenarioToken(match.Opponent) + " in " + sanitizeScenarioToken(match.Tournament) +
		venueClause + ". The UC fan friend is blocked by pre-match travel, the match, and stadium traffic."

	return text
}

func sanitizeScenarioToken(raw string) string {
	clean, ok := sanitizeBanter(raw)
	if !ok {
		return "rival"
	}

	runes := []rune(clean)
	if len(runes) > 48 {
		return string(runes[:48])
	}

	return clean
}

func firstConfirmedMatch(matches []Match) Match {
	for _, m := range matches {
		if !m.TBD {
			return m
		}
	}

	if len(matches) > 0 {
		return matches[0]
	}

	return Match{}
}

func matchesFingerprint(matches []Match) string {
	parts := make([]string, 0, len(matches))
	for _, m := range matches {
		parts = append(parts, matchFingerprint(m))
	}

	return strings.Join(parts, ",")
}

func matchFingerprint(m Match) string {
	if m.TBD {
		fp := m.ID + ":" + string(m.Condition) + ":" + m.Date.String() + ":tbd"
		return fp
	}

	fp := m.ID + ":" + string(m.Condition) + ":" + m.Kickoff.UTC().Format(time.RFC3339)
	return fp
}
