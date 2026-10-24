// Package discord is the Discord adapter of Chatisimo. It delivers module notifications as
// direct messages and, in later changes, turns slash commands and interactions into calls to
// the feature modules. It holds no domain rules.
package discord

import "github.com/HectorVeraO/chatisimo/internal/platform/config"

// Settings are the values read from the database at startup.
type Settings struct {
	// Token is the bot token (setting discord.token).
	Token config.Secret
	// APIBaseURL is the REST base URL, such as https://discord.com/api/v10 (setting
	// discord.api_base_url).
	APIBaseURL string
}
