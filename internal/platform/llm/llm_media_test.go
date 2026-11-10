package llm_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/platform/llm"
)

type capturedRequest struct {
	Method string
	Path   string
	APIKey string
	Body   map[string]any
}

func newCapturingServer(t *testing.T, status int, reply string) (*httptest.Server, *capturedRequest) {
	t.Helper()

	captured := &capturedRequest{}

	// Tests that share one server across parallel subtests make concurrent requests.
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read error", http.StatusBadRequest)
			return
		}

		mu.Lock()
		captured.Method = r.Method
		captured.Path = r.URL.Path
		captured.APIKey = r.Header.Get("x-goog-api-key")
		captured.Body = nil
		err = json.Unmarshal(raw, &captured.Body)
		mu.Unlock()

		if err != nil {
			http.Error(w, "json error", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)

	return srv, captured
}

func newMediaClient(t *testing.T, srv *httptest.Server, logBuf *bytes.Buffer) *llm.Client {
	t.Helper()

	cfg := llm.Config{
		APIKey:  testAPIKey,
		BaseURL: srv.URL + "/v1/publishers/google/models",
	}
	if logBuf != nil {
		cfg.Logger = slog.New(slog.NewJSONHandler(logBuf, nil))
	}

	client, err := llm.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return client
}

func userParts(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()

	contents, ok := body["contents"].([]any)
	if !ok || len(contents) != 1 {
		t.Fatalf("contents = %#v, want one message", body["contents"])
	}

	message, ok := contents[0].(map[string]any)
	if !ok {
		t.Fatalf("contents[0] = %#v, want object", contents[0])
	}

	if message["role"] != "user" {
		t.Errorf("role = %v, want user", message["role"])
	}

	rawParts, ok := message["parts"].([]any)
	if !ok {
		t.Fatalf("parts = %#v, want array", message["parts"])
	}

	parts := make([]map[string]any, 0, len(rawParts))
	for _, raw := range rawParts {
		part, isObject := raw.(map[string]any)
		if !isObject {
			t.Fatalf("part = %#v, want object", raw)
		}
		parts = append(parts, part)
	}

	return parts
}

func generationConfigOf(t *testing.T, body map[string]any) map[string]any {
	t.Helper()

	genCfg, ok := body["generationConfig"].(map[string]any)
	if !ok {
		t.Fatalf("generationConfig = %#v, want object", body["generationConfig"])
	}

	return genCfg
}

func assertInlinePart(t *testing.T, part map[string]any, wantMIME string, wantData []byte) {
	t.Helper()

	inline, ok := part["inline_data"].(map[string]any)
	if !ok {
		t.Fatalf("part = %#v, want inline_data", part)
	}
	if inline["mime_type"] != wantMIME {
		t.Errorf("mime_type = %v, want %s", inline["mime_type"], wantMIME)
	}
	if inline["data"] != base64.StdEncoding.EncodeToString(wantData) {
		t.Errorf("data = %v, want standard base64 of the media", inline["data"])
	}
	if _, hasText := part["text"]; hasText {
		t.Errorf("part %#v carries text, want media only", part)
	}
}

const okReply = `{"candidates":[{"content":{"parts":[{"text":" {\"arrival\":false} "}]}}]}`

func TestGenerateWithMediaSendsTextImagesAndAudioInOneRequest(t *testing.T) {
	t.Parallel()

	pngBytes := []byte("\x89PNG synthetic image one")
	gifBytes := []byte("GIF89a synthetic image two")
	oggBytes := []byte("OggS synthetic audio bytes")

	cases := []struct {
		name     string
		model    string
		wantPath string
	}{
		{
			name:     "default model when Config.Model is empty",
			model:    "",
			wantPath: "/v1/publishers/google/models/" + llm.DefaultModel + ":generateContent",
		},
		{
			name:     "configured model",
			model:    "gemini-custom-lite",
			wantPath: "/v1/publishers/google/models/gemini-custom-lite:generateContent",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv, captured := newCapturingServer(t, http.StatusOK, okReply)

			var logBuf bytes.Buffer
			client, err := llm.New(llm.Config{
				APIKey:  testAPIKey,
				Model:   tc.model,
				BaseURL: srv.URL + "/v1/publishers/google/models",
				Logger:  slog.New(slog.NewJSONHandler(&logBuf, nil)),
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			got, err := client.GenerateWithMedia(context.Background(), llm.MediaRequest{
				Prompt: "Read the message.",
				Text:   "  caption text  ",
				Images: []llm.Image{
					{MIMEType: "image/png", Data: pngBytes},
					{MIMEType: "image/gif", Data: gifBytes},
				},
				Audio: &llm.Audio{MIMEType: "audio/ogg", Data: oggBytes},
			})
			if err != nil {
				t.Fatalf("GenerateWithMedia: %v", err)
			}
			if got != `{"arrival":false}` {
				t.Errorf("GenerateWithMedia = %q, want trimmed model text", got)
			}

			if captured.Method != http.MethodPost || captured.Path != tc.wantPath {
				t.Errorf("request = %s %s, want POST %s", captured.Method, captured.Path, tc.wantPath)
			}
			if captured.APIKey != testAPIKey {
				t.Errorf("x-goog-api-key header was not sent")
			}

			parts := userParts(t, captured.Body)
			if len(parts) != 4 {
				t.Fatalf("parts = %#v, want two images, the audio, then one text", parts)
			}
			assertInlinePart(t, parts[0], "image/png", pngBytes)
			assertInlinePart(t, parts[1], "image/gif", gifBytes)
			assertInlinePart(t, parts[2], "audio/ogg", oggBytes)
			if parts[3]["text"] != "caption text" {
				t.Errorf("parts[3] text = %v, want trimmed caption", parts[3]["text"])
			}

			genCfg := generationConfigOf(t, captured.Body)
			if genCfg["maxOutputTokens"] != float64(256) {
				t.Errorf("generationConfig = %#v, want maxOutputTokens 256", genCfg)
			}
			if _, hasMIME := genCfg["responseMimeType"]; hasMIME {
				t.Errorf("generationConfig = %#v, want no responseMimeType unless JSON is asked", genCfg)
			}

			if system := extractSystemInstructionText(t, captured.Body); system != "Read the message." {
				t.Errorf("system instruction = %q, want the request prompt", system)
			}

			logged := logBuf.String()
			secrets := []string{
				testAPIKey, "caption text",
				base64.StdEncoding.EncodeToString(pngBytes), base64.StdEncoding.EncodeToString(oggBytes),
			}
			for _, secret := range secrets {
				if strings.Contains(logged, secret) {
					t.Errorf("log output %q leaked %q", logged, secret)
				}
			}
		})
	}
}

func TestGenerateWithMediaJSONFlagOnTheWire(t *testing.T) {
	t.Parallel()

	srv, captured := newCapturingServer(t, http.StatusOK, okReply)
	client := newMediaClient(t, srv, nil)

	_, err := client.GenerateWithMedia(context.Background(), llm.MediaRequest{Text: "hola", JSON: true})
	if err != nil {
		t.Fatalf("GenerateWithMedia: %v", err)
	}

	genCfg := generationConfigOf(t, captured.Body)
	if genCfg["responseMimeType"] != "application/json" {
		t.Errorf("generationConfig = %#v, want responseMimeType application/json", genCfg)
	}
}

func TestGenerateWithMediaAcceptsEachPartAlone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		req      llm.MediaRequest
		wantMIME string
		wantText string
	}{
		{"text only", llm.MediaRequest{Text: " hola "}, "", "hola"},
		{
			"one image without text",
			llm.MediaRequest{Text: "   ", Images: []llm.Image{{MIMEType: "image/webp", Data: []byte("synthetic")}}},
			"image/webp", "",
		},
		{
			"audio without text",
			llm.MediaRequest{Audio: &llm.Audio{MIMEType: "audio/mpeg", Data: []byte("synthetic")}},
			"audio/mpeg", "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv, captured := newCapturingServer(t, http.StatusOK, okReply)
			client := newMediaClient(t, srv, nil)

			_, err := client.GenerateWithMedia(context.Background(), tc.req)
			if err != nil {
				t.Fatalf("GenerateWithMedia: %v", err)
			}

			parts := userParts(t, captured.Body)
			if len(parts) != 1 {
				t.Fatalf("parts = %#v, want exactly one part", parts)
			}
			if tc.wantText != "" && parts[0]["text"] != tc.wantText {
				t.Errorf("parts[0] = %#v, want text %q", parts[0], tc.wantText)
			}
			if tc.wantMIME != "" {
				assertInlinePart(t, parts[0], tc.wantMIME, []byte("synthetic"))
			}
		})
	}
}

func TestGenerateWithMediaNormalizesParameterisedAudioMIME(t *testing.T) {
	t.Parallel()

	srv, captured := newCapturingServer(t, http.StatusOK, okReply)
	client := newMediaClient(t, srv, nil)

	_, err := client.GenerateWithMedia(context.Background(), llm.MediaRequest{
		Audio: &llm.Audio{MIMEType: " Audio/OGG; codecs=opus ", Data: []byte("synthetic")},
	})
	if err != nil {
		t.Fatalf("GenerateWithMedia: %v", err)
	}

	parts := userParts(t, captured.Body)
	assertInlinePart(t, parts[0], "audio/ogg", []byte("synthetic"))
}

func TestGenerateWithMediaUsesConfiguredPromptWhenPromptIsBlank(t *testing.T) {
	t.Parallel()

	const configuredPrompt = "synthetic configured system prompt"

	srv, captured := newCapturingServer(t, http.StatusOK, okReply)

	client, err := llm.New(llm.Config{
		APIKey:  testAPIKey,
		BaseURL: srv.URL + "/v1/publishers/google/models",
		Prompt:  configuredPrompt,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.GenerateWithMedia(context.Background(), llm.MediaRequest{Prompt: "  ", Text: "x"})
	if err != nil {
		t.Fatalf("GenerateWithMedia: %v", err)
	}

	if system := extractSystemInstructionText(t, captured.Body); system != configuredPrompt {
		t.Errorf("system instruction = %q, want %q", system, configuredPrompt)
	}
}

func TestGenerateWithMediaBoundaries(t *testing.T) {
	t.Parallel()

	srv, _ := newCapturingServer(t, http.StatusOK, okReply)
	client := newMediaClient(t, srv, nil)

	image := func(size int) llm.Image {
		return llm.Image{MIMEType: "image/png", Data: bytes.Repeat([]byte{'a'}, size)}
	}
	audio := func(mime string, size int) *llm.Audio {
		return &llm.Audio{MIMEType: mime, Data: bytes.Repeat([]byte{'a'}, size)}
	}

	accepted := []struct {
		name string
		req  llm.MediaRequest
	}{
		{"one image", llm.MediaRequest{Images: []llm.Image{image(1)}}},
		{"maximum image count", llm.MediaRequest{Images: []llm.Image{image(1), image(1)}}},
		{"maximum image size", llm.MediaRequest{Images: []llm.Image{image(llm.MaxImageBytes)}}},
		{"one byte of audio", llm.MediaRequest{Audio: audio("audio/ogg", 1)}},
		{"maximum audio size", llm.MediaRequest{Audio: audio("audio/ogg", llm.MaxAudioBytes)}},
		{"parameterised audio type", llm.MediaRequest{Audio: audio("audio/ogg; codecs=opus", 1)}},
		{"opus", llm.MediaRequest{Audio: audio("audio/opus", 1)}},
		{"mpeg", llm.MediaRequest{Audio: audio("audio/mpeg", 1)}},
		{"mp3", llm.MediaRequest{Audio: audio("audio/mp3", 1)}},
		{"wav", llm.MediaRequest{Audio: audio("audio/wav", 1)}},
		{"aac", llm.MediaRequest{Audio: audio("audio/aac", 1)}},
		{"flac", llm.MediaRequest{Audio: audio("audio/flac", 1)}},
		{"m4a", llm.MediaRequest{Audio: audio("audio/m4a", 1)}},
		{"webm", llm.MediaRequest{Audio: audio("audio/webm", 1)}},
	}
	for _, tc := range accepted {
		t.Run("accepts "+tc.name, func(t *testing.T) {
			t.Parallel()

			_, genErr := client.GenerateWithMedia(context.Background(), tc.req)
			if genErr != nil {
				t.Errorf("GenerateWithMedia: %v", genErr)
			}
		})
	}

	rejected := []struct {
		name string
		req  llm.MediaRequest
	}{
		{"an empty request", llm.MediaRequest{}},
		{"blank text without media", llm.MediaRequest{Text: " \n\t "}},
		{"too many images", llm.MediaRequest{Text: "x", Images: []llm.Image{image(1), image(1), image(1)}}},
		{"unsupported image type", llm.MediaRequest{Images: []llm.Image{{MIMEType: "application/pdf", Data: []byte("x")}}}},
		{"empty image type", llm.MediaRequest{Images: []llm.Image{{MIMEType: "", Data: []byte("x")}}}},
		{"empty image data", llm.MediaRequest{Text: "x", Images: []llm.Image{{MIMEType: "image/png"}}}},
		{"oversize image data", llm.MediaRequest{Images: []llm.Image{image(llm.MaxImageBytes + 1)}}},
		{"empty audio data", llm.MediaRequest{Text: "x", Audio: &llm.Audio{MIMEType: "audio/ogg"}}},
		{"oversize audio data", llm.MediaRequest{Audio: audio("audio/ogg", llm.MaxAudioBytes+1)}},
		{"image type as audio", llm.MediaRequest{Audio: audio("image/png", 1)}},
		{"video type as audio", llm.MediaRequest{Audio: audio("video/mp4", 1)}},
		{"empty audio type", llm.MediaRequest{Audio: audio("", 1)}},
		{"unlisted audio type", llm.MediaRequest{Audio: audio("audio/midi", 1)}},
	}
	for _, tc := range rejected {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			t.Parallel()

			_, genErr := client.GenerateWithMedia(context.Background(), tc.req)
			if genErr == nil {
				t.Error("GenerateWithMedia succeeded, want error")
			}
		})
	}
}

func TestGenerateWithMediaAggregatesErrorsWithoutLeaking(t *testing.T) {
	t.Parallel()

	client, err := llm.New(llm.Config{APIKey: testAPIKey})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	//nolint:staticcheck // testing aggregated nil context + bad prompt + bad media
	_, err = client.GenerateWithMedia(nil, llm.MediaRequest{
		Prompt: "secret-prompt\x00marker",
		Text:   "secret-text-marker",
		Images: []llm.Image{
			{MIMEType: "text/secret-mime", Data: []byte("secret-image-marker")},
			{MIMEType: "image/png"},
			{MIMEType: "image/png", Data: []byte("x")},
		},
		Audio: &llm.Audio{MIMEType: "text/secret-audio-mime"},
	})
	if err == nil {
		t.Fatal("GenerateWithMedia with invalid inputs succeeded, want error")
	}

	message := err.Error()
	wanted := []string{
		"nil context", "control characters", "image count", "image 0 has an unsupported MIME",
		"image 1 is empty", "audio has an unsupported MIME", "audio is empty",
	}
	for _, want := range wanted {
		if !strings.Contains(message, want) {
			t.Errorf("error %q missing aggregated problem %q", message, want)
		}
	}
	leaks := []string{"secret-prompt", "secret-text-marker", "secret-image-marker", "secret-mime", "secret-audio", testAPIKey}
	for _, leaked := range leaks {
		if strings.Contains(message, leaked) {
			t.Errorf("error %q leaked %q", message, leaked)
		}
	}

	var nilClient *llm.Client
	_, err = nilClient.GenerateWithMedia(context.Background(), llm.MediaRequest{Text: "x"})
	if err == nil {
		t.Error("nil client GenerateWithMedia succeeded, want error")
	}
}

func TestGenerateWithMediaReportsNon200WithoutLeaking(t *testing.T) {
	t.Parallel()

	srv, _ := newCapturingServer(t, http.StatusInternalServerError, `{"error":"boom"}`)

	var logBuf bytes.Buffer
	client := newMediaClient(t, srv, &logBuf)

	_, err := client.GenerateWithMedia(context.Background(), llm.MediaRequest{
		Text:   "caption-marker",
		Images: []llm.Image{{MIMEType: "image/png", Data: []byte("image-marker")}},
		Audio:  &llm.Audio{MIMEType: "audio/ogg", Data: []byte("audio-marker")},
	})
	if err == nil {
		t.Fatal("GenerateWithMedia succeeded on HTTP 500, want error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error %q does not name the status", err.Error())
	}

	logged := logBuf.String()
	if !strings.Contains(logged, "llm request returned non-200 status") {
		t.Errorf("log output %q does not report the failed request", logged)
	}
	for _, leaked := range []string{"caption-marker", "image-marker", "audio-marker", testAPIKey} {
		if strings.Contains(err.Error(), leaked) || strings.Contains(logged, leaked) {
			t.Errorf("error or log leaked %q", leaked)
		}
	}
}

func TestGenerateWithMediaStopsWhenContextEnds(t *testing.T) {
	t.Parallel()

	srv, _ := newCapturingServer(t, http.StatusOK, okReply)
	client := newMediaClient(t, srv, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.GenerateWithMedia(ctx, llm.MediaRequest{Text: "text-marker"})
	if err == nil {
		t.Fatal("GenerateWithMedia with a canceled context succeeded, want error")
	}
	if !strings.Contains(err.Error(), "canceled or timed out") || strings.Contains(err.Error(), "text-marker") {
		t.Errorf("error %q, want a cancellation error without the text", err.Error())
	}
}

func TestGenerateWithMediaUsageReportsTokenCounts(t *testing.T) {
	t.Parallel()

	t.Run("sums candidate and thinking tokens as output", func(t *testing.T) {
		t.Parallel()

		const reply = `{"candidates":[{"content":{"parts":[{"text":"ok"}]}}],` +
			`"usageMetadata":{"promptTokenCount":120,"candidatesTokenCount":7,"thoughtsTokenCount":30,"totalTokenCount":157}}`

		srv, _ := newCapturingServer(t, http.StatusOK, reply)
		client := newMediaClient(t, srv, nil)

		text, usage, err := client.GenerateWithMediaUsage(context.Background(), llm.MediaRequest{Text: "hola"})
		if err != nil {
			t.Fatalf("GenerateWithMediaUsage: %v", err)
		}
		if text != "ok" || usage.PromptTokens != 120 || usage.OutputTokens != 37 {
			t.Errorf("text = %q, usage = %+v, want ok with 120 prompt and 37 output tokens", text, usage)
		}
	})

	t.Run("is zero when the response carries no usage", func(t *testing.T) {
		t.Parallel()

		srv, _ := newCapturingServer(t, http.StatusOK, okReply)
		client := newMediaClient(t, srv, nil)

		_, usage, err := client.GenerateWithMediaUsage(context.Background(), llm.MediaRequest{Text: "hola"})
		if err != nil {
			t.Fatalf("GenerateWithMediaUsage: %v", err)
		}
		if usage != (llm.Usage{}) {
			t.Errorf("usage = %+v, want zero", usage)
		}
	})
}

func TestGenerateTextRequestCarriesNoInlineData(t *testing.T) {
	t.Parallel()

	srv, captured := newCapturingServer(t, http.StatusOK, okReply)
	client := newMediaClient(t, srv, nil)

	_, err := client.Generate(context.Background(), "hola")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	parts := userParts(t, captured.Body)
	if len(parts) != 1 || parts[0]["text"] != "hola" {
		t.Fatalf("parts = %#v, want a single text part", parts)
	}
	if _, hasInline := parts[0]["inline_data"]; hasInline {
		t.Errorf("text request carries inline_data: %#v", parts[0])
	}
	if _, hasMIME := generationConfigOf(t, captured.Body)["responseMimeType"]; hasMIME {
		t.Errorf("text request asks for a response MIME type: %#v", captured.Body["generationConfig"])
	}
}
