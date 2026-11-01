package capitime_test

import (
	"testing"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/capitime"
)

func TestParseArrivalMessageDetectsExplicitClocksAndSlang(t *testing.T) {
	loc := capitime.Santiago()
	// Sunday 2026-11-01 21:00:00 America/Santiago
	ref := time.Date(2026, 11, 1, 21, 0, 0, 0, loc)

	tests := []struct {
		name        string
		input       string
		wantDetect  bool
		wantImmed   bool
		wantClock   string
		wantDayDiff int
	}{
		{
			name:        "explicit clock with llego",
			input:       "22:30 llego",
			wantDetect:  true,
			wantImmed:   false,
			wantClock:   "22:30",
			wantDayDiff: 0,
		},
		{
			name:        "bare midnight clock rolls to next day when current time is 21:00",
			input:       "00:00",
			wantDetect:  true,
			wantImmed:   false,
			wantClock:   "00:00",
			wantDayDiff: 1,
		},
		{
			name:        "hasta las clock",
			input:       "hasta las 23:40",
			wantDetect:  true,
			wantImmed:   false,
			wantClock:   "23:40",
			wantDayDiff: 0,
		},
		{
			name:        "12-hour pm clock",
			input:       "llego a las 10pm",
			wantDetect:  true,
			wantImmed:   false,
			wantClock:   "22:00",
			wantDayDiff: 0,
		},
		{
			name:        "voy altiro immediate",
			input:       "voy altiro cabros",
			wantDetect:  true,
			wantImmed:   true,
			wantClock:   "21:00",
			wantDayDiff: 0,
		},
		{
			name:        "voy voy immediate",
			input:       "voy voy",
			wantDetect:  true,
			wantImmed:   true,
			wantClock:   "21:00",
			wantDayDiff: 0,
		},
		{
			name:        "toy ready immediate",
			input:       "toy ready mi king",
			wantDetect:  true,
			wantImmed:   true,
			wantClock:   "21:00",
			wantDayDiff: 0,
		},
		{
			name:        "bare ready immediate",
			input:       "ready",
			wantDetect:  true,
			wantImmed:   true,
			wantClock:   "21:00",
			wantDayDiff: 0,
		},
		{
			name:        "llegando immediate",
			input:       "llegando",
			wantDetect:  true,
			wantImmed:   true,
			wantClock:   "21:00",
			wantDayDiff: 0,
		},
		{
			name:        "lleganding immediate",
			input:       "lleganding",
			wantDetect:  true,
			wantImmed:   true,
			wantClock:   "21:00",
			wantDayDiff: 0,
		},
		{
			name:        "relative minutes",
			input:       "en 15 minutos llego",
			wantDetect:  true,
			wantImmed:   false,
			wantClock:   "21:15",
			wantDayDiff: 0,
		},
		{
			name:        "tomorrow morning",
			input:       "llego mañana en la mañana",
			wantDetect:  true,
			wantImmed:   false,
			wantClock:   "10:00",
			wantDayDiff: 1,
		},
		{
			name:        "next wednesday from sunday",
			input:       "llego el miércoles",
			wantDetect:  true,
			wantImmed:   false,
			wantClock:   "20:00",
			wantDayDiff: 3,
		},
		{
			name:        "self reference nickname Capi in third person",
			input:       "el Capi llega a las 22:15",
			wantDetect:  true,
			wantImmed:   false,
			wantClock:   "22:15",
			wantDayDiff: 0,
		},
		{
			name:        "self reference nickname Braulitros immediate",
			input:       "Braulitros llegando",
			wantDetect:  true,
			wantImmed:   true,
			wantClock:   "21:00",
			wantDayDiff: 0,
		},
		{
			name:        "self reference nickname Braulio",
			input:       "Braulio viene a las 23:00",
			wantDetect:  true,
			wantImmed:   false,
			wantClock:   "23:00",
			wantDayDiff: 0,
		},
		{
			name:        "self reference nickname Capis",
			input:       "Capis toy ready",
			wantDetect:  true,
			wantImmed:   true,
			wantClock:   "21:00",
			wantDayDiff: 0,
		},
		{
			name:        "spanglish hoy estare al night in pc",
			input:       "Hoy estaré al night in pc",
			wantDetect:  true,
			wantImmed:   false,
			wantClock:   "22:00",
			wantDayDiff: 0,
		},
		{
			name:        "mas rato me meto",
			input:       "más rato me meto al pc",
			wantDetect:  true,
			wantImmed:   false,
			wantClock:   "21:30",
			wantDayDiff: 0,
		},
		{
			name:       "rejects past tense ayer llegue",
			input:      "ayer llegué a las 22:30",
			wantDetect: false,
		},
		{
			name:       "rejects past tense ya llegue hace rato",
			input:      "ya llegué hace rato",
			wantDetect: false,
		},
		{
			name:       "rejects past tense anoche me conecte",
			input:      "anoche me conecté a las 23:00",
			wantDetect: false,
		},
		{
			name:       "rejects third person story about brother",
			input:      "mi hermano llega a las 22:30",
			wantDetect: false,
		},
		{
			name:       "rejects third person story about uber",
			input:      "el uber llega en 5 minutos",
			wantDetect: false,
		},
		{
			name:       "rejects third person story about named friend",
			input:      "Juan llega a las 22:00",
			wantDetect: false,
		},
		{
			name:       "rejects unrelated chat message",
			input:      "jaja que buena partida cabros",
			wantDetect: false,
		},
		{
			name:       "rejects empty message",
			input:      "   ",
			wantDetect: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := capitime.ParseArrivalMessage(tc.input, ref)
			if got.Detected != tc.wantDetect {
				t.Fatalf("ParseArrivalMessage(%q).Detected = %v, want %v", tc.input, got.Detected, tc.wantDetect)
			}
			if !tc.wantDetect {
				return
			}
			if got.Immediate != tc.wantImmed {
				t.Errorf("ParseArrivalMessage(%q).Immediate = %v, want %v", tc.input, got.Immediate, tc.wantImmed)
			}
			gotInLoc := got.ClaimedAt.In(loc)
			if gotClock := gotInLoc.Format("15:04"); gotClock != tc.wantClock {
				t.Errorf("ParseArrivalMessage(%q) clock = %q, want %q", tc.input, gotClock, tc.wantClock)
			}
			wantDate := ref.AddDate(0, 0, tc.wantDayDiff).Format("2006-01-02")
			if gotDate := gotInLoc.Format("2006-01-02"); gotDate != wantDate {
				t.Errorf("ParseArrivalMessage(%q) date = %q, want %q", tc.input, gotDate, wantDate)
			}
			if got.ClaimSummary == "" {
				t.Errorf("ParseArrivalMessage(%q) ClaimSummary is empty", tc.input)
			}
		})
	}
}
