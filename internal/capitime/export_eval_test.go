//go:build eval

package capitime

import (
	"context"
	"time"
)

// ReadTextForEval asks reader whether text announces an arrival at now, read after the earlier texts
// of the same member (oldest first), and returns the claim with the decision the service would log.
// It lets the local evaluation score the model read with the production prompt and parser, without a
// database or a guild configuration.
func ReadTextForEval(
	ctx context.Context,
	reader ArrivalReader,
	earlier []string,
	text string,
	now time.Time,
) (ArrivalClaim, string) {
	sanitized := make([]string, 0, len(earlier))
	for _, raw := range earlier {
		if clean := sanitizeInputText(raw); clean != "" {
			sanitized = append(sanitized, clean)
		}
	}

	content := messageContent{text: sanitizeInputText(text)}
	claim, decided := readArrival(ctx, reader, content, sanitized, now)

	return claim, string(decided)
}
