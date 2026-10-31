package uccmd_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/discord/internal/uccmd"
	"github.com/HectorVeraO/chatisimo/internal/uccalendar"
)

func TestTryMapErrorInMapsUCCalendarErrorsAndSkipsOthers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		loc         uccmd.Locale
		err         error
		wantMatched bool
		wantOutcome string
		wantSubstr  string
	}{
		{
			name:        "nil error is not matched",
			loc:         uccmd.LocaleENUS,
			err:         nil,
			wantMatched: false,
		},
		{
			name:        "unrelated error is not matched",
			loc:         uccmd.LocaleESCL,
			err:         errors.New("database timeout"),
			wantMatched: false,
		},
		{
			name:        "schedule unavailable in English",
			loc:         uccmd.LocaleENUS,
			err:         fmt.Errorf("check: %w", uccalendar.ErrScheduleUnavailable),
			wantMatched: true,
			wantOutcome: "rejected:schedule_unavailable",
			wantSubstr:  "schedule is not available",
		},
		{
			name:        "schedule unavailable in Spanish",
			loc:         uccmd.LocaleESCL,
			err:         uccalendar.ErrScheduleUnavailable,
			wantMatched: true,
			wantOutcome: "rejected:schedule_unavailable",
			wantSubstr:  "calendario de la UC no est\u00e1 disponible",
		},
		{
			name:        "mutually exclusive options",
			loc:         uccmd.LocaleESCL,
			err:         &uccalendar.InputError{Field: "query", Code: uccalendar.InputCodeMutuallyExclusive},
			wantMatched: true,
			wantOutcome: "rejected:invalid_input",
			wantSubstr:  "ventana o fecha, pero no ambas",
		},
		{
			name:        "invalid date",
			loc:         uccmd.LocaleENUS,
			err:         &uccalendar.InputError{Field: "date", Code: uccalendar.InputCodeInvalidDate},
			wantMatched: true,
			wantOutcome: "rejected:invalid_input",
			wantSubstr:  "Invalid date",
		},
		{
			name:        "invalid window",
			loc:         uccmd.LocaleENUS,
			err:         &uccalendar.InputError{Field: "window", Code: uccalendar.InputCodeUnknown},
			wantMatched: true,
			wantOutcome: "rejected:invalid_input",
			wantSubstr:  "Unknown time window",
		},
		{
			name:        "generic query InputError falls back to generic invalid input",
			loc:         uccmd.LocaleENUS,
			err:         &uccalendar.InputError{Field: "query", Code: uccalendar.InputCodeRequired},
			wantMatched: true,
			wantOutcome: "rejected:invalid_input",
			wantSubstr:  "Invalid command input.",
		},
		{
			name:        "settings or internal InputError field is not mapped as user rejection",
			loc:         uccmd.LocaleENUS,
			err:         &uccalendar.InputError{Field: "snowflake", Code: uccalendar.InputCodeInvalidSnowflake},
			wantMatched: false,
		},
		{
			name:        "bare ErrInvalidInput sentinel without user field is not mapped as user rejection",
			loc:         uccmd.LocaleESCL,
			err:         fmt.Errorf("wrapped: %w", uccalendar.ErrInvalidInput),
			wantMatched: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rejection, matched := uccmd.TryMapErrorIn(tt.loc, "req-123", tt.err)
			if matched != tt.wantMatched {
				t.Fatalf("matched = %v, want %v", matched, tt.wantMatched)
			}
			if !tt.wantMatched {
				return
			}
			if rejection.Outcome != tt.wantOutcome {
				t.Errorf("Outcome = %q, want %q", rejection.Outcome, tt.wantOutcome)
			}
			if rejection.Internal {
				t.Error("Internal = true, want false for domain rejections")
			}
			if !strings.Contains(rejection.Text, tt.wantSubstr) {
				t.Errorf("Text = %q, want substring %q", rejection.Text, tt.wantSubstr)
			}
		})
	}
}

func TestTryMapErrorDefaultsToEnglishWithoutRouterLocale(t *testing.T) {
	t.Parallel()

	rejection, matched := uccmd.TryMapError("req-456", uccalendar.ErrScheduleUnavailable)
	if !matched {
		t.Fatal("TryMapError did not match ErrScheduleUnavailable")
	}
	if !strings.Contains(rejection.Text, "schedule is not available") {
		t.Errorf("Text = %q, want English text", rejection.Text)
	}
}
