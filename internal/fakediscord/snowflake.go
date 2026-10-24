package fakediscord

import (
	"crypto/rand"
	"encoding/binary"
	"strconv"
	"sync"
	"time"
)

const (
	// discordEpochMillis is 2015-01-01T00:00:00Z, the epoch Discord snowflakes count from.
	discordEpochMillis = 1420070400000
	snowflakeSeqMask   = 0x3FFFFF
	snowflakeSeqShift  = 22
)

// snowflakes hands out time-based Discord-style IDs. The counter never resets, so two IDs
// made in the same millisecond still differ, and a restarted process (a later timestamp)
// never reuses an ID an earlier run stored.
type snowflakes struct {
	mu  sync.Mutex
	now func() time.Time
	seq uint64
}

// newSnowflakes starts the counter at a random value, so two generators created in the same
// millisecond still hand out different IDs.
func newSnowflakes() *snowflakes {
	var start [4]byte
	_, err := rand.Read(start[:])
	if err != nil {
		panic("fakediscord: read random bytes: " + err.Error())
	}

	return &snowflakes{now: time.Now, seq: uint64(binary.BigEndian.Uint32(start[:]))}
}

// next returns a fresh snowflake as the decimal string Discord uses on the wire.
func (g *snowflakes) next() string {
	g.mu.Lock()
	defer g.mu.Unlock()

	millis := g.now().UnixMilli() - discordEpochMillis
	if millis < 0 {
		panic("fakediscord: clock is before the Discord epoch")
	}

	g.seq++
	id := (uint64(millis) << snowflakeSeqShift) | (g.seq & snowflakeSeqMask)

	return strconv.FormatUint(id, 10)
}

// isSnowflake reports whether s looks like a decimal snowflake.
func isSnowflake(s string) bool {
	const maxDigits = 20
	if s == "" || len(s) > maxDigits {
		return false
	}
	for _, r := range s {
		isDigit := r >= '0' && r <= '9'
		if !isDigit {
			return false
		}
	}

	return true
}
