package uccalendar

import "errors"

// Sentinel errors returned by Service and parsers. Callers branch on them with errors.Is and
// errors.As; error strings never contain raw user input, guild IDs, user IDs, or channel IDs.
var (
	// ErrInvalidInput reports an invalid parameter, setting value, query window, or date.
	ErrInvalidInput = errors.New("uccalendar: invalid input")
	// ErrForbidden reports that the caller lacks server-management permission for guild config.
	ErrForbidden = errors.New("uccalendar: forbidden")
	// ErrScheduleUnavailable reports that no ESPN schedule sync has succeeded yet.
	ErrScheduleUnavailable = errors.New("uccalendar: schedule is unavailable")
)

// InputCode identifies one machine-readable validation failure reason on InputError.
type InputCode string

// Validation codes returned on InputError.Code.
const (
	InputCodeRequired           InputCode = "required"
	InputCodeEmpty              InputCode = "empty"
	InputCodeUnknown            InputCode = "unknown"
	InputCodeMutuallyExclusive  InputCode = "mutually_exclusive"
	InputCodeInvalidDate        InputCode = "invalid_date"
	InputCodeYearOutOfRange     InputCode = "year_out_of_range"
	InputCodeInvalidClock       InputCode = "invalid_clock"
	InputCodeInvalidDuration    InputCode = "invalid_duration"
	InputCodeDurationOutOfRange InputCode = "duration_out_of_range"
	InputCodeInvalidSnowflake   InputCode = "invalid_snowflake"
	InputCodeInvalidURL         InputCode = "invalid_url"
)

// InputError reports one invalid field or parameter. It matches ErrInvalidInput via errors.Is.
type InputError struct {
	Field string
	Code  InputCode
}

// Error returns the field name and machine-readable code without the rejected input value.
func (e *InputError) Error() string {
	text := ErrInvalidInput.Error() + ": " + e.Field + " (" + string(e.Code) + ")"
	return text
}

// Is reports whether target is ErrInvalidInput.
func (e *InputError) Is(target error) bool {
	return target == ErrInvalidInput
}
