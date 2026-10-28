package giftexchangecmd

import (
	"errors"
	"log/slog"
	"time"

	"github.com/disgoorg/disgo/handler"

	"github.com/HectorVeraO/chatisimo/internal/discord/internal/router"
)

// Handlers is the handler set of the giftexchange feature.
type Handlers struct {
	svc Service
	// guildName returns the name of the guild with the given snowflake, or the text for an
	// unknown server.
	guildName func(subject string) string
	now       func() time.Time
	loc       *time.Location
	logger    *slog.Logger
}

// New validates its arguments, reporting every problem together, and returns the handlers.
// loc is the time zone people type times in.
func New(
	svc Service,
	guildName func(subject string) string,
	now func() time.Time,
	loc *time.Location,
	logger *slog.Logger,
) (*Handlers, error) {
	var problems []error
	if svc == nil {
		problems = append(problems, errors.New("giftexchangecmd: service is nil"))
	}
	if guildName == nil {
		problems = append(problems, errors.New("giftexchangecmd: guild name lookup is nil"))
	}
	if now == nil {
		problems = append(problems, errors.New("giftexchangecmd: clock is nil"))
	}
	if loc == nil {
		problems = append(problems, errors.New("giftexchangecmd: location is nil"))
	}
	if logger == nil {
		problems = append(problems, errors.New("giftexchangecmd: logger is nil"))
	}
	if len(problems) > 0 {
		return nil, errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
	}

	h := &Handlers{svc: svc, guildName: guildName, now: now, loc: loc, logger: logger}
	return h, nil
}

// Register adds every handler of the feature to r. Each group has its own hook, which the
// track that builds the group fills.
func (h *Handlers) Register(r *router.Router) {
	h.registerHelp(r)
	h.registerList(r)
	h.registerMembership(r)
	h.registerRules(r)
	h.registerDraw(r)
	h.registerQuestions(r)
	h.registerPreferences(r)
}

// verb registers the named verb of the command trees on every command path. It panics on a
// name that is not in the trees, which is a programming error.
func (h *Handlers) verb(
	r *router.Router,
	name string,
	command handler.SlashCommandHandler,
	autocomplete handler.AutocompleteHandler,
) {
	for _, spec := range verbs() {
		if spec.name != name {
			continue
		}

		v := router.Verb{Roots: commandRoots, Name: name, Command: command, Autocomplete: autocomplete}
		if spec.guildOnly {
			v.NotInDM = errGuildOnly
		}
		r.RegisterVerb(v)
		return
	}

	panic("giftexchangecmd: unknown verb " + name)
}

func (h *Handlers) catalogFor(req *router.Request) Catalog {
	return CatalogFor(LocaleFromDiscord(req.EffectiveLocale()))
}
