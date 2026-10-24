package pgtest

import "github.com/HectorVeraO/chatisimo/internal/platform/db"

// TemplateNameFor exposes the template name a set of sources maps to.
func TemplateNameFor(sources []db.Source) (string, error) {
	return templateNameFor(sources)
}
