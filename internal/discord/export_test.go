package discord

import "github.com/disgoorg/disgo/discord"

// AddGuildToCache stands in for the Gateway, which fills the guild cache the notifier reads.
func (n *Notifier) AddGuildToCache(g discord.Guild) {
	n.caches.AddGuild(g)
}
