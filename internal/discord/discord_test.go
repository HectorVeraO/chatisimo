//go:build !race

// These tests open a real disgo Gateway client; see gateway_harness_test.go for why they are
// left out of the race run.

package discord_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	appdiscord "github.com/HectorVeraO/chatisimo/internal/discord"
	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
	"github.com/HectorVeraO/chatisimo/internal/platform/config"
)

const (
	guildOnlyText = "This command works only in a server."
	internalLead  = "Something went wrong. Reference "

	callbackMessage = 4
)

func slashOrFail(t *testing.T, h *harness, user string, in fakediscord.Slash) fakediscord.Result {
	t.Helper()

	result, err := h.fake.Slash(t.Context(), h.users[user], in)
	if err != nil {
		t.Fatalf("Slash %s: %v", in.Path, err)
	}

	return result
}

func TestNewReportsEveryMissingDependency(t *testing.T) {
	t.Parallel()

	_, err := appdiscord.New(nil, nil, nil, nil)
	if err == nil {
		t.Fatal("New accepted nil dependencies")
	}

	for _, want := range []string{"notifier", "giftexchange", "uccalendar", "logger"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}

func TestNewDoesNoIO(t *testing.T) {
	w := newWorld(t)
	s := newService(t, w)

	_, err := appdiscord.New(w.notifier, s.svc, s.uc, w.logger)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if len(w.fake.Commands()) != 0 {
		t.Error("New registered commands")
	}
	if _, identified := w.fake.Intents(); identified {
		t.Error("New opened the Gateway")
	}
}

func TestOpenRegistersBothTreesAndAsksForTheGuildsIntentOnly(t *testing.T) {
	h := newHarness(t)

	intents, identified := h.fake.Intents()
	if !identified || intents != 1 {
		t.Errorf("Intents() = %d, %v, want 1, true", intents, identified)
	}

	var names []string
	for _, command := range h.fake.Commands() {
		names = append(names, command.Name)
		if command.GuildID != "" {
			t.Errorf("command %s is registered for a guild", command.Name)
		}
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"chatisimo", "ge", "uc"}) {
		t.Errorf("registered commands = %v, want chatisimo, ge and uc", names)
	}
}

func TestOpenTwiceFails(t *testing.T) {
	h := newHarness(t)

	err := h.adapter.Open(t.Context())
	if err == nil {
		t.Fatal("second Open succeeded")
	}
	if !strings.Contains(err.Error(), "already open") {
		t.Errorf("second Open error = %q, want the already open error", err)
	}
}

func TestOpenReportsABadTokenWithoutLeakingIt(t *testing.T) {
	w := newWorld(t)
	s := newService(t, w)

	bad := config.NewSecret(w.fake.Token() + "x")
	notifier, err := appdiscord.NewNotifier(appdiscord.Settings{Token: bad, APIBaseURL: w.fake.RESTBase()}, w.logger)
	if err != nil {
		t.Fatalf("NewNotifier: %v", err)
	}
	adapter, err := appdiscord.New(notifier, s.svc, s.uc, w.logger)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	openErr := adapter.Open(t.Context())
	if openErr == nil {
		t.Fatal("Open succeeded with a wrong token")
	}
	if strings.Contains(openErr.Error(), bad.Reveal()) {
		t.Errorf("Open error holds the token: %v", openErr)
	}

	adapter.Close(context.Background())
}

func TestHelpAnswersEphemerallyOnBothPathsInAGuildAndInTheBotDM(t *testing.T) {
	h := newHarness(t)

	paths := []string{"/ge/help", "/chatisimo/giftexchange/help"}
	var first string
	for _, path := range paths {
		for _, inGuild := range []bool{true, false} {
			result := slashOrFail(t, h, userA, h.slashAt(path, inGuild, nil))

			if result.Status != fakediscord.StatusResponded || result.CallbackType != callbackMessage {
				t.Fatalf("%s guild=%v: status %q type %d, want a type 4 message", path, inGuild, result.Status, result.CallbackType)
			}
			if result.Message == nil || result.Message.EphemeralFor != userA {
				t.Fatalf("%s guild=%v: reply is not ephemeral to the caller: %+v", path, inGuild, result.Message)
			}
			if !strings.Contains(result.Message.Content, "/ge create") || !strings.Contains(result.Message.Content, "/ge help") {
				t.Errorf("%s guild=%v: help lacks its verbs: %q", path, inGuild, result.Message.Content)
			}
			if first == "" {
				first = result.Message.Content
			}
			if result.Message.Content != first {
				t.Errorf("%s guild=%v: help differs between paths", path, inGuild)
			}
		}
	}
}

func TestEveryReplyCarriesTheEmptyAllowedMentions(t *testing.T) {
	h := newHarness(t)

	slashOrFail(t, h, userA, h.ge("help", true, nil))

	records := h.fake.Outbound()
	if len(records) == 0 {
		t.Fatal("no outbound records")
	}
	for _, record := range records {
		sent := record.AllowedMentionsSent
		if sent == nil || sent.Parse == nil || sent.Users == nil || sent.Roles == nil {
			t.Fatalf("record %d allowed_mentions = %+v, want empty non-nil lists", record.Seq, sent)
		}
		if len(sent.Parse)+len(sent.Users)+len(sent.Roles) != 0 {
			t.Errorf("record %d allowed_mentions permits mentions: %+v", record.Seq, sent)
		}
	}
}

func TestAComponentWithAnUnknownCustomIDAnswersInternal(t *testing.T) {
	h := newHarness(t)
	help := slashOrFail(t, h, userA, h.ge("help", true, nil))

	for i, customID := range []string{"/gx/unknown/abc", "/zz", "/" + strings.Repeat("a", 99)} {
		result, err := h.fake.ClickForged(t.Context(), h.users[userA], *help.Message, customID)
		if err != nil {
			t.Fatalf("ClickForged %q: %v", customID, err)
		}

		if result.Message == nil || !strings.HasPrefix(result.Message.Content, internalLead) {
			t.Fatalf("custom ID %q: reply = %+v, want the Internal text", customID, result.Message)
		}
		if result.Message.EphemeralFor != userA {
			t.Errorf("custom ID %q: the Internal reply is not ephemeral to the caller", customID)
		}

		reference := strings.TrimSuffix(strings.TrimPrefix(result.Message.Content, internalLead), ".")
		lines := h.waitInteractionLogs(t, i+2)
		last := lines[len(lines)-1]
		if last["route"] != "unmatched" || last["outcome"] != "error" || last["request_id"] != reference {
			t.Errorf("custom ID %q: log line = %v, want an unmatched error for reference %s", customID, last, reference)
		}
		if last["kind"] != "component" {
			t.Errorf("custom ID %q: kind = %v, want component", customID, last["kind"])
		}
	}
}

func TestACustomIDWithoutALeadingSlashIsNeverRouted(t *testing.T) {
	h := newHarness(t)
	help := slashOrFail(t, h, userA, h.ge("help", true, nil))

	result, err := h.fake.ClickForged(t.Context(), h.users[userA], *help.Message, "nope")
	if err != nil {
		t.Fatalf("ClickForged: %v", err)
	}

	if result.Status != fakediscord.StatusTimeout {
		t.Errorf("status = %q, want timeout: the interaction is dropped", result.Status)
	}
}

func TestEachInteractionWritesOneLogLineWithOnlyTheListedFields(t *testing.T) {
	h := newHarness(t)

	slashOrFail(t, h, userA, h.ge("help", true, nil))
	slashOrFail(t, h, userB, h.slashAt("/chatisimo/giftexchange/help", false, nil))

	lines := h.waitInteractionLogs(t, 2)

	guildKeys := []string{
		"context", "duration_ms", "group_id", "kind", "level", "msg", "outcome", "person_id", "request_id", "route", "time",
	}
	dmKeys := slices.DeleteFunc(slices.Clone(guildKeys), func(key string) bool { return key == "group_id" })
	for i, line := range lines {
		var keys []string
		for key := range line {
			keys = append(keys, key)
		}
		slices.Sort(keys)

		want := guildKeys
		if i == 1 {
			want = dmKeys
		}
		if !slices.Equal(keys, want) {
			t.Errorf("line %d keys = %v, want %v", i, keys, want)
		}
		if line["kind"] != "command" || line["outcome"] != "ok" || line["level"] != "INFO" {
			t.Errorf("line %d = %v", i, line)
		}
	}
	if lines[0]["route"] != "/ge/help" || lines[0]["context"] != "guild" {
		t.Errorf("guild line = %v", lines[0])
	}
	if lines[1]["route"] != "/chatisimo/giftexchange/help" || lines[1]["context"] != "dm" {
		t.Errorf("DM line = %v", lines[1])
	}

	for _, line := range h.logRecords(t) {
		raw, err := json.Marshal(line)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		for _, secret := range []string{userA, userB, guildID, h.fake.Token()} {
			if strings.Contains(string(raw), secret) {
				t.Errorf("log line holds %q: %s", secret, raw)
			}
		}
	}
}

func TestShuttingTheGatewayDownKeepsNotificationsFlowing(t *testing.T) {
	h := newHarness(t)

	closeCtx, cancel := context.WithTimeout(t.Context(), closeDeadline)
	defer cancel()
	h.adapter.CloseGateway(closeCtx)
	h.adapter.CloseGateway(closeCtx)

	err := h.notifier.Notify(t.Context(), cancelledNotice(userA))
	if err != nil {
		t.Fatalf("Notify after CloseGateway: %v", err)
	}

	dms := h.fake.DMs(h.users[userA])
	if len(dms) != 1 {
		t.Fatalf("%d DMs delivered, want 1", len(dms))
	}
}

func TestADrawnExchangeLeaksNoPairThroughRepliesOrLogs(t *testing.T) {
	h := newHarness(t)
	h.startRunner(t, h.world)

	exchange := h.drawThree(t)
	h.markDrawn(exchange)

	waitFor(t, "the three draw DMs", func() bool {
		return finishedCount(t, exchange, "delivered") == 3
	})

	help := slashOrFail(t, h, userA, h.ge("help", true, nil))
	pairs := h.truePairs(t)
	if len(pairs) != 3 {
		t.Errorf("%d true pairs, want 3", len(pairs))
	}
	if help.Message == nil {
		t.Fatal("help did not answer with a message")
	}
	named := slices.Clone(pairMarkers)
	for _, pair := range pairs {
		named = append(named, pair.recipient, pair.recipientID, pair.giverID)
	}
	for _, leaked := range named {
		if leaked != "" && strings.Contains(help.Message.Content, leaked) {
			t.Errorf("the help reply holds %q", leaked)
		}
	}
}

func TestAnAdapterWhoseGatewayIsClosedRefusesToOpenAgain(t *testing.T) {
	h := newHarness(t)

	openCtx, cancel := context.WithTimeout(t.Context(), closeDeadline)
	defer cancel()

	if err := h.adapter.Open(openCtx); err == nil {
		t.Error("Open on an open adapter succeeded, want an error")
	}

	h.adapter.CloseGateway(openCtx)
	if err := h.adapter.Open(openCtx); err == nil {
		t.Error("Open after CloseGateway succeeded, want an error")
	}
}
