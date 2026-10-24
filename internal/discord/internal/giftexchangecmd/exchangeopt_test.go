package giftexchangecmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
	"uuid"

	"github.com/disgoorg/disgo/discord"

	"github.com/HectorVeraO/chatisimo/internal/discord/internal/router"
	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

// stubService satisfies Service for tests of code that never calls the module.
type stubService struct {
	Service
}

func summaryNamed(name string) giftexchange.ExchangeSummary {
	s := giftexchange.ExchangeSummary{ID: giftexchange.ExchangeID(uuid.NewV7()), Name: name}
	return s
}

func TestResolveExchangeAutoFillsTheOnlyCandidate(t *testing.T) {
	only := summaryNamed("Winter")

	got, err := resolveExchange("", []giftexchange.ExchangeSummary{only})
	if err != nil || got != only.ID {
		t.Errorf("resolveExchange = %v, %v; want %v", got, err, only.ID)
	}
}

func TestResolveExchangeTreatsBlankTextAsEmpty(t *testing.T) {
	only := summaryNamed("Winter")

	got, err := resolveExchange("  \t", []giftexchange.ExchangeSummary{only})
	if err != nil || got != only.ID {
		t.Errorf("resolveExchange = %v, %v; want %v", got, err, only.ID)
	}
}

func TestResolveExchangeWithNoCandidatesAsksForNone(t *testing.T) {
	_, err := resolveExchange("", nil)

	if !errors.Is(err, errNoCandidates) {
		t.Errorf("error = %v, want errNoCandidates", err)
	}
}

func TestResolveExchangeWithSeveralCandidatesListsTheirNames(t *testing.T) {
	candidates := []giftexchange.ExchangeSummary{summaryNamed("Winter"), summaryNamed("Summer")}

	_, err := resolveExchange("", candidates)

	var choose *chooseExchangeError
	if !errors.As(err, &choose) {
		t.Fatalf("error = %v, want *chooseExchangeError", err)
	}
	if strings.Join(choose.Names, ",") != "Winter,Summer" {
		t.Errorf("names = %v, want Winter and Summer", choose.Names)
	}
}

func TestResolveExchangePassesAnExchangeIDThroughEvenWhenItIsNotACandidate(t *testing.T) {
	stranger := giftexchange.ExchangeID(uuid.NewV7())

	got, err := resolveExchange(stranger.String(), []giftexchange.ExchangeSummary{summaryNamed("Winter")})
	if err != nil || got != stranger {
		t.Errorf("resolveExchange = %v, %v; want %v", got, err, stranger)
	}
}

func TestResolveExchangeMatchesANameIgnoringCase(t *testing.T) {
	winter := summaryNamed("Winter Exchange")
	candidates := []giftexchange.ExchangeSummary{summaryNamed("Summer"), winter}

	got, err := resolveExchange("  winter EXCHANGE ", candidates)
	if err != nil || got != winter.ID {
		t.Errorf("resolveExchange = %v, %v; want %v", got, err, winter.ID)
	}
}

func TestResolveExchangeRefusesTextThatMatchesNoneOrSeveral(t *testing.T) {
	candidates := []giftexchange.ExchangeSummary{summaryNamed("Winter"), summaryNamed("winter")}
	cases := map[string]string{
		"unknown name":     "Spring",
		"substring":        "Wint",
		"two same names":   "WINTER",
		"almost an ID":     "0198a3b0-0000-7000-8000-00000000000",
		"right to left":    "\u202eretniW",
		"mention":          "<@100000000000000001>",
		"only whitespace+": "Winter\u200b",
	}

	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := resolveExchange(raw, candidates)
			if !errors.Is(err, errNoMatch) {
				t.Errorf("resolveExchange(%q) error = %v, want errNoMatch", raw, err)
			}
		})
	}
}

func FuzzResolveExchange(f *testing.F) {
	known := summaryNamed("Winter")
	for _, seed := range []string{
		"", " ", "Winter", "winter", known.ID.String(), strings.ToUpper(known.ID.String()),
		"0198a3b0-0000-7000-8000-000000000001", "\u202eretniW", "e\u0301", "\U0001F381",
		strings.Repeat("a", 5000), "<@100000000000000001>", "\x00",
	} {
		f.Add(seed)
	}
	candidates := []giftexchange.ExchangeSummary{known, summaryNamed("e\u0301t\u00e9"), summaryNamed("\U0001F381 Gifts")}

	f.Fuzz(func(t *testing.T, raw string) {
		got, err := resolveExchange(raw, candidates)
		if err != nil {
			isKnown := errors.Is(err, errNoMatch) || errors.Is(err, errNoCandidates)
			var choose *chooseExchangeError
			if !isKnown && !errors.As(err, &choose) {
				t.Fatalf("resolveExchange(%q) error = %v, want a resolution error", raw, err)
			}
			return
		}

		parsed, parseErr := giftexchange.ParseExchangeID(strings.TrimSpace(raw))
		isParsed := parseErr == nil && parsed == got
		isCandidate := false
		for _, candidate := range candidates {
			isCandidate = isCandidate || candidate.ID == got
		}
		if !isParsed && !isCandidate {
			t.Fatalf("resolveExchange(%q) = %v, neither parsed nor a candidate", raw, got)
		}
	})
}

func TestFiltersKeepTheSummariesTheirVerbCanActOn(t *testing.T) {
	const (
		open      = giftexchange.StateOpen
		drawn     = giftexchange.StateDrawn
		revealed  = giftexchange.StateRevealed
		cancelled = giftexchange.StateCancelled
	)
	const (
		nobody      = ""
		participant = "participant"
		organizer   = "organizer"
		both        = "both"
	)

	cases := []struct {
		verb   string
		state  giftexchange.State
		caller string
		keeps  bool
	}{
		{"join", open, nobody, true},
		{"join", open, participant, false},
		{"join", drawn, nobody, false},
		{"leave", open, participant, true},
		{"leave", drawn, participant, true},
		{"leave", revealed, participant, false},
		{"leave", open, nobody, false},
		{"rules", cancelled, nobody, true},
		{"rename", open, organizer, true},
		{"cancel", drawn, organizer, true},
		{"remove", cancelled, organizer, false},
		{"remove", open, participant, false},
		{"exclude", open, organizer, true},
		{"unexclude", drawn, organizer, false},
		{"draw", open, organizer, true},
		{"draw", open, participant, false},
		{"draw", drawn, both, false},
		{"reveal", drawn, organizer, true},
		{"reveal", open, organizer, false},
		{"reveal", drawn, participant, false},
		{"recipient", drawn, participant, true},
		{"recipient", revealed, participant, true},
		{"recipient", open, participant, false},
		{"recipient", drawn, organizer, false},
		{"giver", revealed, participant, true},
		{"giver", drawn, participant, false},
		{"ask", drawn, participant, true},
		{"ask", revealed, participant, false},
		{"questions", drawn, both, true},
		{"questions", drawn, organizer, false},
	}

	for _, c := range cases {
		name := fmt.Sprintf("%s/%s/%q", c.verb, c.state, c.caller)
		t.Run(name, func(t *testing.T) {
			group := giftexchange.GroupID(uuid.NewV7())
			summary := giftexchange.ExchangeSummary{
				ID:                  giftexchange.ExchangeID(uuid.NewV7()),
				Group:               giftexchange.Group{ID: group},
				State:               c.state,
				CallerIsOrganizer:   c.caller == organizer || c.caller == both,
				CallerIsParticipant: c.caller == participant || c.caller == both,
			}

			h := newTestHandlersWith(t, &listingService{summaries: []giftexchange.ExchangeSummary{summary}})
			req := &router.Request{Group: &group}

			got, err := h.exchangeFor(context.Background(), req, c.verb, "")

			if c.keeps && (err != nil || got != summary.ID) {
				t.Errorf("exchangeFor = %v, %v; want %v", got, err, summary.ID)
			}
			if !c.keeps && !errors.Is(err, errNoCandidates) {
				t.Errorf("exchangeFor = %v, %v; want errNoCandidates", got, err)
			}
		})
	}
}

// listingService answers ExchangesFor with a fixed list.
type listingService struct {
	Service
	summaries []giftexchange.ExchangeSummary
}

func (l *listingService) ExchangesFor(
	context.Context, giftexchange.Actor, *giftexchange.GroupID,
) ([]giftexchange.ExchangeSummary, error) {
	return l.summaries, nil
}

func newTestHandlers(tb testing.TB) *Handlers {
	tb.Helper()

	return newTestHandlersWith(tb, &stubService{})
}

func newTestHandlersWith(tb testing.TB, svc Service) *Handlers {
	tb.Helper()

	guildName := func(subject string) string {
		if subject == "" {
			return GuildNameOrUnknown("")
		}

		return "Server " + subject
	}
	h, err := New(svc, guildName, time.Now, time.UTC, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		tb.Fatalf("New: %v", err)
	}

	return h
}

func choiceOf(tb testing.TB, c discord.AutocompleteChoice) discord.AutocompleteChoiceString {
	tb.Helper()

	choice, ok := c.(discord.AutocompleteChoiceString)
	if !ok {
		tb.Fatalf("choice is a %T, want a string choice", c)
	}

	return choice
}

func TestChoicesInADMNameTheServer(t *testing.T) {
	h := newTestHandlers(t)
	winter := summaryNamed("Winter")
	winter.Group.Subject = "alpha"
	unknown := summaryNamed("Spring")

	choices := h.choicesFor(&router.Request{}, []giftexchange.ExchangeSummary{winter, unknown}, "")

	if len(choices) != 2 {
		t.Fatalf("%d choices, want 2", len(choices))
	}
	if got := choiceOf(t, choices[0]); got.Name != "Winter - Server alpha" || got.Value != winter.ID.String() {
		t.Errorf("first choice = %+v", got)
	}
	if got := choiceOf(t, choices[1]); got.Name != "Spring - unknown server" {
		t.Errorf("second choice = %+v", got)
	}
}

func TestChoicesTruncateALongNameTo100Runes(t *testing.T) {
	h := newTestHandlers(t)
	long := summaryNamed(strings.Repeat("\u00e9", 100))
	long.Group.Subject = "alpha"

	choices := h.choicesFor(&router.Request{}, []giftexchange.ExchangeSummary{long}, "")

	got := choiceOf(t, choices[0]).Name
	if utf8.RuneCountInString(got) != 100 || !strings.HasSuffix(got, "...") {
		t.Errorf("choice name has %d runes and ends %q, want 100 runes ending in ...", utf8.RuneCountInString(got), got[len(got)-3:])
	}
}
