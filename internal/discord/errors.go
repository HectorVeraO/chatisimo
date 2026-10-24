package discord

import (
	"fmt"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

// Discord JSON error codes after which a person can never be reached by DM.
const (
	codeCannotSendToUser = 50007
	codeNoMutualGuild    = 50278
)

// deliveryError is Discord's answer to a REST call, reduced to the HTTP status and the Discord
// error code. The REST error itself holds the request, with the bot token, and the message
// bodies, so it never leaves the code that called Discord.
type deliveryError struct {
	Status int
	Code   int
}

// Error returns the status and code only.
func (e deliveryError) Error() string {
	text := fmt.Sprintf("discord status %d code %d", e.Status, e.Code)
	return text
}

// Is makes a delivery error match giftexchange.ErrUndeliverable when the code says the person
// can never be reached.
func (e deliveryError) Is(target error) bool {
	isUndeliverable := e.Code == codeCannotSendToUser || e.Code == codeNoMutualGuild
	return target == giftexchange.ErrUndeliverable && isUndeliverable
}

// transportError is a failure to complete a REST call, without the URL, which holds the
// channel snowflake.
type transportError struct {
	Op string
}

// Error returns the operation only.
func (e transportError) Error() string {
	text := e.Op + " failed"
	return text
}
