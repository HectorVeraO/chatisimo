package uccmd

import (
	"errors"

	"github.com/HectorVeraO/chatisimo/internal/discord/internal/router"
	"github.com/HectorVeraO/chatisimo/internal/uccalendar"
)

const (
	outcomeScheduleUnavailable = "rejected:schedule_unavailable"
	outcomeInvalidInput        = "rejected:invalid_input"
	outcomeForbidden           = "rejected:forbidden"
	outcomeGuildOnly           = "rejected:guild_only"
)

// Adapter-level validation errors returned by UC command handlers before calling uccalendar.
var (
	// ErrGuildOnly reports that a guild-only UC command was invoked outside a server.
	ErrGuildOnly = errors.New("uccmd: command works only in a server")
	// ErrFriendIsBot reports that the selected friend user is a bot account.
	ErrFriendIsBot = errors.New("uccmd: friend user cannot be a bot")
	// ErrInvalidFriend reports that the selected friend option is not a member of the server.
	ErrInvalidFriend = errors.New("uccmd: friend is not a member of this server")
	// ErrInvalidChannel reports that the selected channel option is not a text or announcement
	// channel of the server.
	ErrInvalidChannel = errors.New("uccmd: channel is not a text or announcement channel of this server")
)

// TryMapError maps a uccalendar domain or adapter error to a localized router.Rejection using the
// locale attached to err by the router (or English when none is attached). It returns false when
// err is not a UC error so the adapter can chain other feature mappers.
func TryMapError(requestID string, err error) (router.Rejection, bool) {
	loc := LocaleFromDiscord(router.ErrorLocale(err))
	return TryMapErrorIn(loc, requestID, err)
}

// TryMapErrorIn maps a uccalendar domain or adapter error to a router.Rejection in loc (falling
// back to LocaleENUS). It returns false for any non-UC error.
func TryMapErrorIn(loc Locale, _ string, err error) (router.Rejection, bool) {
	if err == nil {
		return router.Rejection{}, false
	}

	c := CatalogFor(loc)
	switch {
	case errors.Is(err, ErrGuildOnly):
		rejection := router.Rejection{
			Text:    c.Get(textConfigGuildOnly),
			Outcome: outcomeGuildOnly,
		}
		return rejection, true
	case errors.Is(err, uccalendar.ErrForbidden):
		rejection := router.Rejection{
			Text:    c.Get(textConfigForbidden),
			Outcome: outcomeForbidden,
		}
		return rejection, true
	case errors.Is(err, uccalendar.ErrScheduleUnavailable):
		rejection := router.Rejection{
			Text:    c.Get(textScheduleUnavailable),
			Outcome: outcomeScheduleUnavailable,
		}
		return rejection, true
	case errors.Is(err, ErrFriendIsBot):
		rejection := router.Rejection{
			Text:    c.Get(textConfigFriendBot),
			Outcome: outcomeInvalidInput,
		}
		return rejection, true
	case errors.Is(err, ErrInvalidFriend):
		rejection := router.Rejection{
			Text:    c.Get(textConfigInvalidFriend),
			Outcome: outcomeInvalidInput,
		}
		return rejection, true
	case errors.Is(err, ErrInvalidChannel):
		rejection := router.Rejection{
			Text:    c.Get(textConfigInvalidChannel),
			Outcome: outcomeInvalidInput,
		}
		return rejection, true
	}

	var inputErr *uccalendar.InputError
	if errors.As(err, &inputErr) {
		msgID, ok := mapInputErrorField(inputErr)
		if !ok {
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

func mapInputErrorField(inputErr *uccalendar.InputError) (MsgID, bool) {
	switch {
	case inputErr.Field == "query" && inputErr.Code == uccalendar.InputCodeMutuallyExclusive:
		return textMutuallyExclusiveOptions, true
	case inputErr.Field == "query":
		return textInvalidInput, true
	case inputErr.Field == dateOptionName:
		return textInvalidDate, true
	case inputErr.Field == windowOptionName:
		return textInvalidWindow, true
	case inputErr.Field == "update":
		return textConfigEmptyUpdate, true
	case inputErr.Field == friendOptionName || inputErr.Field == "friend_user_id":
		return textConfigInvalidFriend, true
	case inputErr.Field == channelOptionName || inputErr.Field == "channel_id":
		return textConfigInvalidChannel, true
	case inputErr.Field == clearTargetOptionName:
		return textConfigInvalidClearTarget, true
	default:
		return "", false
	}
}
