package jobs

import "time"

// Clock is the time source River uses when set (river.Config.Test.Time). Production leaves it nil.
// In tests the module and both clients share one fake clock that starts at time.Now() and only
// moves forward; otherwise inserted jobs and the fetch query disagree on "now".
type Clock interface {
	Now() time.Time
}

// riverClock adapts a Clock to River's time generator.
type riverClock struct {
	clock Clock
}

func (c riverClock) Now() time.Time {
	return c.clock.Now()
}

func (c riverClock) NowOrNil() *time.Time {
	now := c.clock.Now()

	return &now
}
