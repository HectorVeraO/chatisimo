package capitime_test

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/HectorVeraO/chatisimo/internal/capitime"
	"github.com/HectorVeraO/chatisimo/internal/platform/llm"
)

const (
	arrivalJSON    = `{"arrival":true,"immediate":true,"offset_minutes":0,"clock":""}`
	noArrivalJSON  = `{"arrival":false}`
	stickerURL     = "https://cdn.discordapp.com/stickers/500000000000000001.png"
	attachmentURL  = "https://cdn.discordapp.com/attachments/300000000000000001/500000000000000002/photo.png?ex=abc"
	proxiedURL     = "https://media.discordapp.net/attachments/300000000000000001/500000000000000003/photo.jpg?ex=def"
	syntheticImage = "synthetic image bytes"
)

type visionCall struct {
	system string
	text   string
	images []llm.Image
}

type fakeVision struct {
	mu       sync.Mutex
	response string
	err      error
	calls    []visionCall
}

func (v *fakeVision) GenerateWithImages(_ context.Context, system, text string, images []llm.Image) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.calls = append(v.calls, visionCall{system: system, text: text, images: slices.Clone(images)})
	if v.err != nil {
		return "", v.err
	}

	return v.response, nil
}

func (v *fakeVision) Calls() []visionCall {
	v.mu.Lock()
	defer v.mu.Unlock()

	return slices.Clone(v.calls)
}

type fakeFetcher struct {
	mu      sync.Mutex
	failFor map[string]bool
	urls    []string
}

func (f *fakeFetcher) Fetch(_ context.Context, rawURL string, maxBytes int) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.urls = append(f.urls, rawURL)
	if maxBytes != llm.MaxImageBytes {
		return nil, errors.New("unexpected size limit")
	}
	if f.failFor[rawURL] {
		return nil, errors.New("synthetic fetch failure")
	}

	return []byte(syntheticImage + " " + rawURL), nil
}

func (f *fakeFetcher) URLs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.urls)
}

// classifierSpy answers the stage 2 text classifier with an arrival and counts those calls; any
// other generation request (banter) fails so the static fallback is used.
type classifierSpy struct {
	mu    sync.Mutex
	calls int
}

func (c *classifierSpy) GenerateWithPrompt(_ context.Context, system, _ string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !strings.Contains(system, "Clasifica") {
		return "", errors.New("synthetic banter failure")
	}

	c.calls++

	return arrivalJSON, nil
}

func (c *classifierSpy) Calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.calls
}

type imageHarness struct {
	svc     *capitime.Service
	clock   *fakeClock
	vision  *fakeVision
	fetcher *fakeFetcher
}

type imageHarnessOptions struct {
	debounce   string
	noVision   bool
	textLLM    capitime.LLMGenerator
	visionJSON string
}

func newImageHarness(t *testing.T, opts imageHarnessOptions) *imageHarness {
	t.Helper()
	resetGuildConfigs(t)

	debounce := opts.debounce
	if debounce == "" {
		debounce = "0s"
	}
	visionJSON := opts.visionJSON
	if visionJSON == "" {
		visionJSON = arrivalJSON
	}

	clk := newFakeClock(time.Date(2026, 11, 1, 21, 0, 0, 0, capitime.Santiago()))
	settings := newFakeSettings(map[string]string{
		capitime.SettingDebounceWindow:   debounce,
		capitime.SettingEasterEggPercent: "0",
		capitime.SettingDelayMinMinutes:  "60",
		capitime.SettingDelayMaxMinutes:  "60",
	})

	harness := &imageHarness{
		clock:   clk,
		vision:  &fakeVision{response: visionJSON},
		fetcher: &fakeFetcher{failFor: map[string]bool{}},
	}

	deps := capitime.Deps{
		Pool:     shared.Pool(),
		Clock:    clk.Now,
		Settings: settings,
		LLM:      opts.textLLM,
	}
	if !opts.noVision {
		deps.Vision = harness.vision
		deps.Images = harness.fetcher
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

func imageMessage(content string, images ...capitime.ImageRef) capitime.MessageInput {
	return capitime.MessageInput{
		GuildID:   guildOneID,
		ChannelID: chanGeneralID,
		MessageID: msgOneID,
		AuthorID:  capiUserID,
		Content:   content,
		Images:    images,
	}
}

func stickerRef(name string) capitime.ImageRef {
	return capitime.ImageRef{URL: stickerURL, MIMEType: "image/png", Name: name}
}

func attachmentRef() capitime.ImageRef {
	return capitime.ImageRef{URL: attachmentURL, MIMEType: "image/png", Size: 1234}
}

func (h *imageHarness) handle(t *testing.T, msg capitime.MessageInput) capitime.Reply {
	t.Helper()

	reply, err := h.svc.HandleMessage(t.Context(), msg)
	if err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	return reply
}

func (h *imageHarness) assertUntouched(t *testing.T) {
	t.Helper()

	if urls := h.fetcher.URLs(); len(urls) != 0 {
		t.Errorf("fetcher called %d times, want 0", len(urls))
	}
	if calls := h.vision.Calls(); len(calls) != 0 {
		t.Errorf("vision called %d times, want 0", len(calls))
	}
}

func TestHandleMessageRepliesToStickerOnlyArrival(t *testing.T) {
	h := newImageHarness(t, imageHarnessOptions{})

	reply := h.handle(t, imageMessage("", stickerRef("VOY")))
	if !reply.Handled || !reply.Prediction.Immediate {
		t.Fatalf("reply = %+v, want a handled immediate arrival", reply)
	}
	if reply.ReplyToID != msgOneID || !strings.Contains(reply.Content, "Capi Time") {
		t.Errorf("reply = %+v, want a Capi Time reply to the sticker message", reply)
	}

	if urls := h.fetcher.URLs(); !slices.Equal(urls, []string{stickerURL}) {
		t.Fatalf("fetched %v, want exactly the sticker URL", urls)
	}

	calls := h.vision.Calls()
	if len(calls) != 1 {
		t.Fatalf("vision calls = %d, want 1", len(calls))
	}
	if calls[0].text != "sticker: VOY" {
		t.Errorf("vision text = %q, want the sticker hint", calls[0].text)
	}
	if !strings.Contains(calls[0].system, "ENTRANDING") || !strings.Contains(calls[0].system, `"arrival"`) {
		t.Errorf("vision system instruction = %q, want text-in-image guidance and the JSON contract", calls[0].system)
	}
	if len(calls[0].images) != 1 || calls[0].images[0].MIMEType != "image/png" ||
		string(calls[0].images[0].Data) != syntheticImage+" "+stickerURL {
		t.Errorf("vision images = %+v, want the fetched sticker bytes as image/png", calls[0].images)
	}
}

func TestHandleMessageRepliesToAttachmentOnlyArrival(t *testing.T) {
	h := newImageHarness(t, imageHarnessOptions{})

	jpeg := capitime.ImageRef{URL: proxiedURL, MIMEType: "Image/JPEG; charset=binary", Size: 10}
	reply := h.handle(t, imageMessage("   ", attachmentRef(), jpeg))
	if !reply.Handled {
		t.Fatalf("reply = %+v, want handled", reply)
	}

	if urls := h.fetcher.URLs(); !slices.Equal(urls, []string{attachmentURL, proxiedURL}) {
		t.Fatalf("fetched %v, want both attachment URLs in order", urls)
	}

	calls := h.vision.Calls()
	if len(calls) != 1 || len(calls[0].images) != 2 {
		t.Fatalf("vision calls = %+v, want one call with two images", calls)
	}
	if calls[0].images[1].MIMEType != "image/jpeg" {
		t.Errorf("second image MIME = %q, want normalized image/jpeg", calls[0].images[1].MIMEType)
	}
	if calls[0].text != "" {
		t.Errorf("vision text = %q, want empty for an attachment without caption", calls[0].text)
	}
}

func TestHandleMessageCaptionDecidedByStageOneSkipsImages(t *testing.T) {
	h := newImageHarness(t, imageHarnessOptions{})

	reply := h.handle(t, imageMessage("22:30 llego", stickerRef("VOY")))
	if !reply.Handled {
		t.Fatalf("reply = %+v, want the stage 1 claim to be trusted", reply)
	}
	h.assertUntouched(t)

	past := h.handle(t, imageMessage("ayer llegué a las 22:30", stickerRef("VOY")))
	if past.Handled {
		t.Fatalf("past-tense caption reply = %+v, want silence", past)
	}
	h.assertUntouched(t)
}

func TestHandleMessageUndecidedCaptionIsReadWithImages(t *testing.T) {
	textLLM := &classifierSpy{}
	h := newImageHarness(t, imageHarnessOptions{textLLM: textLLM})

	reply := h.handle(t, imageMessage("mira\x00 esto", attachmentRef()))
	if !reply.Handled {
		t.Fatalf("reply = %+v, want handled", reply)
	}

	calls := h.vision.Calls()
	if len(calls) != 1 || calls[0].text != "mira  esto" {
		t.Fatalf("vision calls = %+v, want one call with the sanitized caption", calls)
	}
	if textLLM.Calls() != 0 {
		t.Errorf("text classifier calls = %d, want 0 when the vision model reads the message", textLLM.Calls())
	}
}

func TestHandleMessageStaysSilentBeforeAnyFetch(t *testing.T) {
	t.Run("author is not the target", func(t *testing.T) {
		h := newImageHarness(t, imageHarnessOptions{})
		msg := imageMessage("", stickerRef("VOY"))
		msg.AuthorID = otherUserID

		if reply := h.handle(t, msg); reply.Handled {
			t.Fatalf("reply = %+v, want silence", reply)
		}
		h.assertUntouched(t)
	})

	t.Run("channel is not monitored", func(t *testing.T) {
		h := newImageHarness(t, imageHarnessOptions{})
		msg := imageMessage("", stickerRef("VOY"))
		msg.ChannelID = chanOfftopic

		if reply := h.handle(t, msg); reply.Handled {
			t.Fatalf("reply = %+v, want silence", reply)
		}
		h.assertUntouched(t)
	})

	t.Run("author is a bot", func(t *testing.T) {
		h := newImageHarness(t, imageHarnessOptions{})
		msg := imageMessage("", stickerRef("VOY"))
		msg.AuthorBot = true

		if reply := h.handle(t, msg); reply.Handled {
			t.Fatalf("reply = %+v, want silence", reply)
		}
		h.assertUntouched(t)
	})

	t.Run("message has neither text nor images", func(t *testing.T) {
		h := newImageHarness(t, imageHarnessOptions{})

		if reply := h.handle(t, imageMessage("")); reply.Handled {
			t.Fatalf("reply = %+v, want silence", reply)
		}
		h.assertUntouched(t)
	})

	t.Run("reply is inside the debounce window", func(t *testing.T) {
		h := newImageHarness(t, imageHarnessOptions{debounce: "2m"})

		if first := h.handle(t, imageMessage("", stickerRef("VOY"))); !first.Handled {
			t.Fatalf("first reply = %+v, want handled", first)
		}
		fetchedBefore := len(h.fetcher.URLs())
		visionBefore := len(h.vision.Calls())

		h.clock.Advance(30 * time.Second)
		if second := h.handle(t, imageMessage("", stickerRef("VOY"))); second.Handled {
			t.Fatalf("second reply = %+v, want debounced", second)
		}
		if len(h.fetcher.URLs()) != fetchedBefore || len(h.vision.Calls()) != visionBefore {
			t.Error("debounced message reached the fetcher or the vision model")
		}
	})
}

func TestHandleMessageIgnoresUnusableImages(t *testing.T) {
	valid := attachmentRef()
	cases := []struct {
		name   string
		images []capitime.ImageRef
	}{
		{"Lottie sticker type", []capitime.ImageRef{{URL: stickerURL, MIMEType: "application/json"}}},
		{"video type", []capitime.ImageRef{{URL: attachmentURL, MIMEType: "video/mp4"}}},
		{"empty type", []capitime.ImageRef{{URL: attachmentURL}}},
		{"oversize attachment", []capitime.ImageRef{{URL: attachmentURL, MIMEType: "image/png", Size: llm.MaxImageBytes + 1}}},
		{"negative size", []capitime.ImageRef{{URL: attachmentURL, MIMEType: "image/png", Size: -1}}},
		{"foreign host", []capitime.ImageRef{{URL: "https://example.com/a.png", MIMEType: "image/png"}}},
		{"look-alike host", []capitime.ImageRef{{URL: "https://cdn.discordapp.com.example.com/a.png", MIMEType: "image/png"}}},
		{"credentials in URL", []capitime.ImageRef{{URL: "https://cdn.discordapp.com@example.com/a.png", MIMEType: "image/png"}}},
		{"explicit port", []capitime.ImageRef{{URL: "https://cdn.discordapp.com:8443/a.png", MIMEType: "image/png"}}},
		{"plain HTTP", []capitime.ImageRef{{URL: "http://cdn.discordapp.com/a.png", MIMEType: "image/png"}}},
		{"empty URL", []capitime.ImageRef{{MIMEType: "image/png"}}},
		{"more usable images than the model accepts", []capitime.ImageRef{valid, valid, valid}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newImageHarness(t, imageHarnessOptions{})

			if reply := h.handle(t, imageMessage("", tc.images...)); reply.Handled {
				t.Fatalf("reply = %+v, want silence", reply)
			}
			h.assertUntouched(t)
		})
	}

	t.Run("an unusable image beside a usable one is dropped", func(t *testing.T) {
		h := newImageHarness(t, imageHarnessOptions{})
		lottie := capitime.ImageRef{URL: stickerURL, MIMEType: "application/json"}

		if reply := h.handle(t, imageMessage("", lottie, valid)); !reply.Handled {
			t.Fatalf("reply = %+v, want handled", reply)
		}
		if urls := h.fetcher.URLs(); !slices.Equal(urls, []string{attachmentURL}) {
			t.Errorf("fetched %v, want only the usable image", urls)
		}
	})
}

func TestHandleMessageReadsAttachmentAtTheSizeLimit(t *testing.T) {
	h := newImageHarness(t, imageHarnessOptions{})
	atLimit := capitime.ImageRef{URL: attachmentURL, MIMEType: "image/png", Size: llm.MaxImageBytes}

	reply := h.handle(t, imageMessage("", atLimit))
	if !reply.Handled {
		t.Fatalf("reply = %+v, want an attachment of exactly the size limit to be read", reply)
	}
}

func TestHandleMessageStaysSilentWhenImagesCannotBeRead(t *testing.T) {
	t.Run("first fetch fails", func(t *testing.T) {
		h := newImageHarness(t, imageHarnessOptions{})
		h.fetcher.failFor[stickerURL] = true

		if reply := h.handle(t, imageMessage("", stickerRef("VOY"))); reply.Handled {
			t.Fatalf("reply = %+v, want silence", reply)
		}
		if calls := h.vision.Calls(); len(calls) != 0 {
			t.Errorf("vision called %d times after a failed fetch, want 0", len(calls))
		}
	})

	t.Run("second fetch fails", func(t *testing.T) {
		h := newImageHarness(t, imageHarnessOptions{})
		h.fetcher.failFor[proxiedURL] = true
		second := capitime.ImageRef{URL: proxiedURL, MIMEType: "image/jpeg"}

		if reply := h.handle(t, imageMessage("", attachmentRef(), second)); reply.Handled {
			t.Fatalf("reply = %+v, want silence", reply)
		}
		if calls := h.vision.Calls(); len(calls) != 0 {
			t.Errorf("vision called %d times after a failed fetch, want 0", len(calls))
		}
	})

	t.Run("vision says no arrival", func(t *testing.T) {
		h := newImageHarness(t, imageHarnessOptions{visionJSON: noArrivalJSON})

		if reply := h.handle(t, imageMessage("", attachmentRef())); reply.Handled {
			t.Fatalf("reply = %+v, want silence", reply)
		}
	})

	t.Run("vision returns text that is not JSON", func(t *testing.T) {
		h := newImageHarness(t, imageHarnessOptions{visionJSON: "the sticker says hello"})

		if reply := h.handle(t, imageMessage("", attachmentRef())); reply.Handled {
			t.Fatalf("reply = %+v, want silence", reply)
		}
	})

	t.Run("vision fails and a later message still gets a reply", func(t *testing.T) {
		h := newImageHarness(t, imageHarnessOptions{debounce: "2m"})
		h.vision.err = errors.New("synthetic vision failure")

		if reply := h.handle(t, imageMessage("", attachmentRef())); reply.Handled {
			t.Fatalf("reply = %+v, want silence", reply)
		}

		h.vision.mu.Lock()
		h.vision.err = nil
		h.vision.mu.Unlock()

		if reply := h.handle(t, imageMessage("", attachmentRef())); !reply.Handled {
			t.Fatalf("reply after recovery = %+v, want handled: a failed read must not use the debounce slot", reply)
		}
	})
}

func TestHandleMessageWithoutVisionKeepsTextBehavior(t *testing.T) {
	t.Run("sticker without text is ignored", func(t *testing.T) {
		h := newImageHarness(t, imageHarnessOptions{noVision: true})

		if reply := h.handle(t, imageMessage("", stickerRef("VOY"))); reply.Handled {
			t.Fatalf("reply = %+v, want silence", reply)
		}
		h.assertUntouched(t)
	})

	t.Run("undecided caption goes to the text classifier", func(t *testing.T) {
		textLLM := &classifierSpy{}
		h := newImageHarness(t, imageHarnessOptions{noVision: true, textLLM: textLLM})

		reply := h.handle(t, imageMessage("terminando de comer y me sumo", stickerRef("VOY")))
		if !reply.Handled || textLLM.Calls() != 1 {
			t.Fatalf("reply = %+v, text classifier calls = %d, want handled through one text call", reply, textLLM.Calls())
		}
	})
}

func TestHandleMessageUndecidedCaptionWithUnusableImageUsesTextClassifier(t *testing.T) {
	textLLM := &classifierSpy{}
	h := newImageHarness(t, imageHarnessOptions{textLLM: textLLM})
	lottie := capitime.ImageRef{URL: stickerURL, MIMEType: "application/json"}

	reply := h.handle(t, imageMessage("terminando de comer y me sumo", lottie))
	if !reply.Handled || textLLM.Calls() != 1 {
		t.Fatalf("reply = %+v, text classifier calls = %d, want handled through one text call", reply, textLLM.Calls())
	}
	h.assertUntouched(t)
}

func TestHandleMessageUndecidedCaptionFallsBackToTextClassifierWhenImagesYieldNothing(t *testing.T) {
	const caption = "terminando de comer y me sumo"

	t.Run("image fetch fails", func(t *testing.T) {
		textLLM := &classifierSpy{}
		h := newImageHarness(t, imageHarnessOptions{textLLM: textLLM})
		h.fetcher.failFor[attachmentURL] = true

		reply := h.handle(t, imageMessage(caption, attachmentRef()))
		if !reply.Handled || textLLM.Calls() != 1 {
			t.Fatalf("reply = %+v, text classifier calls = %d, want handled through one text call", reply, textLLM.Calls())
		}
		if calls := h.vision.Calls(); len(calls) != 0 {
			t.Errorf("vision called %d times after a failed fetch, want 0", len(calls))
		}
	})

	t.Run("vision call fails", func(t *testing.T) {
		textLLM := &classifierSpy{}
		h := newImageHarness(t, imageHarnessOptions{textLLM: textLLM})
		h.vision.err = errors.New("synthetic vision failure")

		reply := h.handle(t, imageMessage(caption, attachmentRef()))
		if !reply.Handled || textLLM.Calls() != 1 {
			t.Fatalf("reply = %+v, text classifier calls = %d, want handled through one text call", reply, textLLM.Calls())
		}
	})
}

func TestHandleMessageSanitizesStickerHints(t *testing.T) {
	h := newImageHarness(t, imageHarnessOptions{})

	longName := strings.Repeat("\u05d0", 200)
	reply := h.handle(t, imageMessage("", stickerRef("VOY\x00\n"+longName)))
	if !reply.Handled {
		t.Fatalf("reply = %+v, want handled", reply)
	}

	calls := h.vision.Calls()
	if len(calls) != 1 {
		t.Fatalf("vision calls = %d, want 1", len(calls))
	}
	if strings.ContainsAny(calls[0].text, "\x00") || strings.Count(calls[0].text, "\n") != 0 {
		t.Errorf("vision text %q keeps control characters", calls[0].text)
	}
	if runes := utf8.RuneCountInString(calls[0].text); runes > len("sticker: ")+64 {
		t.Errorf("vision text has %d runes, want the sticker name capped at 64", runes)
	}
}

func FuzzHandleMessageImageRefs(f *testing.F) {
	seeds := []struct {
		rawURL, mime, name string
		size               int
	}{
		{stickerURL, "image/png", "VOY", 0},
		{attachmentURL, "image/jpeg", "", 1234},
		{proxiedURL, "image/webp", "ENTRANDING", 99},
		{stickerURL, "application/json", "lottie", 0},
		{"http://cdn.discordapp.com/a.png", "image/png", "", 1},
		{"https://cdn.discordapp.com@example.com/x", "image/png", "", 1},
		{"https://cdn.discordapp.com.evil.example/x", "image/png", "", 1},
		{"https://CDN.DISCORDAPP.COM/a.png", "IMAGE/PNG; q=1", "", 1},
		{"https://cdn.discordapp.com:444/a.png", "image/png", "", 1},
		{"//cdn.discordapp.com/a.png", "image/png", "", 1},
		{"javascript:alert(1)", "image/png", "", 1},
		{"", "", "", 0},
		{attachmentURL, "image/png", "", llm.MaxImageBytes + 1},
		{attachmentURL, "image/png", "", -5},
		{stickerURL, "image/png", strings.Repeat("\u202e\u200b\u05d0", 40), 0},
		{stickerURL, "image/png", "bad\xff\xfeutf8", 0},
		{"https://cdn.discordapp.com/a\x00b.png", "image/png", "", 1},
	}
	for _, seed := range seeds {
		f.Add(seed.rawURL, seed.mime, seed.name, seed.size)
	}

	vision := &fakeVision{response: noArrivalJSON}
	fetcher := &fakeFetcher{failFor: map[string]bool{}}

	_, err := shared.Pool().Exec(context.Background(), "DELETE FROM capitime.guild_config")
	if err != nil {
		f.Fatalf("reset capitime.guild_config: %v", err)
	}

	clk := newFakeClock(time.Date(2026, 11, 1, 21, 0, 0, 0, capitime.Santiago()))
	svc, err := capitime.New(capitime.Deps{
		Pool:     shared.Pool(),
		Clock:    clk.Now,
		Settings: newFakeSettings(map[string]string{capitime.SettingDebounceWindow: "0s"}),
		Vision:   vision,
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

	f.Fuzz(func(t *testing.T, rawURL, mime, name string, size int) {
		msg := imageMessage("", capitime.ImageRef{URL: rawURL, MIMEType: mime, Name: name, Size: size})

		_, handleErr := svc.HandleMessage(t.Context(), msg)
		if handleErr != nil {
			t.Fatalf("HandleMessage: %v", handleErr)
		}

		for _, fetched := range fetcher.URLs() {
			parsed, parseErr := url.Parse(fetched)
			if parseErr != nil {
				t.Fatalf("fetched an unparsable URL %q: %v", fetched, parseErr)
			}

			isAllowedHost := slices.Contains(capitime.DiscordImageHosts(), strings.ToLower(parsed.Host))
			if parsed.Scheme != "https" || parsed.User != nil || !isAllowedHost {
				t.Fatalf("fetched %q, want HTTPS on a Discord CDN host without credentials", fetched)
			}
		}
	})
}
