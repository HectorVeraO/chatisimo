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

const okReply = `{"candidates":[{"content":{"parts":[{"text":" {\"arrival\":false} "}]}}]}`

func TestGenerateWithImagesSendsInlineDataToConfiguredModel(t *testing.T) {
	t.Parallel()

	pngBytes := []byte("\x89PNG synthetic image one")
	gifBytes := []byte("GIF89a synthetic image two")

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

			got, err := client.GenerateWithImages(context.Background(), "Read the image.", "  caption text  ", []llm.Image{
				{MIMEType: "image/png", Data: pngBytes},
				{MIMEType: "image/gif", Data: gifBytes},
			})
			if err != nil {
				t.Fatalf("GenerateWithImages: %v", err)
			}
			if got != `{"arrival":false}` {
				t.Errorf("GenerateWithImages = %q, want trimmed model text", got)
			}

			if captured.Method != http.MethodPost || captured.Path != tc.wantPath {
				t.Errorf("request = %s %s, want POST %s", captured.Method, captured.Path, tc.wantPath)
			}
			if captured.APIKey != testAPIKey {
				t.Errorf("x-goog-api-key header was not sent")
			}

			parts := userParts(t, captured.Body)
			if len(parts) != 3 {
				t.Fatalf("parts = %#v, want two images then one text", parts)
			}

			wantImages := []struct {
				mime string
				data []byte
			}{
				{"image/png", pngBytes},
				{"image/gif", gifBytes},
			}
			for i, want := range wantImages {
				inline, ok := parts[i]["inline_data"].(map[string]any)
				if !ok {
					t.Fatalf("parts[%d] = %#v, want inline_data", i, parts[i])
				}
				if inline["mime_type"] != want.mime {
					t.Errorf("parts[%d] mime_type = %v, want %s", i, inline["mime_type"], want.mime)
				}
				wantData := base64.StdEncoding.EncodeToString(want.data)
				if inline["data"] != wantData {
					t.Errorf("parts[%d] data = %v, want standard base64 of the image", i, inline["data"])
				}
				if _, hasText := parts[i]["text"]; hasText {
					t.Errorf("parts[%d] carries text, want image only", i)
				}
			}
			if parts[2]["text"] != "caption text" {
				t.Errorf("parts[2] text = %v, want trimmed caption", parts[2]["text"])
			}

			genCfg, ok := captured.Body["generationConfig"].(map[string]any)
			if !ok || genCfg["maxOutputTokens"] != float64(256) {
				t.Errorf("generationConfig = %#v, want maxOutputTokens 256", captured.Body["generationConfig"])
			}

			system, ok := captured.Body["systemInstruction"].(map[string]any)
			if !ok {
				t.Fatalf("systemInstruction = %#v, want object", captured.Body["systemInstruction"])
			}
			systemParts, ok := system["parts"].([]any)
			if !ok || len(systemParts) != 1 {
				t.Fatalf("systemInstruction.parts = %#v", system["parts"])
			}
			systemPart, ok := systemParts[0].(map[string]any)
			if !ok || systemPart["text"] != "Read the image." {
				t.Errorf("systemInstruction part = %#v, want prompt text", systemParts[0])
			}

			logged := logBuf.String()
			for _, secret := range []string{testAPIKey, "caption text", base64.StdEncoding.EncodeToString(pngBytes)} {
				if strings.Contains(logged, secret) {
					t.Errorf("log output %q leaked %q", logged, secret)
				}
			}
		})
	}
}

func TestGenerateWithImagesAllowsEmptyText(t *testing.T) {
	t.Parallel()

	srv, captured := newCapturingServer(t, http.StatusOK, okReply)

	client, err := llm.New(llm.Config{
		APIKey:  testAPIKey,
		BaseURL: srv.URL + "/v1/publishers/google/models",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.GenerateWithImages(context.Background(), "", "   ", []llm.Image{
		{MIMEType: "image/webp", Data: []byte("synthetic")},
	})
	if err != nil {
		t.Fatalf("GenerateWithImages: %v", err)
	}

	parts := userParts(t, captured.Body)
	if len(parts) != 1 {
		t.Fatalf("parts = %#v, want only the image part", parts)
	}
	if _, hasInline := parts[0]["inline_data"]; !hasInline {
		t.Errorf("parts[0] = %#v, want inline_data", parts[0])
	}
}

func TestGenerateWithImagesUsesConfiguredPromptWhenPromptIsBlank(t *testing.T) {
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

	_, err = client.GenerateWithImages(context.Background(), "  ", "x", []llm.Image{
		{MIMEType: "image/png", Data: []byte("synthetic")},
	})
	if err != nil {
		t.Fatalf("GenerateWithImages: %v", err)
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

func TestGenerateWithImagesBoundaries(t *testing.T) {
	t.Parallel()

	srv, _ := newCapturingServer(t, http.StatusOK, okReply)

	client, err := llm.New(llm.Config{
		APIKey:  testAPIKey,
		BaseURL: srv.URL + "/v1/publishers/google/models",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	image := func(size int) llm.Image {
		return llm.Image{MIMEType: "image/png", Data: bytes.Repeat([]byte{'a'}, size)}
	}

	accepted := []struct {
		name   string
		images []llm.Image
	}{
		{"one image", []llm.Image{image(1)}},
		{"maximum image count", []llm.Image{image(1), image(1)}},
		{"maximum image size", []llm.Image{image(llm.MaxImageBytes)}},
	}
	for _, tc := range accepted {
		t.Run("accepts "+tc.name, func(t *testing.T) {
			t.Parallel()

			_, genErr := client.GenerateWithImages(context.Background(), "", "x", tc.images)
			if genErr != nil {
				t.Errorf("GenerateWithImages: %v", genErr)
			}
		})
	}

	rejected := []struct {
		name   string
		images []llm.Image
	}{
		{"no images", nil},
		{"too many images", []llm.Image{image(1), image(1), image(1)}},
		{"unsupported MIME type", []llm.Image{{MIMEType: "application/pdf", Data: []byte("x")}}},
		{"empty MIME type", []llm.Image{{MIMEType: "", Data: []byte("x")}}},
		{"empty data", []llm.Image{{MIMEType: "image/png"}}},
		{"oversize data", []llm.Image{image(llm.MaxImageBytes + 1)}},
	}
	for _, tc := range rejected {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			t.Parallel()

			_, genErr := client.GenerateWithImages(context.Background(), "", "x", tc.images)
			if genErr == nil {
				t.Error("GenerateWithImages succeeded, want error")
			}
		})
	}
}

func TestGenerateWithImagesAggregatesErrorsWithoutLeaking(t *testing.T) {
	t.Parallel()

	client, err := llm.New(llm.Config{APIKey: testAPIKey})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	secretData := []byte("secret-image-marker")
	//nolint:staticcheck // testing aggregated nil context + bad prompt + bad images
	_, err = client.GenerateWithImages(nil, "secret-prompt\x00marker", "secret-text-marker", []llm.Image{
		{MIMEType: "text/secret-mime", Data: secretData},
		{MIMEType: "image/png"},
		{MIMEType: "image/png", Data: []byte("x")},
	})
	if err == nil {
		t.Fatal("GenerateWithImages with invalid inputs succeeded, want error")
	}

	message := err.Error()
	for _, want := range []string{"nil context", "control characters", "image count", "unsupported MIME", "is empty"} {
		if !strings.Contains(message, want) {
			t.Errorf("error %q missing aggregated problem %q", message, want)
		}
	}
	for _, leaked := range []string{"secret-prompt", "secret-text-marker", "secret-image-marker", "secret-mime", testAPIKey} {
		if strings.Contains(message, leaked) {
			t.Errorf("error %q leaked %q", message, leaked)
		}
	}

	var nilClient *llm.Client
	_, err = nilClient.GenerateWithImages(context.Background(), "", "x", []llm.Image{{MIMEType: "image/png", Data: []byte("x")}})
	if err == nil {
		t.Error("nil client GenerateWithImages succeeded, want error")
	}
}

func TestGenerateWithImagesReportsNon200WithoutLeaking(t *testing.T) {
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

	_, err = client.GenerateWithImages(context.Background(), "", "caption-marker", []llm.Image{
		{MIMEType: "image/png", Data: []byte("image-marker")},
	})
	if err == nil {
		t.Fatal("GenerateWithImages succeeded on HTTP 500, want error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error %q does not name the status", err.Error())
	}
	for _, leaked := range []string{"caption-marker", "image-marker", testAPIKey} {
		if strings.Contains(err.Error(), leaked) || strings.Contains(logBuf.String(), leaked) {
			t.Errorf("error or log leaked %q", leaked)
		}
	}
}

func TestGenerateTextRequestCarriesNoInlineData(t *testing.T) {
	t.Parallel()

	srv, captured := newCapturingServer(t, http.StatusOK, okReply)

	client, err := llm.New(llm.Config{
		APIKey:  testAPIKey,
		BaseURL: srv.URL + "/v1/publishers/google/models",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.Generate(context.Background(), "hola")
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
}
