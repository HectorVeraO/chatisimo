package capitime_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
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
	arrivalJSON    = `{"arrival":true,"day":"","clock":"","minutes":0}`
	noArrivalJSON  = `{"arrival":false,"day":"","clock":"","minutes":0}`
	stickerURL     = "https://cdn.discordapp.com/stickers/500000000000000001.png"
	attachmentURL  = "https://cdn.discordapp.com/attachments/300000000000000001/500000000000000002/photo.png?ex=abc"
	proxiedURL     = "https://media.discordapp.net/attachments/300000000000000001/500000000000000003/photo.jpg?ex=def"
	voiceURL       = "https://cdn.discordapp.com/attachments/300000000000000001/500000000000000010/voice-message.ogg?ex=abc"
	audioFileURL   = "https://cdn.discordapp.com/attachments/300000000000000001/500000000000000011/clip.mp3?ex=def"
	secondAudioURL = "https://media.discordapp.net/attachments/300000000000000001/500000000000000012/clip2.wav?ex=ghi"
	syntheticMedia = "synthetic media bytes"
	messageText    = "terminando de comer y me sumo"
	nowLine        = "ahora: domingo 2026-11-01 21:00"
)

// fakeReader stands in for the model: it records every request and answers with the text scripted
// for the message, or with one fixed text.
type fakeReader struct {
	mu       sync.Mutex
	response string
	byText   map[string]string
	err      error
	calls    []llm.MediaRequest
}

func (r *fakeReader) GenerateWithMedia(_ context.Context, req llm.MediaRequest) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	recorded := req
	recorded.Images = slices.Clone(req.Images)
	if req.Audio != nil {
		audio := *req.Audio
		recorded.Audio = &audio
	}
	r.calls = append(r.calls, recorded)

	if r.err != nil {
		return "", r.err
	}

	for fragment, answer := range r.byText {
		if strings.Contains(req.Text, fragment) {
			return answer, nil
		}
	}

	return r.response, nil
}

// AnswerFor scripts the answer to every request whose text contains fragment.
func (r *fakeReader) AnswerFor(fragment, answer string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.byText == nil {
		r.byText = map[string]string{}
	}
	r.byText[fragment] = answer
}

func (r *fakeReader) SetResponse(response string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.response = response
}

func (r *fakeReader) SetError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.err = err
}

func (r *fakeReader) Calls() []llm.MediaRequest {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.calls)
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

	return []byte(syntheticMedia + " " + rawURL), nil
}

func (f *mediaFetcher) Fail(rawURLs ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, rawURL := range rawURLs {
		f.failFor[rawURL] = true
	}
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

// logBuffer is a concurrency-safe sink for the service's decision log.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	n, err := b.buf.Write(p)
	if err != nil {
		return n, fmt.Errorf("write log: %w", err)
	}

	return n, nil
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// entries returns the logged lines as decoded JSON objects.
func (b *logBuffer) entries(t *testing.T) []map[string]any {
	t.Helper()

	var entries []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		if line == "" {
			continue
		}

		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("log line %q is not JSON: %v", line, err)
		}
		entries = append(entries, entry)
	}

	return entries
}

type readHarness struct {
	svc      *capitime.Service
	clock    *fakeClock
	settings *fakeSettings
	reader   *fakeReader
	fetcher  *mediaFetcher
	logs     *logBuffer
}

type readHarnessOptions struct {
	debounce string
	noReader bool
	answer   string
	// now replaces the harness clock as the Service clock when it is set.
	now func() time.Time
}

// newReadHarness builds a Service on Sunday 2026-11-01 21:00 in Santiago whose target member is
// capiUserID in chanGeneralID of guildOneID, with a fixed 60-minute delay and no easter egg.
func newReadHarness(t *testing.T, opts readHarnessOptions) *readHarness {
	t.Helper()
	resetGuildConfigs(t)

	debounce := opts.debounce
	if debounce == "" {
		debounce = "0s"
	}
	answer := opts.answer
	if answer == "" {
		answer = arrivalJSON
	}

	clk := newFakeClock(time.Date(2026, 11, 1, 21, 0, 0, 0, capitime.Santiago()))
	settings := newFakeSettings(map[string]string{
		capitime.SettingDebounceWindow:   debounce,
		capitime.SettingEasterEggPercent: "0",
		capitime.SettingDelayMinMinutes:  "60",
		capitime.SettingDelayMaxMinutes:  "60",
	})

	harness := &readHarness{
		clock:    clk,
		settings: settings,
		reader:   &fakeReader{response: answer},
		fetcher:  &mediaFetcher{failFor: map[string]bool{}},
		logs:     &logBuffer{},
	}

	deps := capitime.Deps{
		Pool:     shared.Pool(),
		Clock:    clk.Now,
		Settings: settings,
		Images:   harness.fetcher,
		Logger:   newJSONLogger(harness.logs),
	}
	if !opts.noReader {
		deps.Reader = harness.reader
	}
	if opts.now != nil {
		deps.Clock = opts.now
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

func targetMessage(content string) capitime.MessageInput {
	return capitime.MessageInput{
		GuildID:   guildOneID,
		ChannelID: chanGeneralID,
		MessageID: msgOneID,
		AuthorID:  capiUserID,
		Content:   content,
	}
}

func imageMessage(content string, images ...capitime.ImageRef) capitime.MessageInput {
	msg := targetMessage(content)
	msg.Images = images

	return msg
}

func audioMessage(content string, audio ...capitime.AudioRef) capitime.MessageInput {
	msg := targetMessage(content)
	msg.Audio = audio

	return msg
}

func stickerRef(name string) capitime.ImageRef {
	return capitime.ImageRef{URL: stickerURL, MIMEType: "image/png", Name: name}
}

func attachmentRef() capitime.ImageRef {
	return capitime.ImageRef{URL: attachmentURL, MIMEType: "image/png", Size: 1234}
}

func voiceRef() capitime.AudioRef {
	return capitime.AudioRef{
		URL: voiceURL, MIMEType: "audio/ogg", Size: 20000, DurationSeconds: 4.5, IsVoiceMessage: true,
	}
}

func fileRef() capitime.AudioRef {
	return capitime.AudioRef{URL: audioFileURL, MIMEType: "audio/mpeg", Size: 90000}
}

func (h *readHarness) handle(t *testing.T, msg capitime.MessageInput) capitime.Reply {
	t.Helper()

	reply, err := h.svc.HandleMessage(t.Context(), msg)
	if err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	return reply
}

func (h *readHarness) assertUntouched(t *testing.T) {
	t.Helper()

	if calls := h.fetcher.Calls(); len(calls) != 0 {
		t.Errorf("fetcher called %d times, want 0", len(calls))
	}
	if calls := h.reader.Calls(); len(calls) != 0 {
		t.Errorf("reader called %d times, want 0", len(calls))
	}
}

// onlyCall returns the single request the reader received.
func (h *readHarness) onlyCall(t *testing.T) llm.MediaRequest {
	t.Helper()

	calls := h.reader.Calls()
	if len(calls) != 1 {
		t.Fatalf("reader calls = %d, want exactly 1", len(calls))
	}

	return calls[0]
}

func imageMIMEs(req llm.MediaRequest) []string {
	mimes := make([]string, 0, len(req.Images))
	for _, image := range req.Images {
		mimes = append(mimes, image.MIMEType)
	}

	return mimes
}

func TestHandleMessageReadsCaptionImagesAndAudioInOneCall(t *testing.T) {
	h := newReadHarness(t, readHarnessOptions{})

	msg := imageMessage("mira\x00 esto", stickerRef("VOY"), attachmentRef())
	msg.Audio = []capitime.AudioRef{voiceRef()}

	reply := h.handle(t, msg)
	if !reply.Handled || !reply.Prediction.Immediate {
		t.Fatalf("reply = %+v, want a handled immediate arrival", reply)
	}
	if reply.ReplyToID != msgOneID || !strings.Contains(reply.Content, "Capi Time") {
		t.Errorf("reply = %+v, want a Capi Time reply to the message", reply)
	}

	wantFetches := []fetchCall{
		{url: stickerURL, maxBytes: llm.MaxImageBytes},
		{url: attachmentURL, maxBytes: llm.MaxImageBytes},
		{url: voiceURL, maxBytes: llm.MaxAudioBytes},
	}
	if fetches := h.fetcher.Calls(); !slices.Equal(fetches, wantFetches) {
		t.Fatalf("fetches = %+v, want %+v", fetches, wantFetches)
	}

	call := h.onlyCall(t)
	if !call.JSON {
		t.Error("request did not ask for JSON output")
	}
	if want := nowLine + "\nmensaje: mira  esto\nsticker: VOY"; call.Text != want {
		t.Errorf("request text = %q, want %q", call.Text, want)
	}
	if len(call.Images) != 2 || string(call.Images[0].Data) != syntheticMedia+" "+stickerURL ||
		string(call.Images[1].Data) != syntheticMedia+" "+attachmentURL {
		t.Errorf("request images = %+v, want the two downloaded images in order", call.Images)
	}
	if !slices.Equal(imageMIMEs(call), []string{"image/png", "image/png"}) {
		t.Errorf("image MIME types = %v, want two image/png", imageMIMEs(call))
	}
	if call.Audio == nil || call.Audio.MIMEType != "audio/ogg" || string(call.Audio.Data) != syntheticMedia+" "+voiceURL {
		t.Errorf("request audio = %+v, want the downloaded voice message as audio/ogg", call.Audio)
	}
	for _, want := range []string{"Capi, Capis, Braulio, Braulitros", `"arrival"`, `"day"`, `"clock"`, `"minutes"`} {
		if !strings.Contains(call.Prompt, want) {
			t.Errorf("system instruction %q does not mention %q", call.Prompt, want)
		}
	}
}

func TestHandleMessageRepliesToMediaOnlyArrival(t *testing.T) {
	t.Run("sticker", func(t *testing.T) {
		h := newReadHarness(t, readHarnessOptions{})

		if reply := h.handle(t, imageMessage("", stickerRef("VOY"))); !reply.Handled {
			t.Fatalf("reply = %+v, want handled", reply)
		}
		if call := h.onlyCall(t); call.Text != nowLine+"\nsticker: VOY" || call.Audio != nil {
			t.Errorf("request = %+v, want the sticker hint and no audio", call)
		}
	})

	t.Run("attachments with a parameterised type and blank text", func(t *testing.T) {
		h := newReadHarness(t, readHarnessOptions{})
		jpeg := capitime.ImageRef{URL: proxiedURL, MIMEType: "Image/JPEG; charset=binary", Size: 10}

		if reply := h.handle(t, imageMessage("   ", attachmentRef(), jpeg)); !reply.Handled {
			t.Fatalf("reply = %+v, want handled", reply)
		}

		call := h.onlyCall(t)
		if call.Text != nowLine {
			t.Errorf("request text = %q, want only the reference instant", call.Text)
		}
		if !slices.Equal(imageMIMEs(call), []string{"image/png", "image/jpeg"}) {
			t.Errorf("image MIME types = %v, want the second normalized to image/jpeg", imageMIMEs(call))
		}
	})

	t.Run("voice message", func(t *testing.T) {
		h := newReadHarness(t, readHarnessOptions{})

		if reply := h.handle(t, audioMessage("", voiceRef())); !reply.Handled {
			t.Fatalf("reply = %+v, want handled", reply)
		}

		call := h.onlyCall(t)
		if call.Text != nowLine || len(call.Images) != 0 || call.Audio == nil || call.Audio.MIMEType != "audio/ogg" {
			t.Errorf("request = %+v, want the voice message alone", call)
		}
	})

	t.Run("audio file with a parameterised type and no announced size or duration", func(t *testing.T) {
		h := newReadHarness(t, readHarnessOptions{})
		file := capitime.AudioRef{URL: audioFileURL, MIMEType: "Audio/MPEG; charset=binary"}

		if reply := h.handle(t, audioMessage("", file)); !reply.Handled {
			t.Fatalf("reply = %+v, want handled", reply)
		}
		if call := h.onlyCall(t); call.Audio == nil || call.Audio.MIMEType != "audio/mpeg" {
			t.Errorf("request audio = %+v, want the normalized audio/mpeg type", call.Audio)
		}
	})
}

func TestHandleMessageStaysSilentBeforeAnyDownloadOrRead(t *testing.T) {
	mediaMessage := func() capitime.MessageInput {
		msg := imageMessage(messageText, stickerRef("VOY"))
		msg.Audio = []capitime.AudioRef{voiceRef()}

		return msg
	}

	cases := []struct {
		name   string
		mutate func(*capitime.MessageInput)
	}{
		{"author is not the target", func(m *capitime.MessageInput) { m.AuthorID = otherUserID }},
		{"channel is not monitored", func(m *capitime.MessageInput) { m.ChannelID = chanOfftopic }},
		{"author is a bot", func(m *capitime.MessageInput) { m.AuthorBot = true }},
		{"guild ID is not a snowflake", func(m *capitime.MessageInput) { m.GuildID = "not-a-snowflake" }},
		{"message has neither text nor media", func(m *capitime.MessageInput) {
			m.Content, m.Images, m.Audio = "", nil, nil
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newReadHarness(t, readHarnessOptions{})
			msg := mediaMessage()
			tc.mutate(&msg)

			if reply := h.handle(t, msg); reply.Handled {
				t.Fatalf("reply = %+v, want silence", reply)
			}
			h.assertUntouched(t)
			if logged := h.logs.String(); logged != "" {
				t.Errorf("a message stopped by a gate was logged as evaluated: %s", logged)
			}
		})
	}

	t.Run("reply is inside the debounce window", func(t *testing.T) {
		h := newReadHarness(t, readHarnessOptions{debounce: "2m"})

		if first := h.handle(t, mediaMessage()); !first.Handled {
			t.Fatalf("first reply = %+v, want handled", first)
		}
		fetchedBefore := len(h.fetcher.Calls())
		readsBefore := len(h.reader.Calls())

		h.clock.Advance(30 * time.Second)
		if second := h.handle(t, mediaMessage()); second.Handled {
			t.Fatalf("second reply = %+v, want debounced", second)
		}
		if len(h.fetcher.Calls()) != fetchedBefore || len(h.reader.Calls()) != readsBefore {
			t.Error("debounced message reached the fetcher or the model")
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
			h := newReadHarness(t, readHarnessOptions{})

			if reply := h.handle(t, imageMessage("", tc.images...)); reply.Handled {
				t.Fatalf("reply = %+v, want silence", reply)
			}
			h.assertUntouched(t)
		})
	}

	t.Run("an unusable image beside a usable one is dropped", func(t *testing.T) {
		h := newReadHarness(t, readHarnessOptions{})
		lottie := capitime.ImageRef{URL: stickerURL, MIMEType: "application/json"}

		if reply := h.handle(t, imageMessage("", lottie, valid)); !reply.Handled {
			t.Fatalf("reply = %+v, want handled", reply)
		}
		if urls := h.fetcher.URLs(); !slices.Equal(urls, []string{attachmentURL}) {
			t.Errorf("fetched %v, want only the usable image", urls)
		}
	})

	t.Run("too many images are ignored while the text and the audio are still read", func(t *testing.T) {
		h := newReadHarness(t, readHarnessOptions{})
		msg := imageMessage(messageText, valid, valid, valid)
		msg.Audio = []capitime.AudioRef{voiceRef()}

		if reply := h.handle(t, msg); !reply.Handled {
			t.Fatalf("reply = %+v, want handled", reply)
		}
		if urls := h.fetcher.URLs(); !slices.Equal(urls, []string{voiceURL}) {
			t.Errorf("fetched %v, want only the audio", urls)
		}

		call := h.onlyCall(t)
		if len(call.Images) != 0 || call.Audio == nil || call.Text != nowLine+"\nmensaje: "+messageText {
			t.Errorf("request = %+v, want the text and the audio without images", call)
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
			h := newReadHarness(t, readHarnessOptions{})

			if reply := h.handle(t, audioMessage("", tc.audio)); reply.Handled {
				t.Fatalf("reply = %+v, want silence", reply)
			}
			h.assertUntouched(t)
		})
	}

	t.Run("an unusable audio beside a usable one is dropped", func(t *testing.T) {
		h := newReadHarness(t, readHarnessOptions{})
		tooLong := capitime.AudioRef{URL: voiceURL, MIMEType: "audio/ogg", DurationSeconds: 300}

		if reply := h.handle(t, audioMessage("", tooLong, fileRef())); !reply.Handled {
			t.Fatalf("reply = %+v, want handled", reply)
		}
		if urls := h.fetcher.URLs(); !slices.Equal(urls, []string{audioFileURL}) {
			t.Errorf("fetched %v, want only the usable audio", urls)
		}
	})

	t.Run("only the first usable audio is read", func(t *testing.T) {
		h := newReadHarness(t, readHarnessOptions{})
		second := capitime.AudioRef{URL: secondAudioURL, MIMEType: "audio/wav", Size: 10}

		if reply := h.handle(t, audioMessage("", voiceRef(), second)); !reply.Handled {
			t.Fatalf("reply = %+v, want handled", reply)
		}
		if urls := h.fetcher.URLs(); !slices.Equal(urls, []string{voiceURL}) {
			t.Errorf("fetched %v, want only the first audio", urls)
		}
		if call := h.onlyCall(t); call.Audio == nil || call.Audio.MIMEType != "audio/ogg" {
			t.Errorf("request audio = %+v, want the first audio", call.Audio)
		}
	})
}

func TestHandleMessageReadsMediaAtTheLimits(t *testing.T) {
	cases := []struct {
		name string
		msg  capitime.MessageInput
	}{
		{
			"image size exactly at the limit",
			imageMessage("", capitime.ImageRef{URL: attachmentURL, MIMEType: "image/png", Size: llm.MaxImageBytes}),
		},
		{
			"audio size exactly at the limit",
			audioMessage("", capitime.AudioRef{URL: voiceURL, MIMEType: "audio/ogg", Size: llm.MaxAudioBytes}),
		},
		{
			"audio duration exactly at the limit",
			audioMessage("", capitime.AudioRef{URL: voiceURL, MIMEType: "audio/ogg", DurationSeconds: llm.MaxAudioSeconds}),
		},
		{
			"the maximum number of images",
			imageMessage("", attachmentRef(), capitime.ImageRef{URL: proxiedURL, MIMEType: "image/jpeg"}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newReadHarness(t, readHarnessOptions{})

			if reply := h.handle(t, tc.msg); !reply.Handled {
				t.Fatalf("reply = %+v, want media at the limit to be read", reply)
			}
		})
	}
}

func TestHandleMessageFailedDownloadDropsOnlyThatItem(t *testing.T) {
	second := capitime.ImageRef{URL: proxiedURL, MIMEType: "image/jpeg", Name: "LLEGANDO"}

	t.Run("the image fails and the text is still read", func(t *testing.T) {
		h := newReadHarness(t, readHarnessOptions{})
		h.fetcher.Fail(attachmentURL)

		if reply := h.handle(t, imageMessage(messageText, attachmentRef())); !reply.Handled {
			t.Fatalf("reply = %+v, want handled through the text", reply)
		}
		if call := h.onlyCall(t); len(call.Images) != 0 || call.Text != nowLine+"\nmensaje: "+messageText {
			t.Errorf("request = %+v, want the text alone", call)
		}
	})

	t.Run("one of two images fails and its sticker hint goes with it", func(t *testing.T) {
		h := newReadHarness(t, readHarnessOptions{})
		h.fetcher.Fail(stickerURL)

		if reply := h.handle(t, imageMessage("", stickerRef("VOY"), second)); !reply.Handled {
			t.Fatalf("reply = %+v, want handled through the other image", reply)
		}

		call := h.onlyCall(t)
		if !slices.Equal(imageMIMEs(call), []string{"image/jpeg"}) || call.Text != nowLine+"\nsticker: LLEGANDO" {
			t.Errorf("request = %+v, want only the image that was downloaded and its hint", call)
		}
	})

	t.Run("the audio fails and the image is still read", func(t *testing.T) {
		h := newReadHarness(t, readHarnessOptions{})
		h.fetcher.Fail(voiceURL)

		msg := imageMessage("", attachmentRef())
		msg.Audio = []capitime.AudioRef{voiceRef()}
		if reply := h.handle(t, msg); !reply.Handled {
			t.Fatalf("reply = %+v, want handled through the image", reply)
		}
		if call := h.onlyCall(t); len(call.Images) != 1 || call.Audio != nil {
			t.Errorf("request = %+v, want the image without audio", call)
		}
	})

	t.Run("the image fails and the audio is still read", func(t *testing.T) {
		h := newReadHarness(t, readHarnessOptions{})
		h.fetcher.Fail(attachmentURL)

		msg := imageMessage("", attachmentRef())
		msg.Audio = []capitime.AudioRef{voiceRef()}
		if reply := h.handle(t, msg); !reply.Handled {
			t.Fatalf("reply = %+v, want handled through the audio", reply)
		}
		if call := h.onlyCall(t); len(call.Images) != 0 || call.Audio == nil {
			t.Errorf("request = %+v, want the audio without images", call)
		}
	})

	t.Run("every download fails and there is no text", func(t *testing.T) {
		h := newReadHarness(t, readHarnessOptions{})
		h.fetcher.Fail(stickerURL, voiceURL)

		msg := imageMessage("", stickerRef("VOY"))
		msg.Audio = []capitime.AudioRef{voiceRef()}
		if reply := h.handle(t, msg); reply.Handled {
			t.Fatalf("reply = %+v, want silence", reply)
		}
		if calls := h.reader.Calls(); len(calls) != 0 {
			t.Errorf("reader called %d times with nothing to read, want 0", len(calls))
		}
	})
}

func TestHandleMessageStaysSilentWhenTheModelGivesNoClaim(t *testing.T) {
	msg := imageMessage(messageText, attachmentRef())
	msg.Audio = []capitime.AudioRef{voiceRef()}

	t.Run("model says no arrival", func(t *testing.T) {
		h := newReadHarness(t, readHarnessOptions{answer: noArrivalJSON})

		if reply := h.handle(t, msg); reply.Handled {
			t.Fatalf("reply = %+v, want silence", reply)
		}
		h.onlyCall(t)
	})

	t.Run("model returns text that is not JSON", func(t *testing.T) {
		h := newReadHarness(t, readHarnessOptions{answer: "the message says hello"})

		if reply := h.handle(t, msg); reply.Handled {
			t.Fatalf("reply = %+v, want silence", reply)
		}
	})

	t.Run("model fails once and nothing else is tried", func(t *testing.T) {
		h := newReadHarness(t, readHarnessOptions{})
		h.reader.SetError(errors.New("synthetic model failure"))

		if reply := h.handle(t, msg); reply.Handled {
			t.Fatalf("reply = %+v, want silence", reply)
		}
		h.onlyCall(t)
	})

	t.Run("a failed read does not use the debounce slot", func(t *testing.T) {
		h := newReadHarness(t, readHarnessOptions{debounce: "2m"})
		h.reader.SetError(errors.New("synthetic model failure"))

		if reply := h.handle(t, msg); reply.Handled {
			t.Fatalf("reply = %+v, want silence", reply)
		}

		h.reader.SetError(nil)

		if reply := h.handle(t, msg); !reply.Handled {
			t.Fatalf("reply after recovery = %+v, want handled", reply)
		}
	})
}

func TestHandleMessageWithoutReaderStaysSilent(t *testing.T) {
	h := newReadHarness(t, readHarnessOptions{noReader: true})

	media := imageMessage("", stickerRef("VOY"))
	media.Audio = []capitime.AudioRef{voiceRef()}
	if reply := h.handle(t, media); reply.Handled {
		t.Fatalf("media-only reply = %+v, want silence", reply)
	}
	for _, text := range []string{messageText, "22:30 llego", "voy altiro"} {
		if reply := h.handle(t, targetMessage(text)); reply.Handled {
			t.Fatalf("reply to %q = %+v, want silence without a reader", text, reply)
		}
	}
	h.assertUntouched(t)
}

func TestHandleMessageSanitizesStickerHints(t *testing.T) {
	h := newReadHarness(t, readHarnessOptions{})

	longName := strings.Repeat("\u05d0", 200)
	if reply := h.handle(t, imageMessage("", stickerRef("VOY\x00\n"+longName))); !reply.Handled {
		t.Fatalf("reply = %+v, want handled", reply)
	}

	call := h.onlyCall(t)
	hint, hasHint := strings.CutPrefix(call.Text, nowLine+"\nsticker: ")
	if !hasHint {
		t.Fatalf("request text = %q, want one sticker line after the reference instant", call.Text)
	}
	if strings.ContainsAny(hint, "\x00\n") {
		t.Errorf("sticker hint %q keeps control characters", hint)
	}
	if runes := utf8.RuneCountInString(hint); runes > 64 {
		t.Errorf("sticker hint has %d runes, want at most 64", runes)
	}
}

// newFuzzService builds a Service whose target member is capiUserID in every channel of
// guildOneID, without debounce.
func newFuzzService(f *testing.F, clk *fakeClock, reader capitime.ArrivalReader, fetcher capitime.ImageFetcher) *capitime.Service {
	f.Helper()

	_, err := shared.Pool().Exec(context.Background(), "DELETE FROM capitime.guild_config")
	if err != nil {
		f.Fatalf("reset capitime.guild_config: %v", err)
	}

	svc, err := capitime.New(capitime.Deps{
		Pool:     shared.Pool(),
		Clock:    clk.Now,
		Settings: newFakeSettings(map[string]string{capitime.SettingDebounceWindow: "0s"}),
		Reader:   reader,
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

	return svc
}

func assertFetchedFromDiscordCDN(t *testing.T, calls []fetchCall, wantMaxBytes int) {
	t.Helper()

	for _, call := range calls {
		parsed, parseErr := url.Parse(call.url)
		if parseErr != nil {
			t.Fatalf("fetched an unparsable URL %q: %v", call.url, parseErr)
		}

		isAllowedHost := slices.Contains(capitime.DiscordImageHosts(), strings.ToLower(parsed.Host))
		if parsed.Scheme != "https" || parsed.User != nil || !isAllowedHost {
			t.Fatalf("fetched %q, want HTTPS on a Discord CDN host without credentials", call.url)
		}
		if call.maxBytes != wantMaxBytes {
			t.Fatalf("fetched with limit %d, want %d", call.maxBytes, wantMaxBytes)
		}
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

	clk := newFakeClock(time.Date(2026, 11, 1, 21, 0, 0, 0, capitime.Santiago()))
	fetcher := &mediaFetcher{failFor: map[string]bool{}}
	svc := newFuzzService(f, clk, &fakeReader{response: noArrivalJSON}, fetcher)

	f.Fuzz(func(t *testing.T, rawURL, mime, name string, size int) {
		// Each input gets a fresh minute, so the per-minute read cap never hides the downloads.
		clk.Advance(2 * time.Minute)
		alreadyFetched := len(fetcher.Calls())

		msg := imageMessage("", capitime.ImageRef{URL: rawURL, MIMEType: mime, Name: name, Size: size})

		_, handleErr := svc.HandleMessage(t.Context(), msg)
		if handleErr != nil {
			t.Fatalf("HandleMessage: %v", handleErr)
		}

		assertFetchedFromDiscordCDN(t, fetcher.Calls()[alreadyFetched:], llm.MaxImageBytes)
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

	clk := newFakeClock(time.Date(2026, 11, 1, 21, 0, 0, 0, capitime.Santiago()))
	fetcher := &mediaFetcher{failFor: map[string]bool{}}
	svc := newFuzzService(f, clk, &fakeReader{response: noArrivalJSON}, fetcher)

	f.Fuzz(func(t *testing.T, rawURL, mime string, size int, duration float64, isVoice bool) {
		// Each input gets a fresh minute, so the per-minute read cap never hides the downloads.
		clk.Advance(2 * time.Minute)
		alreadyFetched := len(fetcher.Calls())

		ref := capitime.AudioRef{URL: rawURL, MIMEType: mime, Size: size, DurationSeconds: duration, IsVoiceMessage: isVoice}

		_, handleErr := svc.HandleMessage(t.Context(), audioMessage("", ref))
		if handleErr != nil {
			t.Fatalf("HandleMessage: %v", handleErr)
		}

		assertFetchedFromDiscordCDN(t, fetcher.Calls()[alreadyFetched:], llm.MaxAudioBytes)
	})
}
