package fakediscord

import (
	"net/http"
	"slices"
)

const (
	ucTeamID            = "885"
	ucTeamName          = "Universidad Catolica"
	defaultUCOpponentID = "9999"
	defaultUCTournament = "Chilean Primera Division"
)

// UCFixture configures one synthetic match served by the fake ESPN UC schedule endpoint.
type UCFixture struct {
	ID   string `json:"id"`
	Date string `json:"date"`
	// OpponentID defaults to "9999" when empty.
	OpponentID string `json:"opponent_id"`
	Opponent   string `json:"opponent"`
	// Home reports whether Universidad Catolica (team 885) is the home side.
	Home  bool   `json:"home"`
	Venue string `json:"venue"`
	// Tournament defaults to "Chilean Primera Division" when empty.
	Tournament   string `json:"tournament"`
	TimeValid    bool   `json:"time_valid"`
	StatusDetail string `json:"status_detail"`
	// Completed serves the match from the base schedule endpoint instead of ?fixture=true.
	Completed bool `json:"completed"`
	Canceled  bool `json:"canceled"`
}

// SetUCFixtures replaces the synthetic UC schedule fixtures served by the fake.
func (s *Server) SetUCFixtures(fixtures []UCFixture) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ucFixtures = slices.Clone(fixtures)
}

type ucScheduleResponseJSON struct {
	Events []ucScheduleEventJSON `json:"events"`
}

type ucScheduleEventJSON struct {
	ID           string              `json:"id"`
	Date         string              `json:"date"`
	Name         string              `json:"name"`
	League       ucLeagueJSON        `json:"league"`
	Competitions []ucCompetitionJSON `json:"competitions"`
}

type ucLeagueJSON struct {
	Name string `json:"name"`
}

type ucCompetitionJSON struct {
	ID          string             `json:"id"`
	Date        string             `json:"date"`
	TimeValid   bool               `json:"timeValid"`
	Venue       ucVenueJSON        `json:"venue"`
	Status      ucStatusJSON       `json:"status"`
	Competitors []ucCompetitorJSON `json:"competitors"`
}

type ucVenueJSON struct {
	FullName string `json:"fullName"`
}

type ucStatusJSON struct {
	Type ucStatusTypeJSON `json:"type"`
}

type ucStatusTypeJSON struct {
	State  string `json:"state"`
	Name   string `json:"name"`
	Detail string `json:"detail"`
}

type ucCompetitorJSON struct {
	HomeAway string     `json:"homeAway"`
	Team     ucTeamJSON `json:"team"`
}

type ucTeamJSON struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Name        string `json:"name"`
}

func (s *Server) handleGetUCSchedule(w http.ResponseWriter, r *http.Request) {
	wantUpcoming := r.URL.Query().Get("fixture") == "true"

	s.mu.Lock()
	fixtures := slices.Clone(s.ucFixtures)
	s.mu.Unlock()

	events := make([]ucScheduleEventJSON, 0, len(fixtures))
	for _, fixture := range fixtures {
		isMatchingBucket := fixture.Completed != wantUpcoming
		if !isMatchingBucket {
			continue
		}
		events = append(events, fixtureToEvent(fixture))
	}

	writeJSON(w, http.StatusOK, ucScheduleResponseJSON{Events: events})
}

func fixtureToEvent(f UCFixture) ucScheduleEventJSON {
	opponentID := f.OpponentID
	if opponentID == "" {
		opponentID = defaultUCOpponentID
	}

	tournament := f.Tournament
	if tournament == "" {
		tournament = defaultUCTournament
	}

	ucTeam := ucTeamJSON{
		ID:          ucTeamID,
		DisplayName: ucTeamName,
		Name:        ucTeamName,
	}
	opponentTeam := ucTeamJSON{
		ID:          opponentID,
		DisplayName: f.Opponent,
		Name:        f.Opponent,
	}

	homeTeam := opponentTeam
	awayTeam := ucTeam
	if f.Home {
		homeTeam = ucTeam
		awayTeam = opponentTeam
	}

	state := "pre"
	if f.Completed {
		state = "post"
	}

	statusName := "STATUS_SCHEDULED"
	if f.Canceled {
		statusName = "STATUS_CANCELED"
	}

	eventName := homeTeam.DisplayName + " vs " + awayTeam.DisplayName
	event := ucScheduleEventJSON{
		ID:     f.ID,
		Date:   f.Date,
		Name:   eventName,
		League: ucLeagueJSON{Name: tournament},
		Competitions: []ucCompetitionJSON{
			{
				ID:        f.ID,
				Date:      f.Date,
				TimeValid: f.TimeValid,
				Venue:     ucVenueJSON{FullName: f.Venue},
				Status: ucStatusJSON{
					Type: ucStatusTypeJSON{
						State:  state,
						Name:   statusName,
						Detail: f.StatusDetail,
					},
				},
				Competitors: []ucCompetitorJSON{
					{HomeAway: "home", Team: homeTeam},
					{HomeAway: "away", Team: awayTeam},
				},
			},
		},
	}

	return event
}
