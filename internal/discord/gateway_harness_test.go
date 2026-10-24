//go:build !race

// The harness opens a real disgo Gateway client. disgo v0.19 touches the fields of its gateway
// from several goroutines without synchronization, so the race detector reports races inside
// the library; the fake's own Gateway tests carry the same tag.

package discord_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	appdiscord "github.com/HectorVeraO/chatisimo/internal/discord"
	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
	"github.com/HectorVeraO/chatisimo/internal/giftexchange"
)

// harness is a world with the module, the notifier and the adapter wired together, connected
// to the fake over the Gateway.
type harness struct {
	*world
	*service
	adapter *appdiscord.Adapter
	// failUnscopedListingAt makes the Nth unscoped ExchangesFor call fail once armed with N.
	failUnscopedListingAt atomic.Int64
	drawn                 []giftexchange.ExchangeID
	// secrets are texts a test typed that must never reach the logs.
	secrets []string
}

// flakyService is the module with an unscoped exchange listing whose Nth call can be made to fail.
type flakyService struct {
	*giftexchange.Service
	failAt *atomic.Int64
}

func (f flakyService) ExchangesFor(
	ctx context.Context, a giftexchange.Actor, g *giftexchange.GroupID,
) ([]giftexchange.ExchangeSummary, error) {
	if g == nil && f.failAt.Load() > 0 && f.failAt.Add(-1) == 0 {
		return nil, errors.New("listing unavailable")
	}

	return f.Service.ExchangesFor(ctx, a, g) //nolint:wrapcheck // test double passes the module's result through
}

// newHarness wires the module, the notifier and the adapter against the fake, opens the
// Gateway and waits until the fake sees the bot ready. When the test draws, its cleanup
// checks that no pair leaked.
func newHarness(t *testing.T) *harness {
	t.Helper()

	w := newWorld(t)
	s := newService(t, w)
	h := &harness{world: w, service: s}
	adapter, err := appdiscord.New(w.notifier, flakyService{Service: s.svc, failAt: &h.failUnscopedListingAt}, w.logger)
	if err != nil {
		t.Fatalf("discord.New: %v", err)
	}
	h.adapter = adapter

	openCtx, cancel := context.WithTimeout(t.Context(), deliveryDeadline)
	defer cancel()

	openErr := adapter.Open(openCtx)
	if openErr != nil {
		t.Fatalf("Open: %v", openErr)
	}
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), closeDeadline)
		defer closeCancel()

		adapter.Close(closeCtx)
		w.notifierClosed = true
	})

	readyErr := w.fake.WaitReady(openCtx)
	if readyErr != nil {
		t.Fatalf("WaitReady: %v", readyErr)
	}

	t.Cleanup(func() { h.checkConfidentiality(t) })

	return h
}

// markDrawn tells the cleanup that the exchange was drawn, so it checks its pairs.
func (h *harness) markDrawn(id giftexchange.ExchangeID) {
	h.drawn = append(h.drawn, id)
}

// truePair is one giver and the recipient the module assigned, by snowflake and person ID.
type truePair struct {
	giver, recipient     string
	giverID, recipientID string
}

// truePairs reads the pairs of the drawn exchanges from the module as each participant.
func (h *harness) truePairs(t *testing.T) []truePair {
	t.Helper()

	var pairs []truePair
	for _, id := range h.drawn {
		for _, subject := range []string{userA, userB, userC} {
			who := h.people[subject]
			// The test context is already cancelled when cleanups run.
			ctx, cancel := context.WithTimeout(context.Background(), deliveryDeadline)
			assignment, err := h.svc.MyAssignment(ctx, who.actor, id)
			cancel()
			if errors.Is(err, giftexchange.ErrNotParticipant) {
				continue
			}
			if err != nil {
				t.Errorf("MyAssignment: %v", err)
				continue
			}

			pair := truePair{giver: subject, giverID: who.id.String(), recipientID: assignment.Recipient.ID.String()}
			for _, identity := range assignment.Recipient.Identities {
				pair.recipient = identity.Subject
			}
			pairs = append(pairs, pair)
		}
	}

	return pairs
}

var pairMarkers = []string{"Your recipient", "Your new recipient", "Your giver"}

func (h *harness) checkConfidentiality(t *testing.T) {
	t.Helper()

	if len(h.drawn) == 0 {
		return
	}

	pairs := h.truePairs(t)
	h.checkOutbound(t, pairs)
	h.checkLogs(t, pairs)
}

// checkOutbound requires every record that names a counterpart to be visible to exactly the
// person it is meant for.
func (h *harness) checkOutbound(t *testing.T, pairs []truePair) {
	t.Helper()

	for _, record := range h.fake.Outbound() {
		text := record.Content
		for _, embed := range record.Embeds {
			text += string(embed)
		}

		isGiverView := strings.Contains(text, "Your giver")
		hasMarker := isGiverView
		for _, marker := range pairMarkers {
			if strings.Contains(text, marker) {
				hasMarker = true
			}
		}
		if !hasMarker {
			continue
		}

		mentioned := snowflakePattern.FindAllString(strings.Join(record.MentionsRendered, " "), -1)
		var want []string
		for _, pair := range pairs {
			if isGiverView && slices.Contains(mentioned, pair.giver) {
				want = append(want, pair.recipient)
			}
			if !isGiverView && slices.Contains(mentioned, pair.recipient) {
				want = append(want, pair.giver)
			}
		}

		got := slices.Clone(record.VisibleTo)
		slices.Sort(got)
		slices.Sort(want)
		want = slices.Compact(want)
		if !slices.Equal(got, want) {
			t.Errorf("record %d names a counterpart but is visible to %v, want %v", record.Seq, got, want)
		}
	}
}

// checkLogs requires the captured logs to hold no snowflake, token, name or typed text, and
// no line with both person IDs of a pair.
func (h *harness) checkLogs(t *testing.T, pairs []truePair) {
	t.Helper()

	forbidden := append([]string{
		userA, userB, userC, userGap, guildID, h.fake.Token(), h.fake.ApplicationID(),
		exchangeName, guildName, "tester-",
	}, h.secrets...)

	logs := h.logs.String()
	for _, needle := range forbidden {
		if needle != "" && strings.Contains(logs, needle) {
			t.Errorf("logs hold %q", needle)
		}
	}

	for _, line := range strings.Split(logs, "\n") {
		for _, pair := range pairs {
			if strings.Contains(line, pair.giverID) && strings.Contains(line, pair.recipientID) {
				t.Errorf("a log line holds both person IDs of one pair: %s", line)
			}
		}
	}
}

// logRecords decodes the captured JSON log lines.
func (h *harness) logRecords(t *testing.T) []map[string]any {
	t.Helper()

	var records []map[string]any
	for _, line := range strings.Split(h.logs.String(), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}

		var record map[string]any
		err := json.Unmarshal([]byte(line), &record)
		if err != nil {
			t.Fatalf("log line is not JSON: %v", err)
		}
		records = append(records, record)
	}

	return records
}

// interactionLogs returns the request log lines.
func (h *harness) interactionLogs(t *testing.T) []map[string]any {
	t.Helper()

	var lines []map[string]any
	for _, record := range h.logRecords(t) {
		if record["msg"] == "interaction" {
			lines = append(lines, record)
		}
	}

	return lines
}

// ge returns a slash command on the short path in the harness guild, or in the bot's DM.
func (h *harness) ge(verb string, inGuild bool, options map[string]any) fakediscord.Slash {
	return h.slashAt("/ge/"+verb, inGuild, options)
}

func (h *harness) slashAt(path string, inGuild bool, options map[string]any) fakediscord.Slash {
	in := fakediscord.Slash{Path: path, Options: options}
	if inGuild {
		guild := h.guild
		in.Guild = &guild
	}

	return in
}

// waitInteractionLogs waits until at least n request lines were logged, which happens just
// after the reply that the test observed, and returns them.
func (h *harness) waitInteractionLogs(t *testing.T, n int) []map[string]any {
	t.Helper()

	var lines []map[string]any
	waitFor(t, "request log lines", func() bool {
		lines = h.interactionLogs(t)
		return len(lines) >= n
	})

	return lines
}
