package capitimecmd

import (
	"errors"

	"github.com/HectorVeraO/chatisimo/internal/capitime"
	"github.com/HectorVeraO/chatisimo/internal/discord/internal/router"
)

const (
	outcomeInvalidInput = "rejected:invalid_input"
	outcomeForbidden    = "rejected:forbidden"
	outcomeGuildOnly    = "rejected:guild_only"
)

// Adapter-level validation errors returned by Capi Time command handlers.
var (
	// ErrGuildOnly reports that /capitime-config was invoked outside a server.
	ErrGuildOnly = errors.New("capitimecmd: command works only in a server")
	// ErrInvalidTargetUser reports that the selected target user is not a non-bot member of the server.
	ErrInvalidTargetUser = errors.New("capitimecmd: target user is not a valid non-bot member of this server")
	// ErrInvalidChannel reports that the selected channel is not a text channel of the server.
	ErrInvalidChannel = errors.New("capitimecmd: channel is not a text channel of this server")
)

// TryMapError maps a capitime domain or adapter error to a localized router.Rejection using the
// locale attached to err by the router (or es-CL when none is attached). It returns false when err
// is not a Capi Time error so the adapter can chain other feature mappers.
func TryMapError(requestID string, err error) (router.Rejection, bool) {
	loc := ResolveLocale(router.ErrorLocale(err))
	return TryMapErrorIn(loc, requestID, err)
}

// TryMapErrorIn maps a capitime domain or adapter error to a router.Rejection in loc. It returns
// false for any non-Capi Time error.
func TryMapErrorIn(loc Locale, _ string, err error) (router.Rejection, bool) {
	if err == nil {
		return router.Rejection{}, false
	}

	switch {
	case errors.Is(err, ErrGuildOnly):
		return router.Rejection{
			Text:    T(loc, textErrGuildOnly),
			Outcome: outcomeGuildOnly,
		}, true
	case errors.Is(err, capitime.ErrForbidden):
		return router.Rejection{
			Text:    T(loc, textErrForbidden),
			Outcome: outcomeForbidden,
		}, true
	case errors.Is(err, ErrInvalidTargetUser), errors.Is(err, ErrInvalidChannel):
		return router.Rejection{
			Text:    T(loc, textErrInvalidSnowflake),
			Outcome: outcomeInvalidInput,
		}, true
	}

	var inputErr *capitime.InputError
	if errors.As(err, &inputErr) {
		msgID := mapInputErrorCode(inputErr.Code)
		return router.Rejection{
			Text:    T(loc, msgID),
			Outcome: outcomeInvalidInput,
		}, true
	}

	if errors.Is(err, capitime.ErrInvalidInput) {
		return router.Rejection{
			Text:    T(loc, textErrInvalidInput),
			Outcome: outcomeInvalidInput,
		}, true
	}

	return router.Rejection{}, false
}

func mapInputErrorCode(code capitime.ErrorCode) MsgID {
	switch code {
	case capitime.ErrCodeEmptyUpdate:
		return textErrEmptySet
	case capitime.ErrCodeSnowflake:
		return textErrInvalidSnowflake
	case capitime.ErrCodeLocale:
		return textErrInvalidLocale
	case capitime.ErrCodeClearTarget:
		return textErrInvalidClearTarget
	case capitime.ErrCodeChannelCap:
		return textErrChannelCap
	case capitime.ErrCodeSetting:
		return textErrInvalidInput
	default:
		return textErrInvalidInput
	}
}
