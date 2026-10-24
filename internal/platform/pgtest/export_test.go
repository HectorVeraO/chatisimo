package pgtest

import (
	"context"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/platform/db"
)

// TemplateNameFor exposes the template name a set of sources maps to.
func TemplateNameFor(sources []db.Source) (string, error) {
	return templateNameFor(sources)
}

// NewAt is New with a fixed clock: the clone name, the template stamp and the sweep age use now.
func NewAt(ctx context.Context, opts Options, now time.Time) (*Database, error) {
	return newAt(ctx, opts, now)
}
