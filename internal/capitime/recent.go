package capitime

import (
	"sync"
	"time"
)

const (
	// recentMaxAge is how long a message stays usable as context for the ones that follow it.
	recentMaxAge = 5 * time.Minute
	// maxContextMessages is the most earlier messages one read may carry. It is the upper bound of
	// the capitime.context_messages setting and the most texts kept per member and channel.
	maxContextMessages = 10
)

// recentText is the sanitized text of one message and the instant it was handled.
type recentText struct {
	text string
	at   time.Time
}

// recentMessages keeps, per guild, channel and member, the text of the last messages that member
// sent there. It lives in memory only: a restart forgets every text, and nothing here is stored or
// logged.
type recentMessages struct {
	mu      sync.Mutex
	entries map[string][]recentText
}

func newRecentMessages() *recentMessages {
	return &recentMessages{entries: make(map[string][]recentText)}
}

// recentKey names the messages of one member in one channel of one guild.
func recentKey(guildID, channelID, authorID string) string {
	return guildID + ":" + channelID + ":" + authorID
}

// isRecent reports whether a message handled at at still counts as context at now.
func isRecent(at, now time.Time) bool {
	age := now.Sub(at)

	return age >= 0 && age <= recentMaxAge
}

// pruneLocked drops the expired texts of every key, and every key left without texts, so the map
// never outgrows the members who wrote in the last recentMaxAge. It must run with r.mu held.
func (r *recentMessages) pruneLocked(now time.Time) {
	for key, texts := range r.entries {
		var kept []recentText
		for _, entry := range texts {
			if isRecent(entry.at, now) {
				kept = append(kept, entry)
			}
		}

		if len(kept) == 0 {
			delete(r.entries, key)
		} else {
			r.entries[key] = kept
		}
	}
}

// exchange reads the current instant from clock and returns it with the newest texts of key that
// are still recent then, at most limit of them and oldest first; it then records text as the newest
// of key. An empty text is not recorded, and a limit of zero or less returns no texts. The clock is
// read, and the texts are read and recorded, under one lock: messages handled at once are stamped
// in the order they are recorded, so none prunes or misses the other.
func (r *recentMessages) exchange(key, text string, clock func() time.Time, limit int) ([]string, time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := clock()
	r.pruneLocked(now)

	texts := r.entries[key]
	limit = min(limit, len(texts))

	var earlier []string
	if limit > 0 {
		earlier = make([]string, 0, limit)
		for _, entry := range texts[len(texts)-limit:] {
			earlier = append(earlier, entry.text)
		}
	}

	if text == "" {
		return earlier, now
	}

	texts = append(texts, recentText{text: text, at: now})
	if len(texts) > maxContextMessages {
		texts = texts[len(texts)-maxContextMessages:]
	}
	r.entries[key] = texts

	return earlier, now
}

// clear forgets every text of key.
func (r *recentMessages) clear(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.entries, key)
}
