//go:build e2e

package e2e

import (
	"strings"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
)

func TestCapiTimeConfigAndArrivalDetectorE2E(t *testing.T) {
	t.Parallel()

	s := newGuild(t, 2)
	manager := s.fake.AddUser(fakediscord.User{Name: "Manager", Locale: "es-419"})
	created.add("user snowflake", manager.ID)
	s.fake.AddMember(s.guild, manager)

	nonManager := s.fake.AddUser(fakediscord.User{Name: "Member", Locale: "en-US"})
	created.add("user snowflake", nonManager.ID)
	s.fake.AddMember(s.guild, nonManager)

	capiUser := s.fake.AddUser(fakediscord.User{Name: "Braulio", Locale: "es-419"})
	created.add("user snowflake", capiUser.ID)
	s.fake.AddMember(s.guild, capiUser)

	targetMention := "<@" + capiUser.ID + ">"
	channelMention := "<#" + s.guild.ChannelID + ">"

	// Non-managers cannot mutate or inspect /capitime-config.
	deniedSet := s.slash(t, nonManager, "/capitime-config/set", map[string]any{"target": capiUser})
	if deniedSet.message.EphemeralFor != nonManager.ID || strings.Contains(deniedSet.text, targetMention) {
		t.Errorf("non-manager /capitime-config/set = %q (ephemeral=%q), want ephemeral refusal",
			deniedSet.text, deniedSet.message.EphemeralFor)
	}

	deniedShow := s.slash(t, nonManager, "/capitime-config/show", nil)
	if deniedShow.message.EphemeralFor != nonManager.ID {
		t.Errorf("non-manager /capitime-config/show EphemeralFor = %q, want %q",
			deniedShow.message.EphemeralFor, nonManager.ID)
	}

	// Grant ManageGuild ("32") to manager and configure target user and monitored channel.
	if err := s.fake.SetMemberPermissions(s.guild, manager, "32"); err != nil {
		t.Fatalf("SetMemberPermissions manager: %v", err)
	}

	setReply := s.slash(t, manager, "/capitime-config/set", map[string]any{
		"target":  capiUser,
		"channel": s.guild.ChannelID,
		"locale":  "es-CL",
	})
	if setReply.message.EphemeralFor != manager.ID ||
		!strings.Contains(setReply.text, targetMention) ||
		!strings.Contains(setReply.text, channelMention) {
		t.Fatalf("/capitime-config/set = %q (ephemeral=%q), want ephemeral confirmation with %s and %s",
			setReply.text, setReply.message.EphemeralFor, targetMention, channelMention)
	}

	showReply := s.slash(t, manager, "/capitime-config/show", nil)
	if showReply.message.EphemeralFor != manager.ID ||
		!strings.Contains(showReply.text, targetMention) ||
		!strings.Contains(showReply.text, channelMention) {
		t.Errorf("/capitime-config/show = %q (ephemeral=%q), want ephemeral config mentioning %s and %s",
			showReply.text, showReply.message.EphemeralFor, targetMention, channelMention)
	}

	otherChannelID := s.fake.AddChannel(s.guild, "off-topic")
	created.add("channel snowflake", otherChannelID)

	// 1. Non-target user message in monitored channel -> silent.
	const nonTargetText = "22:30 llego"
	created.add("user message text", nonTargetText)
	if _, err := s.fake.SendUserMessage(nonManager, s.guild, s.guild.ChannelID, nonTargetText); err != nil {
		t.Fatalf("SendUserMessage nonManager: %v", err)
	}

	// 2. Target user message in unmonitored channel -> silent.
	if _, err := s.fake.SendUserMessage(capiUser, s.guild, otherChannelID, nonTargetText); err != nil {
		t.Fatalf("SendUserMessage capiUser otherChannel: %v", err)
	}

	// 3. Target user past-tense message in monitored channel -> silent.
	const pastText = "ayer llegué a las 22:30"
	created.add("user message text", pastText)
	if _, err := s.fake.SendUserMessage(capiUser, s.guild, s.guild.ChannelID, pastText); err != nil {
		t.Fatalf("SendUserMessage capiUser pastText: %v", err)
	}

	// 4. Target user arrival claim in monitored channel -> triggers single-line Capi Time reply referencing triggerID.
	const arrivalText = "22:30 llego al tiro"
	created.add("user message text", arrivalText)
	triggerID, err := s.fake.SendUserMessage(capiUser, s.guild, s.guild.ChannelID, arrivalText)
	if err != nil {
		t.Fatalf("SendUserMessage capiUser arrivalText: %v", err)
	}

	var replyMsg fakediscord.Message
	waitFor(t, "public Capi Time reply in monitored channel", func() bool {
		for _, m := range s.fake.Visible(manager, s.guild.ChannelID) {
			if m.EphemeralFor == "" && strings.Contains(m.Content, "Capi Time") {
				replyMsg = m
				return true
			}
		}
		return false
	})

	if replyMsg.ReplyToID != triggerID {
		t.Errorf("replyMsg.ReplyToID = %q, want %q", replyMsg.ReplyToID, triggerID)
	}
	if !strings.Contains(replyMsg.Content, "<t:") || strings.Contains(replyMsg.Content, "\n") {
		t.Errorf("replyMsg.Content = %q, want single-line message with Discord timestamp tags", replyMsg.Content)
	}

	// 5. Immediate follow-up arrival message within debounce window -> suppressed.
	const debouncedText = "voy saliendo"
	created.add("user message text", debouncedText)
	if _, err := s.fake.SendUserMessage(capiUser, s.guild, s.guild.ChannelID, debouncedText); err != nil {
		t.Fatalf("SendUserMessage capiUser debouncedText: %v", err)
	}

	// Clear configuration and verify show reports unset.
	clearReply := s.slash(t, manager, "/capitime-config/clear", map[string]any{"target": "all"})
	if clearReply.message.EphemeralFor != manager.ID || !strings.Contains(clearReply.text, "Sin configurar") {
		t.Errorf("/capitime-config/clear = %q, want unset confirmation", clearReply.text)
	}

	capiReplies := 0
	for _, m := range s.fake.Visible(manager, s.guild.ChannelID) {
		if m.EphemeralFor == "" && strings.Contains(m.Content, "Capi Time") {
			capiReplies++
		}
	}
	if capiReplies != 1 {
		t.Errorf("capiReplies = %d, want 1", capiReplies)
	}
}
