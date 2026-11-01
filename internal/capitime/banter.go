package capitime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/HectorVeraO/chatisimo/internal/capitime/internal/templates"
)

const (
	banterTimeout       = 3 * time.Second
	maxBanterRunes      = 180
	maxDiscordLineBytes = 400
)

var (
	errBanterUnavailable = errors.New("capitime: banter generator unavailable")
	errBanterInvalid     = errors.New("capitime: invalid banter output")
)

var voseoTokens = []string{
	"tenés", "tenes", "podés", "podes", "sos", "llegás", "llegas vos", "venís", "venis",
}

// LLMBanterConfig configures an LLMBanterGenerator.
type LLMBanterConfig struct {
	Generator LLMGenerator
}

// LLMBanterGenerator generates short Chilean Spanish banter lines via LLMGenerator with a 3-second
// timeout.
type LLMBanterGenerator struct {
	gen LLMGenerator
}

// NewLLMBanterGenerator constructs an LLMBanterGenerator.
func NewLLMBanterGenerator(cfg LLMBanterConfig) *LLMBanterGenerator {
	return &LLMBanterGenerator{gen: cfg.Generator}
}

// GenerateBanter requests a single-line banter sentence from the underlying LLM and sanitizes the
// result.
func (g *LLMBanterGenerator) GenerateBanter(ctx context.Context, input BanterInput) (string, error) {
	if ctx == nil {
		return "", errors.New("capitime: context is nil")
	}
	if g == nil || g.gen == nil {
		return "", errBanterUnavailable
	}

	systemPrompt := strings.TrimSpace(input.Prompt)
	if systemPrompt == "" {
		if input.EasterEgg {
			systemPrompt = DefaultEasterEggPrompt
		} else {
			systemPrompt = DefaultBanterPrompt
		}
	}

	userMsg := buildLLMUserContext(input)
	callCtx, cancel := context.WithTimeout(ctx, banterTimeout)
	defer cancel()

	raw, err := g.gen.GenerateWithPrompt(callCtx, systemPrompt, userMsg)
	if err != nil {
		return "", errBanterUnavailable
	}

	cleaned, ok := sanitizeBanterOutput(raw)
	if !ok {
		return "", errBanterInvalid
	}

	return cleaned, nil
}

func buildLLMUserContext(input BanterInput) string {
	mode := "standard_delay"
	if input.EasterEgg {
		mode = "easter_egg_on_time"
	}

	return fmt.Sprintf(
		"locale=%s mode=%s claim=%s claimed_clock=%s capi_clock=%s delay_minutes=%d",
		input.Locale,
		mode,
		input.ClaimSummary,
		input.ClaimedClock,
		input.CapiClock,
		input.DelayMinutes,
	)
}

func sanitizeBanterOutput(raw string) (string, bool) {
	if !utf8.ValidString(raw) {
		return "", false
	}

	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range raw {
		if r == '\n' || r == '\r' || r == '\t' {
			b.WriteByte(' ')
			continue
		}
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}

	collapsed := strings.Join(strings.Fields(b.String()), " ")
	collapsed = strings.Trim(collapsed, `"'`)
	collapsed = strings.ReplaceAll(collapsed, "@everyone", "everyone")
	collapsed = strings.ReplaceAll(collapsed, "@here", "here")
	collapsed = strings.ReplaceAll(collapsed, "<@", "< @")

	if collapsed == "" || utf8.RuneCountInString(collapsed) > maxBanterRunes {
		return "", false
	}

	lower := strings.ToLower(collapsed)
	for _, forbidden := range voseoTokens {
		if strings.Contains(lower, forbidden) {
			return "", false
		}
	}

	return collapsed, true
}

func renderFallbackBanter(loc Locale, input BanterInput, variant int) string {
	params := templates.Params{
		ClaimSummary: input.ClaimSummary,
		ClaimedClock: input.ClaimedClock,
		CapiClock:    input.CapiClock,
		DelayMinutes: input.DelayMinutes,
		Immediate:    input.Immediate,
		EasterEgg:    input.EasterEgg,
		Variant:      variant,
	}
	if loc == LocaleENUS {
		return templates.RenderENUS(params)
	}

	return templates.RenderESCL(params)
}

func formatReplyLine(loc Locale, pred Prediction, banter string) string {
	unix := pred.PredictedAt.Unix()
	timeTag := fmt.Sprintf("<t:%d:t>", unix)
	relTag := fmt.Sprintf("<t:%d:R>", unix)

	header := "\u23f0 **Capi Time:**"
	if pred.EasterEgg {
		if loc == LocaleENUS {
			header = "\U0001f6a8 **Capi Time Miracle:**"
		} else {
			header = "\U0001f6a8 **Milagro Capi Time:**"
		}
	}

	singleLineBanter := strings.Join(strings.Fields(banter), " ")
	line := fmt.Sprintf("%s %s (%s) \u2014 %s", header, timeTag, relTag, singleLineBanter)
	if len(line) > maxDiscordLineBytes {
		line = line[:maxDiscordLineBytes]
		for !utf8.ValidString(line) && len(line) > 0 {
			line = line[:len(line)-1]
		}
	}

	return line
}
