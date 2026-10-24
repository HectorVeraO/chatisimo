package pgtest

import (
	"time"

	"github.com/HectorVeraO/chatisimo/internal/platform/db"
)

// TemplateNameFor exposes the template name a set of sources maps to.
func TemplateNameFor(sources []db.Source) (string, error) {
	return templateNameFor(sources)
}

// StampTemplatesWith makes templates built from now on look as if they were built at the given
// time, and returns the function that restores the real clock.
func StampTemplatesWith(builtAt time.Time) (restore func()) {
	previous := templateBuiltAt
	templateBuiltAt = func() time.Time { return builtAt }

	return func() { templateBuiltAt = previous }
}
