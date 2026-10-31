package fakediscord_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
)

const ucSchedulePath = "/apis/site/v2/sports/soccer/all/teams/885/schedule"

type espnScheduleDoc struct {
	Events []struct {
		ID     string `json:"id"`
		Date   string `json:"date"`
		Name   string `json:"name"`
		League struct {
			Name string `json:"name"`
		} `json:"league"`
		Competitions []struct {
			ID        string `json:"id"`
			Date      string `json:"date"`
			TimeValid bool   `json:"timeValid"`
			Venue     struct {
				FullName string `json:"fullName"`
			} `json:"venue"`
			Status struct {
				Type struct {
					State  string `json:"state"`
					Name   string `json:"name"`
					Detail string `json:"detail"`
				} `json:"type"`
			} `json:"status"`
			Competitors []struct {
				HomeAway string `json:"homeAway"`
				Team     struct {
					ID          string `json:"id"`
					DisplayName string `json:"displayName"`
					Name        string `json:"name"`
				} `json:"team"`
			} `json:"competitors"`
		} `json:"competitions"`
	} `json:"events"`
}

func TestUCBase(t *testing.T) {
	t.Parallel()

	unstarted := newFake(t, fakediscord.Options{})
	if got := unstarted.UCBase(); got != "" {
		t.Errorf("UCBase() before Start = %q, want empty", got)
	}

	started := startFake(t, fakediscord.Options{})
	want := started.URL() + "/apis/site/v2/sports/soccer"
	if got := started.UCBase(); got != want {
		t.Errorf("UCBase() = %q, want %q", got, want)
	}
}

func TestUCScheduleDefaultsToEmptyEvents(t *testing.T) {
	t.Parallel()
	srv := newFake(t, fakediscord.Options{})

	for _, path := range []string{ucSchedulePath, ucSchedulePath + "?fixture=true"} {
		t.Run(path, func(t *testing.T) {
			got := call(t, srv, http.MethodGet, path, "", "")

			if got.status != http.StatusOK {
				t.Fatalf("status = %d, want 200", got.status)
			}
			if contentType := got.header.Get("Content-Type"); contentType != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", contentType)
			}
			if string(got.body) != `{"events":[]}` {
				t.Errorf("body = %q, want %q", got.body, `{"events":[]}`)
			}
		})
	}
}

func TestSetUCFixturesSeparatesCompletedAndUpcomingAndShapesESPNJSON(t *testing.T) {
	t.Parallel()
	srv := newFake(t, fakediscord.Options{})

	fixtures := []fakediscord.UCFixture{
		{
			ID:           "701001",
			Date:         "2026-10-25T21:00Z",
			Opponent:     "Union Espanola",
			Home:         true,
			Venue:        "Claro Arena",
			TimeValid:    true,
			StatusDetail: "FT",
			Completed:    true,
		},
		{
			ID:           "701002",
			Date:         "2026-11-22T20:00Z",
			OpponentID:   "4321",
			Opponent:     "Rival FC",
			Home:         false,
			Venue:        "Estadio Nacional",
			Tournament:   "Copa Chile",
			TimeValid:    false,
			StatusDetail: "11/22 - TBD",
			Completed:    false,
		},
		{
			ID:           "701003",
			Date:         "2026-11-29T18:00Z",
			Opponent:     "Deportes Sur",
			Home:         true,
			Venue:        "Claro Arena",
			TimeValid:    true,
			StatusDetail: "Canceled",
			Completed:    false,
			Canceled:     true,
		},
	}
	srv.SetUCFixtures(fixtures)
	fixtures[0].Opponent = "Mutated By Caller"

	completedReply := call(t, srv, http.MethodGet, ucSchedulePath, "", "")
	if completedReply.status != http.StatusOK {
		t.Fatalf("completed status = %d, body %s", completedReply.status, completedReply.body)
	}
	var completedDoc espnScheduleDoc
	decodeJSON(t, completedReply.body, &completedDoc)
	if len(completedDoc.Events) != 1 {
		t.Fatalf("completed events = %d, want 1", len(completedDoc.Events))
	}

	past := completedDoc.Events[0]
	if past.ID != "701001" || past.Date != "2026-10-25T21:00Z" || past.League.Name != "Chilean Primera Division" {
		t.Errorf("completed event header = %+v", past)
	}
	if len(past.Competitions) != 1 {
		t.Fatalf("completed competitions = %d, want 1", len(past.Competitions))
	}
	pastComp := past.Competitions[0]
	if !pastComp.TimeValid || pastComp.Venue.FullName != "Claro Arena" {
		t.Errorf("completed competition = %+v", pastComp)
	}
	if pastComp.Status.Type.State != "post" || pastComp.Status.Type.Name != "STATUS_SCHEDULED" || pastComp.Status.Type.Detail != "FT" {
		t.Errorf("completed status = %+v", pastComp.Status.Type)
	}
	if len(pastComp.Competitors) != 2 {
		t.Fatalf("completed competitors = %d, want 2", len(pastComp.Competitors))
	}
	if pastComp.Competitors[0].HomeAway != "home" || pastComp.Competitors[0].Team.ID != "885" || pastComp.Competitors[0].Team.DisplayName != "Universidad Catolica" {
		t.Errorf("home competitor = %+v", pastComp.Competitors[0])
	}
	if pastComp.Competitors[1].HomeAway != "away" || pastComp.Competitors[1].Team.ID != "9999" || pastComp.Competitors[1].Team.DisplayName != "Union Espanola" {
		t.Errorf("away competitor = %+v", pastComp.Competitors[1])
	}

	upcomingReply := call(t, srv, http.MethodGet, ucSchedulePath+"?fixture=true", "", "")
	if upcomingReply.status != http.StatusOK {
		t.Fatalf("upcoming status = %d, body %s", upcomingReply.status, upcomingReply.body)
	}
	var upcomingDoc espnScheduleDoc
	decodeJSON(t, upcomingReply.body, &upcomingDoc)
	if len(upcomingDoc.Events) != 2 {
		t.Fatalf("upcoming events = %d, want 2", len(upcomingDoc.Events))
	}

	away := upcomingDoc.Events[0]
	if away.ID != "701002" || away.League.Name != "Copa Chile" {
		t.Errorf("upcoming away event = %+v", away)
	}
	awayComp := away.Competitions[0]
	if awayComp.TimeValid || awayComp.Venue.FullName != "Estadio Nacional" {
		t.Errorf("upcoming away competition = %+v", awayComp)
	}
	if awayComp.Status.Type.State != "pre" || awayComp.Status.Type.Name != "STATUS_SCHEDULED" || awayComp.Status.Type.Detail != "11/22 - TBD" {
		t.Errorf("upcoming away status = %+v", awayComp.Status.Type)
	}
	if awayComp.Competitors[0].HomeAway != "home" || awayComp.Competitors[0].Team.ID != "4321" || awayComp.Competitors[0].Team.Name != "Rival FC" {
		t.Errorf("upcoming home competitor = %+v", awayComp.Competitors[0])
	}
	if awayComp.Competitors[1].HomeAway != "away" || awayComp.Competitors[1].Team.ID != "885" || awayComp.Competitors[1].Team.Name != "Universidad Catolica" {
		t.Errorf("upcoming away competitor = %+v", awayComp.Competitors[1])
	}

	canceledComp := upcomingDoc.Events[1].Competitions[0]
	if canceledComp.Status.Type.State != "pre" || canceledComp.Status.Type.Name != "STATUS_CANCELED" || canceledComp.Status.Type.Detail != "Canceled" {
		t.Errorf("canceled status = %+v", canceledComp.Status.Type)
	}
}

func TestUCScheduleSupportsFaultInjection(t *testing.T) {
	t.Parallel()
	srv := newFake(t, fakediscord.Options{})

	if err := srv.FailNext("GET /all/teams/885/schedule", http.StatusServiceUnavailable, 0); err != nil {
		t.Fatalf("FailNext short route: %v", err)
	}
	first := call(t, srv, http.MethodGet, ucSchedulePath, "", "")
	if first.status != http.StatusServiceUnavailable {
		t.Fatalf("first status = %d, want 503", first.status)
	}
	recovered := call(t, srv, http.MethodGet, ucSchedulePath, "", "")
	if recovered.status != http.StatusOK {
		t.Fatalf("recovered status = %d, want 200", recovered.status)
	}

	queued := control(t, srv, http.MethodPost, "/faults",
		`{"route":"GET /apis/site/v2/sports/soccer/all/teams/885/schedule","status":429,"code":0}`)
	if queued.status != http.StatusNoContent {
		t.Fatalf("control fault = %d %s, want 204", queued.status, queued.body)
	}
	limited := call(t, srv, http.MethodGet, ucSchedulePath+"?fixture=true", "", "")
	if limited.status != http.StatusTooManyRequests {
		t.Fatalf("limited status = %d, want 429", limited.status)
	}
	afterLimit := call(t, srv, http.MethodGet, ucSchedulePath+"?fixture=true", "", "")
	if afterLimit.status != http.StatusOK {
		t.Fatalf("after limit status = %d, want 200", afterLimit.status)
	}
}

func TestControlUCFixturesAndReset(t *testing.T) {
	t.Parallel()
	srv := newFake(t, fakediscord.Options{})

	seedBody := `{"fixtures":[` +
		`{"id":"8001","date":"2026-10-18T19:00Z","opponent":"Club Norte","home":true,"venue":"Claro Arena","time_valid":true,"status_detail":"FT","completed":true},` +
		`{"id":"8002","date":"2026-11-01T21:00Z","opponent":"Club Costa","home":false,"venue":"Estadio Regional","time_valid":true,"status_detail":"Scheduled","completed":false}` +
		`]}`
	seeded := control(t, srv, http.MethodPost, "/uc-fixtures", seedBody)
	if seeded.status != http.StatusNoContent {
		t.Fatalf("seed uc-fixtures = %d %s, want 204", seeded.status, seeded.body)
	}

	var completedDoc espnScheduleDoc
	decodeJSON(t, call(t, srv, http.MethodGet, ucSchedulePath, "", "").body, &completedDoc)
	if len(completedDoc.Events) != 1 || completedDoc.Events[0].ID != "8001" {
		t.Fatalf("completed events after control seed = %+v, want [8001]", completedDoc.Events)
	}

	var upcomingDoc espnScheduleDoc
	decodeJSON(t, call(t, srv, http.MethodGet, ucSchedulePath+"?fixture=true", "", "").body, &upcomingDoc)
	if len(upcomingDoc.Events) != 1 || upcomingDoc.Events[0].ID != "8002" {
		t.Fatalf("upcoming events after control seed = %+v, want [8002]", upcomingDoc.Events)
	}

	var tooMany strings.Builder
	tooMany.WriteString(`{"fixtures":[`)
	for i := range 1001 {
		if i > 0 {
			tooMany.WriteByte(',')
		}
		fmt.Fprintf(&tooMany, `{"id":"%d"}`, i)
	}
	tooMany.WriteString(`]}`)

	badRequests := []struct {
		name string
		body string
		want int
	}{
		{"malformed JSON", `{`, http.StatusBadRequest},
		{"missing fixtures field", `{}`, http.StatusBadRequest},
		{"null fixtures", `{"fixtures":null}`, http.StatusBadRequest},
		{"fixtures not an array", `{"fixtures":"nope"}`, http.StatusBadRequest},
		{"over 1000 fixtures", tooMany.String(), http.StatusBadRequest},
		{"oversized body", `{"fixtures":[{"id":"` + strings.Repeat("x", 2<<20) + `"}]}`, http.StatusRequestEntityTooLarge},
	}
	for _, tc := range badRequests {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			got := control(t, srv, http.MethodPost, "/uc-fixtures", tc.body)
			if got.status != tc.want {
				t.Errorf("status = %d %s, want %d", got.status, got.body, tc.want)
			}
		})
	}

	decodeJSON(t, call(t, srv, http.MethodGet, ucSchedulePath+"?fixture=true", "", "").body, &upcomingDoc)
	if len(upcomingDoc.Events) != 1 || upcomingDoc.Events[0].ID != "8002" {
		t.Errorf("rejected control requests altered seeded fixtures: %+v", upcomingDoc.Events)
	}

	reset := control(t, srv, http.MethodPost, "/reset", "")
	if reset.status != http.StatusNoContent {
		t.Fatalf("reset = %d %s, want 204", reset.status, reset.body)
	}
	if body := string(call(t, srv, http.MethodGet, ucSchedulePath, "", "").body); body != `{"events":[]}` {
		t.Errorf("completed after reset = %q, want empty events", body)
	}
	if body := string(call(t, srv, http.MethodGet, ucSchedulePath+"?fixture=true", "", "").body); body != `{"events":[]}` {
		t.Errorf("upcoming after reset = %q, want empty events", body)
	}
}

func TestUCScheduleLogsDoNotLeakQueryStringsOrBodies(t *testing.T) {
	t.Parallel()
	var logs syncBuffer
	srv := newFake(t, fakediscord.Options{Logger: capturingLogger(&logs)})

	payload, err := json.Marshal(map[string]any{
		"fixtures": []fakediscord.UCFixture{
			{ID: "9001", Date: "2026-11-10T20:00Z", Opponent: "UnloggedOpponentProbe"},
		},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	seeded := control(t, srv, http.MethodPost, "/uc-fixtures", string(payload))
	if seeded.status != http.StatusNoContent {
		t.Fatalf("seed = %d", seeded.status)
	}
	fetched := call(t, srv, http.MethodGet, ucSchedulePath+"?fixture=true&probe=UnloggedQueryProbe", "", "")
	if fetched.status != http.StatusOK {
		t.Fatalf("fetch = %d", fetched.status)
	}

	logged := logs.String()
	for _, forbidden := range []string{"fixture=true", "UnloggedQueryProbe", "UnloggedOpponentProbe"} {
		if strings.Contains(logged, forbidden) {
			t.Errorf("logs leaked %q:\n%s", forbidden, logged)
		}
	}
}
