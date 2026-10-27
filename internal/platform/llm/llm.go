// Package llm calls the Vertex AI generateContent REST endpoint to rephrase anonymous gift
// exchange questions into neutral phrasing.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// DefaultModel is the Vertex AI model used when Config.Model is empty.
	DefaultModel = "gemini-3.5-flash-lite"
	// DefaultBaseURL is the Vertex AI publisher models base URL used when Config.BaseURL is empty.
	DefaultBaseURL = "https://aiplatform.googleapis.com/v1/publishers/google/models"
	// DefaultTimeout bounds each Rephrase request.
	DefaultTimeout = 10 * time.Second

	maxResponseBytes = 64 * 1024
	redacted         = "[redacted]"

	rephraseSystemInstruction = "Rewrite the following anonymous gift-exchange question in neutral, natural phrasing so the author's writing style, slang, punctuation habits, typos, and capitalization cannot identify them. Preserve the language of the input question and its exact meaning and constraints. Output ONLY the rewritten question as a single line of plain text under 450 characters, with no quotes, preamble, or explanation."
)

// Config configures a Client. String, GoString, Format and LogValue render "[redacted]" so APIKey
// cannot leak into logs or formatted output.
type Config struct {
	APIKey     string
	Model      string
	BaseURL    string
	HTTPClient *http.Client
	Logger     *slog.Logger
}

// String renders "[redacted]".
func (Config) String() string {
	return redacted
}

// GoString renders "[redacted]".
func (Config) GoString() string {
	return redacted
}

// Format writes "[redacted]" for every verb.
func (Config) Format(f fmt.State, _ rune) {
	// A failed write to the formatter's destination cannot be reported from Format.
	_, _ = io.WriteString(f, redacted)
}

// LogValue renders "[redacted]" in structured logs.
func (Config) LogValue() slog.Value {
	return slog.StringValue(redacted)
}

// Client rephrases question text through the Vertex AI generateContent endpoint.
type Client struct {
	apiKey     string
	model      string
	baseURL    string
	httpClient *http.Client
	log        *slog.Logger
}

// String renders "[redacted]".
func (*Client) String() string {
	return redacted
}

// GoString renders "[redacted]".
func (*Client) GoString() string {
	return redacted
}

// Format writes "[redacted]" for every verb.
func (*Client) Format(f fmt.State, _ rune) {
	// A failed write to the formatter's destination cannot be reported from Format.
	_, _ = io.WriteString(f, redacted)
}

// LogValue renders "[redacted]" in structured logs.
func (*Client) LogValue() slog.Value {
	return slog.StringValue(redacted)
}

// New validates cfg and constructs a Client. Validation errors never include the API key or raw
// setting values.
func New(cfg Config) (*Client, error) {
	var problems []error

	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		problems = append(problems, errors.New("llm: APIKey is empty"))
	} else if strings.ContainsAny(apiKey, " \t\r\n") {
		problems = append(problems, errors.New("llm: APIKey must not contain whitespace"))
	}

	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = DefaultModel
	} else if strings.ContainsAny(model, "/\\?# \t\r\n") {
		problems = append(problems, errors.New("llm: Model contains invalid characters"))
	}

	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	} else if err := validateBaseURL(baseURL); err != nil {
		problems = append(problems, err)
	}

	if len(problems) > 0 {
		return nil, errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: DefaultTimeout}
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	client := &Client{
		apiKey:     apiKey,
		model:      model,
		baseURL:    baseURL,
		httpClient: httpClient,
		log:        logger,
	}

	return client, nil
}

func validateBaseURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return errors.New("llm: BaseURL is not a valid URL")
	}

	var problems []error

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		problems = append(problems, errors.New("llm: BaseURL scheme must be http or https"))
	}

	if parsed.Host == "" {
		problems = append(problems, errors.New("llm: BaseURL host is empty"))
	}

	if parsed.User != nil {
		problems = append(problems, errors.New("llm: BaseURL must not include userinfo"))
	}

	if parsed.RawQuery != "" || parsed.Fragment != "" {
		problems = append(problems, errors.New("llm: BaseURL must not include query or fragment"))
	}

	return errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
}

type generateContentRequest struct {
	SystemInstruction contentInstruction `json:"systemInstruction"`
	Contents          []contentMessage   `json:"contents"`
	GenerationConfig  generationConfig   `json:"generationConfig"`
}

type contentInstruction struct {
	Parts []contentPart `json:"parts"`
}

type contentMessage struct {
	Role  string        `json:"role"`
	Parts []contentPart `json:"parts"`
}

type contentPart struct {
	Text string `json:"text"`
}

type generationConfig struct {
	Temperature     float64 `json:"temperature"`
	MaxOutputTokens int     `json:"maxOutputTokens"`
}

type generateContentResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
}

// Rephrase rewrites text into neutral phrasing. Neither the input question, the rephrased text, nor
// the API key is ever logged or included in returned errors.
func (c *Client) Rephrase(ctx context.Context, text string) (string, error) {
	if c == nil {
		return "", errors.New("llm: nil client")
	}

	if ctx == nil {
		return "", errors.New("llm: nil context")
	}

	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", errors.New("llm: question text is empty")
	}

	callCtx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()

	payload := generateContentRequest{
		SystemInstruction: contentInstruction{
			Parts: []contentPart{{Text: rephraseSystemInstruction}},
		},
		Contents: []contentMessage{
			{
				Role:  "user",
				Parts: []contentPart{{Text: trimmed}},
			},
		},
		GenerationConfig: generationConfig{
			Temperature:     0.2,
			MaxOutputTokens: 256,
		},
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return "", errors.New("llm: encode request failed")
	}

	endpoint := c.baseURL + "/" + url.PathEscape(c.model) + ":generateContent"

	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", errors.New("llm: build request failed")
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.log.WarnContext(ctx, "llm rephrase request failed", slog.String("model", c.model))

		if ctxErr := callCtx.Err(); ctxErr != nil {
			return "", fmt.Errorf("llm: request canceled or timed out: %w", ctxErr)
		}

		return "", errors.New("llm: request failed")
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		c.log.WarnContext(ctx, "llm rephrase response read failed", slog.String("model", c.model))
		return "", errors.New("llm: read response failed")
	}

	if resp.StatusCode != http.StatusOK {
		c.log.WarnContext(
			ctx,
			"llm rephrase returned non-200 status",
			slog.String("model", c.model),
			slog.Int("status", resp.StatusCode),
		)

		return "", fmt.Errorf("llm: unexpected status %d", resp.StatusCode)
	}

	rephrased, err := parseGenerateContentResponse(respBytes)
	if err != nil {
		c.log.WarnContext(ctx, "llm rephrase returned invalid response", slog.String("model", c.model))
		return "", err
	}

	return rephrased, nil
}

func parseGenerateContentResponse(raw []byte) (string, error) {
	var parsed generateContentResponse

	err := json.Unmarshal(raw, &parsed)
	if err != nil {
		return "", errors.New("llm: invalid response JSON")
	}

	if len(parsed.Candidates) == 0 {
		return "", errors.New("llm: response has no candidates")
	}

	var builder strings.Builder

	for _, part := range parsed.Candidates[0].Content.Parts {
		builder.WriteString(part.Text)
	}

	rephrased := strings.TrimSpace(builder.String())
	if rephrased == "" {
		return "", errors.New("llm: response candidate text is empty")
	}

	return rephrased, nil
}
