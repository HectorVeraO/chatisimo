package giftexchange

import (
	"errors"
	"strconv"
)

// Callers branch on these with errors.Is. No error text contains a person ID, an identity
// subject, a name, a question text, a rule value supplied by the user or a pair.
var (
	ErrUnauthenticated     = errors.New("giftexchange: no authenticated actor")
	ErrNotFound            = errors.New("giftexchange: not found")
	ErrForbidden           = errors.New("giftexchange: not allowed for this caller")
	ErrInvalidInput        = errors.New("giftexchange: invalid input")
	ErrInvalidState        = errors.New("giftexchange: not allowed in the current state")
	ErrAlreadyParticipant  = errors.New("giftexchange: already a participant")
	ErrNotParticipant      = errors.New("giftexchange: not a participant")
	ErrExchangeFull        = errors.New("giftexchange: exchange is full")
	ErrTooFewParticipants  = errors.New("giftexchange: too few participants")
	ErrNoValidAssignment   = errors.New("giftexchange: exclusions leave no valid assignment")
	ErrUnknownRule         = errors.New("giftexchange: unknown rule")
	ErrRuleFixed           = errors.New("giftexchange: rule is not configurable")
	ErrRuleLocked          = errors.New("giftexchange: rule can no longer change")
	ErrQuestionsDisabled   = errors.New("giftexchange: questions are disabled")
	ErrQuestionClosed      = errors.New("giftexchange: question is closed")
	ErrAlreadyAnswered     = errors.New("giftexchange: question already answered")
	ErrRateLimited         = errors.New("giftexchange: too many questions")
	ErrRephraseUnavailable = errors.New("giftexchange: question rephrasing is unavailable")
	// ErrUndeliverable is returned (wrapped) by a Notifier when the addressee can never be
	// reached (Discord 50007, 50278). The notify worker then cancels the job instead of retrying.
	ErrUndeliverable = errors.New("giftexchange: notification undeliverable")
)

// InputErrorCode identifies one validation reason returned by InputError.
type InputErrorCode string

// Validation error codes returned by InputError.
const (
	InputCodeRequired            InputErrorCode = "required"
	InputCodeUnknown             InputErrorCode = "unknown"
	InputCodeEmpty               InputErrorCode = "empty"
	InputCodeTooLong             InputErrorCode = "too_long"
	InputCodeInvalidUTF8         InputErrorCode = "invalid_utf8"
	InputCodeInvalidText         InputErrorCode = "invalid_text"
	InputCodeControlChars        InputErrorCode = "control_chars"
	InputCodeLineBreaks          InputErrorCode = "line_breaks"
	InputCodeBidiChars           InputErrorCode = "bidi_chars"
	InputCodeNoVisibleChar       InputErrorCode = "no_visible_char"
	InputCodeNotCanonicalUUID    InputErrorCode = "not_canonical_uuid"
	InputCodeZeroUUID            InputErrorCode = "zero_uuid"
	InputCodeEditedViaExclusions InputErrorCode = "edited_via_exclusions"
	InputCodeInvalidInstant      InputErrorCode = "invalid_instant"
	InputCodeYearOutOfRange      InputErrorCode = "year_out_of_range"
	InputCodeInvalidDuration     InputErrorCode = "invalid_duration"
	InputCodeDurationOutOfRange  InputErrorCode = "duration_out_of_range"
	InputCodeNotOneOf            InputErrorCode = "not_one_of"
	InputCodeInvalidBool         InputErrorCode = "invalid_bool"
	InputCodeMustBeFuture        InputErrorCode = "must_be_future"
	InputCodeBeforeRevealAt      InputErrorCode = "before_reveal_at"
	InputCodeAfterDrawAt         InputErrorCode = "after_draw_at"
	InputCodeSameAsGiver         InputErrorCode = "same_as_giver"
)

// AllInputErrorCodes returns every validation error code the module can return.
func AllInputErrorCodes() []InputErrorCode {
	return []InputErrorCode{
		InputCodeRequired,
		InputCodeUnknown,
		InputCodeEmpty,
		InputCodeTooLong,
		InputCodeInvalidUTF8,
		InputCodeInvalidText,
		InputCodeControlChars,
		InputCodeLineBreaks,
		InputCodeBidiChars,
		InputCodeNoVisibleChar,
		InputCodeNotCanonicalUUID,
		InputCodeZeroUUID,
		InputCodeEditedViaExclusions,
		InputCodeInvalidInstant,
		InputCodeYearOutOfRange,
		InputCodeInvalidDuration,
		InputCodeDurationOutOfRange,
		InputCodeNotOneOf,
		InputCodeInvalidBool,
		InputCodeMustBeFuture,
		InputCodeBeforeRevealAt,
		InputCodeAfterDrawAt,
		InputCodeSameAsGiver,
	}
}

// InputError reports one invalid field. It matches ErrInvalidInput with errors.Is. Field is a
// fixed name ("name", "text", "rule", "draw_at", "deps.Pool", ...); Code is a machine-readable
// InputErrorCode, with Min, Max and Allowed holding any bounds.
type InputError struct {
	Field   string
	Code    InputErrorCode
	Min     int
	Max     int
	Allowed []string
}

// Error returns the field and the error code, never the rejected input.
func (e *InputError) Error() string {
	text := ErrInvalidInput.Error() + ": " + e.Field + " (" + string(e.Code) + ")"
	return text
}

// Is reports whether target is ErrInvalidInput.
func (e *InputError) Is(target error) bool {
	return target == ErrInvalidInput
}

// StateError reports an operation not allowed in the exchange's current state, so a client can
// explain a stale button. It matches ErrInvalidState.
type StateError struct {
	Op    string
	State State
}

// Error names the operation and the state.
func (e *StateError) Error() string {
	text := ErrInvalidState.Error() + ": " + e.Op + " in state " + string(e.State)
	return text
}

// Is reports whether target is ErrInvalidState.
func (e *StateError) Is(target error) bool {
	return target == ErrInvalidState
}

// RuleLockedError reports a configurable rule that cannot change in State. It matches
// ErrRuleLocked.
type RuleLockedError struct {
	Rule  RuleName
	State State
}

// Error names the rule and the state.
func (e *RuleLockedError) Error() string {
	text := ErrRuleLocked.Error() + ": " + string(e.Rule) + " in state " + string(e.State)
	return text
}

// Is reports whether target is ErrRuleLocked.
func (e *RuleLockedError) Is(target error) bool {
	return target == ErrRuleLocked
}

// InfeasibleError reports that no assignment satisfies the exclusions. Stranded counts the givers
// a maximum matching leaves without a recipient (a count only). It matches ErrNoValidAssignment.
type InfeasibleError struct {
	Stranded int
}

// Error carries the stranded count and nothing else.
func (e *InfeasibleError) Error() string {
	text := ErrNoValidAssignment.Error() + ": " + strconv.Itoa(e.Stranded) + " givers stranded"
	return text
}

// Is reports whether target is ErrNoValidAssignment.
func (e *InfeasibleError) Is(target error) bool {
	return target == ErrNoValidAssignment
}
