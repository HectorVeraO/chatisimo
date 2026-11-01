package capitime

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidInput indicates caller input failed validation.
	ErrInvalidInput = errors.New("capitime: invalid input")

	// ErrForbidden indicates the caller lacks ManageGuild or Administrator permission to mutate
	// per-guild Capi Time configuration.
	ErrForbidden = errors.New("capitime: forbidden")
)

// ErrorCode classifies a validation failure on caller input so adapters can localize the reply
// without matching error text.
type ErrorCode string

// Validation error codes carried by InputError.
const (
	ErrCodeSnowflake   ErrorCode = "snowflake"
	ErrCodeLocale      ErrorCode = "locale"
	ErrCodeClearTarget ErrorCode = "clear_target"
	ErrCodeEmptyUpdate ErrorCode = "empty_update"
	ErrCodeChannelCap  ErrorCode = "channel_cap"
	ErrCodeSetting     ErrorCode = "setting"
)

// InputError is a validation error wrapping ErrInvalidInput and carrying a structured ErrorCode.
type InputError struct {
	Code   ErrorCode
	Detail string
	Cause  error
}

// Error returns a fixed validation description without echoing untrusted caller text.
func (e *InputError) Error() string {
	if e == nil {
		return ErrInvalidInput.Error()
	}
	if e.Detail == "" {
		return fmt.Sprintf("%s (%s)", ErrInvalidInput.Error(), e.Code)
	}

	return fmt.Sprintf("%s (%s): %s", ErrInvalidInput.Error(), e.Code, e.Detail)
}

// Unwrap exposes ErrInvalidInput and any underlying cause to errors.Is and errors.As.
func (e *InputError) Unwrap() []error {
	if e == nil {
		return nil
	}
	if e.Cause == nil {
		return []error{ErrInvalidInput}
	}

	return []error{ErrInvalidInput, e.Cause}
}

func newInputError(code ErrorCode, detail string, cause error) error {
	return &InputError{
		Code:   code,
		Detail: detail,
		Cause:  cause,
	}
}
