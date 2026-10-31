package uccmd

import (
	"errors"

	"github.com/HectorVeraO/chatisimo/internal/discord/internal/router"
	"github.com/HectorVeraO/chatisimo/internal/uccalendar"
)

const (
	outcomeScheduleUnavailable = "rejected:schedule_unavailable"
	outcomeInvalidInput        = "rejected:invalid_input"
)

// TryMapError maps a uccalendar domain error to a localized router.Rejection using the locale
// attached to err by the router (or English when none is attached). It returns false when err is
// not a uccalendar domain error so the adapter can chain other feature mappers.
func TryMapError(requestID string, err error) (router.Rejection, bool) {
	loc := LocaleFromDiscord(router.ErrorLocale(err))
	return TryMapErrorIn(loc, requestID, err)
}

// TryMapErrorIn maps a uccalendar domain error to a router.Rejection in loc (falling back to
// LocaleENUS). It returns false for any non-uccalendar error.
func TryMapErrorIn(loc Locale, _ string, err error) (router.Rejection, bool) {
	if err == nil {
		return router.Rejection{}, false
	}

	c := CatalogFor(loc)
	if errors.Is(err, uccalendar.ErrScheduleUnavailable) {
		rejection := router.Rejection{
			Text:    c.Get(textScheduleUnavailable),
			Outcome: outcomeScheduleUnavailable,
		}
		return rejection, true
	}

	var inputErr *uccalendar.InputError
	if errors.As(err, &inputErr) {
		var msgID MsgID
		switch {
		case inputErr.Field == "query" && inputErr.Code == uccalendar.InputCodeMutuallyExclusive:
			msgID = textMutuallyExclusiveOptions
		case inputErr.Field == "query":
			msgID = textInvalidInput
		case inputErr.Field == dateOptionName:
			msgID = textInvalidDate
		case inputErr.Field == windowOptionName:
			msgID = textInvalidWindow
		default:
			return router.Rejection{}, false
		}

		rejection := router.Rejection{
			Text:    c.Get(msgID),
			Outcome: outcomeInvalidInput,
		}
		return rejection, true
	}

	return router.Rejection{}, false
}
