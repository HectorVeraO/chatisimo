//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
)

const (
	authFailureTimeout = 15 * time.Second
	// closeUnknownError is a Gateway close code a client may resume after.
	closeUnknownError = 4000
	// closeAuthFailed is the Gateway close code for a rejected token.
	closeAuthFailed = 4004
)

// These tests touch the shared Gateway, so they are not parallel: Go runs the serial tests of
// the package before it releases the parallel ones.

func TestGatewayResume(t *testing.T) {
	s := newGuild(t, 2)

	resumesBefore := shared.fake.Resumes()
	shared.fake.DropGateway(closeUnknownError)

	// The bot answers again once it has reconnected; until then the fake has nobody to deliver to.
	waitFor(t, "the bot to answer after the drop", func() bool {
		guild := s.guild
		result, err := s.fake.Slash(t.Context(), s.members[0], fakediscord.Slash{Guild: &guild, Path: "/ge/help"})

		return err == nil && result.Status != fakediscord.StatusTimeout
	})

	// A fresh identify would also leave the bot answering; only an accepted resume proves the session survived.
	waitFor(t, "the fake to accept a Gateway resume", func() bool {
		return shared.fake.Resumes() > resumesBefore
	})

	name := s.exchangeName("Winter exchange")
	exchange, _ := s.createExchange(t, s.members[0], name)
	got := s.join(t, s.members[1], exchange.id)

	if !strings.Contains(got.text, textJoined) {
		t.Errorf("join after the resume = %q, want a confirmation", got.text)
	}
}

func TestAuthFailureExits(t *testing.T) {
	bin := shared.bin
	b, err := newBackend(t.Context(), bin, shared.coverDir)
	if err != nil {
		t.Fatalf("create backend: %v", err)
	}
	t.Cleanup(func() {
		closeErr := b.close()
		if closeErr != nil {
			t.Errorf("close backend: %v", closeErr)
		}
	})

	err = b.fake.FailNext("GATEWAY identify", 0, closeAuthFailed)
	if err != nil {
		t.Fatalf("FailNext: %v", err)
	}
	err = b.configure(t.Context())
	if err != nil {
		t.Fatalf("configure: %v", err)
	}

	logPath := filepath.Join(t.TempDir(), "authfail.log")
	p, err := b.start(logPath)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		if !p.hasExited() {
			p.kill()
		}
	})

	code, err := p.waitExit(authFailureTimeout)
	if err != nil {
		t.Fatal(err)
	}
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}

	//nolint:gosec // logPath is a file this test created
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(raw), "open discord gateway") {
		t.Errorf("the log of the failed run = %q, want the Gateway open failure", raw)
	}
	if strings.Contains(string(raw), b.fake.Token()) {
		t.Error("the log of the failed run holds the token")
	}
}

// dmAttempts counts the DM requests the bot made to the user that ended with this status.
func dmAttempts(s *scene, user fakediscord.User, status int) int {
	count := 0
	for _, record := range s.fake.Outbound() {
		isAttempt := record.Source == "dm" && record.Status == status &&
			len(record.VisibleTo) == 1 && record.VisibleTo[0] == user.ID
		if isAttempt {
			count++
		}
	}

	return count
}

// The fault tests are not parallel: their faults apply to the next matching request of the one
// fake, whoever sends it.

func TestDMClosed(t *testing.T) {
	s := newGuild(t, 4)
	organizer, givers := s.members[0], s.members[1:]
	closed := givers[0]
	exchange, _ := s.setup(t, organizer, givers)

	err := s.fake.SetDMsOpen(closed, false)
	if err != nil {
		t.Fatalf("close DMs: %v", err)
	}
	t.Cleanup(func() {
		reopenErr := s.fake.SetDMsOpen(closed, true)
		if reopenErr != nil {
			t.Errorf("reopen DMs: %v", reopenErr)
		}
	})

	s.draw(t, organizer, exchange.id)
	for _, giver := range givers[1:] {
		s.awaitTitled(t, giver, titleRecipient, 1)
	}
	waitFor(t, "the refused DM attempt", func() bool { return dmAttempts(s, closed, 403) >= 1 })

	own := s.slash(t, closed, "/ge/recipient", map[string]any{"exchange": exchange.id})
	if !hasTitle(t, own.message, titleRecipient) {
		t.Errorf("/ge recipient with DMs closed = %q, want the recipient", own.text)
	}
	if got := dmAttempts(s, closed, 403); got != 1 {
		t.Errorf("DM attempts refused with 50007 = %d, want exactly 1", got)
	}
	if got := dmAttempts(s, closed, 200); got != 0 {
		t.Errorf("DMs delivered to the closed user = %d, want 0", got)
	}
}

func TestRateLimitedDM(t *testing.T) {
	s := newGuild(t, 4)
	organizer, givers := s.members[0], s.members[1:]
	exchange, _ := s.setup(t, organizer, givers)
	const route = "POST /channels/{id}/messages"

	err := shared.fake.FailNext(route, 429, 0)
	if err != nil {
		t.Fatalf("FailNext: %v", err)
	}
	s.draw(t, organizer, exchange.id)
	pairs := s.pairsAfterDraw(t, givers)
	assertDerangement(t, pairs)

	pending, err := shared.fake.PendingFaults(route)
	if err != nil || pending != 0 {
		t.Errorf("rate limits still queued = %d, %v, want 0: the fault never fired", pending, err)
	}
	for _, giver := range givers {
		if got := len(s.dmsWithTitle(t, giver, titleRecipient)); got != 1 {
			t.Errorf("%s received %d recipient DMs, want exactly 1", giver.Name, got)
		}
		if got := dmAttempts(s, giver, 200); got != 1 {
			t.Errorf("%s has %d delivered DM records, want exactly 1", giver.Name, got)
		}
	}
}
