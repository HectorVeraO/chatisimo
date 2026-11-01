//go:build !race

// These tests open a real disgo Gateway client; see gateway_harness_test.go for why they are
// left out of the race run.

package discord_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/capitime"
	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
)

func resetCapiGuildConfigsOnCleanup(t *testing.T, h *harness, guildIDs ...string) {
	t.Helper()

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), closeDeadline)
		defer cancel()

		for _, id := range guildIDs {
			scope := capitime.GuildScope{GuildID: id, CanManageGuild: true}
			_, err := h.capi.ClearGuildConfig(ctx, scope, capitime.ClearTargetAll)
			if err != nil {
				t.Errorf("cleanup ClearGuildConfig(%s): %v", id, err)
			}
		}
	})
}

func TestCapiTimeConfigAndArrivalDetectorEndToEndOverGateway(t *testing.T) {
	h := newHarness(t)
	resetCapiGuildConfigsOnCleanup(t, h, h.guild.ID)
	santiago := mustLoadSantiago(t)

	// Sunday 2026-11-01 21:00 in America/Santiago.
	h.clock.Set(time.Date(2026, time.November, 1, 21, 0, 0, 0, santiago))

	mustSetUserLocale(t, h, userA, "es-419")
	mustSetUserLocale(t, h, userB, "en-US")

	// Non-manager cannot run /capitime-config set.
	denied := slashOrFail(t, h, userB, h.slashAt("/capitime-config/set", true, map[string]any{"target": userC}))
	if denied.Message == nil || denied.Message.EphemeralFor != userB || !strings.Contains(denied.Message.Content, "Manage Server") {
		t.Fatalf("non-manager /capitime-config/set = %+v, want ephemeral forbidden", denied.Message)
	}

	// DM invocation is blocked by Discord client context validation, and forged DM payloads fail with guild_only.
	if _, dmErr := h.fake.Slash(t.Context(), h.users[userA], fakediscord.Slash{Path: "/capitime-config/show"}); dmErr == nil {
		t.Fatal("unbypassed DM /capitime-config/show succeeded, want context validation error")
	}
	dmDenied := slashOrFail(t, h, userA, fakediscord.Slash{
		Path:             "/capitime-config/show",
		BypassValidation: true,
	})
	if dmDenied.Message == nil || !strings.Contains(dmDenied.Message.Content, "solo se puede usar dentro de un servidor") {
		t.Fatalf("DM /capitime-config/show = %+v, want guild-only rejection", dmDenied.Message)
	}

	// Grant ManageGuild ("32") to userA and verify invalid option rejections before setting valid config.
	mustSetGuildMemberPerms(t, h, h.guild, h.users[userA], "32")

	voiceCh := h.fake.AddTypedChannel(h.guild, "General Voice", 2)
	for _, badCall := range []struct {
		name string
		in   fakediscord.Slash
	}{
		{
			name: "empty set",
			in:   h.slashAt("/capitime-config/set", true, nil),
		},
		{
			name: "bot target",
			in:   h.slashAt("/capitime-config/set", true, map[string]any{"target": h.fake.ApplicationID()}),
		},
		{
			name: "voice channel",
			in: fakediscord.Slash{
				Guild:            &h.guild,
				Path:             "/capitime-config/set",
				Options:          map[string]any{"channel": voiceCh},
				BypassValidation: true,
			},
		},
		{
			name: "invalid locale",
			in: fakediscord.Slash{
				Guild:            &h.guild,
				Path:             "/capitime-config/set",
				Options:          map[string]any{"locale": "pt-BR"},
				BypassValidation: true,
			},
		},
		{
			name: "invalid clear target",
			in: fakediscord.Slash{
				Guild:            &h.guild,
				Path:             "/capitime-config/clear",
				Options:          map[string]any{"target": "everything"},
				BypassValidation: true,
			},
		},
	} {
		rej := slashOrFail(t, h, userA, badCall.in)
		if rej.Message == nil || rej.Message.EphemeralFor != userA {
			t.Errorf("%s: got %+v, want ephemeral rejection", badCall.name, rej.Message)
		}
	}

	setRes := slashOrFail(t, h, userA, h.slashAt("/capitime-config/set", true, map[string]any{
		"target":  userC,
		"channel": h.guild.ChannelID,
		"locale":  "es-CL",
	}))
	if setRes.Message == nil || setRes.Message.EphemeralFor != userA {
		t.Fatalf("/capitime-config/set = %+v, want ephemeral confirmation", setRes.Message)
	}
	if !strings.Contains(setRes.Message.Content, "<@"+userC+">") || !strings.Contains(setRes.Message.Content, "<#"+h.guild.ChannelID+">") {
		t.Errorf("/capitime-config/set content = %q, missing target or channel mention", setRes.Message.Content)
	}

	// /capitime-config/show displays the configured target, channel, locale, and debounce window.
	showRes := slashOrFail(t, h, userA, h.slashAt("/capitime-config/show", true, nil))
	if showRes.Message == nil || !strings.Contains(showRes.Message.Content, "<@"+userC+">") {
		t.Fatalf("/capitime-config/show = %+v, missing target user", showRes.Message)
	}

	// Create a secondary channel not in the monitored list.
	otherChannelID := h.fake.AddChannel(h.guild, "off-topic")

	// 1. Message from non-target userA in monitored channel -> silent.
	if _, sendErr := h.fake.SendUserMessage(h.users[userA], h.guild, h.guild.ChannelID, "22:30 llego"); sendErr != nil {
		t.Fatalf("SendUserMessage userA: %v", sendErr)
	}

	// 2. Message from target userC in unmonitored channel -> silent.
	if _, sendErr := h.fake.SendUserMessage(h.users[userC], h.guild, otherChannelID, "22:30 llego"); sendErr != nil {
		t.Fatalf("SendUserMessage userC otherChannel: %v", sendErr)
	}

	// 3. Past-tense message from target userC in monitored channel -> silent.
	if _, sendErr := h.fake.SendUserMessage(h.users[userC], h.guild, h.guild.ChannelID, "ayer llegué a las 22:30"); sendErr != nil {
		t.Fatalf("SendUserMessage userC past tense: %v", sendErr)
	}

	// 4. Real arrival message from target userC in monitored channel -> triggers Capi Time reply referencing triggerMsgID!
	triggerMsgID, err := h.fake.SendUserMessage(h.users[userC], h.guild, h.guild.ChannelID, "22:30 llego")
	if err != nil {
		t.Fatalf("SendUserMessage userC arrival: %v", err)
	}

	var replyMsg fakediscord.Message
	waitFor(t, "Capi Time channel reply", func() bool {
		msgs := h.fake.Visible(h.users[userA], h.guild.ChannelID)
		for _, m := range msgs {
			if m.EphemeralFor == "" && strings.Contains(m.Content, "Capi Time") {
				replyMsg = m
				return true
			}
		}
		return false
	})

	if replyMsg.ReplyToID != triggerMsgID {
		t.Errorf("replyMsg.ReplyToID = %q, want %q", replyMsg.ReplyToID, triggerMsgID)
	}
	if !strings.Contains(replyMsg.Content, "<t:") || strings.Contains(replyMsg.Content, "\n") {
		t.Errorf("replyMsg.Content = %q, want single-line message with Discord timestamp tags", replyMsg.Content)
	}

	// 5. Immediate follow-up arrival message within 2m debounce window is suppressed.
	if _, sendErr := h.fake.SendUserMessage(h.users[userC], h.guild, h.guild.ChannelID, "voy altiro"); sendErr != nil {
		t.Fatalf("SendUserMessage userC debounced: %v", sendErr)
	}

	// Clear configuration and verify show reports unset.
	clearRes := slashOrFail(t, h, userA, h.slashAt("/capitime-config/clear", true, map[string]any{"target": "all"}))
	if clearRes.Message == nil || !strings.Contains(clearRes.Message.Content, "Sin configurar") {
		t.Errorf("/capitime-config/clear = %+v, want unset confirmation", clearRes.Message)
	}

	publicCapiCount := 0
	for _, m := range h.fake.Visible(h.users[userA], h.guild.ChannelID) {
		if m.EphemeralFor == "" && strings.Contains(m.Content, "Capi Time") {
			publicCapiCount++
		}
	}
	if publicCapiCount != 1 {
		t.Errorf("publicCapiCount = %d, want 1 (debounced second message must stay silent)", publicCapiCount)
	}

	assertNoUCLogLeaks(t, h, "22:30 llego", "voy altiro")
}
