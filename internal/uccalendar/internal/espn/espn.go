// Package espn fetches and normalizes Universidad Catolica schedule events from the ESPN soccer
// site API.
package espn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
)

const (
	// TeamID is Universidad Catolica's ESPN soccer team identifier.
	TeamID = "885"

	schedulePath        = "/all/teams/" + TeamID + "/schedule"
	fixtureSchedulePath = schedulePath + "?fixture=true"
	maxResponseBytes    = 2 << 20
	minCalendarYear     = 2000
	maxCalendarYear     = 2100
	defaultTournament   = "Primera Division"
)

// Event is one normalized Universidad Catolica match parsed from ESPN.
type Event struct {
	ID         string
	Opponent   string
	Tournament string
	Venue      string
	Home       bool
	TBD        bool
	Year       int
	Month      time.Month
	Day        int
	Kickoff    time.Time
}

// Client fetches completed and upcoming fixtures from an ESPN-compatible base URL.
type Client struct {
	httpClient *http.Client
	loc        *time.Location
}

// New constructs a Client using httpClient and loc.
func New(httpClient *http.Client, loc *time.Location) (*Client, error) {
	var problems []error

	if httpClient == nil {
		problems = append(problems, errors.New("espn: nil http client"))
	}

	if loc == nil {
		problems = append(problems, errors.New("espn: nil timezone location"))
	}

	if len(problems) > 0 {
		return nil, errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
	}

	client := &Client{
		httpClient: httpClient,
		loc:        loc,
	}

	return client, nil
}

// FetchSchedule fetches both the default schedule feed and the upcoming fixture feed from
// baseURL, merges them by event ID with the fixture entry taking precedence, and returns the
// chronologically sorted events.
func (c *Client) FetchSchedule(ctx context.Context, baseURL string) ([]Event, error) {
	var problems []error
	if ctx == nil {
		problems = append(problems, errors.New("espn: nil context"))
	}

	isUninitialized := c == nil || c.httpClient == nil || c.loc == nil
	if isUninitialized {
		problems = append(problems, errors.New("espn: uninitialized client"))
	}

	trimmedBase := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmedBase == "" {
		problems = append(problems, errors.New("espn: empty base URL"))
	}

	if len(problems) > 0 {
		return nil, errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
	}

	completed, err := c.fetchEndpoint(ctx, trimmedBase+schedulePath)
	if err != nil {
		return nil, fmt.Errorf("fetch schedule: %w", err)
	}

	fixtures, err := c.fetchEndpoint(ctx, trimmedBase+fixtureSchedulePath)
	if err != nil {
		return nil, fmt.Errorf("fetch upcoming fixtures: %w", err)
	}

	byID := make(map[string]scopedEvent, len(completed.Events)+len(fixtures.Events))

	for _, ev := range completed.Events {
		id := strings.TrimSpace(ev.ID)
		if id == "" {
			continue
		}

		byID[id] = scopedEvent{
			raw:        ev,
			leagueName: completed.League.Name,
			seasonSlug: completed.Season.Slug,
		}
	}

	for _, ev := range fixtures.Events {
		id := strings.TrimSpace(ev.ID)
		if id == "" {
			continue
		}

		byID[id] = scopedEvent{
			raw:        ev,
			leagueName: fixtures.League.Name,
			seasonSlug: fixtures.Season.Slug,
		}
	}

	events := make([]Event, 0, len(byID))

	for _, item := range byID {
		parsed, keep, parseErr := c.normalizeEvent(item)
		if parseErr != nil {
			return nil, parseErr
		}

		if keep {
			events = append(events, parsed)
		}
	}

	slices.SortFunc(events, compareEvents)

	return events, nil
}

func (c *Client) fetchEndpoint(ctx context.Context, endpoint string) (schedulePayload, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return schedulePayload{}, errors.New("espn: build request failed")
	}

	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return schedulePayload{}, fmt.Errorf("espn: request failed: %w", err)
	}

	defer func() {
		// Closing an already-read HTTP response body is best-effort.
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return schedulePayload{}, fmt.Errorf("espn: unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return schedulePayload{}, errors.New("espn: read response failed")
	}

	if len(body) > maxResponseBytes {
		return schedulePayload{}, errors.New("espn: response exceeds 2 MiB limit")
	}

	var parsed schedulePayload

	err = json.Unmarshal(body, &parsed)
	if err != nil {
		return schedulePayload{}, errors.New("espn: invalid JSON payload")
	}

	return parsed, nil
}

type scopedEvent struct {
	raw        rawEvent
	leagueName string
	seasonSlug string
}

type schedulePayload struct {
	League rawLeague  `json:"league"`
	Season rawSeason  `json:"season"`
	Events []rawEvent `json:"events"`
}

type rawLeague struct {
	Name string `json:"name"`
}

type rawSeason struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"displayName"`
}

type rawEvent struct {
	ID           string           `json:"id"`
	Date         string           `json:"date"`
	TimeValid    *bool            `json:"timeValid"`
	League       rawLeague        `json:"league"`
	Season       rawSeason        `json:"season"`
	Status       rawStatusWrapper `json:"status"`
	Competitions []rawCompetition `json:"competitions"`
}

type rawCompetition struct {
	ID          string           `json:"id"`
	Date        string           `json:"date"`
	TimeValid   *bool            `json:"timeValid"`
	Venue       rawVenue         `json:"venue"`
	League      rawLeague        `json:"league"`
	Competitors []rawCompetitor  `json:"competitors"`
	Status      rawStatusWrapper `json:"status"`
}

type rawVenue struct {
	FullName  string `json:"fullName"`
	ShortName string `json:"shortName"`
}

type rawCompetitor struct {
	ID       string  `json:"id"`
	HomeAway string  `json:"homeAway"`
	Team     rawTeam `json:"team"`
}

type rawTeam struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	DisplayName      string `json:"displayName"`
	ShortDisplayName string `json:"shortDisplayName"`
}

type rawStatusWrapper struct {
	Type rawStatusType `json:"type"`
}

type rawStatusType struct {
	Name        string `json:"name"`
	State       string `json:"state"`
	Description string `json:"description"`
	Detail      string `json:"detail"`
	ShortDetail string `json:"shortDetail"`
}

func (c *Client) normalizeEvent(item scopedEvent) (Event, bool, error) {
	ev := item.raw
	if len(ev.Competitions) == 0 {
		return Event{}, false, nil
	}

	comp := ev.Competitions[0]

	if isCanceledStatus(comp.Status.Type) || isCanceledStatus(ev.Status.Type) {
		return Event{}, false, nil
	}

	ucIndex, found := findUCCompetitor(comp.Competitors)
	if !found {
		return Event{}, false, nil
	}

	ucComp := comp.Competitors[ucIndex]
	oppComp, hasOpponent := findOpponentCompetitor(comp.Competitors, ucIndex)

	isHome := resolveIsHome(ucComp, oppComp, hasOpponent)
	opponent := firstNonEmpty(
		oppComp.Team.DisplayName,
		oppComp.Team.Name,
		oppComp.Team.ShortDisplayName,
		"TBD",
	)
	venue := firstNonEmpty(comp.Venue.FullName, comp.Venue.ShortName)
	tournament := firstNonEmpty(
		ev.League.Name,
		comp.League.Name,
		item.leagueName,
		ev.Season.Slug,
		item.seasonSlug,
		defaultTournament,
	)

	rawDate := firstNonEmpty(comp.Date, ev.Date)
	if rawDate == "" {
		return Event{}, false, fmt.Errorf("espn: event %s has empty date", strings.TrimSpace(ev.ID))
	}

	isTBD := detectTBD(ev, comp, rawDate)
	if isTBD {
		year, month, day, err := parseYYYYMMDDPrefix(rawDate)
		if err != nil {
			return Event{}, false, fmt.Errorf("espn: event %s invalid TBD date: %w", strings.TrimSpace(ev.ID), err)
		}

		result := Event{
			ID:         strings.TrimSpace(ev.ID),
			Opponent:   opponent,
			Tournament: tournament,
			Venue:      venue,
			Home:       isHome,
			TBD:        true,
			Year:       year,
			Month:      month,
			Day:        day,
		}

		return result, true, nil
	}

	kickoffUTC, err := parseISOTimestamp(rawDate)
	if err != nil {
		return Event{}, false, fmt.Errorf("espn: event %s invalid kickoff timestamp: %w", strings.TrimSpace(ev.ID), err)
	}

	kickoffLocal := kickoffUTC.In(c.loc)

	result := Event{
		ID:         strings.TrimSpace(ev.ID),
		Opponent:   opponent,
		Tournament: tournament,
		Venue:      venue,
		Home:       isHome,
		TBD:        false,
		Year:       kickoffLocal.Year(),
		Month:      kickoffLocal.Month(),
		Day:        kickoffLocal.Day(),
		Kickoff:    kickoffLocal,
	}

	return result, true, nil
}

func isCanceledStatus(st rawStatusType) bool {
	tokens := []string{st.Name, st.State, st.Description}
	for _, tok := range tokens {
		trimmed := strings.TrimSpace(tok)
		isCanceled := strings.EqualFold(trimmed, "STATUS_CANCELED") ||
			strings.EqualFold(trimmed, "STATUS_CANCELLED") ||
			strings.EqualFold(trimmed, "canceled") ||
			strings.EqualFold(trimmed, "cancelled")
		if isCanceled {
			return true
		}
	}

	return false
}

func findUCCompetitor(competitors []rawCompetitor) (int, bool) {
	for i, comp := range competitors {
		isTeamID := strings.TrimSpace(comp.Team.ID) == TeamID || strings.TrimSpace(comp.ID) == TeamID
		if isTeamID {
			return i, true
		}
	}

	for i, comp := range competitors {
		isNamedUC := matchesUCName(comp.Team.DisplayName) ||
			matchesUCName(comp.Team.Name) ||
			matchesUCName(comp.Team.ShortDisplayName)
		if isNamedUC {
			return i, true
		}
	}

	return -1, false
}

func matchesUCName(raw string) bool {
	lower := strings.ToLower(strings.TrimSpace(raw))
	if lower == "" {
		return false
	}

	hasPlain := strings.Contains(lower, "catolica")
	hasAccented := strings.Contains(lower, "cat\u00f3lica")

	return hasPlain || hasAccented
}

func findOpponentCompetitor(competitors []rawCompetitor, ucIndex int) (rawCompetitor, bool) {
	for i, comp := range competitors {
		if i != ucIndex {
			return comp, true
		}
	}

	return rawCompetitor{}, false
}

func resolveIsHome(ucComp, oppComp rawCompetitor, hasOpponent bool) bool {
	if strings.EqualFold(strings.TrimSpace(ucComp.HomeAway), "home") {
		return true
	}

	if strings.EqualFold(strings.TrimSpace(ucComp.HomeAway), "away") {
		return false
	}

	if hasOpponent && strings.EqualFold(strings.TrimSpace(oppComp.HomeAway), "home") {
		return false
	}

	return true
}

func detectTBD(ev rawEvent, comp rawCompetition, rawDate string) bool {
	timeValid := true
	if comp.TimeValid != nil {
		timeValid = *comp.TimeValid
	} else if ev.TimeValid != nil {
		timeValid = *ev.TimeValid
	}

	if !timeValid {
		return true
	}

	if len(rawDate) == 10 {
		return true
	}

	details := []string{
		comp.Status.Type.Detail,
		comp.Status.Type.ShortDetail,
		comp.Status.Type.Name,
		ev.Status.Type.Detail,
		ev.Status.Type.ShortDetail,
		ev.Status.Type.Name,
	}
	for _, detail := range details {
		if strings.Contains(strings.ToUpper(detail), "TBD") {
			return true
		}
	}

	return false
}

func parseYYYYMMDDPrefix(raw string) (int, time.Month, int, error) {
	if len(raw) < 10 {
		return 0, 0, 0, errors.New("date shorter than YYYY-MM-DD")
	}

	prefix := raw[:10]
	if prefix[4] != '-' || prefix[7] != '-' {
		return 0, 0, 0, errors.New("date missing YYYY-MM-DD separators")
	}

	year, okYear := parseFixedDigits(prefix[0:4])
	monthNum, okMonth := parseFixedDigits(prefix[5:7])
	day, okDay := parseFixedDigits(prefix[8:10])
	if !okYear || !okMonth || !okDay {
		return 0, 0, 0, errors.New("date has non-digit fields")
	}

	if year < minCalendarYear || year > maxCalendarYear {
		return 0, 0, 0, errors.New("date year out of range")
	}

	if monthNum < 1 || monthNum > 12 || day < 1 || day > 31 {
		return 0, 0, 0, errors.New("date month or day out of range")
	}

	month := time.Month(monthNum)
	check := time.Date(year, month, day, 12, 0, 0, 0, time.UTC)
	isValidCalendarDay := check.Year() == year && check.Month() == month && check.Day() == day
	if !isValidCalendarDay {
		return 0, 0, 0, errors.New("invalid calendar day")
	}

	return year, month, day, nil
}

func parseFixedDigits(s string) (int, bool) {
	val := 0
	for i := range len(s) {
		ch := s[i]
		if ch < '0' || ch > '9' {
			return 0, false
		}

		val = val*10 + int(ch-'0')
	}

	return val, true
}

func parseISOTimestamp(raw string) (time.Time, error) {
	layouts := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04Z07:00",
		"2006-01-02T15:04Z",
	}

	for _, layout := range layouts {
		parsed, err := time.Parse(layout, raw)
		if err == nil {
			return parsed, nil
		}
	}

	return time.Time{}, errors.New("unsupported ISO-8601 timestamp format")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		trimmed := strings.TrimSpace(v)
		if trimmed != "" {
			return trimmed
		}
	}

	return ""
}

func compareEvents(a, b Event) int {
	if a.Year != b.Year {
		if a.Year < b.Year {
			return -1
		}

		return 1
	}

	if a.Month != b.Month {
		if a.Month < b.Month {
			return -1
		}

		return 1
	}

	if a.Day != b.Day {
		if a.Day < b.Day {
			return -1
		}

		return 1
	}

	switch {
	case !a.TBD && !b.TBD:
		if a.Kickoff.Before(b.Kickoff) {
			return -1
		}

		if a.Kickoff.After(b.Kickoff) {
			return 1
		}
	case !a.TBD && b.TBD:
		return -1
	case a.TBD && !b.TBD:
		return 1
	}

	return strings.Compare(a.ID, b.ID)
}
