package llm_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
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

		_, err := llm.New(llm.Config{
			APIKey:  badKey,
			Model:   badModel,
			BaseURL: badURL,
		})
		if err == nil {
			t.Fatal("New succeeded for invalid config, want error")
		}

		msg := err.Error()
		for _, want := range []string{"APIKey", "Model", "BaseURL"} {
			if !strings.Contains(msg, want) {
				t.Errorf("error %q does not mention %q", msg, want)
			}
		}

		for _, forbidden := range []string{badKey, badModel, badURL, "user@"} {
			if strings.Contains(msg, forbidden) {
				t.Errorf("error %q leaked input value %q", msg, forbidden)
			}
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

	if _, hasSys := gotBody["systemInstruction"]; !hasSys {
		t.Errorf("request body missing systemInstruction: %#v", gotBody)
	}

	logged := logBuf.String()
	for _, secret := range []string{testAPIKey, rawQuestion, rephrasedQuestion} {
		if strings.Contains(logged, secret) {
			t.Errorf("log output %q leaked %q", logged, secret)
		}
	}
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
