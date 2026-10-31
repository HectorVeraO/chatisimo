package llm_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/HectorVeraO/chatisimo/internal/platform/llm"
)

const testAPIKey = "synthetic-vertex-api-key-00000000"

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestNewValidation(t *testing.T) {
	t.Parallel()

	t.Run("aggregates multiple validation problems without leaking values", func(t *testing.T) {
		t.Parallel()

		badKey := "secret-with space"
		badModel := "bad/model?name"
		badURL := "ftp://user@example.com/models?foo=bar"
		badPrompt := "prompt-marker\x00with-nul"

		_, err := llm.New(llm.Config{
			APIKey:      badKey,
			Model:       badModel,
			BaseURL:     badURL,
			Prompt:      badPrompt,
			Temperature: -0.5,
		})
		if err == nil {
			t.Fatal("New succeeded for invalid config, want error")
		}

		msg := err.Error()
		for _, want := range []string{"APIKey", "Model", "BaseURL", "Prompt", "Temperature"} {
			if !strings.Contains(msg, want) {
				t.Errorf("error %q does not mention %q", msg, want)
			}
		}

		for _, forbidden := range []string{badKey, badModel, badURL, badPrompt, "prompt-marker", "user@"} {
			if strings.Contains(msg, forbidden) {
				t.Errorf("error %q leaked input value %q", msg, forbidden)
			}
		}
	})

	t.Run("rejects Prompt with disallowed control characters or excessive length", func(t *testing.T) {
		t.Parallel()

		cases := []struct {
			name   string
			prompt string
		}{
			{name: "nul byte", prompt: "rewrite\x00question"},
			{name: "lone carriage return", prompt: "line 1\rline 2"},
			{name: "tab character", prompt: "line 1\tline 2"},
			{name: "c1 control", prompt: "line 1\u0085line 2"},
			{name: "whitespace and control only", prompt: "  \x00  "},
			{name: "invalid utf8", prompt: "bad\xff\xfeutf8"},
			{name: "exceeds max bytes", prompt: strings.Repeat("p", (16<<10)+1)},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				_, err := llm.New(llm.Config{
					APIKey: testAPIKey,
					Prompt: tc.prompt,
				})
				if err == nil {
					t.Fatal("New succeeded with invalid Prompt, want error")
				}

				if strings.Contains(err.Error(), testAPIKey) {
					t.Errorf("error %q leaked APIKey", err.Error())
				}
			})
		}
	})

	t.Run("rejects empty APIKey", func(t *testing.T) {
		t.Parallel()

		_, err := llm.New(llm.Config{APIKey: "   "})
		if err == nil {
			t.Fatal("New succeeded with empty APIKey, want error")
		}
	})

	t.Run("rejects BaseURL with empty host", func(t *testing.T) {
		t.Parallel()

		_, err := llm.New(llm.Config{
			APIKey:  testAPIKey,
			BaseURL: "https:///models",
		})
		if err == nil {
			t.Fatal("New succeeded with hostless BaseURL, want error")
		}
	})

	t.Run("rejects out-of-range or NaN Temperature", func(t *testing.T) {
		t.Parallel()

		cases := []struct {
			name        string
			temperature float64
		}{
			{name: "negative", temperature: -0.01},
			{name: "above max", temperature: llm.MaxTemperature + 0.01},
			{name: "nan", temperature: math.NaN()},
			{name: "positive infinity", temperature: math.Inf(1)},
			{name: "negative infinity", temperature: math.Inf(-1)},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				_, err := llm.New(llm.Config{
					APIKey:      testAPIKey,
					Temperature: tc.temperature,
				})
				if err == nil {
					t.Fatalf("New succeeded for temperature %v, want error", tc.temperature)
				}

				if !strings.Contains(err.Error(), "Temperature") {
					t.Errorf("error %q does not mention Temperature", err.Error())
				}

				if strings.Contains(err.Error(), testAPIKey) {
					t.Errorf("error %q leaked APIKey", err.Error())
				}
			})
		}
	})
}

func TestRedaction(t *testing.T) {
	t.Parallel()

	cfg := llm.Config{
		APIKey:  testAPIKey,
		Model:   llm.DefaultModel,
		BaseURL: llm.DefaultBaseURL,
	}

	client, err := llm.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))
	logger.Info("inspect llm", slog.Any("config", cfg), slog.Any("client", client))

	rendered := []string{
		cfg.String(),
		cfg.GoString(),
		fmt.Sprintf("%s|%v|%+v|%#v", cfg, cfg, cfg, cfg),
		client.String(),
		client.GoString(),
		fmt.Sprintf("%s|%v|%+v|%#v", client, client, client, client),
		logBuf.String(),
	}

	for _, s := range rendered {
		if strings.Contains(s, testAPIKey) {
			t.Fatalf("rendered output %q leaked APIKey", s)
		}

		if !strings.Contains(s, "[redacted]") {
			t.Fatalf("rendered output %q does not contain [redacted]", s)
		}
	}
}

func TestRephraseSuccess(t *testing.T) {
	t.Parallel()

	const (
		rawQuestion       = "yo bro do u like green tea or nah???"
		rephrasedQuestion = "Do you enjoy drinking green tea?"
	)

	var (
		gotMethod string
		gotPath   string
		gotQuery  string
		gotAPIKey string
		gotBody   map[string]any
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAPIKey = r.Header.Get("x-goog-api-key")

		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read error", http.StatusBadRequest)
			return
		}

		if err := json.Unmarshal(raw, &gotBody); err != nil {
			http.Error(w, "json error", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"candidates":[{"content":{"parts":[{"text":"  %s\n"}]}}]}`, rephrasedQuestion)
	}))
	defer srv.Close()

	var logBuf bytes.Buffer
	client, err := llm.New(llm.Config{
		APIKey:  testAPIKey,
		BaseURL: srv.URL + "/v1/publishers/google/models/",
		Logger:  slog.New(slog.NewJSONHandler(&logBuf, nil)),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	got, err := client.Rephrase(context.Background(), rawQuestion)
	if err != nil {
		t.Fatalf("Rephrase: %v", err)
	}

	if got != rephrasedQuestion {
		t.Errorf("Rephrase = %q, want %q", got, rephrasedQuestion)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}

	wantPath := "/v1/publishers/google/models/" + llm.DefaultModel + ":generateContent"
	if gotPath != wantPath {
		t.Errorf("path = %q, want %q", gotPath, wantPath)
	}

	if gotQuery != "" {
		t.Errorf("query = %q, want empty (API key must never be in query string)", gotQuery)
	}

	if gotAPIKey != testAPIKey {
		t.Errorf("x-goog-api-key = %q, want %q", gotAPIKey, testAPIKey)
	}

	contents, ok := gotBody["contents"].([]any)
	if !ok || len(contents) != 1 {
		t.Fatalf("contents = %#v, want 1 message", gotBody["contents"])
	}

	gotPrompt := extractSystemInstructionText(t, gotBody)
	if gotPrompt != llm.DefaultPrompt {
		t.Errorf("systemInstruction text = %q, want DefaultPrompt %q", gotPrompt, llm.DefaultPrompt)
	}

	for _, requiredFragment := range []string{
		"2nd person singular",
		"recipient",
		"someone",
		"alguien",
		"tuteo",
		"voseo",
		"vosotros",
		"rey",
		"king",
		"kingcito",
		"kin",
		"bro",
		"amigo",
		"hmno",
		"wn",
	} {
		if !strings.Contains(gotPrompt, requiredFragment) {
			t.Errorf("DefaultPrompt %q does not contain required instruction fragment %q", gotPrompt, requiredFragment)
		}
	}

	logged := logBuf.String()
	for _, secret := range []string{testAPIKey, rawQuestion, rephrasedQuestion} {
		if strings.Contains(logged, secret) {
			t.Errorf("log output %q leaked %q", logged, secret)
		}
	}
}

func TestRephraseCustomMultilinePrompt(t *testing.T) {
	t.Parallel()

	const (
		customPrompt      = "Rule 1: Keep 2nd person singular.\nRule 2: Use neutral Spanish tuteo.\nRule 3: Drop vocative slang."
		rawQuestion       = "hola kingcito que te gusta?"
		rephrasedQuestion = "Que te gusta recibir de regalo?"
	)

	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read error", http.StatusBadRequest)
			return
		}

		if err := json.Unmarshal(raw, &gotBody); err != nil {
			http.Error(w, "json error", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"candidates":[{"content":{"parts":[{"text":%q}]}}]}`, rephrasedQuestion)
	}))
	defer srv.Close()

	client, err := llm.New(llm.Config{
		APIKey:  testAPIKey,
		BaseURL: srv.URL + "/v1/publishers/google/models",
		Prompt:  "  " + strings.ReplaceAll(customPrompt, "\n", "\r\n") + "\r\n",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	got, err := client.Rephrase(context.Background(), rawQuestion)
	if err != nil {
		t.Fatalf("Rephrase: %v", err)
	}

	if got != rephrasedQuestion {
		t.Errorf("Rephrase = %q, want %q", got, rephrasedQuestion)
	}

	gotPrompt := extractSystemInstructionText(t, gotBody)
	if gotPrompt != customPrompt {
		t.Errorf("systemInstruction text = %q, want %q", gotPrompt, customPrompt)
	}
}

func extractSystemInstructionText(t *testing.T, body map[string]any) string {
	t.Helper()

	sysInst, ok := body["systemInstruction"].(map[string]any)
	if !ok {
		t.Fatalf("request body missing systemInstruction object: %#v", body)
	}

	parts, ok := sysInst["parts"].([]any)
	if !ok || len(parts) != 1 {
		t.Fatalf("systemInstruction.parts = %#v, want 1 part", sysInst["parts"])
	}

	part, ok := parts[0].(map[string]any)
	if !ok {
		t.Fatalf("systemInstruction.parts[0] = %#v, want object", parts[0])
	}

	text, ok := part["text"].(string)
	if !ok {
		t.Fatalf("systemInstruction.parts[0].text = %#v, want string", part["text"])
	}

	return text
}

func TestRephraseErrorsNeverLeakQuestionOrSecret(t *testing.T) {
	t.Parallel()

	const (
		rawQuestion = "unique-raw-question-marker-998877"
		respMarker  = "upstream-response-body-marker-112233"
	)

	cases := []struct {
		name    string
		handler roundTripFunc
	}{
		{
			name: "http 500",
			handler: func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusInternalServerError,
					Body:       io.NopCloser(strings.NewReader(respMarker)),
					Header:     make(http.Header),
				}, nil
			},
		},
		{
			name: "http 429",
			handler: func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusTooManyRequests,
					Body:       io.NopCloser(strings.NewReader(respMarker)),
					Header:     make(http.Header),
				}, nil
			},
		},
		{
			name: "malformed json",
			handler: func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("{not-json:" + respMarker)),
					Header:     make(http.Header),
				}, nil
			},
		},
		{
			name: "empty candidates",
			handler: func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"candidates":[]}`)),
					Header:     make(http.Header),
				}, nil
			},
		},
		{
			name: "empty candidate text",
			handler: func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"parts":[{"text":"   \n\t  "}]}}]}`)),
					Header:     make(http.Header),
				}, nil
			},
		},
		{
			name: "transport failure",
			handler: func(*http.Request) (*http.Response, error) {
				return nil, errors.New("dial error: " + respMarker)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var logBuf bytes.Buffer
			client, err := llm.New(llm.Config{
				APIKey:     testAPIKey,
				HTTPClient: &http.Client{Transport: tc.handler},
				Logger:     slog.New(slog.NewJSONHandler(&logBuf, nil)),
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			got, err := client.Rephrase(context.Background(), rawQuestion)
			if err == nil {
				t.Fatalf("Rephrase = %q, want error", got)
			}

			combined := err.Error() + "\n" + logBuf.String()
			for _, forbidden := range []string{testAPIKey, rawQuestion, respMarker} {
				if strings.Contains(combined, forbidden) {
					t.Errorf("error/log output %q leaked %q", combined, forbidden)
				}
			}
		})
	}
}

func TestRephraseInputAndContextValidation(t *testing.T) {
	t.Parallel()

	var nilClient *llm.Client
	if _, err := nilClient.Rephrase(context.Background(), "hello?"); err == nil {
		t.Fatal("nilClient.Rephrase succeeded, want error")
	}

	client, err := llm.New(llm.Config{
		APIKey: testAPIKey,
		HTTPClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				<-req.Context().Done()
				return nil, req.Context().Err()
			}),
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	//nolint:staticcheck // testing nil context guard
	if _, err := client.Rephrase(nil, "hello?"); err == nil {
		t.Fatal("Rephrase(nil, ...) succeeded, want error")
	}

	if _, err := client.Rephrase(context.Background(), "   "); err == nil {
		t.Fatal("Rephrase with blank text succeeded, want error")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = client.Rephrase(ctx, "Do you like books?")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Rephrase with canceled ctx = %v, want context.Canceled", err)
	}
}

func FuzzRephraseResponse(f *testing.F) {
	f.Add([]byte(`{"candidates":[{"content":{"parts":[{"text":"What is your favorite color?"}]}}]}`))
	f.Add([]byte(`{"candidates":[]}`))
	f.Add([]byte(`{"candidates":[{"content":{"parts":[{"text":"   "}]}}]}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`not json`))

	f.Fuzz(func(t *testing.T, body []byte) {
		client, err := llm.New(llm.Config{
			APIKey: testAPIKey,
			HTTPClient: &http.Client{
				Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(bytes.NewReader(body)),
						Header:     make(http.Header),
					}, nil
				}),
			},
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		got, err := client.Rephrase(context.Background(), "Do you prefer coffee or tea?")
		if err != nil {
			if strings.Contains(err.Error(), testAPIKey) {
				t.Fatalf("error %q leaked API key", err.Error())
			}

			return
		}

		if strings.TrimSpace(got) == "" {
			t.Fatalf("Rephrase returned empty/whitespace text %q without error", got)
		}

		if got != strings.TrimSpace(got) {
			t.Fatalf("Rephrase returned untrimmed text %q", got)
		}
	})
}

func TestGenerateTemperatureWirePayload(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name            string
		cfgTemperature  float64
		wantTemperature float64
	}{
		{
			name:            "zero defaults to DefaultTemperature",
			cfgTemperature:  0,
			wantTemperature: llm.DefaultTemperature,
		},
		{
			name:            "custom temperature 0.7",
			cfgTemperature:  0.7,
			wantTemperature: 0.7,
		},
		{
			name:            "maximum temperature boundary",
			cfgTemperature:  llm.MaxTemperature,
			wantTemperature: llm.MaxTemperature,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var gotBody map[string]any

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, "read error", http.StatusBadRequest)
					return
				}

				err = json.Unmarshal(raw, &gotBody)
				if err != nil {
					http.Error(w, "json error", http.StatusBadRequest)
					return
				}

				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"  La franja juega hoy  "}]}}]}`)
			}))
			defer srv.Close()

			var logBuf bytes.Buffer
			client, err := llm.New(llm.Config{
				APIKey:      testAPIKey,
				BaseURL:     srv.URL + "/v1/publishers/google/models",
				Prompt:      "Write one short football banter line.",
				Temperature: tc.cfgTemperature,
				Logger:      slog.New(slog.NewJSONHandler(&logBuf, nil)),
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			got, err := client.Generate(context.Background(), "Scenario: blocked_home")
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}

			if got != "La franja juega hoy" {
				t.Errorf("Generate = %q, want %q", got, "La franja juega hoy")
			}

			genCfg, ok := gotBody["generationConfig"].(map[string]any)
			if !ok {
				t.Fatalf("generationConfig = %#v, want object", gotBody["generationConfig"])
			}

			gotTemp, ok := genCfg["temperature"].(float64)
			if !ok {
				t.Fatalf("generationConfig.temperature = %#v, want float64", genCfg["temperature"])
			}

			if gotTemp != tc.wantTemperature {
				t.Errorf("generationConfig.temperature = %v, want %v", gotTemp, tc.wantTemperature)
			}

			logged := logBuf.String()
			for _, secret := range []string{testAPIKey, "Scenario: blocked_home", "La franja juega hoy"} {
				if strings.Contains(logged, secret) {
					t.Errorf("log output %q leaked %q", logged, secret)
				}
			}
		})
	}
}

func TestGenerateInputAndContextValidation(t *testing.T) {
	t.Parallel()

	var nilClient *llm.Client
	_, err := nilClient.Generate(context.Background(), "hello")
	if err == nil {
		t.Fatal("nilClient.Generate succeeded, want error")
	}

	client, err := llm.New(llm.Config{
		APIKey:      testAPIKey,
		Temperature: 0.7,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	//nolint:staticcheck // testing nil context guard
	_, err = client.Generate(nil, "hello")
	if err == nil {
		t.Fatal("Generate(nil, ...) succeeded, want error")
	}

	_, err = client.Generate(context.Background(), "   ")
	if err == nil {
		t.Fatal("Generate with blank text succeeded, want error")
	}
}
