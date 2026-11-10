//go:build eval

package capitime

import (
	"context"
	"time"
)

// ReadTextForEval asks reader whether text announces an arrival at now and returns the claim with
// the decision the service would log. It lets the local evaluation score the model read with the
// production prompt and parser, without a database or a guild configuration.
func ReadTextForEval(ctx context.Context, reader ArrivalReader, text string, now time.Time) (ArrivalClaim, string) {
	content := messageContent{text: sanitizeInputText(text)}
	claim, decided := readArrival(ctx, reader, content, now)

	return claim, string(decided)
}
