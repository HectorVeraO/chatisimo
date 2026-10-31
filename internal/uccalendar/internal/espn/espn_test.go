package espn_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"

	"go.uber.org/goleak"

	"github.com/HectorVeraO/chatisimo/internal/uccalendar/internal/espn"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func santiagoLoc(t *testing.T) *time.Location {
	t.Helper()

	loc, err := time.LoadLocation("America/Santiago")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}

	return loc
}

func TestNewValidation(t *testing.T) {
	t.Parallel()

	_, err := espn.New(nil, nil)
	if err == nil {
		t.Fatal("New(nil, nil) succeeded, want joined error")
	}

	msg := err.Error()
	if !strings.Contains(msg, "http client") || !strings.Contains(msg, "timezone") {
		t.Errorf("error %q did not aggregate both validation problems", msg)
	}
}

func TestFetchScheduleMergesDeduplicatesAndNormalizes(t *testing.T) {
	t.Parallel()

	completedJSON := `{
		"season": {"slug": "chi-primera"},
		"events": [
			{
				"id": "7001",
				"date": "2026-10-25T20:00Z",
				"timeValid": true,
				"league": {"name": "Liga de Primera"},
				"competitions": [{
					"id": "7001",
					"date": "2026-10-25T20:00Z",
					"timeValid": true,
					"venue": {"fullName": "Claro Arena"},
					"competitors": [
						{"id": "885", "homeAway": "home", "team": {"id": "885", "displayName": "Universidad Catolica"}},
						{"id": "884", "homeAway": "away", "team": {"id": "884", "displayName": "Colo-Colo"}}
					],
					"status": {"type": {"name": "STATUS_FINAL", "detail": "FT"}}
				}]
			},
			{
				"id": "7002",
				"date": "2026-11-01T00:00Z",
				"timeValid": false,
				"competitions": [{
					"id": "7002",
					"date": "2026-11-01T00:00Z",
					"timeValid": false,
					"venue": {"fullName": "Estadio Nacional"},
					"competitors": [
						{"id": "900", "homeAway": "home", "team": {"id": "900", "displayName": "Universidad de Chile"}},
						{"id": "885", "homeAway": "away", "team": {"id": "885", "displayName": "Universidad Catolica"}}
					],
					"status": {"type": {"name": "STATUS_SCHEDULED", "detail": "TBD"}}
				}]
			},
			{
				"id": "7003",
				"date": "2026-11-05T22:00Z",
				"timeValid": true,
				"competitions": [{
					"id": "7003",
					"date": "2026-11-05T22:00Z",
					"timeValid": true,
					"competitors": [
						{"id": "885", "homeAway": "home", "team": {"id": "885", "displayName": "Universidad Catolica"}},
						{"id": "901", "homeAway": "away", "team": {"id": "901", "displayName": "Palestino"}}
					],
					"status": {"type": {"name": "STATUS_CANCELED", "detail": "Canceled"}}
				}]
			}
		]
	}`

	fixturesJSON := `{
		"events": [
			{
				"id": "7002",
				"date": "2026-11-01T21:30:00Z",
				"timeValid": true,
				"league": {"name": "Copa Chile"},
				"competitions": [{
					"id": "7002",
					"date": "2026-11-01T21:30:00Z",
					"timeValid": true,
					"venue": {"fullName": "Estadio Nacional"},
					"competitors": [
						{"id": "900", "homeAway": "home", "team": {"id": "900", "name": "U. de Chile"}},
						{"id": "999", "homeAway": "away", "team": {"id": "999", "displayName": "Universidad Cat\u00f3lica"}}
					],
					"status": {"type": {"name": "STATUS_SCHEDULED", "detail": "Sun, Nov 1"}}
				}]
			},
			{
				"id": "7004",
				"date": "2026-11-08T00:00Z",
				"competitions": [{
					"id": "7004",
					"date": "2026-11-08T00:00Z",
					"timeValid": false,
					"venue": {"shortName": "El Teniente"},
					"competitors": [
						{"id": "885", "team": {"id": "885", "name": "U. Catolica"}},
						{"id": "902", "homeAway": "home", "team": {"id": "902", "name": "O'Higgins"}}
					],
					"status": {"type": {"name": "STATUS_SCHEDULED", "detail": "Sun, Nov 8 - TBD"}}
				}]
			},
			{
				"id": "7005",
				"date": "2026-11-12T20:00Z",
				"competitions": [{
					"id": "7005",
					"date": "2026-11-12T20:00Z",
					"competitors": [
						{"id": "910", "homeAway": "home", "team": {"id": "910", "name": "Everton"}},
						{"id": "911", "homeAway": "away", "team": {"id": "911", "name": "Audax"}}
					]
				}]
			}
		]
	}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apis/site/v2/sports/soccer/all/teams/885/schedule" {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("fixture") == "true" {
			_, _ = io.WriteString(w, fixturesJSON)
			return
		}

		_, _ = io.WriteString(w, completedJSON)
	}))
	defer srv.Close()

	loc := santiagoLoc(t)
	client, err := espn.New(srv.Client(), loc)
	if err != nil {
		t.Fatalf("espn.New: %v", err)
	}

	events, err := client.FetchSchedule(context.Background(), srv.URL+"/apis/site/v2/sports/soccer/")
	if err != nil {
		t.Fatalf("FetchSchedule: %v", err)
	}

	if len(events) != 3 {
		t.Fatalf("len(events) = %d, want 3 (got %+v)", len(events), events)
	}

	first := events[0]
	if first.ID != "7001" || !first.Home || first.TBD || first.Opponent != "Colo-Colo" {
		t.Errorf("events[0] = %+v, want confirmed home match 7001 vs Colo-Colo", first)
	}

	second := events[1]
	if second.ID != "7002" || second.TBD || second.Home {
		t.Errorf("events[1] = %+v, want fixture=true confirmed away override for 7002", second)
	}

	if second.Tournament != "Copa Chile" || second.Opponent != "U. de Chile" {
		t.Errorf("events[1] metadata = (%q, %q), want (Copa Chile, U. de Chile)", second.Tournament, second.Opponent)
	}

	if second.Kickoff.Hour() != 18 || second.Kickoff.Minute() != 30 {
		t.Errorf("events[1] kickoff in Santiago = %v, want 18:30 local (-03)", second.Kickoff)
	}

	third := events[2]
	if third.ID != "7004" || !third.TBD || third.Home {
		t.Errorf("events[2] = %+v, want TBD away match 7004", third)
	}

	if third.Year != 2026 || third.Month != time.November || third.Day != 8 {
		t.Errorf("events[2] TBD date = %04d-%02d-%02d, want 2026-11-08 without timezone shift", third.Year, third.Month, third.Day)
	}

	if !third.Kickoff.IsZero() {
		t.Errorf("events[2] TBD Kickoff = %v, want zero", third.Kickoff)
	}

	if third.Tournament != "Primera Division" {
		t.Errorf("events[2] fallback Tournament = %q, want Primera Division", third.Tournament)
	}
}

func TestFetchScheduleChronologicalSortingAcrossYearsMonthsAndSameDay(t *testing.T) {
	t.Parallel()

	fixturesJSON := `{
		"events": [
			{
				"id": "b-tbd",
				"date": "2026-11-15",
				"competitions": [{
					"competitors": [{"id": "885", "team": {"id": "885"}}]
				}]
			},
			{
				"id": "a-tbd",
				"date": "2026-11-15",
				"competitions": [{
					"competitors": [{"id": "885", "team": {"id": "885"}}]
				}]
			},
			{
				"id": "late-confirmed",
				"date": "2026-11-15T22:00Z",
				"timeValid": true,
				"competitions": [{
					"timeValid": true,
					"competitors": [{"id": "885", "homeAway": "home", "team": {"id": "885"}}]
				}]
			},
			{
				"id": "early-confirmed",
				"date": "2026-11-15T18:00Z",
				"timeValid": true,
				"competitions": [{
					"timeValid": true,
					"competitors": [{"id": "885", "homeAway": "home", "team": {"id": "885"}}]
				}]
			},
			{
				"id": "next-year",
				"date": "2027-01-10T20:00Z",
				"timeValid": true,
				"competitions": [{
					"timeValid": true,
					"competitors": [{"id": "885", "homeAway": "home", "team": {"id": "885"}}]
				}]
			},
			{
				"id": "prev-year",
				"date": "2025-12-01T20:00Z",
				"timeValid": true,
				"competitions": [{
					"timeValid": true,
					"competitors": [{"id": "885", "homeAway": "home", "team": {"id": "885"}}]
				}]
			}
		]
	}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("fixture") == "true" {
			_, _ = io.WriteString(w, fixturesJSON)
			return
		}

		_, _ = io.WriteString(w, `{"events":[]}`)
	}))
	defer srv.Close()

	client, err := espn.New(srv.Client(), santiagoLoc(t))
	if err != nil {
		t.Fatalf("espn.New: %v", err)
	}

	events, err := client.FetchSchedule(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("FetchSchedule: %v", err)
	}

	wantOrder := []string{"prev-year", "early-confirmed", "late-confirmed", "a-tbd", "b-tbd", "next-year"}
	if len(events) != len(wantOrder) {
		t.Fatalf("len(events) = %d, want %d", len(events), len(wantOrder))
	}

	for i, wantID := range wantOrder {
		if events[i].ID != wantID {
			t.Errorf("events[%d].ID = %q, want %q", i, events[i].ID, wantID)
		}
	}
}

func TestFetchScheduleErrorPaths(t *testing.T) {
	t.Parallel()

	loc := santiagoLoc(t)

	var nilClient *espn.Client
	_, err := nilClient.FetchSchedule(context.Background(), "http://127.0.0.1")
	if err == nil {
		t.Fatal("nilClient.FetchSchedule succeeded, want error")
	}

	baseClient, err := espn.New(&http.Client{Timeout: time.Second}, loc)
	if err != nil {
		t.Fatalf("espn.New: %v", err)
	}

	//nolint:staticcheck // testing nil context guard
	_, err = baseClient.FetchSchedule(nil, "http://127.0.0.1")
	if err == nil {
		t.Fatal("FetchSchedule(nil, ...) succeeded, want error")
	}

	_, err = baseClient.FetchSchedule(context.Background(), "   ")
	if err == nil {
		t.Fatal("FetchSchedule with empty URL succeeded, want error")
	}

	oversized := bytes.Repeat([]byte(" "), (2<<20)+16)
	cases := []struct {
		name         string
		fixtureError bool
		status       int
		body         []byte
	}{
		{
			name:   "non-200 on base schedule",
			status: http.StatusBadGateway,
			body:   []byte(`{"events":[]}`),
		},
		{
			name:         "non-200 on fixture schedule",
			fixtureError: true,
			status:       http.StatusServiceUnavailable,
			body:         []byte(`{"events":[]}`),
		},
		{
			name:   "response exceeds 2 MiB",
			status: http.StatusOK,
			body:   oversized,
		},
		{
			name:   "malformed JSON",
			status: http.StatusOK,
			body:   []byte(`{"events":[`),
		},
		{
			name:   "invalid kickoff timestamp",
			status: http.StatusOK,
			body: []byte(`{"events":[{"id":"1","date":"not-a-date","timeValid":true,` +
				`"competitions":[{"competitors":[{"id":"885","homeAway":"home","team":{"id":"885"}}]}]}]}`),
		},
		{
			name:   "invalid TBD calendar date",
			status: http.StatusOK,
			body: []byte(`{"events":[{"id":"2","date":"2026-02-31T00:00Z","timeValid":false,` +
				`"competitions":[{"competitors":[{"id":"885","homeAway":"home","team":{"id":"885"}}]}]}]}`),
		},
		{
			name:   "short TBD date",
			status: http.StatusOK,
			body: []byte(`{"events":[{"id":"3","date":"2026-10","timeValid":false,` +
				`"competitions":[{"competitors":[{"id":"885","team":{"id":"885"}}]}]}]}`),
		},
		{
			name:   "missing TBD date separators",
			status: http.StatusOK,
			body: []byte(`{"events":[{"id":"4","date":"2026/10/31","timeValid":false,` +
				`"competitions":[{"competitors":[{"id":"885","team":{"id":"885"}}]}]}]}`),
		},
		{
			name:   "non-digit TBD date",
			status: http.StatusOK,
			body: []byte(`{"events":[{"id":"5","date":"202a-10-31","timeValid":false,` +
				`"competitions":[{"competitors":[{"id":"885","team":{"id":"885"}}]}]}]}`),
		},
		{
			name:   "out-of-range TBD year",
			status: http.StatusOK,
			body: []byte(`{"events":[{"id":"6","date":"1999-10-31","timeValid":false,` +
				`"competitions":[{"competitors":[{"id":"885","team":{"id":"885"}}]}]}]}`),
		},
		{
			name:   "out-of-range TBD month",
			status: http.StatusOK,
			body: []byte(`{"events":[{"id":"7","date":"2026-13-01","timeValid":false,` +
				`"competitions":[{"competitors":[{"id":"885","team":{"id":"885"}}]}]}]}`),
		},
		{
			name:   "empty event date",
			status: http.StatusOK,
			body: []byte(`{"events":[{"id":"8","date":"","timeValid":false,` +
				`"competitions":[{"competitors":[{"id":"885","team":{"id":"885"}}]}]}]}`),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				isFixture := r.URL.Query().Get("fixture") == "true"
				if tc.fixtureError && !isFixture {
					w.WriteHeader(http.StatusOK)
					_, _ = io.WriteString(w, `{"events":[]}`)

					return
				}

				w.WriteHeader(tc.status)
				_, _ = w.Write(tc.body)
			}))
			defer srv.Close()

			c, newErr := espn.New(srv.Client(), loc)
			if newErr != nil {
				t.Fatalf("espn.New: %v", newErr)
			}

			_, fetchErr := c.FetchSchedule(context.Background(), srv.URL)
			if fetchErr == nil {
				t.Fatal("FetchSchedule succeeded, want error")
			}
		})
	}
}
