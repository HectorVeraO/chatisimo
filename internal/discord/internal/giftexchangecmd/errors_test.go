package giftexchangecmd_test

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/discord/internal/giftexchangecmd"
	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

const testReference = "01a16eec-90c4-701c-a17c-6d3c5a3b3c1d"

func TestMapErrorShowsTheTextAndOutcomeOfEveryModuleError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		err          error
		wantOutcome  string
		wantContains string
	}{
		{"not found", giftexchange.ErrNotFound, "rejected:not_found", "That exchange does not exist here."},
		{"forbidden", giftexchange.ErrForbidden, "rejected:forbidden", "You are not allowed to do that in this exchange."},
		{"already participant", giftexchange.ErrAlreadyParticipant, "rejected:already_participant", "You are already in this exchange."},
		{"not participant", giftexchange.ErrNotParticipant, "rejected:not_participant", "You are not in this exchange."},
		{"full", giftexchange.ErrExchangeFull, "rejected:full", strconv.Itoa(giftexchange.MaxParticipants)},
		{"too few", giftexchange.ErrTooFewParticipants, "rejected:too_few", strconv.Itoa(giftexchange.MinParticipants)},
		{"infeasible", &giftexchange.InfeasibleError{Stranded: 2}, "rejected:infeasible", "2"},
		{"unknown rule", giftexchange.ErrUnknownRule, "rejected:rule", "Unknown rule."},
		{"unknown rule from the parser", unknownRuleFromParser(), "rejected:rule", "Unknown rule."},
		{"fixed rule", giftexchange.ErrRuleFixed, "rejected:rule", "This rule can't be changed."},
		{
			"locked rule",
			&giftexchange.RuleLockedError{Rule: giftexchange.RuleDrawAt, State: giftexchange.StateDrawn},
			"rejected:rule", "drawn",
		},
		{"questions off", giftexchange.ErrQuestionsDisabled, "rejected:questions_off", "The organizer turned off questions for this exchange."},
		{"rate limited", giftexchange.ErrRateLimited, "rejected:rate_limited", "You reached the limit of 20 questions per day for this exchange."},
		{"rephrase unavailable", giftexchange.ErrRephraseUnavailable, "rejected:rephrase_unavailable", "Could not rephrase your question right now. Your question was not sent; please try again."},
		{"question closed", giftexchange.ErrQuestionClosed, "rejected:question", "This question is closed and can no longer be answered."},
		{"already answered", giftexchange.ErrAlreadyAnswered, "rejected:question", "You already answered this question."},
		{
			"invalid state",
			&giftexchange.StateError{Op: "draw", State: giftexchange.StateRevealed},
			"rejected:state", "revealed",
		},
		{"unauthenticated", giftexchange.ErrUnauthenticated, "error", testReference},
		{"anything else", errors.New("disk on fire"), "error", testReference},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := giftexchangecmd.MapError(testReference, fmt.Errorf("wrapped: %w", tt.err))

			if got.Outcome != tt.wantOutcome {
				t.Errorf("outcome = %q, want %q", got.Outcome, tt.wantOutcome)
			}
			if tt.wantContains == "" || !strings.Contains(got.Text, tt.wantContains) {
				t.Errorf("text %q does not contain %q", got.Text, tt.wantContains)
			}
			if got.Internal != (tt.wantOutcome == "error") {
				t.Errorf("Internal = %v for outcome %s", got.Internal, got.Outcome)
			}
		})
	}
}

func TestMapErrorListsEveryInvalidFieldAndEscapesThem(t *testing.T) {
	t.Parallel()

	joined := errors.Join(
		&giftexchange.InputError{Field: "draw_at", Code: giftexchange.InputCodeMustBeFuture},
		fmt.Errorf("outer: %w", errors.Join(&giftexchange.InputError{Field: "name", Code: giftexchange.InputCodeRequired})),
	)

	got := giftexchangecmd.MapError(testReference, joined)

	if got.Outcome != "rejected:invalid_input" {
		t.Errorf("outcome = %s", got.Outcome)
	}
	for _, want := range []string{`draw\_at must be in the future`, "name is required"} {
		if !strings.Contains(got.Text, want) {
			t.Errorf("text %q lacks %q", got.Text, want)
		}
	}
}

func TestMapErrorCapsALongListOfInvalidFields(t *testing.T) {
	t.Parallel()

	var problems []error
	for range 60 {
		problems = append(problems, &giftexchange.InputError{Field: "rule", Code: giftexchange.InputCodeUnknown})
	}

	got := giftexchangecmd.MapError(testReference, errors.Join(problems...))

	const maxLines = 25
	if lines := strings.Count(got.Text, "\n"); lines > maxLines {
		t.Errorf("%d lines in the reply for 60 problems, want a bounded list", lines)
	}
	if !strings.Contains(got.Text, "40") {
		t.Errorf("text does not say how many are left: %q", got.Text)
	}
}

func TestMapErrorTextNeverHoldsTheErrorString(t *testing.T) {
	t.Parallel()

	secret := "snowflake 100000000000000001"
	got := giftexchangecmd.MapError(testReference, fmt.Errorf("%s: %w", secret, giftexchange.ErrForbidden))

	if strings.Contains(got.Text, secret) {
		t.Errorf("the reply holds the error string: %q", got.Text)
	}
}

func TestGuildNameOrUnknown(t *testing.T) {
	t.Parallel()

	if got := giftexchangecmd.GuildNameOrUnknown(""); got == "" {
		t.Error("an empty guild name gives an empty text")
	}
	if got := giftexchangecmd.GuildNameOrUnknown("Test server"); got != "Test server" {
		t.Errorf("name = %q", got)
	}
	if got := giftexchangecmd.GuildNameOrUnknownIn(giftexchangecmd.LocaleESCL, ""); got != "servidor desconocido" {
		t.Errorf("GuildNameOrUnknownIn(es-CL, empty) = %q, want servidor desconocido", got)
	}
	if got := giftexchangecmd.GuildNameOrUnknownIn("fr", ""); got != "unknown server" {
		t.Errorf("GuildNameOrUnknownIn(fr, empty) = %q, want fallback unknown server", got)
	}
}

func TestMapErrorInLocalizesAndFallsBackToEnglish(t *testing.T) {
	t.Parallel()

	es := giftexchangecmd.MapErrorIn(giftexchangecmd.LocaleESCL, testReference, giftexchange.ErrNotFound)
	if es.Outcome != "rejected:not_found" || es.Text != "Ese intercambio no existe aquí." {
		t.Errorf("MapErrorIn(es-CL) = %+v", es)
	}

	fallback := giftexchangecmd.MapErrorIn("pt-BR", testReference, giftexchange.ErrNotFound)
	if fallback.Outcome != "rejected:not_found" || fallback.Text != "That exchange does not exist here." {
		t.Errorf("MapErrorIn(pt-BR) = %+v", fallback)
	}
}

func TestNewReportsEveryMissingDependency(t *testing.T) {
	t.Parallel()

	_, err := giftexchangecmd.New(nil, nil, nil, nil, nil)
	if err == nil {
		t.Fatal("New accepted nil dependencies")
	}
	for _, want := range []string{"service", "guild name", "clock", "location", "logger"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}

func unknownRuleFromParser() error {
	_, err := giftexchange.ParseRuleName("no-such-rule")
	return fmt.Errorf("parse rule: %w", err)
}

type inputErrorSample struct {
	err        *giftexchange.InputError
	wantReason string
}

func sampleInputErrorForCode(code giftexchange.InputErrorCode) inputErrorSample {
	switch code {
	case giftexchange.InputCodeRequired:
		return inputErrorSample{
			err:        &giftexchange.InputError{Field: "name", Code: code},
			wantReason: "is required",
		}
	case giftexchange.InputCodeUnknown:
		return inputErrorSample{
			err:        &giftexchange.InputError{Field: "rule", Code: code},
			wantReason: "is unknown",
		}
	case giftexchange.InputCodeEmpty:
		return inputErrorSample{
			err:        &giftexchange.InputError{Field: "name", Code: code},
			wantReason: "is empty",
		}
	case giftexchange.InputCodeTooLong:
		return inputErrorSample{
			err: &giftexchange.InputError{
				Field: "name",
				Code:  code,
				Max:   giftexchange.MaxNameRunes,
			},
			wantReason: "is too long",
		}
	case giftexchange.InputCodeInvalidUTF8:
		return inputErrorSample{
			err:        &giftexchange.InputError{Field: "name", Code: code},
			wantReason: "is not valid UTF-8",
		}
	case giftexchange.InputCodeInvalidText:
		return inputErrorSample{
			err:        &giftexchange.InputError{Field: "name", Code: code},
			wantReason: "is not valid text",
		}
	case giftexchange.InputCodeControlChars:
		return inputErrorSample{
			err:        &giftexchange.InputError{Field: "name", Code: code},
			wantReason: "contains control characters",
		}
	case giftexchange.InputCodeLineBreaks:
		return inputErrorSample{
			err:        &giftexchange.InputError{Field: "name", Code: code},
			wantReason: "contains line breaks",
		}
	case giftexchange.InputCodeBidiChars:
		return inputErrorSample{
			err:        &giftexchange.InputError{Field: "name", Code: code},
			wantReason: "contains bidirectional control characters",
		}
	case giftexchange.InputCodeNoVisibleChar:
		return inputErrorSample{
			err:        &giftexchange.InputError{Field: "name", Code: code},
			wantReason: "has no visible character",
		}
	case giftexchange.InputCodeNotCanonicalUUID:
		return inputErrorSample{
			err:        &giftexchange.InputError{Field: "exchange_id", Code: code},
			wantReason: "is not a canonical uuid",
		}
	case giftexchange.InputCodeZeroUUID:
		return inputErrorSample{
			err:        &giftexchange.InputError{Field: "exchange_id", Code: code},
			wantReason: "must not be the zero uuid",
		}
	case giftexchange.InputCodeEditedViaExclusions:
		return inputErrorSample{
			err:        &giftexchange.InputError{Field: "rule", Code: code},
			wantReason: "is edited through the exclusion methods",
		}
	case giftexchange.InputCodeInvalidInstant:
		return inputErrorSample{
			err:        &giftexchange.InputError{Field: "draw_at", Code: code},
			wantReason: "is not unset or an RFC 3339 time with an offset",
		}
	case giftexchange.InputCodeYearOutOfRange:
		return inputErrorSample{
			err: &giftexchange.InputError{
				Field: "draw_at",
				Code:  code,
				Min:   2000,
				Max:   9999,
			},
			wantReason: "is outside the years 2000 to 9999",
		}
	case giftexchange.InputCodeInvalidDuration:
		return inputErrorSample{
			err:        &giftexchange.InputError{Field: "reminder_lead", Code: code},
			wantReason: "is not a duration such as 0, 36h, 90m, 3d or 1d12h",
		}
	case giftexchange.InputCodeDurationOutOfRange:
		return inputErrorSample{
			err:        &giftexchange.InputError{Field: "reminder_lead", Code: code},
			wantReason: "is outside 0 to 14 days in whole seconds",
		}
	case giftexchange.InputCodeNotOneOf:
		return inputErrorSample{
			err: &giftexchange.InputError{
				Field:   "dropout_policy",
				Code:    code,
				Allowed: []string{"patch", "redraw"},
			},
			wantReason: "is not one of patch, redraw",
		}
	case giftexchange.InputCodeInvalidBool:
		return inputErrorSample{
			err:        &giftexchange.InputError{Field: "avoid_last_round", Code: code},
			wantReason: "is not on, off, true or false",
		}
	case giftexchange.InputCodeMustBeFuture:
		return inputErrorSample{
			err:        &giftexchange.InputError{Field: "draw_at", Code: code},
			wantReason: "must be in the future",
		}
	case giftexchange.InputCodeBeforeRevealAt:
		return inputErrorSample{
			err:        &giftexchange.InputError{Field: "draw_at", Code: code},
			wantReason: "must be before reveal_at",
		}
	case giftexchange.InputCodeAfterDrawAt:
		return inputErrorSample{
			err:        &giftexchange.InputError{Field: "reveal_at", Code: code},
			wantReason: "must be after draw_at",
		}
	case giftexchange.InputCodeSameAsGiver:
		return inputErrorSample{
			err:        &giftexchange.InputError{Field: "recipient", Code: code},
			wantReason: "must differ from the giver",
		}
	default:
		return inputErrorSample{
			err: &giftexchange.InputError{Field: "name", Code: code},
		}
	}
}

func TestMapErrorInRendersEveryInputErrorCodeInBothLocales(t *testing.T) {
	t.Parallel()

	codes := giftexchange.AllInputErrorCodes()
	if len(codes) == 0 {
		t.Fatal("AllInputErrorCodes returned no codes")
	}

	for _, code := range codes {
		t.Run(string(code), func(t *testing.T) {
			t.Parallel()

			sample := sampleInputErrorForCode(code)

			en := giftexchangecmd.MapErrorIn(giftexchangecmd.LocaleENUS, testReference, sample.err)
			es := giftexchangecmd.MapErrorIn(giftexchangecmd.LocaleESCL, testReference, sample.err)

			if en.Outcome != "rejected:invalid_input" || es.Outcome != "rejected:invalid_input" {
				t.Fatalf("outcomes = %q, %q; want rejected:invalid_input", en.Outcome, es.Outcome)
			}
			if strings.Contains(en.Text, "invalid input") {
				t.Errorf("en rendered fallback for code %q: %q", code, en.Text)
			}
			if strings.Contains(es.Text, "entrada inválida") {
				t.Errorf("es-CL rendered fallback for code %q: %q", code, es.Text)
			}
			if en.Text == es.Text {
				t.Errorf("code %q rendered identical text in en-US and es-CL: %q", code, en.Text)
			}
			if sample.wantReason == "" {
				t.Fatalf("sampleInputErrorForCode(%q) is missing expected English wantReason", code)
			}
			wantENLine := "- " + strings.ReplaceAll(sample.err.Field+" "+sample.wantReason, "_", `\_`)
			if !strings.Contains(en.Text, wantENLine) {
				t.Errorf("en text %q does not contain %q", en.Text, wantENLine)
			}
		})
	}
}

func TestMapErrorInRendersAggregatedInputErrorsAndFieldNamesInBothLocales(t *testing.T) {
	t.Parallel()

	joined := errors.Join(
		&giftexchange.InputError{
			Field: "draw_at",
			Code:  giftexchange.InputCodeMustBeFuture,
		},
		&giftexchange.InputError{
			Field: "reveal_at",
			Code:  giftexchange.InputCodeAfterDrawAt,
		},
		&giftexchange.InputError{
			Field: "reminder_lead",
			Code:  giftexchange.InputCodeDurationOutOfRange,
		},
		&giftexchange.InputError{
			Field:   "dropout_policy",
			Code:    giftexchange.InputCodeNotOneOf,
			Allowed: []string{"patch", "redraw"},
		},
		&giftexchange.InputError{
			Field: "name",
			Code:  giftexchange.InputCodeTooLong,
			Max:   giftexchange.MaxNameRunes,
		},
	)

	en := giftexchangecmd.MapErrorIn(giftexchangecmd.LocaleENUS, testReference, joined)
	for _, want := range []string{
		"Not saved:",
		`- draw\_at must be in the future`,
		`- reveal\_at must be after draw\_at`,
		`- reminder\_lead is outside 0 to 14 days in whole seconds`,
		`- dropout\_policy is not one of patch, redraw`,
		"- name is too long",
	} {
		if !strings.Contains(en.Text, want) {
			t.Errorf("en text %q lacks %q", en.Text, want)
		}
	}

	es := giftexchangecmd.MapErrorIn(giftexchangecmd.LocaleESCL, testReference, joined)
	for _, want := range []string{
		"No se guardó:",
		"- fecha de sorteo debe estar en el futuro",
		"- fecha de revelación debe ser posterior a la fecha de sorteo",
		"- recordatorio previo está fuera del rango de 0 a 14 días en segundos enteros",
		"- política ante bajas no es uno de patch, redraw",
		"- nombre es demasiado largo",
	} {
		if !strings.Contains(es.Text, want) {
			t.Errorf("es-CL text %q lacks %q", es.Text, want)
		}
	}
}
