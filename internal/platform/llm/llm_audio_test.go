package llm_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/platform/llm"
)

func TestGenerateWithAudioSendsInlineDataToConfiguredModel(t *testing.T) {
	t.Parallel()

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

			got, err := client.GenerateWithAudio(
				context.Background(), "Listen to the audio.", "  caption text  ",
				llm.Audio{MIMEType: "audio/ogg", Data: oggBytes},
			)
			if err != nil {
				t.Fatalf("GenerateWithAudio: %v", err)
			}
			if got != `{"arrival":false}` {
				t.Errorf("GenerateWithAudio = %q, want trimmed model text", got)
			}

			if captured.Method != http.MethodPost || captured.Path != tc.wantPath {
				t.Errorf("request = %s %s, want POST %s", captured.Method, captured.Path, tc.wantPath)
			}
			if captured.APIKey != testAPIKey {
				t.Errorf("x-goog-api-key header was not sent")
			}

			parts := userParts(t, captured.Body)
			if len(parts) != 2 {
				t.Fatalf("parts = %#v, want the audio then one text", parts)
			}

			inline, ok := parts[0]["inline_data"].(map[string]any)
			if !ok {
				t.Fatalf("parts[0] = %#v, want inline_data first", parts[0])
			}
			if inline["mime_type"] != "audio/ogg" {
				t.Errorf("mime_type = %v, want audio/ogg", inline["mime_type"])
			}
			if inline["data"] != base64.StdEncoding.EncodeToString(oggBytes) {
				t.Errorf("data = %v, want standard base64 of the audio", inline["data"])
			}
			if _, hasText := parts[0]["text"]; hasText {
				t.Errorf("parts[0] carries text, want audio only")
			}
			if parts[1]["text"] != "caption text" {
				t.Errorf("parts[1] text = %v, want trimmed caption", parts[1]["text"])
			}

			genCfg, ok := captured.Body["generationConfig"].(map[string]any)
			if !ok || genCfg["maxOutputTokens"] != float64(256) {
				t.Errorf("generationConfig = %#v, want maxOutputTokens 256", captured.Body["generationConfig"])
			}

			logged := logBuf.String()
			for _, secret := range []string{testAPIKey, "caption text", base64.StdEncoding.EncodeToString(oggBytes)} {
				if strings.Contains(logged, secret) {
					t.Errorf("log output %q leaked %q", logged, secret)
				}
			}
		})
	}
}

func TestGenerateWithAudioAllowsEmptyText(t *testing.T) {
	t.Parallel()

	srv, captured := newCapturingServer(t, http.StatusOK, okReply)

	client, err := llm.New(llm.Config{
		APIKey:  testAPIKey,
		BaseURL: srv.URL + "/v1/publishers/google/models",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.GenerateWithAudio(context.Background(), "", "   ", llm.Audio{
		MIMEType: "audio/mpeg",
		Data:     []byte("synthetic"),
	})
	if err != nil {
		t.Fatalf("GenerateWithAudio: %v", err)
	}

	parts := userParts(t, captured.Body)
	if len(parts) != 1 {
		t.Fatalf("parts = %#v, want only the audio part", parts)
	}
	if _, hasInline := parts[0]["inline_data"]; !hasInline {
		t.Errorf("parts[0] = %#v, want inline_data", parts[0])
	}
}

func TestGenerateWithAudioNormalizesParameterisedMIME(t *testing.T) {
	t.Parallel()

	srv, captured := newCapturingServer(t, http.StatusOK, okReply)

	client, err := llm.New(llm.Config{
		APIKey:  testAPIKey,
		BaseURL: srv.URL + "/v1/publishers/google/models",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.GenerateWithAudio(context.Background(), "", "", llm.Audio{
		MIMEType: " Audio/OGG; codecs=opus ",
		Data:     []byte("synthetic"),
	})
	if err != nil {
		t.Fatalf("GenerateWithAudio: %v", err)
	}

	parts := userParts(t, captured.Body)
	inline, ok := parts[0]["inline_data"].(map[string]any)
	if !ok || inline["mime_type"] != "audio/ogg" {
		t.Errorf("parts[0] = %#v, want inline_data with the normalized audio/ogg type", parts[0])
	}
}

func TestGenerateWithAudioUsesConfiguredPromptWhenPromptIsBlank(t *testing.T) {
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

	_, err = client.GenerateWithAudio(context.Background(), "  ", "x", llm.Audio{
		MIMEType: "audio/wav",
		Data:     []byte("synthetic"),
	})
	if err != nil {
		t.Fatalf("GenerateWithAudio: %v", err)
	}

	instruction, isObject := captured.Body["systemInstruction"].(map[string]any)
	if !isObject {
		t.Fatalf("systemInstruction = %#v, want object", captured.Body["systemInstruction"])
	}

	parts, isArray := instruction["parts"].([]any)
	if !isArray || len(parts) != 1 {
		t.Fatalf("systemInstruction.parts = %#v, want one part", instruction["parts"])
	}

	part, isPart := parts[0].(map[string]any)
	if !isPart || part["text"] != configuredPrompt {
		t.Errorf("system instruction part = %#v, want text %q", parts[0], configuredPrompt)
	}
}

func TestGenerateWithAudioBoundaries(t *testing.T) {
	t.Parallel()

	srv, _ := newCapturingServer(t, http.StatusOK, okReply)

	client, err := llm.New(llm.Config{
		APIKey:  testAPIKey,
		BaseURL: srv.URL + "/v1/publishers/google/models",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	audio := func(mime string, size int) llm.Audio {
		return llm.Audio{MIMEType: mime, Data: bytes.Repeat([]byte{'a'}, size)}
	}

	accepted := []struct {
		name  string
		audio llm.Audio
	}{
		{"one byte", audio("audio/ogg", 1)},
		{"maximum size", audio("audio/ogg", llm.MaxAudioBytes)},
		{"parameterised type", audio("audio/ogg; codecs=opus", 1)},
		{"opus", audio("audio/opus", 1)},
		{"mpeg", audio("audio/mpeg", 1)},
		{"mp3", audio("audio/mp3", 1)},
		{"wav", audio("audio/wav", 1)},
		{"aac", audio("audio/aac", 1)},
		{"flac", audio("audio/flac", 1)},
		{"m4a", audio("audio/m4a", 1)},
		{"webm", audio("audio/webm", 1)},
	}
	for _, tc := range accepted {
		t.Run("accepts "+tc.name, func(t *testing.T) {
			t.Parallel()

			_, genErr := client.GenerateWithAudio(context.Background(), "", "x", tc.audio)
			if genErr != nil {
				t.Errorf("GenerateWithAudio: %v", genErr)
			}
		})
	}

	rejected := []struct {
		name  string
		audio llm.Audio
	}{
		{"empty data", llm.Audio{MIMEType: "audio/ogg"}},
		{"oversize data", audio("audio/ogg", llm.MaxAudioBytes+1)},
		{"image type", audio("image/png", 1)},
		{"video type", audio("video/mp4", 1)},
		{"empty type", audio("", 1)},
		{"unlisted audio type", audio("audio/midi", 1)},
	}
	for _, tc := range rejected {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			t.Parallel()

			_, genErr := client.GenerateWithAudio(context.Background(), "", "x", tc.audio)
			if genErr == nil {
				t.Error("GenerateWithAudio succeeded, want error")
			}
		})
	}
}

func TestGenerateWithAudioAggregatesErrorsWithoutLeaking(t *testing.T) {
	t.Parallel()

	client, err := llm.New(llm.Config{APIKey: testAPIKey})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	//nolint:staticcheck // testing aggregated nil context + bad prompt + bad audio
	_, err = client.GenerateWithAudio(nil, "secret-prompt\x00marker", "secret-text-marker", llm.Audio{
		MIMEType: "text/secret-mime",
	})
	if err == nil {
		t.Fatal("GenerateWithAudio with invalid inputs succeeded, want error")
	}

	message := err.Error()
	for _, want := range []string{"nil context", "control characters", "unsupported MIME", "is empty"} {
		if !strings.Contains(message, want) {
			t.Errorf("error %q missing aggregated problem %q", message, want)
		}
	}
	for _, leaked := range []string{"secret-prompt", "secret-text-marker", "secret-mime", testAPIKey} {
		if strings.Contains(message, leaked) {
			t.Errorf("error %q leaked %q", message, leaked)
		}
	}

	var nilClient *llm.Client
	_, err = nilClient.GenerateWithAudio(context.Background(), "", "x", llm.Audio{MIMEType: "audio/ogg", Data: []byte("x")})
	if err == nil {
		t.Error("nil client GenerateWithAudio succeeded, want error")
	}
}

func TestGenerateWithAudioReportsNon200WithoutLeaking(t *testing.T) {
	t.Parallel()

	srv, _ := newCapturingServer(t, http.StatusInternalServerError, `{"error":"boom"}`)

	var logBuf bytes.Buffer
	client, err := llm.New(llm.Config{
		APIKey:  testAPIKey,
		BaseURL: srv.URL + "/v1/publishers/google/models",
		Logger:  slog.New(slog.NewJSONHandler(&logBuf, nil)),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.GenerateWithAudio(context.Background(), "", "caption-marker", llm.Audio{
		MIMEType: "audio/ogg",
		Data:     []byte("audio-marker"),
	})
	if err == nil {
		t.Fatal("GenerateWithAudio succeeded on HTTP 500, want error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error %q does not name the status", err.Error())
	}
	for _, leaked := range []string{"caption-marker", "audio-marker", testAPIKey} {
		if strings.Contains(err.Error(), leaked) || strings.Contains(logBuf.String(), leaked) {
			t.Errorf("error or log leaked %q", leaked)
		}
	}
}
