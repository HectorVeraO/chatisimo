//go:build e2e

package e2e

import (
	"fmt"
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

	// The guild serial makes each text unique on the fake model the parallel tests share.
	nonTargetText := fmt.Sprintf("22:30 llego %d desde otra cuenta", s.serial)
	pastText := fmt.Sprintf("ayer llegué %d minutos tarde", s.serial)
	arrivalText := fmt.Sprintf("22:30 llego al tiro, somos %d listos", s.serial)
	debouncedText := fmt.Sprintf("voy saliendo con los %d cabros", s.serial)
	for _, text := range []string{nonTargetText, pastText, arrivalText, debouncedText} {
		created.add("user message text", text)
	}

	scripted := []struct{ text, answer string }{
		{nonTargetText, `{"arrival":true,"day":"","clock":"22:30","minutes":0}`},
		{pastText, `{"arrival":false,"day":"","clock":"","minutes":0}`},
		{arrivalText, `{"arrival":true,"day":"","clock":"22:30","minutes":0}`},
		{debouncedText, `{"arrival":true,"day":"","clock":"","minutes":0}`},
	}
	for _, rule := range scripted {
		if err := s.fake.SetGenerated(classifierMarker, "mensaje: "+rule.text, rule.answer); err != nil {
			t.Fatalf("SetGenerated: %v", err)
		}
	}

	// 1. Non-target user message in monitored channel -> silent, never read.
	if _, err := s.fake.SendUserMessage(nonManager, s.guild, s.guild.ChannelID, nonTargetText); err != nil {
		t.Fatalf("SendUserMessage nonManager: %v", err)
	}

	// 2. Target user message in unmonitored channel -> silent, never read.
	if _, err := s.fake.SendUserMessage(capiUser, s.guild, otherChannelID, nonTargetText); err != nil {
		t.Fatalf("SendUserMessage capiUser otherChannel: %v", err)
	}

	// 3. Target user past-tense message in monitored channel -> read by the model, silent.
	if _, err := s.fake.SendUserMessage(capiUser, s.guild, s.guild.ChannelID, pastText); err != nil {
		t.Fatalf("SendUserMessage capiUser pastText: %v", err)
	}
	waitFor(t, "the model read of the past-tense message", func() bool {
		return len(arrivalReads(s.fake, pastText)) == 1
	})

	// 4. Target user arrival claim in monitored channel -> triggers single-line Capi Time reply referencing triggerID.
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

	// 5. Immediate follow-up arrival message within debounce window -> suppressed, never read.
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

	for text, want := range map[string]int{nonTargetText: 0, pastText: 1, arrivalText: 1, debouncedText: 0} {
		if reads := arrivalReads(s.fake, text); len(reads) != want {
			t.Errorf("model reads of %q = %d, want %d", text, len(reads), want)
		}
	}
}

// classifierMarker is a fragment of the system instruction of the Capi Time arrival read. It keeps
// the scripted answers of these tests away from every other request the shared fake model serves.
const classifierMarker = "clasificador"

// arrivalReads returns the arrival reads the fake model served for a message with the given text.
func arrivalReads(fake *fakediscord.Server, text string) []fakediscord.GenerateCall {
	var reads []fakediscord.GenerateCall
	for _, call := range fake.GenerateCalls() {
		if strings.Contains(call.System, classifierMarker) && strings.Contains(call.User, "mensaje: "+text) {
			reads = append(reads, call)
		}
	}

	return reads
}

func TestCapiTimeModelReadDecidesEachMessageOnceE2E(t *testing.T) {
	t.Parallel()

	s := newGuild(t, 2)
	manager := s.fake.AddUser(fakediscord.User{Name: "Manager", Locale: "es-419"})
	created.add("user snowflake", manager.ID)
	s.fake.AddMember(s.guild, manager)

	capiUser := s.fake.AddUser(fakediscord.User{Name: "Target", Locale: "es-419"})
	created.add("user snowflake", capiUser.ID)
	s.fake.AddMember(s.guild, capiUser)

	if err := s.fake.SetMemberPermissions(s.guild, manager, "32"); err != nil {
		t.Fatalf("SetMemberPermissions manager: %v", err)
	}
	s.slash(t, manager, "/capitime-config/set", map[string]any{
		"target":  capiUser,
		"channel": s.guild.ChannelID,
		"locale":  "es-CL",
	})

	// The guild serial makes each text unique on the fake model the parallel tests share.
	commentText := fmt.Sprintf("Dia lunes 17:24 y se cayo el servidor %d otra vez", s.serial)
	captionText := fmt.Sprintf("miren la foto %d del servidor", s.serial)
	arrivalText := fmt.Sprintf("termino la pega %d y me sumo", s.serial)
	const imageURL = "https://media.example.com/capitime/photo.png"
	for _, text := range []string{commentText, captionText, arrivalText, imageURL} {
		created.add("user message text", text)
	}

	const noArrival = `{"arrival":false,"day":"","clock":"","minutes":0}`
	scripted := []struct{ text, answer string }{
		{commentText, noArrival},
		{captionText, noArrival},
		{arrivalText, `{"arrival":true,"day":"","clock":"","minutes":15}`},
	}
	for _, rule := range scripted {
		if err := s.fake.SetGenerated(classifierMarker, "mensaje: "+rule.text, rule.answer); err != nil {
			t.Fatalf("SetGenerated: %v", err)
		}
	}

	// 1. A comment with the current day and time is read by the model, which sees no arrival -> silent.
	if _, err := s.fake.SendUserMessage(capiUser, s.guild, s.guild.ChannelID, commentText); err != nil {
		t.Fatalf("SendUserMessage comment: %v", err)
	}
	waitFor(t, "the model read of the comment", func() bool {
		return len(arrivalReads(s.fake, commentText)) == 1
	})

	// 2. A caption with an image costs one read. The image is not on the Discord CDN, so the bot
	// drops it without any download and the read carries the caption alone.
	photo := []fakediscord.Attachment{{
		ID: "500000000000000021", Filename: "photo.png", ContentType: "image/png", URL: imageURL, Size: 2048,
	}}
	if _, err := s.fake.SendUserMessageWithMedia(capiUser, s.guild, s.guild.ChannelID, captionText, photo, nil); err != nil {
		t.Fatalf("SendUserMessageWithMedia caption: %v", err)
	}
	waitFor(t, "the model read of the caption", func() bool {
		return len(arrivalReads(s.fake, captionText)) == 1
	})
	if read := arrivalReads(s.fake, captionText)[0]; len(read.MIMETypes) != 0 {
		t.Errorf("caption read media = %v, want none: the image is not downloadable", read.MIMETypes)
	}

	// 3. A promise is read by the model, which sees an arrival -> one reply.
	triggerID, err := s.fake.SendUserMessage(capiUser, s.guild, s.guild.ChannelID, arrivalText)
	if err != nil {
		t.Fatalf("SendUserMessage arrival: %v", err)
	}

	var replies []fakediscord.Message
	waitFor(t, "public Capi Time reply to the promise", func() bool {
		replies = replies[:0]
		for _, m := range s.fake.Visible(manager, s.guild.ChannelID) {
			if m.EphemeralFor == "" && strings.Contains(m.Content, "Capi Time") {
				replies = append(replies, m)
			}
		}

		return len(replies) > 0
	})

	if len(replies) != 1 || replies[0].ReplyToID != triggerID {
		t.Fatalf("replies = %+v, want one reply to the promise %q and none to the comment or the caption", replies, triggerID)
	}
	if !strings.Contains(replies[0].Content, "<t:") || strings.Contains(replies[0].Content, "\n") {
		t.Errorf("reply content = %q, want single-line message with Discord timestamp tags", replies[0].Content)
	}

	for _, text := range []string{commentText, captionText, arrivalText} {
		if reads := arrivalReads(s.fake, text); len(reads) != 1 {
			t.Errorf("model reads of %q = %d, want exactly 1", text, len(reads))
		}
	}
}

func TestCapiTimeReadsAPromiseSplitAcrossMessagesE2E(t *testing.T) {
	t.Parallel()

	s := newGuild(t, 2)
	manager := s.fake.AddUser(fakediscord.User{Name: "Manager", Locale: "es-419"})
	created.add("user snowflake", manager.ID)
	s.fake.AddMember(s.guild, manager)

	capiUser := s.fake.AddUser(fakediscord.User{Name: "Target", Locale: "es-419"})
	created.add("user snowflake", capiUser.ID)
	s.fake.AddMember(s.guild, capiUser)

	if err := s.fake.SetMemberPermissions(s.guild, manager, "32"); err != nil {
		t.Fatalf("SetMemberPermissions manager: %v", err)
	}
	s.slash(t, manager, "/capitime-config/set", map[string]any{
		"target":  capiUser,
		"channel": s.guild.ChannelID,
		"locale":  "es-CL",
	})

	// The guild serial makes each text unique on the fake model the parallel tests share.
	firstPart := fmt.Sprintf("cabros del lobby %d en 15 min", s.serial)
	secondPart := fmt.Sprintf("me conecto al lobby %d", s.serial)
	for _, text := range []string{firstPart, secondPart} {
		created.add("user message text", text)
	}

	scripted := []struct{ text, answer string }{
		{firstPart, `{"arrival":false,"day":"","clock":"","minutes":0}`},
		{secondPart, `{"arrival":true,"day":"","clock":"","minutes":15}`},
	}
	for _, rule := range scripted {
		if err := s.fake.SetGenerated(classifierMarker, "mensaje: "+rule.text, rule.answer); err != nil {
			t.Fatalf("SetGenerated: %v", err)
		}
	}

	// 1. The first part alone is read by the model, which sees no arrival yet -> silent.
	if _, err := s.fake.SendUserMessage(capiUser, s.guild, s.guild.ChannelID, firstPart); err != nil {
		t.Fatalf("SendUserMessage first part: %v", err)
	}
	waitFor(t, "the model read of the first part", func() bool {
		return len(arrivalReads(s.fake, firstPart)) == 1
	})
	if read := arrivalReads(s.fake, firstPart)[0]; strings.Contains(read.User, "anterior: ") {
		t.Errorf("first read = %q, want no earlier message", read.User)
	}

	// 2. The second part is read after the first, completes the promise -> one reply, to it.
	secondID, err := s.fake.SendUserMessage(capiUser, s.guild, s.guild.ChannelID, secondPart)
	if err != nil {
		t.Fatalf("SendUserMessage second part: %v", err)
	}

	var replies []fakediscord.Message
	waitFor(t, "public Capi Time reply to the completed promise", func() bool {
		replies = replies[:0]
		for _, m := range s.fake.Visible(manager, s.guild.ChannelID) {
			if m.EphemeralFor == "" && strings.Contains(m.Content, "Capi Time") {
				replies = append(replies, m)
			}
		}

		return len(replies) > 0
	})

	if len(replies) != 1 || replies[0].ReplyToID != secondID {
		t.Fatalf("replies = %+v, want one reply to the second part %q and none to the first", replies, secondID)
	}
	if !strings.Contains(replies[0].Content, "<t:") || strings.Contains(replies[0].Content, "\n") {
		t.Errorf("reply content = %q, want single-line message with Discord timestamp tags", replies[0].Content)
	}

	reads := arrivalReads(s.fake, secondPart)
	if len(reads) != 1 {
		t.Fatalf("model reads of %q = %d, want exactly 1", secondPart, len(reads))
	}
	if want := "anterior: " + firstPart + "\nmensaje: " + secondPart; !strings.Contains(reads[0].User, want) {
		t.Errorf("second read = %q, want it to carry %q", reads[0].User, want)
	}
}
