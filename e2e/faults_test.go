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

	shared.fake.DropGateway(closeUnknownError)

	// The bot answers again once it has reconnected; until then the fake has nobody to deliver to.
	waitFor(t, "the bot to answer after the drop", func() bool {
		guild := s.guild
		result, err := s.fake.Slash(t.Context(), s.members[0], fakediscord.Slash{Guild: &guild, Path: "/ge/help"})

		return err == nil && result.Status != fakediscord.StatusTimeout
	})

	// A fresh identify would also leave the bot answering; only an accepted resume proves the session survived.
	waitFor(t, "the fake to accept a Gateway resume", func() bool {
		return strings.Contains(shared.fakeLog.String(), `msg="gateway resume" accepted=true`)
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
	if strings.Contains(string(raw), b.fake.Token()) {
		t.Error("the log of the failed run holds the token")
	}
}
