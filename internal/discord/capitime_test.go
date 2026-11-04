package discord_test

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/capitime"
	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
	"github.com/HectorVeraO/chatisimo/internal/platform/llm"
)

const (
	capiStickerID       = "500000000000000001"
	capiAttachmentID    = "500000000000000002"
	capiLottieID        = "500000000000000003"
	capiGIFStickerID    = "500000000000000004"
	capiDocumentID      = "500000000000000005"
	capiAPNGStickerID   = "500000000000000006"
	capiVoiceID         = "500000000000000007"
	capiAudioFileID     = "500000000000000008"
	capiAudioExtraID    = "500000000000000009"
	capiVideoID         = "500000000000000010"
	capiLongVoiceID     = "500000000000000011"
	capiBigAudioID      = "500000000000000012"
	capiLongVoiceURL    = "https://cdn.discordapp.com/attachments/300000000000000001/500000000000000011/long.ogg?ex=mno"
	capiBigAudioURL     = "https://cdn.discordapp.com/attachments/300000000000000001/500000000000000012/big.mp3?ex=pqr"
	capiVoiceURL        = "https://cdn.discordapp.com/attachments/300000000000000001/500000000000000007/voice-message.ogg?ex=abc"
	capiAudioFileURL    = "https://cdn.discordapp.com/attachments/300000000000000001/500000000000000008/clip.mp3?ex=def"
	capiAudioExtraURL   = "https://cdn.discordapp.com/attachments/300000000000000001/500000000000000009/extra.wav?ex=ghi"
	capiVideoURL        = "https://cdn.discordapp.com/attachments/300000000000000001/500000000000000010/movie.mp4?ex=jkl"
	capiAttachmentURL   = "https://cdn.discordapp.com/attachments/300000000000000001/500000000000000002/photo.png?ex=abc"
	capiDocumentURL     = "https://cdn.discordapp.com/attachments/300000000000000001/500000000000000005/notes.pdf?ex=def"
	capiArrivalJSON     = `{"arrival":true,"immediate":true,"offset_minutes":0,"clock":""}`
	stickerFormatPNG    = 1
	stickerFormatAPNG   = 2
	stickerFormatLottie = 3
	stickerFormatGIF    = 4
)

// fakeCapiVision answers every image read with an arrival and records the requests.
type fakeCapiVision struct {
	mu    sync.Mutex
	texts []string
	mimes [][]string
}

func (v *fakeCapiVision) GenerateWithImages(_ context.Context, _, text string, images []llm.Image) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	mimes := make([]string, 0, len(images))
	for _, image := range images {
		mimes = append(mimes, image.MIMEType)
	}
	v.texts = append(v.texts, text)
	v.mimes = append(v.mimes, mimes)

	return capiArrivalJSON, nil
}

func (v *fakeCapiVision) snapshot() ([]string, [][]string) {
	v.mu.Lock()
	defer v.mu.Unlock()

	return slices.Clone(v.texts), slices.Clone(v.mimes)
}

// fakeCapiImages serves synthetic bytes for every URL and records which were requested.
type fakeCapiImages struct {
	mu   sync.Mutex
	urls []string
}

func (f *fakeCapiImages) Fetch(_ context.Context, url string, _ int) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.urls = append(f.urls, url)

	return []byte("synthetic image bytes"), nil
}

func (f *fakeCapiImages) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.urls)
}

// fakeCapiAudio answers every audio read with an arrival and records the requests.
type fakeCapiAudio struct {
	mu    sync.Mutex
	texts []string
	mimes []string
}

func (a *fakeCapiAudio) GenerateWithAudio(_ context.Context, _, text string, audio llm.Audio) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.texts = append(a.texts, text)
	a.mimes = append(a.mimes, audio.MIMEType)

	return capiArrivalJSON, nil
}

func (a *fakeCapiAudio) snapshot() ([]string, []string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	return slices.Clone(a.texts), slices.Clone(a.mimes)
}

func publicCapiReplies(h *harness) []fakediscord.Message {
	var replies []fakediscord.Message
	for _, m := range h.fake.Visible(h.users[userA], h.guild.ChannelID) {
		if m.EphemeralFor == "" && strings.Contains(m.Content, "Capi Time") {
			replies = append(replies, m)
		}
	}

	return replies
}

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

func TestCapiTimeReadsArrivalFromStickersAndAttachmentsOverGateway(t *testing.T) {
	h := newHarness(t)
	resetCapiGuildConfigsOnCleanup(t, h, h.guild.ID)
	santiago := mustLoadSantiago(t)

	h.clock.Set(time.Date(2026, time.November, 1, 21, 0, 0, 0, santiago))

	target := userC
	channel := h.guild.ChannelID
	_, err := h.capi.UpdateGuildConfig(
		t.Context(),
		capitime.GuildScope{GuildID: h.guild.ID, CanManageGuild: true},
		capitime.GuildConfigUpdate{TargetUserID: &target, ChannelID: &channel},
	)
	if err != nil {
		t.Fatalf("UpdateGuildConfig: %v", err)
	}

	stickerOnly := []fakediscord.StickerItem{{ID: capiStickerID, Name: "VOY", FormatType: stickerFormatPNG}}

	// A sticker from someone who is not the target is never downloaded.
	if _, sendErr := h.fake.SendUserMessageWithMedia(h.users[userA], h.guild, "", "", nil, stickerOnly); sendErr != nil {
		t.Fatalf("SendUserMessageWithMedia userA: %v", sendErr)
	}

	// Lottie stickers and non-image attachments from the target cannot be read.
	unreadable := []fakediscord.StickerItem{{ID: capiLottieID, Name: "ENTRANDING", FormatType: stickerFormatLottie}}
	document := []fakediscord.Attachment{{
		ID: capiDocumentID, Filename: "notes.pdf", ContentType: "application/pdf", URL: capiDocumentURL, Size: 10,
	}}
	if _, sendErr := h.fake.SendUserMessageWithMedia(h.users[userC], h.guild, "", "", document, unreadable); sendErr != nil {
		t.Fatalf("SendUserMessageWithMedia unreadable: %v", sendErr)
	}

	// A PNG sticker next to a Lottie one: only the PNG reaches the vision model.
	mixed := append([]fakediscord.StickerItem{}, unreadable...)
	mixed = append(mixed, stickerOnly...)
	stickerMsgID, err := h.fake.SendUserMessageWithMedia(h.users[userC], h.guild, "", "", nil, mixed)
	if err != nil {
		t.Fatalf("SendUserMessageWithMedia sticker: %v", err)
	}

	waitFor(t, "Capi Time reply to the sticker", func() bool {
		return len(publicCapiReplies(h)) == 1
	})
	first := publicCapiReplies(h)[0]
	if first.ReplyToID != stickerMsgID {
		t.Errorf("sticker reply ReplyToID = %q, want %q", first.ReplyToID, stickerMsgID)
	}

	urls := h.capiImages.snapshot()
	if len(urls) != 1 || !strings.HasPrefix(urls[0], "https://cdn.discordapp.com/stickers/"+capiStickerID) {
		t.Fatalf("fetched %v, want only the PNG sticker URL", urls)
	}
	texts, mimes := h.capiVision.snapshot()
	if !slices.Equal(texts, []string{"sticker: VOY"}) || !slices.Equal(mimes[0], []string{"image/png"}) {
		t.Fatalf("vision requests = %v %v, want one PNG read with the sticker hint", texts, mimes)
	}

	// An image attachment is read after the debounce window passes.
	h.clock.Advance(3 * time.Minute)
	photo := []fakediscord.Attachment{{
		ID: capiAttachmentID, Filename: "photo.png", ContentType: "image/png", URL: capiAttachmentURL, Size: 2048,
	}}
	attachmentMsgID, err := h.fake.SendUserMessageWithMedia(h.users[userC], h.guild, "", "", photo, nil)
	if err != nil {
		t.Fatalf("SendUserMessageWithMedia attachment: %v", err)
	}

	waitFor(t, "Capi Time reply to the attachment", func() bool {
		return len(publicCapiReplies(h)) == 2
	})
	if second := publicCapiReplies(h)[1]; second.ReplyToID != attachmentMsgID {
		t.Errorf("attachment reply ReplyToID = %q, want %q", second.ReplyToID, attachmentMsgID)
	}
	if urls = h.capiImages.snapshot(); len(urls) != 2 || urls[1] != capiAttachmentURL {
		t.Fatalf("fetched %v, want the attachment URL second", urls)
	}

	// A GIF sticker keeps its type.
	h.clock.Advance(3 * time.Minute)
	gif := []fakediscord.StickerItem{{ID: capiGIFStickerID, Name: "Ya voy", FormatType: stickerFormatGIF}}
	if _, sendErr := h.fake.SendUserMessageWithMedia(h.users[userC], h.guild, "", "", nil, gif); sendErr != nil {
		t.Fatalf("SendUserMessageWithMedia gif: %v", sendErr)
	}

	waitFor(t, "Capi Time reply to the GIF sticker", func() bool {
		return len(publicCapiReplies(h)) == 3
	})
	_, mimes = h.capiVision.snapshot()
	if len(mimes) != 3 || !slices.Equal(mimes[2], []string{"image/gif"}) {
		t.Errorf("vision MIME types = %v, want the GIF sticker read as image/gif", mimes)
	}

	// An APNG sticker is read as a PNG image.
	h.clock.Advance(3 * time.Minute)
	apng := []fakediscord.StickerItem{{ID: capiAPNGStickerID, Name: "Voy ya", FormatType: stickerFormatAPNG}}
	if _, sendErr := h.fake.SendUserMessageWithMedia(h.users[userC], h.guild, "", "", nil, apng); sendErr != nil {
		t.Fatalf("SendUserMessageWithMedia apng: %v", sendErr)
	}

	waitFor(t, "Capi Time reply to the APNG sticker", func() bool {
		return len(publicCapiReplies(h)) == 4
	})
	_, mimes = h.capiVision.snapshot()
	if len(mimes) != 4 || !slices.Equal(mimes[3], []string{"image/png"}) {
		t.Errorf("vision MIME types = %v, want the APNG sticker read as image/png", mimes)
	}

	assertNoUCLogLeaks(t, h, "VOY", "Ya voy", "Voy ya", "ENTRANDING", capiAttachmentURL, capiDocumentURL, "photo.png")
}

func TestCapiTimeReadsArrivalFromVoiceMessagesAndAudioFilesOverGateway(t *testing.T) {
	h := newHarness(t)
	resetCapiGuildConfigsOnCleanup(t, h, h.guild.ID)
	santiago := mustLoadSantiago(t)

	h.clock.Set(time.Date(2026, time.November, 1, 21, 0, 0, 0, santiago))

	target := userC
	channel := h.guild.ChannelID
	_, err := h.capi.UpdateGuildConfig(
		t.Context(),
		capitime.GuildScope{GuildID: h.guild.ID, CanManageGuild: true},
		capitime.GuildConfigUpdate{TargetUserID: &target, ChannelID: &channel},
	)
	if err != nil {
		t.Fatalf("UpdateGuildConfig: %v", err)
	}

	voice := []fakediscord.Attachment{{
		ID: capiVoiceID, Filename: "voice-message.ogg", ContentType: "audio/ogg", URL: capiVoiceURL,
		Size: 4096, DurationSecs: 4.5, IsVoiceMessage: true,
	}}

	// A voice message from someone who is not the target is never downloaded.
	if _, sendErr := h.fake.SendUserMessageWithMedia(h.users[userA], h.guild, "", "", voice, nil); sendErr != nil {
		t.Fatalf("SendUserMessageWithMedia userA: %v", sendErr)
	}

	// A video from the target cannot be read.
	video := []fakediscord.Attachment{{
		ID: capiVideoID, Filename: "movie.mp4", ContentType: "video/mp4", URL: capiVideoURL, Size: 4096,
	}}
	if _, sendErr := h.fake.SendUserMessageWithMedia(h.users[userC], h.guild, "", "", video, nil); sendErr != nil {
		t.Fatalf("SendUserMessageWithMedia video: %v", sendErr)
	}

	// A voice message longer than the supported limit, and one larger than the size limit, are ignored.
	tooLong := []fakediscord.Attachment{{
		ID: capiLongVoiceID, Filename: "long.ogg", ContentType: "audio/ogg", URL: capiLongVoiceURL,
		Size: 4096, DurationSecs: llm.MaxAudioSeconds + 1, IsVoiceMessage: true,
	}}
	if _, sendErr := h.fake.SendUserMessageWithMedia(h.users[userC], h.guild, "", "", tooLong, nil); sendErr != nil {
		t.Fatalf("SendUserMessageWithMedia long voice: %v", sendErr)
	}
	tooBig := []fakediscord.Attachment{{
		ID: capiBigAudioID, Filename: "big.mp3", ContentType: "audio/mpeg", URL: capiBigAudioURL,
		Size: llm.MaxAudioBytes + 1,
	}}
	if _, sendErr := h.fake.SendUserMessageWithMedia(h.users[userC], h.guild, "", "", tooBig, nil); sendErr != nil {
		t.Fatalf("SendUserMessageWithMedia big audio: %v", sendErr)
	}

	// A voice message from the target gets a reply.
	voiceMsgID, err := h.fake.SendUserMessageWithMedia(h.users[userC], h.guild, "", "", voice, nil)
	if err != nil {
		t.Fatalf("SendUserMessageWithMedia voice: %v", err)
	}

	waitFor(t, "Capi Time reply to the voice message", func() bool {
		return len(publicCapiReplies(h)) == 1
	})
	if first := publicCapiReplies(h)[0]; first.ReplyToID != voiceMsgID {
		t.Errorf("voice message reply ReplyToID = %q, want %q", first.ReplyToID, voiceMsgID)
	}
	if urls := h.capiImages.snapshot(); !slices.Equal(urls, []string{capiVoiceURL}) {
		t.Fatalf("fetched %v, want only the target's voice message", urls)
	}
	texts, mimes := h.capiAudio.snapshot()
	if !slices.Equal(texts, []string{""}) || !slices.Equal(mimes, []string{"audio/ogg"}) {
		t.Fatalf("audio requests = %v %v, want one audio/ogg read without text", texts, mimes)
	}

	// An audio file without duration is read after the debounce window passes.
	h.clock.Advance(3 * time.Minute)
	file := []fakediscord.Attachment{{
		ID: capiAudioFileID, Filename: "clip.mp3", ContentType: "audio/mpeg", URL: capiAudioFileURL, Size: 8192,
	}}
	fileMsgID, err := h.fake.SendUserMessageWithMedia(h.users[userC], h.guild, "", "", file, nil)
	if err != nil {
		t.Fatalf("SendUserMessageWithMedia audio file: %v", err)
	}

	waitFor(t, "Capi Time reply to the audio file", func() bool {
		return len(publicCapiReplies(h)) == 2
	})
	if second := publicCapiReplies(h)[1]; second.ReplyToID != fileMsgID {
		t.Errorf("audio file reply ReplyToID = %q, want %q", second.ReplyToID, fileMsgID)
	}
	_, mimes = h.capiAudio.snapshot()
	if !slices.Equal(mimes, []string{"audio/ogg", "audio/mpeg"}) {
		t.Errorf("audio MIME types = %v, want the file read as audio/mpeg", mimes)
	}

	// Several audios in one message cost one read: the first one.
	h.clock.Advance(3 * time.Minute)
	many := []fakediscord.Attachment{
		file[0],
		{ID: capiAudioExtraID, Filename: "extra.wav", ContentType: "audio/wav", URL: capiAudioExtraURL, Size: 100},
		voice[0],
	}
	if _, sendErr := h.fake.SendUserMessageWithMedia(h.users[userC], h.guild, "", "", many, nil); sendErr != nil {
		t.Fatalf("SendUserMessageWithMedia many audios: %v", sendErr)
	}

	waitFor(t, "Capi Time reply to the message with several audios", func() bool {
		return len(publicCapiReplies(h)) == 3
	})
	if urls := h.capiImages.snapshot(); !slices.Equal(urls, []string{capiVoiceURL, capiAudioFileURL, capiAudioFileURL}) {
		t.Errorf("fetched %v, want one fetch per message and none for the video or the extra audios", urls)
	}

	assertNoUCLogLeaks(t, h, "voice-message.ogg", "clip.mp3", "extra.wav", "movie.mp4", "long.ogg", "big.mp3",
		capiVoiceURL, capiAudioFileURL, capiAudioExtraURL, capiVideoURL)
}
