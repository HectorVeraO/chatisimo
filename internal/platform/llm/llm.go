// Package llm calls the Vertex AI generateContent REST endpoint for text generation and
// anonymous gift-exchange question rephrasing.
package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// DefaultModel is the Vertex AI model used when Config.Model is empty.
	DefaultModel = "gemini-3.5-flash-lite"
	// DefaultBaseURL is the Vertex AI publisher models base URL used when Config.BaseURL is empty.
	DefaultBaseURL = "https://aiplatform.googleapis.com/v1/publishers/google/models"
	// DefaultTimeout bounds each Generate or Rephrase request.
	DefaultTimeout = 10 * time.Second
	// DefaultTemperature is the sampling temperature used when Config.Temperature is zero.
	DefaultTemperature = 0.2
	// MaxTemperature is the highest sampling temperature accepted by Config.Temperature.
	MaxTemperature = 2.0
	// DefaultPrompt is the system instruction sent to Vertex AI when Config.Prompt is empty.
	DefaultPrompt = "Rewrite the following anonymous gift-exchange question in neutral, natural phrasing " +
		"so the author's writing style, slang, punctuation habits, typos, and capitalization cannot identify them. " +
		"The author is a gift-exchange giver asking their assigned recipient directly in the 2nd person singular " +
		"to help choose a gift: keep the question addressed directly to the recipient in the 2nd person singular " +
		"and never turn 2nd-person \"you\", \"tu\", or \"te\" questions into 3rd-person \"someone\" or \"alguien\". " +
		"When the input is in Spanish, always use neutral tuteo (tu: tienes, prefieres, quieres, eres, te gusta) " +
		"and strictly forbid Argentinian voseo (vos, tenes, preferis, queres, sos, stress-accent imperatives) and vosotros. " + //nolint:misspell // Spanish voseo verb
		"Drop vocative nicknames, pet names, and casual friend-address slang (rey, king, kingcito, kin, bro, amigo, " +
		"hmno, wn, weon, compa, crack, etc.) completely instead of translating or keeping them as subjects or nouns. " +
		"Preserve the language of the input question and its exact gift-relevant meaning and constraints. " +
		"Output ONLY the rewritten question as a single line of plain text under 450 characters, with no quotes, preamble, or explanation."

	// MaxImages is the largest number of images GenerateWithMedia accepts in one request.
	MaxImages = 2
	// MaxImageBytes is the largest size in bytes of one image accepted by GenerateWithMedia.
	MaxImageBytes = 4 << 20

	// MaxAudioBytes is the largest size in bytes of the audio accepted by GenerateWithMedia.
	MaxAudioBytes = 2 << 20
	// MaxAudioSeconds is the longest audio, in seconds, that callers should send to GenerateWithMedia.
	// The client cannot measure duration, so callers enforce it from the announced length.
	MaxAudioSeconds = 30

	jsonMIMEType     = "application/json"
	maxPromptBytes   = 16 << 10
	maxResponseBytes = 64 * 1024
	redacted         = "[redacted]"
)

// Config configures a Client. String, GoString, Format and LogValue render "[redacted]" so APIKey
// cannot leak into logs or formatted output.
type Config struct {
	APIKey      string
	Model       string
	BaseURL     string
	Prompt      string
	Temperature float64
	HTTPClient  *http.Client
	Logger      *slog.Logger
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

// Client calls the Vertex AI generateContent endpoint.
type Client struct {
	apiKey      string
	model       string
	baseURL     string
	prompt      string
	temperature float64
	httpClient  *http.Client
	log         *slog.Logger
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

	prompt, promptErr := resolvePrompt(cfg.Prompt, DefaultPrompt)
	if promptErr != nil {
		problems = append(problems, promptErr)
	}

	temperature := cfg.Temperature
	isInvalidTemperature := math.IsNaN(temperature) || temperature < 0 || temperature > MaxTemperature
	if isInvalidTemperature {
		problems = append(problems, errors.New("llm: Temperature is out of range"))
	} else if temperature == 0 {
		temperature = DefaultTemperature
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
		apiKey:      apiKey,
		model:       model,
		baseURL:     baseURL,
		prompt:      prompt,
		temperature: temperature,
		httpClient:  httpClient,
		log:         logger,
	}

	return client, nil
}

func resolvePrompt(raw, fallback string) (string, error) {
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")

	var problems []error

	if !utf8.ValidString(normalized) {
		problems = append(problems, errors.New("llm: Prompt is not valid UTF-8"))
	}

	if hasDisallowedPromptControl(normalized) {
		problems = append(problems, errors.New("llm: Prompt contains invalid control characters"))
	}

	trimmed := strings.TrimSpace(normalized)
	if trimmed == "" {
		if len(problems) > 0 {
			return "", errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
		}

		return fallback, nil
	}

	if len(trimmed) > maxPromptBytes {
		problems = append(problems, fmt.Errorf("llm: Prompt exceeds %d bytes", maxPromptBytes))
	}

	if len(problems) > 0 {
		return "", errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
	}

	return trimmed, nil
}

func hasDisallowedPromptControl(s string) bool {
	for _, r := range s {
		if r != '\n' && unicode.IsControl(r) {
			return true
		}
	}

	return false
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
	Text       string      `json:"text,omitempty"`
	InlineData *inlineData `json:"inline_data,omitempty"`
}

type inlineData struct {
	MIMEType string `json:"mime_type"`
	Data     string `json:"data"`
}

type generationConfig struct {
	Temperature      float64 `json:"temperature"`
	MaxOutputTokens  int     `json:"maxOutputTokens"`
	ResponseMIMEType string  `json:"responseMimeType,omitempty"`
}

type generateContentResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		ThoughtsTokenCount   int `json:"thoughtsTokenCount"`
	} `json:"usageMetadata"`
}

// Usage is the token count the model reported for one request. It is zero when the response
// carries no usage metadata.
type Usage struct {
	// PromptTokens counts the tokens of the request: system instruction, text and media.
	PromptTokens int
	// OutputTokens counts the generated tokens, including the model's thinking tokens.
	OutputTokens int
}

// Rephrase rewrites text into neutral phrasing. Neither the input question, the rephrased text, nor
// the API key is ever logged or included in returned errors.
func (c *Client) Rephrase(ctx context.Context, text string) (string, error) {
	rephrased, err := c.Generate(ctx, text)
	return rephrased, err
}

// Generate sends text as user content to the configured Vertex AI model with the client's system
// prompt and sampling temperature. Neither the input text, the generated text, nor the API key is
// ever logged or included in returned errors.
func (c *Client) Generate(ctx context.Context, text string) (string, error) {
	if c == nil {
		return "", errors.New("llm: nil client")
	}

	return c.generate(ctx, c.prompt, text)
}

// GenerateWithPrompt sends text as user content to the configured Vertex AI model using prompt as
// the system instruction (or the client's configured prompt when prompt is blank). Validation
// errors never include prompt, text, or the API key.
func (c *Client) GenerateWithPrompt(ctx context.Context, prompt, text string) (string, error) {
	if c == nil {
		return "", errors.New("llm: nil client")
	}

	var problems []error
	if ctx == nil {
		problems = append(problems, errors.New("llm: nil context"))
	}

	resolvedPrompt, promptErr := resolvePrompt(prompt, c.prompt)
	if promptErr != nil {
		problems = append(problems, promptErr)
	}

	trimmedText := strings.TrimSpace(text)
	if trimmedText == "" {
		problems = append(problems, errors.New("llm: question text is empty"))
	}

	if len(problems) > 0 {
		return "", errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
	}

	return c.generate(ctx, resolvedPrompt, trimmedText)
}

// Image is one inline image sent to the model. MIMEType must be image/png, image/jpeg, image/gif or
// image/webp.
type Image struct {
	MIMEType string
	Data     []byte
}

// MediaRequest is one request that carries text, images and audio together.
type MediaRequest struct {
	// Prompt is the system instruction; when blank the client's configured prompt is used.
	Prompt string
	// Text is the user text; it is sent after the media and left out when blank.
	Text string
	// Images holds at most MaxImages images of at most MaxImageBytes each.
	Images []Image
	// Audio is an optional clip of at most MaxAudioBytes.
	Audio *Audio
	// JSON asks the model to answer with a JSON document instead of plain text.
	JSON bool
}

// GenerateWithMedia sends the images, then the audio, then the text of req as one user message to
// the client's configured model. At least one of text, images or audio must be present. Validation
// errors never include the prompt, the text, media data or the API key.
func (c *Client) GenerateWithMedia(ctx context.Context, req MediaRequest) (string, error) {
	text, _, err := c.GenerateWithMediaUsage(ctx, req)
	return text, err
}

// GenerateWithMediaUsage is GenerateWithMedia that also returns the token usage the model reported.
func (c *Client) GenerateWithMediaUsage(ctx context.Context, req MediaRequest) (string, Usage, error) {
	if c == nil {
		return "", Usage{}, errors.New("llm: nil client")
	}

	var problems []error
	if ctx == nil {
		problems = append(problems, errors.New("llm: nil context"))
	}

	resolvedPrompt, promptErr := resolvePrompt(req.Prompt, c.prompt)
	if promptErr != nil {
		problems = append(problems, promptErr)
	}

	problems = append(problems, validateImages(req.Images)...)

	userParts := make([]contentPart, 0, len(req.Images)+2)
	for _, image := range req.Images {
		userParts = append(userParts, contentPart{InlineData: &inlineData{
			MIMEType: image.MIMEType,
			Data:     base64.StdEncoding.EncodeToString(image.Data),
		}})
	}

	if req.Audio != nil {
		mime, audioProblems := validateAudio(*req.Audio)
		problems = append(problems, audioProblems...)
		userParts = append(userParts, contentPart{InlineData: &inlineData{
			MIMEType: mime,
			Data:     base64.StdEncoding.EncodeToString(req.Audio.Data),
		}})
	}

	trimmedText := strings.TrimSpace(req.Text)
	if trimmedText != "" {
		userParts = append(userParts, contentPart{Text: trimmedText})
	}

	if len(userParts) == 0 {
		problems = append(problems, errors.New("llm: request has no text, image or audio"))
	}

	if len(problems) > 0 {
		return "", Usage{}, errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
	}

	return c.send(ctx, resolvedPrompt, userParts, req.JSON)
}

func validateImages(images []Image) []error {
	var problems []error

	if len(images) > MaxImages {
		problems = append(problems, fmt.Errorf("llm: image count must be at most %d", MaxImages))
	}

	for i, image := range images {
		switch image.MIMEType {
		case "image/png", "image/jpeg", "image/gif", "image/webp":
		default:
			problems = append(problems, fmt.Errorf("llm: image %d has an unsupported MIME type", i))
		}

		if len(image.Data) == 0 {
			problems = append(problems, fmt.Errorf("llm: image %d is empty", i))
		} else if len(image.Data) > MaxImageBytes {
			problems = append(problems, fmt.Errorf("llm: image %d exceeds %d bytes", i, MaxImageBytes))
		}
	}

	return problems
}

// Audio is one inline audio clip sent to the model. MIMEType must be one of the types
// NormalizeAudioMIME accepts; parameters such as "; codecs=opus" are dropped before sending.
type Audio struct {
	MIMEType string
	Data     []byte
}

// NormalizeAudioMIME maps an announced content type to one of the audio types the model accepts:
// it drops parameters, trims and lowercases. It reports false for any other type.
func NormalizeAudioMIME(raw string) (string, bool) {
	base, _, _ := strings.Cut(raw, ";")
	mime := strings.ToLower(strings.TrimSpace(base))

	switch mime {
	case "audio/ogg", "audio/opus", "audio/mpeg", "audio/mp3", "audio/wav",
		"audio/aac", "audio/flac", "audio/m4a", "audio/webm":
		return mime, true
	default:
		return "", false
	}
}

// validateAudio returns the normalized MIME type of audio and every problem that makes it
// unsendable. The problems never include the audio data.
func validateAudio(audio Audio) (string, []error) {
	var problems []error

	mime, isSupportedType := NormalizeAudioMIME(audio.MIMEType)
	if !isSupportedType {
		problems = append(problems, errors.New("llm: audio has an unsupported MIME type"))
	}

	if len(audio.Data) == 0 {
		problems = append(problems, errors.New("llm: audio is empty"))
	} else if len(audio.Data) > MaxAudioBytes {
		problems = append(problems, fmt.Errorf("llm: audio exceeds %d bytes", MaxAudioBytes))
	}

	return mime, problems
}

func (c *Client) generate(ctx context.Context, systemPrompt, text string) (string, error) {
	if ctx == nil {
		return "", errors.New("llm: nil context")
	}

	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", errors.New("llm: question text is empty")
	}

	generated, _, err := c.send(ctx, systemPrompt, []contentPart{{Text: trimmed}}, false)

	return generated, err
}

// send posts the user parts to the configured model's generateContent endpoint and returns the
// generated text with the reported token usage. It is the single HTTP path for every request.
func (c *Client) send(
	ctx context.Context, systemPrompt string, userParts []contentPart, wantsJSON bool,
) (string, Usage, error) {
	callCtx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()

	payload := generateContentRequest{
		SystemInstruction: contentInstruction{
			Parts: []contentPart{{Text: systemPrompt}},
		},
		Contents: []contentMessage{
			{
				Role:  "user",
				Parts: userParts,
			},
		},
		GenerationConfig: generationConfig{
			Temperature:     c.temperature,
			MaxOutputTokens: 256,
		},
	}
	if wantsJSON {
		payload.GenerationConfig.ResponseMIMEType = jsonMIMEType
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return "", Usage{}, errors.New("llm: encode request failed")
	}

	endpoint := c.baseURL + "/" + url.PathEscape(c.model) + ":generateContent"

	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", Usage{}, errors.New("llm: build request failed")
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.log.WarnContext(ctx, "llm request failed", slog.String("model", c.model))

		if ctxErr := callCtx.Err(); ctxErr != nil {
			return "", Usage{}, fmt.Errorf("llm: request canceled or timed out: %w", ctxErr)
		}

		return "", Usage{}, errors.New("llm: request failed")
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		c.log.WarnContext(ctx, "llm request response read failed", slog.String("model", c.model))
		return "", Usage{}, errors.New("llm: read response failed")
	}

	if resp.StatusCode != http.StatusOK {
		c.log.WarnContext(
			ctx,
			"llm request returned non-200 status",
			slog.String("model", c.model),
			slog.Int("status", resp.StatusCode),
		)

		return "", Usage{}, fmt.Errorf("llm: unexpected status %d", resp.StatusCode)
	}

	generated, usage, err := parseGenerateContentResponse(respBytes)
	if err != nil {
		c.log.WarnContext(ctx, "llm request returned invalid response", slog.String("model", c.model))
		return "", Usage{}, err
	}

	return generated, usage, nil
}

func parseGenerateContentResponse(raw []byte) (string, Usage, error) {
	var parsed generateContentResponse

	err := json.Unmarshal(raw, &parsed)
	if err != nil {
		return "", Usage{}, errors.New("llm: invalid response JSON")
	}

	if len(parsed.Candidates) == 0 {
		return "", Usage{}, errors.New("llm: response has no candidates")
	}

	var builder strings.Builder

	for _, part := range parsed.Candidates[0].Content.Parts {
		builder.WriteString(part.Text)
	}

	generated := strings.TrimSpace(builder.String())
	if generated == "" {
		return "", Usage{}, errors.New("llm: response candidate text is empty")
	}

	usage := Usage{
		PromptTokens: parsed.UsageMetadata.PromptTokenCount,
		OutputTokens: parsed.UsageMetadata.CandidatesTokenCount + parsed.UsageMetadata.ThoughtsTokenCount,
	}

	return generated, usage, nil
}
