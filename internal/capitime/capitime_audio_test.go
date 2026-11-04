package capitime_test

import (
	"context"
	"errors"
	"math"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/capitime"
	"github.com/HectorVeraO/chatisimo/internal/platform/llm"
)

const (
	voiceURL       = "https://cdn.discordapp.com/attachments/300000000000000001/500000000000000010/voice-message.ogg?ex=abc"
	audioFileURL   = "https://cdn.discordapp.com/attachments/300000000000000001/500000000000000011/clip.mp3?ex=def"
	secondAudioURL = "https://media.discordapp.net/attachments/300000000000000001/500000000000000012/clip2.wav?ex=ghi"
	syntheticAudio = "synthetic audio bytes"
	undecidedText  = "terminando de comer y me sumo"
)

type audioCall struct {
	system string
	text   string
	audio  llm.Audio
}

type fakeAudioModel struct {
	mu       sync.Mutex
	response string
	err      error
	calls    []audioCall
}

func (a *fakeAudioModel) GenerateWithAudio(_ context.Context, system, text string, audio llm.Audio) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.calls = append(a.calls, audioCall{system: system, text: text, audio: llm.Audio{
		MIMEType: audio.MIMEType,
		Data:     slices.Clone(audio.Data),
	}})
	if a.err != nil {
		return "", a.err
	}

	return a.response, nil
}

func (a *fakeAudioModel) SetError(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.err = err
}

func (a *fakeAudioModel) Calls() []audioCall {
	a.mu.Lock()
	defer a.mu.Unlock()

	return slices.Clone(a.calls)
}

type fetchCall struct {
	url      string
	maxBytes int
}

// mediaFetcher serves synthetic bytes for images and audio and records every request.
type mediaFetcher struct {
	mu      sync.Mutex
	failFor map[string]bool
	calls   []fetchCall
}

func (f *mediaFetcher) Fetch(_ context.Context, rawURL string, maxBytes int) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, fetchCall{url: rawURL, maxBytes: maxBytes})
	if f.failFor[rawURL] {
		return nil, errors.New("synthetic fetch failure")
	}

	return []byte(syntheticAudio + " " + rawURL), nil
}

func (f *mediaFetcher) Fail(rawURL string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.failFor[rawURL] = true
}

func (f *mediaFetcher) Calls() []fetchCall {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.calls)
}

func (f *mediaFetcher) URLs() []string {
	urls := make([]string, 0)
	for _, call := range f.Calls() {
		urls = append(urls, call.url)
	}

	return urls
}

type audioHarness struct {
	svc     *capitime.Service
	clock   *fakeClock
	audio   *fakeAudioModel
	vision  *fakeVision
	fetcher *mediaFetcher
}

type audioHarnessOptions struct {
	debounce   string
	noAudio    bool
	withVision bool
	textLLM    capitime.LLMGenerator
	audioJSON  string
	visionJSON string
}

func newAudioHarness(t *testing.T, opts audioHarnessOptions) *audioHarness {
	t.Helper()
	resetGuildConfigs(t)

	debounce := opts.debounce
	if debounce == "" {
		debounce = "0s"
	}
	audioJSON := opts.audioJSON
	if audioJSON == "" {
		audioJSON = arrivalJSON
	}
	visionJSON := opts.visionJSON
	if visionJSON == "" {
		visionJSON = noArrivalJSON
	}

	clk := newFakeClock(time.Date(2026, 11, 1, 21, 0, 0, 0, capitime.Santiago()))
	settings := newFakeSettings(map[string]string{
		capitime.SettingDebounceWindow:   debounce,
		capitime.SettingEasterEggPercent: "0",
		capitime.SettingDelayMinMinutes:  "60",
		capitime.SettingDelayMaxMinutes:  "60",
	})

	harness := &audioHarness{
		clock:   clk,
		audio:   &fakeAudioModel{response: audioJSON},
		vision:  &fakeVision{response: visionJSON},
		fetcher: &mediaFetcher{failFor: map[string]bool{}},
	}

	deps := capitime.Deps{
		Pool:     shared.Pool(),
		Clock:    clk.Now,
		Settings: settings,
		LLM:      opts.textLLM,
		Images:   harness.fetcher,
	}
	if !opts.noAudio {
		deps.Audio = harness.audio
	}
	if opts.withVision {
		deps.Vision = harness.vision
	}

	svc, err := capitime.New(deps)
	if err != nil {
		t.Fatalf("capitime.New: %v", err)
	}
	harness.svc = svc

	manager := capitime.GuildScope{GuildID: guildOneID, CanManageGuild: true}
	target := capiUserID
	channel := chanGeneralID
	_, err = svc.UpdateGuildConfig(t.Context(), manager, capitime.GuildConfigUpdate{
		TargetUserID: &target,
		ChannelID:    &channel,
	})
	if err != nil {
		t.Fatalf("UpdateGuildConfig: %v", err)
	}

	return harness
}

func audioMessage(content string, audio ...capitime.AudioRef) capitime.MessageInput {
	return capitime.MessageInput{
		GuildID:   guildOneID,
		ChannelID: chanGeneralID,
		MessageID: msgOneID,
		AuthorID:  capiUserID,
		Content:   content,
		Audio:     audio,
	}
}

func voiceRef() capitime.AudioRef {
	return capitime.AudioRef{
		URL: voiceURL, MIMEType: "audio/ogg", Size: 20000, DurationSeconds: 4.5, IsVoiceMessage: true,
	}
}

func fileRef() capitime.AudioRef {
	return capitime.AudioRef{URL: audioFileURL, MIMEType: "audio/mpeg", Size: 90000}
}

func (h *audioHarness) handle(t *testing.T, msg capitime.MessageInput) capitime.Reply {
	t.Helper()

	reply, err := h.svc.HandleMessage(t.Context(), msg)
	if err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	return reply
}

func (h *audioHarness) assertUntouched(t *testing.T) {
	t.Helper()

	if calls := h.fetcher.Calls(); len(calls) != 0 {
		t.Errorf("fetcher called %d times, want 0", len(calls))
	}
	if calls := h.audio.Calls(); len(calls) != 0 {
		t.Errorf("audio model called %d times, want 0", len(calls))
	}
}

func TestHandleMessageRepliesToVoiceMessageArrival(t *testing.T) {
	h := newAudioHarness(t, audioHarnessOptions{})

	reply := h.handle(t, audioMessage("", voiceRef()))
	if !reply.Handled || !reply.Prediction.Immediate {
		t.Fatalf("reply = %+v, want a handled immediate arrival", reply)
	}
	if reply.ReplyToID != msgOneID || !strings.Contains(reply.Content, "Capi Time") {
		t.Errorf("reply = %+v, want a Capi Time reply to the voice message", reply)
	}

	calls := h.fetcher.Calls()
	if len(calls) != 1 || calls[0].url != voiceURL || calls[0].maxBytes != llm.MaxAudioBytes {
		t.Fatalf("fetch calls = %+v, want one fetch of the voice message with the audio size limit", calls)
	}

	modelCalls := h.audio.Calls()
	if len(modelCalls) != 1 {
		t.Fatalf("audio model calls = %d, want 1", len(modelCalls))
	}
	if modelCalls[0].text != "" {
		t.Errorf("audio model text = %q, want empty for a voice message without caption", modelCalls[0].text)
	}
	if !strings.Contains(modelCalls[0].system, "mensaje de voz") || !strings.Contains(modelCalls[0].system, `"arrival"`) {
		t.Errorf("audio system instruction = %q, want speech guidance and the JSON contract", modelCalls[0].system)
	}
	if modelCalls[0].audio.MIMEType != "audio/ogg" ||
		string(modelCalls[0].audio.Data) != syntheticAudio+" "+voiceURL {
		t.Errorf("audio = %+v, want the fetched bytes as audio/ogg", modelCalls[0].audio)
	}
}

func TestHandleMessageRepliesToAudioFileArrival(t *testing.T) {
	h := newAudioHarness(t, audioHarnessOptions{})

	file := capitime.AudioRef{URL: audioFileURL, MIMEType: "Audio/MPEG; charset=binary"}
	reply := h.handle(t, audioMessage("   ", file))
	if !reply.Handled {
		t.Fatalf("reply = %+v, want handled for an audio file with no announced size or duration", reply)
	}

	modelCalls := h.audio.Calls()
	if len(modelCalls) != 1 || modelCalls[0].audio.MIMEType != "audio/mpeg" {
		t.Fatalf("audio model calls = %+v, want one call with the normalized audio/mpeg type", modelCalls)
	}
}

func TestHandleMessageCaptionDecidedByStageOneSkipsAudio(t *testing.T) {
	h := newAudioHarness(t, audioHarnessOptions{})

	reply := h.handle(t, audioMessage("22:30 llego", voiceRef()))
	if !reply.Handled {
		t.Fatalf("reply = %+v, want the stage 1 claim to be trusted", reply)
	}
	h.assertUntouched(t)

	past := h.handle(t, audioMessage("ayer llegué a las 22:30", voiceRef()))
	if past.Handled {
		t.Fatalf("past-tense caption reply = %+v, want silence", past)
	}
	h.assertUntouched(t)
}

func TestHandleMessageUndecidedCaptionIsHeardWithAudio(t *testing.T) {
	textLLM := &classifierSpy{}
	h := newAudioHarness(t, audioHarnessOptions{textLLM: textLLM})

	reply := h.handle(t, audioMessage("escucha\x00 esto", voiceRef()))
	if !reply.Handled {
		t.Fatalf("reply = %+v, want handled", reply)
	}

	calls := h.audio.Calls()
	if len(calls) != 1 || calls[0].text != "escucha  esto" {
		t.Fatalf("audio model calls = %+v, want one call with the sanitized caption", calls)
	}
	if textLLM.Calls() != 0 {
		t.Errorf("text classifier calls = %d, want 0 when the audio model reads the message", textLLM.Calls())
	}
}

func TestHandleMessageStaysSilentBeforeAnyAudioFetch(t *testing.T) {
	t.Run("author is not the target", func(t *testing.T) {
		h := newAudioHarness(t, audioHarnessOptions{})
		msg := audioMessage("", voiceRef())
		msg.AuthorID = otherUserID

		if reply := h.handle(t, msg); reply.Handled {
			t.Fatalf("reply = %+v, want silence", reply)
		}
		h.assertUntouched(t)
	})

	t.Run("channel is not monitored", func(t *testing.T) {
		h := newAudioHarness(t, audioHarnessOptions{})
		msg := audioMessage("", voiceRef())
		msg.ChannelID = chanOfftopic

		if reply := h.handle(t, msg); reply.Handled {
			t.Fatalf("reply = %+v, want silence", reply)
		}
		h.assertUntouched(t)
	})

	t.Run("author is a bot", func(t *testing.T) {
		h := newAudioHarness(t, audioHarnessOptions{})
		msg := audioMessage("", voiceRef())
		msg.AuthorBot = true

		if reply := h.handle(t, msg); reply.Handled {
			t.Fatalf("reply = %+v, want silence", reply)
		}
		h.assertUntouched(t)
	})

	t.Run("message has neither text, images nor audio", func(t *testing.T) {
		h := newAudioHarness(t, audioHarnessOptions{})

		if reply := h.handle(t, audioMessage("")); reply.Handled {
			t.Fatalf("reply = %+v, want silence", reply)
		}
		h.assertUntouched(t)
	})

	t.Run("reply is inside the debounce window", func(t *testing.T) {
		h := newAudioHarness(t, audioHarnessOptions{debounce: "2m"})

		if first := h.handle(t, audioMessage("", voiceRef())); !first.Handled {
			t.Fatalf("first reply = %+v, want handled", first)
		}
		fetchedBefore := len(h.fetcher.Calls())
		modelBefore := len(h.audio.Calls())

		h.clock.Advance(30 * time.Second)
		if second := h.handle(t, audioMessage("", voiceRef())); second.Handled {
			t.Fatalf("second reply = %+v, want debounced", second)
		}
		if len(h.fetcher.Calls()) != fetchedBefore || len(h.audio.Calls()) != modelBefore {
			t.Error("debounced message reached the fetcher or the audio model")
		}
	})
}

func TestHandleMessageIgnoresUnusableAudio(t *testing.T) {
	cases := []struct {
		name  string
		audio capitime.AudioRef
	}{
		{"image type", capitime.AudioRef{URL: voiceURL, MIMEType: "image/png"}},
		{"video type", capitime.AudioRef{URL: voiceURL, MIMEType: "video/mp4"}},
		{"empty type", capitime.AudioRef{URL: voiceURL}},
		{"unlisted audio type", capitime.AudioRef{URL: voiceURL, MIMEType: "audio/midi"}},
		{"oversize file", capitime.AudioRef{URL: voiceURL, MIMEType: "audio/ogg", Size: llm.MaxAudioBytes + 1}},
		{"negative size", capitime.AudioRef{URL: voiceURL, MIMEType: "audio/ogg", Size: -1}},
		{"too long", capitime.AudioRef{URL: voiceURL, MIMEType: "audio/ogg", DurationSeconds: llm.MaxAudioSeconds + 0.5}},
		{"negative duration", capitime.AudioRef{URL: voiceURL, MIMEType: "audio/ogg", DurationSeconds: -1}},
		{"NaN duration", capitime.AudioRef{URL: voiceURL, MIMEType: "audio/ogg", DurationSeconds: math.NaN()}},
		{"infinite duration", capitime.AudioRef{URL: voiceURL, MIMEType: "audio/ogg", DurationSeconds: math.Inf(1)}},
		{"foreign host", capitime.AudioRef{URL: "https://example.com/a.ogg", MIMEType: "audio/ogg"}},
		{"look-alike host", capitime.AudioRef{URL: "https://cdn.discordapp.com.example.com/a.ogg", MIMEType: "audio/ogg"}},
		{"credentials in URL", capitime.AudioRef{URL: "https://cdn.discordapp.com@example.com/a.ogg", MIMEType: "audio/ogg"}},
		{"plain HTTP", capitime.AudioRef{URL: "http://cdn.discordapp.com/a.ogg", MIMEType: "audio/ogg"}},
		{"empty URL", capitime.AudioRef{MIMEType: "audio/ogg"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newAudioHarness(t, audioHarnessOptions{})

			if reply := h.handle(t, audioMessage("", tc.audio)); reply.Handled {
				t.Fatalf("reply = %+v, want silence", reply)
			}
			h.assertUntouched(t)
		})
	}

	t.Run("an unusable audio beside a usable one is dropped", func(t *testing.T) {
		h := newAudioHarness(t, audioHarnessOptions{})
		tooLong := capitime.AudioRef{URL: voiceURL, MIMEType: "audio/ogg", DurationSeconds: 300}

		if reply := h.handle(t, audioMessage("", tooLong, fileRef())); !reply.Handled {
			t.Fatalf("reply = %+v, want handled", reply)
		}
		if urls := h.fetcher.URLs(); !slices.Equal(urls, []string{audioFileURL}) {
			t.Errorf("fetched %v, want only the usable audio", urls)
		}
	})
}

func TestHandleMessageReadsAudioAtTheLimits(t *testing.T) {
	t.Run("size exactly at the limit", func(t *testing.T) {
		h := newAudioHarness(t, audioHarnessOptions{})
		atLimit := capitime.AudioRef{URL: voiceURL, MIMEType: "audio/ogg", Size: llm.MaxAudioBytes}

		if reply := h.handle(t, audioMessage("", atLimit)); !reply.Handled {
			t.Fatalf("reply = %+v, want an audio of exactly the size limit to be read", reply)
		}
	})

	t.Run("duration exactly at the limit", func(t *testing.T) {
		h := newAudioHarness(t, audioHarnessOptions{})
		atLimit := capitime.AudioRef{URL: voiceURL, MIMEType: "audio/ogg", DurationSeconds: llm.MaxAudioSeconds}

		if reply := h.handle(t, audioMessage("", atLimit)); !reply.Handled {
			t.Fatalf("reply = %+v, want an audio of exactly the duration limit to be read", reply)
		}
	})
}

func TestHandleMessageReadsOnlyTheFirstAudio(t *testing.T) {
	h := newAudioHarness(t, audioHarnessOptions{})
	second := capitime.AudioRef{URL: secondAudioURL, MIMEType: "audio/wav", Size: 10}

	reply := h.handle(t, audioMessage("", voiceRef(), second))
	if !reply.Handled {
		t.Fatalf("reply = %+v, want handled", reply)
	}

	if urls := h.fetcher.URLs(); !slices.Equal(urls, []string{voiceURL}) {
		t.Errorf("fetched %v, want only the first audio", urls)
	}
	if calls := h.audio.Calls(); len(calls) != 1 {
		t.Errorf("audio model calls = %d, want 1", len(calls))
	}
}

func TestHandleMessageStaysSilentWhenAudioCannotBeRead(t *testing.T) {
	t.Run("fetch fails", func(t *testing.T) {
		h := newAudioHarness(t, audioHarnessOptions{})
		h.fetcher.Fail(voiceURL)

		if reply := h.handle(t, audioMessage("", voiceRef())); reply.Handled {
			t.Fatalf("reply = %+v, want silence", reply)
		}
		if calls := h.audio.Calls(); len(calls) != 0 {
			t.Errorf("audio model called %d times after a failed fetch, want 0", len(calls))
		}
	})

	t.Run("model fails", func(t *testing.T) {
		h := newAudioHarness(t, audioHarnessOptions{})
		h.audio.SetError(errors.New("synthetic audio failure"))

		if reply := h.handle(t, audioMessage("", voiceRef())); reply.Handled {
			t.Fatalf("reply = %+v, want silence", reply)
		}
	})

	t.Run("model says no arrival", func(t *testing.T) {
		h := newAudioHarness(t, audioHarnessOptions{audioJSON: noArrivalJSON})

		if reply := h.handle(t, audioMessage("", voiceRef())); reply.Handled {
			t.Fatalf("reply = %+v, want silence for music or an unrelated voice message", reply)
		}
		if calls := h.audio.Calls(); len(calls) != 1 {
			t.Errorf("audio model calls = %d, want the audio to have been heard once", len(calls))
		}
	})

	t.Run("model returns text that is not JSON", func(t *testing.T) {
		h := newAudioHarness(t, audioHarnessOptions{audioJSON: "the voice says hello"})

		if reply := h.handle(t, audioMessage("", voiceRef())); reply.Handled {
			t.Fatalf("reply = %+v, want silence", reply)
		}
	})

	t.Run("a failed read does not use the debounce slot", func(t *testing.T) {
		h := newAudioHarness(t, audioHarnessOptions{debounce: "2m"})
		h.audio.SetError(errors.New("synthetic audio failure"))

		if reply := h.handle(t, audioMessage("", voiceRef())); reply.Handled {
			t.Fatalf("reply = %+v, want silence", reply)
		}

		h.audio.SetError(nil)

		if reply := h.handle(t, audioMessage("", voiceRef())); !reply.Handled {
			t.Fatalf("reply after recovery = %+v, want handled: a failed read must not use the debounce slot", reply)
		}
	})
}

func TestHandleMessageUndecidedCaptionFallsBackToTextClassifierWhenAudioYieldsNothing(t *testing.T) {
	t.Run("audio fetch fails", func(t *testing.T) {
		textLLM := &classifierSpy{}
		h := newAudioHarness(t, audioHarnessOptions{textLLM: textLLM})
		h.fetcher.Fail(voiceURL)

		reply := h.handle(t, audioMessage(undecidedText, voiceRef()))
		if !reply.Handled || textLLM.Calls() != 1 {
			t.Fatalf("reply = %+v, text classifier calls = %d, want handled through one text call", reply, textLLM.Calls())
		}
		if calls := h.audio.Calls(); len(calls) != 0 {
			t.Errorf("audio model called %d times after a failed fetch, want 0", len(calls))
		}
	})

	t.Run("audio model fails", func(t *testing.T) {
		textLLM := &classifierSpy{}
		h := newAudioHarness(t, audioHarnessOptions{textLLM: textLLM})
		h.audio.SetError(errors.New("synthetic audio failure"))

		reply := h.handle(t, audioMessage(undecidedText, voiceRef()))
		if !reply.Handled || textLLM.Calls() != 1 {
			t.Fatalf("reply = %+v, text classifier calls = %d, want handled through one text call", reply, textLLM.Calls())
		}
	})

	t.Run("audio is unusable", func(t *testing.T) {
		textLLM := &classifierSpy{}
		h := newAudioHarness(t, audioHarnessOptions{textLLM: textLLM})
		tooLong := capitime.AudioRef{URL: voiceURL, MIMEType: "audio/ogg", DurationSeconds: 600}

		reply := h.handle(t, audioMessage(undecidedText, tooLong))
		if !reply.Handled || textLLM.Calls() != 1 {
			t.Fatalf("reply = %+v, text classifier calls = %d, want handled through one text call", reply, textLLM.Calls())
		}
		h.assertUntouched(t)
	})
}

func TestHandleMessageWithoutAudioModelKeepsTextBehavior(t *testing.T) {
	t.Run("voice message without text is ignored", func(t *testing.T) {
		h := newAudioHarness(t, audioHarnessOptions{noAudio: true})

		if reply := h.handle(t, audioMessage("", voiceRef())); reply.Handled {
			t.Fatalf("reply = %+v, want silence", reply)
		}
		h.assertUntouched(t)
	})

	t.Run("undecided caption goes to the text classifier", func(t *testing.T) {
		textLLM := &classifierSpy{}
		h := newAudioHarness(t, audioHarnessOptions{noAudio: true, textLLM: textLLM})

		reply := h.handle(t, audioMessage(undecidedText, voiceRef()))
		if !reply.Handled || textLLM.Calls() != 1 {
			t.Fatalf("reply = %+v, text classifier calls = %d, want handled through one text call", reply, textLLM.Calls())
		}
		h.assertUntouched(t)
	})
}

func TestHandleMessageTriesImagesBeforeAudio(t *testing.T) {
	t.Run("audio is not read when the image already gave a claim", func(t *testing.T) {
		h := newAudioHarness(t, audioHarnessOptions{withVision: true, visionJSON: arrivalJSON})

		msg := audioMessage("", voiceRef())
		msg.Images = []capitime.ImageRef{stickerRef("VOY")}

		if reply := h.handle(t, msg); !reply.Handled {
			t.Fatalf("reply = %+v, want handled", reply)
		}
		if calls := h.vision.Calls(); len(calls) != 1 {
			t.Errorf("vision calls = %d, want 1", len(calls))
		}
		if calls := h.audio.Calls(); len(calls) != 0 {
			t.Errorf("audio model called %d times, want 0 once the image path gave a claim", len(calls))
		}
		if urls := h.fetcher.URLs(); !slices.Equal(urls, []string{stickerURL}) {
			t.Errorf("fetched %v, want only the sticker", urls)
		}
	})

	t.Run("audio is read when the image path gave nothing", func(t *testing.T) {
		h := newAudioHarness(t, audioHarnessOptions{withVision: true, visionJSON: noArrivalJSON})

		msg := audioMessage("", voiceRef())
		msg.Images = []capitime.ImageRef{attachmentRef()}

		if reply := h.handle(t, msg); !reply.Handled {
			t.Fatalf("reply = %+v, want handled through the audio", reply)
		}
		if calls := h.vision.Calls(); len(calls) != 1 {
			t.Errorf("vision calls = %d, want 1", len(calls))
		}
		if calls := h.audio.Calls(); len(calls) != 1 {
			t.Errorf("audio model calls = %d, want 1", len(calls))
		}
	})

	t.Run("audio is read when the message has no usable image", func(t *testing.T) {
		h := newAudioHarness(t, audioHarnessOptions{withVision: true})

		msg := audioMessage("", voiceRef())
		msg.Images = []capitime.ImageRef{{URL: stickerURL, MIMEType: "application/json"}}

		if reply := h.handle(t, msg); !reply.Handled {
			t.Fatalf("reply = %+v, want handled through the audio", reply)
		}
		if calls := h.vision.Calls(); len(calls) != 0 {
			t.Errorf("vision calls = %d, want 0 for an unusable image", len(calls))
		}
	})
}

func FuzzHandleMessageAudioRefs(f *testing.F) {
	seeds := []struct {
		rawURL, mime string
		size         int
		duration     float64
		isVoice      bool
	}{
		{voiceURL, "audio/ogg", 20000, 4.5, true},
		{audioFileURL, "audio/mpeg", 0, 0, false},
		{secondAudioURL, "audio/wav", 99, 30, false},
		{voiceURL, "video/mp4", 1, 1, false},
		{"http://cdn.discordapp.com/a.ogg", "audio/ogg", 1, 1, true},
		{"https://cdn.discordapp.com@example.com/x", "audio/ogg", 1, 1, true},
		{"https://cdn.discordapp.com.evil.example/x", "audio/ogg", 1, 1, true},
		{"https://CDN.DISCORDAPP.COM/a.ogg", "AUDIO/OGG; codecs=opus", 1, 1, true},
		{"https://cdn.discordapp.com:444/a.ogg", "audio/ogg", 1, 1, true},
		{"//cdn.discordapp.com/a.ogg", "audio/ogg", 1, 1, true},
		{"javascript:alert(1)", "audio/ogg", 1, 1, true},
		{"", "", 0, 0, false},
		{voiceURL, "audio/ogg", llm.MaxAudioBytes + 1, 1, true},
		{voiceURL, "audio/ogg", -5, 1, true},
		{voiceURL, "audio/ogg", 1, llm.MaxAudioSeconds + 1, true},
		{voiceURL, "audio/ogg", 1, -1, true},
		{voiceURL, "audio/ogg", 1, math.NaN(), true},
		{voiceURL, "audio/ogg", 1, math.Inf(1), true},
		{"https://cdn.discordapp.com/a\x00b.ogg", "audio/ogg", 1, 1, true},
		{"bad\xff\xfeutf8", "bad\xff\xfeutf8", 0, 0, false},
	}
	for _, seed := range seeds {
		f.Add(seed.rawURL, seed.mime, seed.size, seed.duration, seed.isVoice)
	}

	audioModel := &fakeAudioModel{response: noArrivalJSON}
	fetcher := &mediaFetcher{failFor: map[string]bool{}}

	_, err := shared.Pool().Exec(context.Background(), "DELETE FROM capitime.guild_config")
	if err != nil {
		f.Fatalf("reset capitime.guild_config: %v", err)
	}

	clk := newFakeClock(time.Date(2026, 11, 1, 21, 0, 0, 0, capitime.Santiago()))
	svc, err := capitime.New(capitime.Deps{
		Pool:     shared.Pool(),
		Clock:    clk.Now,
		Settings: newFakeSettings(map[string]string{capitime.SettingDebounceWindow: "0s"}),
		Audio:    audioModel,
		Images:   fetcher,
	})
	if err != nil {
		f.Fatalf("capitime.New: %v", err)
	}

	target := capiUserID
	_, err = svc.UpdateGuildConfig(context.Background(), capitime.GuildScope{GuildID: guildOneID, CanManageGuild: true},
		capitime.GuildConfigUpdate{TargetUserID: &target})
	if err != nil {
		f.Fatalf("UpdateGuildConfig: %v", err)
	}

	f.Fuzz(func(t *testing.T, rawURL, mime string, size int, duration float64, isVoice bool) {
		ref := capitime.AudioRef{URL: rawURL, MIMEType: mime, Size: size, DurationSeconds: duration, IsVoiceMessage: isVoice}

		alreadyFetched := len(fetcher.Calls())

		_, handleErr := svc.HandleMessage(t.Context(), audioMessage("", ref))
		if handleErr != nil {
			t.Fatalf("HandleMessage: %v", handleErr)
		}

		for _, call := range fetcher.Calls()[alreadyFetched:] {
			parsed, parseErr := url.Parse(call.url)
			if parseErr != nil {
				t.Fatalf("fetched an unparsable URL %q: %v", call.url, parseErr)
			}

			isAllowedHost := slices.Contains(capitime.DiscordImageHosts(), strings.ToLower(parsed.Host))
			if parsed.Scheme != "https" || parsed.User != nil || !isAllowedHost {
				t.Fatalf("fetched %q, want HTTPS on a Discord CDN host without credentials", call.url)
			}
			if call.maxBytes != llm.MaxAudioBytes {
				t.Fatalf("fetched with limit %d, want %d", call.maxBytes, llm.MaxAudioBytes)
			}
		}
	})
}
