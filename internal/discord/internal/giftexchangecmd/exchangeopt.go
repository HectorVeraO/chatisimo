package giftexchangecmd

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"

	"github.com/HectorVeraO/chatisimo/internal/discord/internal/render"
	"github.com/HectorVeraO/chatisimo/internal/discord/internal/router"
	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

const (
	// maxChoices is Discord's limit on autocomplete choices.
	maxChoices = 25
	// maxChoiceRunes is Discord's limit on the name of a choice.
	maxChoiceRunes = 100

	choiceSuffixSeparator = " - "
)

// exchangeScope tells which exchanges a verb asks the module for.
type exchangeScope int

const (
	// scopeGuild asks for the exchanges of the guild the command came from.
	scopeGuild exchangeScope = iota
	// scopeCaller asks for every exchange the caller is in, whichever the guild, and in a guild
	// keeps those of that guild.
	scopeCaller
)

// exchangeFilter is the row of the exchange option table of one or more verbs.
type exchangeFilter struct {
	verbs       []string
	scope       exchangeScope
	multiState  bool
	preferState giftexchange.State
	// keep reports whether a summary is a candidate for the verb.
	keep func(s giftexchange.ExchangeSummary) bool
}

func isOneOf(state giftexchange.State, states ...giftexchange.State) bool {
	return slices.Contains(states, state)
}

// exchangeFilters is the table of the exchange option: for each verb, where its candidates
// come from and which summaries stay.
func exchangeFilters() []exchangeFilter {
	const (
		open     = giftexchange.StateOpen
		drawn    = giftexchange.StateDrawn
		revealed = giftexchange.StateRevealed
	)

	filters := []exchangeFilter{
		{
			verbs: []string{"join"}, scope: scopeGuild,
			keep: func(s giftexchange.ExchangeSummary) bool {
				return s.State == open && !s.CallerIsParticipant
			},
		},
		{
			verbs: []string{"leave"}, scope: scopeGuild, multiState: true,
			keep: func(s giftexchange.ExchangeSummary) bool {
				return s.CallerIsParticipant && isOneOf(s.State, open, drawn)
			},
		},
		{
			verbs: []string{"rules"}, scope: scopeGuild, multiState: true,
			keep: func(giftexchange.ExchangeSummary) bool { return true },
		},
		{
			verbs: []string{"remove", "rename", "cancel"}, scope: scopeGuild, multiState: true,
			keep: func(s giftexchange.ExchangeSummary) bool {
				return s.CallerIsOrganizer && isOneOf(s.State, open, drawn)
			},
		},
		{
			verbs: []string{"exclude", "unexclude", "draw"}, scope: scopeGuild,
			keep: func(s giftexchange.ExchangeSummary) bool {
				return s.CallerIsOrganizer && s.State == open
			},
		},
		{
			verbs: []string{"reveal"}, scope: scopeGuild,
			keep: func(s giftexchange.ExchangeSummary) bool {
				return s.CallerIsOrganizer && s.State == drawn
			},
		},
		{
			verbs: []string{"recipient"}, scope: scopeCaller, multiState: true, preferState: drawn,
			keep: func(s giftexchange.ExchangeSummary) bool {
				return s.CallerIsParticipant && isOneOf(s.State, drawn, revealed)
			},
		},
		{
			verbs: []string{"giver"}, scope: scopeCaller,
			keep: func(s giftexchange.ExchangeSummary) bool {
				return s.CallerIsParticipant && s.State == revealed
			},
		},
		{
			verbs: []string{"ask", "questions"}, scope: scopeCaller,
			keep: func(s giftexchange.ExchangeSummary) bool {
				return s.CallerIsParticipant && s.State == drawn
			},
		},
		{
			verbs: []string{"preferences"}, scope: scopeCaller, multiState: true,
			keep: func(s giftexchange.ExchangeSummary) bool {
				return s.CallerIsParticipant && isOneOf(s.State, open, drawn)
			},
		},
	}

	return filters
}

// filterFor returns the row of the verb. It panics on a verb that has no exchange option,
// which is a programming error.
func filterFor(verb string) exchangeFilter {
	for _, filter := range exchangeFilters() {
		if slices.Contains(filter.verbs, verb) {
			return filter
		}
	}

	panic("giftexchangecmd: verb without an exchange option " + verb)
}

// candidates returns the exchanges the filter keeps for the caller of req, sorted by priority.
// It makes one module call.
func (h *Handlers) candidates(
	ctx context.Context, req *router.Request, filter exchangeFilter,
) ([]giftexchange.ExchangeSummary, error) {
	kept, _, err := h.listCandidates(ctx, req, filter)
	return kept, err
}

func (h *Handlers) listCandidates(
	ctx context.Context, req *router.Request, filter exchangeFilter,
) ([]giftexchange.ExchangeSummary, []giftexchange.ExchangeSummary, error) {
	var group *giftexchange.GroupID
	if filter.scope == scopeGuild {
		if req.Group == nil {
			return nil, nil, errGuildOnly
		}
		group = req.Group
	}

	listed, err := h.svc.ExchangesFor(ctx, req.Actor, group)
	if err != nil {
		return nil, nil, fmt.Errorf("list exchanges: %w", err)
	}

	kept := make([]giftexchange.ExchangeSummary, 0, len(listed))
	for _, summary := range listed {
		inOtherGuild := filter.scope == scopeCaller && req.Group != nil && summary.Group.ID != *req.Group
		if inOtherGuild || !filter.keep(summary) {
			continue
		}
		kept = append(kept, summary)
	}

	sortByPriority(kept)
	return kept, listed, nil
}

func sortByPriority(summaries []giftexchange.ExchangeSummary) {
	slices.SortStableFunc(summaries, func(a, b giftexchange.ExchangeSummary) int {
		return cmp.Compare(stateTier(a.State), stateTier(b.State))
	})
}

func stateTier(state giftexchange.State) int {
	switch state {
	case giftexchange.StateOpen, giftexchange.StateDrawn:
		return 0
	case giftexchange.StateRevealed:
		return 1
	default:
		return 2
	}
}

// resolveExchange picks the exchange a person meant. An empty raw picks the only candidate,
// or the only candidate in preferState when several exist; with none it returns
// errNoCandidates, with several a *chooseExchangeError. A raw that is an exchange ID is
// returned as is, even when it is not a candidate: the module answers for it. Any other raw
// must match the name of exactly one candidate, ignoring case.
func resolveExchange(
	raw string, candidates []giftexchange.ExchangeSummary, preferState giftexchange.State,
) (giftexchange.ExchangeID, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return resolveEmpty(candidates, preferState)
	}

	id, err := giftexchange.ParseExchangeID(trimmed)
	if err == nil {
		return id, nil
	}

	var match giftexchange.ExchangeID
	matches := 0
	for _, candidate := range candidates {
		if strings.EqualFold(candidate.Name, trimmed) {
			match = candidate.ID
			matches++
		}
	}
	if matches != 1 {
		return giftexchange.ExchangeID{}, errNoMatch
	}

	return match, nil
}

func resolveEmpty(
	candidates []giftexchange.ExchangeSummary, preferState giftexchange.State,
) (giftexchange.ExchangeID, error) {
	switch len(candidates) {
	case 0:
		return giftexchange.ExchangeID{}, errNoCandidates
	case 1:
		return candidates[0].ID, nil
	default:
		if preferState != "" {
			var preferred giftexchange.ExchangeID
			matches := 0
			for _, candidate := range candidates {
				if candidate.State == preferState {
					preferred = candidate.ID
					matches++
				}
			}
			if matches == 1 {
				return preferred, nil
			}
		}

		names := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			names = append(names, candidate.Name)
		}

		return giftexchange.ExchangeID{}, &chooseExchangeError{Names: names}
	}
}

// exchangeFor resolves the exchange option of a verb for the caller of req and records the
// exchange on req for the log. raw is the typed or chosen text, empty when the option was left
// out.
func (h *Handlers) exchangeFor(
	ctx context.Context, req *router.Request, verb, raw string,
) (giftexchange.ExchangeID, error) {
	filter := filterFor(verb)
	candidates, listed, err := h.listCandidates(ctx, req, filter)
	if err != nil {
		return giftexchange.ExchangeID{}, err
	}

	id, err := resolveExchange(raw, candidates, filter.preferState)
	if err != nil {
		return giftexchange.ExchangeID{}, err
	}

	req.SetExchange(id)
	h.setExchangeLocale(ctx, req, id, listed)
	return id, nil
}

func (h *Handlers) setExchangeLocale(
	ctx context.Context,
	req *router.Request,
	id giftexchange.ExchangeID,
	listed []giftexchange.ExchangeSummary,
) {
	for _, summary := range listed {
		if summary.ID == id {
			h.setGroupLocale(req, summary.Group.Subject)
			return
		}
	}
	if req.Group != nil {
		view, err := h.svc.Exchange(ctx, req.Actor, *req.Group, id)
		if err == nil {
			h.setGroupLocale(req, view.Group.Subject)
		}
	}
}

// autocompleteFor returns the autocomplete handler of the exchange option of a verb. It
// answers within the one callback Discord allows: at most 25 candidates whose name contains
// the typed text, ignoring case.
func (h *Handlers) autocompleteFor(verb string) handler.AutocompleteHandler {
	filter := filterFor(verb)

	return func(e *handler.AutocompleteEvent) error {
		req, found := router.FromContext(e.Ctx)
		if !found {
			panic("giftexchangecmd: autocomplete ran without a request")
		}

		typed := e.Data.String(e.Data.Focused().Name)
		candidates, err := h.candidates(e.Ctx, req, filter)
		if err != nil {
			return err
		}

		choices := h.choicesFor(req, candidates, typed, filter.multiState)
		replyErr := req.Autocomplete(e.Ctx, choices)
		if replyErr != nil {
			return replyErr //nolint:wrapcheck // the router reduces REST errors to status and code
		}

		return nil
	}
}

// choicesFor builds the choices for the candidates whose name contains typed. The name is
// plain text, because choices render no markdown, includes the localized state when showState
// is true, and carries the server name in a DM.
func (h *Handlers) choicesFor(
	req *router.Request, candidates []giftexchange.ExchangeSummary, typed string, showState bool,
) []discord.AutocompleteChoice {
	c := h.catalogFor(req)
	loc := c.Locale()
	needle := strings.ToLower(strings.TrimSpace(typed))
	choices := make([]discord.AutocompleteChoice, 0, maxChoices)
	for _, candidate := range candidates {
		if len(choices) == maxChoices {
			break
		}
		if !strings.Contains(strings.ToLower(candidate.Name), needle) {
			continue
		}

		name := candidate.Name
		if showState {
			name += " (" + stateText(c, candidate.State) + ")"
		}
		if !req.InGuild() {
			name += choiceSuffixSeparator + GuildNameOrUnknownIn(loc, h.guildName(candidate.Group.Subject))
		}

		choices = append(choices, discord.AutocompleteChoiceString{
			Name:  render.Truncate(name, maxChoiceRunes, truncationMark),
			Value: candidate.ID.String(),
		})
	}

	return choices
}
