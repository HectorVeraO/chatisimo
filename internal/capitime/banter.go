package capitime

import (
	"context"
	"errors"
	"fmt"
	"regexp"
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

// voseoRe matches the Argentine voseo forms and the family nickname a line must not carry. Each one
// counts only as a whole word, so "esos", "pasos" or "bendito" do not match.
var voseoRe = regexp.MustCompile(
	`(^|[^\p{L}])(tenés|tenes|podés|podes|sos|llegás|llegas vos|venís|venis|bendi)($|[^\p{L}])`,
)

// mentionBreaker disarms the text that pings: mass mentions and the opening of a user or role
// mention.
var mentionBreaker = strings.NewReplacer("@everyone", "everyone", "@here", "here", "<@", "< @")

// isFeatureNameSeparator reports whether r can sit between or inside the two words of the feature
// name without changing how it reads: white space, a hyphen, an underscore, the asterisk of
// Markdown emphasis, or an invisible format character such as a zero-width space.
func isFeatureNameSeparator(r rune) bool {
	switch r {
	case '-', '_', '*':
		return true
	default:
		return unicode.IsSpace(r) || unicode.Is(unicode.Cf, r)
	}
}

// namesFeature reports whether line carries the feature name in any spelling, such as "Capi Time",
// "capitime", "capi-time", "**Capi** Time" or the two words split by an invisible character. The
// nickname alone does not count.
func namesFeature(line string) bool {
	var squeezed strings.Builder
	for _, r := range strings.ToLower(line) {
		if !isFeatureNameSeparator(r) {
			squeezed.WriteRune(r)
		}
	}

	return strings.Contains(squeezed.String(), "capitime")
}

// cutSaid returns the sanitized message text cut to MaxSaidRunes runes, never inside a rune.
func cutSaid(text string) string {
	runes := 0
	for index := range text {
		if runes == MaxSaidRunes {
			return strings.TrimSpace(text[:index])
		}
		runes++
	}

	return text
}

// stripMentions rewrites line so that nothing in it pings: no "@everyone", no "@here" and no
// "<@" opening a user or role mention. It repeats until the text is stable, because removing one
// "@" can join the pieces of another mention.
func stripMentions(line string) string {
	for {
		stripped := mentionBreaker.Replace(line)
		if stripped == line {
			return stripped
		}
		line = stripped
	}
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

// buildLLMUserContext builds the one-line user message of the banter request. The member's text
// goes last and quoted, so it cannot break the line or pass for another field.
func buildLLMUserContext(input BanterInput) string {
	mode := "standard_delay"
	if input.EasterEgg {
		mode = "easter_egg_on_time"
	}

	return fmt.Sprintf(
		"locale=%s mode=%s claim=%s claimed_clock=%s capi_clock=%s delay_minutes=%d said=%q",
		input.Locale,
		mode,
		input.ClaimSummary,
		input.ClaimedClock,
		input.CapiClock,
		input.DelayMinutes,
		input.Said,
	)
}

// sanitizeBanterOutput turns a generated line into one clean line that pings nobody. It reports
// false for a line that is empty, too long, written with voseo or that carries the feature name,
// which belongs to the reply header only.
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
	collapsed = stripMentions(collapsed)

	if collapsed == "" || utf8.RuneCountInString(collapsed) > maxBanterRunes {
		return "", false
	}
	if namesFeature(collapsed) {
		return "", false
	}

	if voseoRe.MatchString(strings.ToLower(collapsed)) {
		return "", false
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
