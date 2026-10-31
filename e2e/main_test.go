//go:build e2e

// Package e2e runs the real chatisimo binary, built with coverage, against an in-process fake
// Discord and a fresh Postgres database, and drives it as synthetic users.
package e2e

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var (
	coverDirFlag = flag.String("coverdir", "", "directory that receives the coverage data of the binary (default: a temp dir)")
	logDirFlag   = flag.String("logdir", "", "directory that receives the binary's stderr as chatisimo.log (default: a temp dir)")
)

// intentGuilds is the Gateway intent the bot must ask for, and no other.
const intentGuilds = 1

// shared is the backend and the binary that every flow test talks to.
var shared *backend

// logFileName is the file of logDir that holds the binary's stderr.
const logFileName = "chatisimo.log"

// harnessDirs holds the directories of one run.
type harnessDirs struct {
	bin      string
	cover    string
	logs     string
	cleanups []string
}

func TestMain(m *testing.M) {
	flag.Parse()

	code, err := runHarness(m)
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e harness: %v\n", err)

		os.Exit(1)
	}

	os.Exit(code)
}

// makeDirs resolves the flags into directories; the defaults are temporary and removed at the end.
func makeDirs() (harnessDirs, error) {
	var dirs harnessDirs

	binDir, err := os.MkdirTemp("", "chatisimo-e2e-bin-")
	if err != nil {
		return dirs, fmt.Errorf("create bin dir: %w", err)
	}
	dirs.bin = binDir
	dirs.cleanups = append(dirs.cleanups, binDir)

	for _, spec := range []struct {
		target *string
		flag   string
		prefix string
	}{
		{&dirs.cover, *coverDirFlag, "chatisimo-e2e-cover-"},
		{&dirs.logs, *logDirFlag, "chatisimo-e2e-logs-"},
	} {
		if spec.flag != "" {
			err = os.MkdirAll(spec.flag, 0o750)
			if err != nil {
				return dirs, fmt.Errorf("create %s: %w", spec.flag, err)
			}
			*spec.target = spec.flag

			continue
		}

		temp, tempErr := os.MkdirTemp("", spec.prefix)
		if tempErr != nil {
			return dirs, fmt.Errorf("create temp dir: %w", tempErr)
		}
		*spec.target = temp
		dirs.cleanups = append(dirs.cleanups, temp)
	}

	return dirs, nil
}

func (d harnessDirs) remove() {
	for _, dir := range d.cleanups {
		// A leftover temp dir is harmless; the run's result does not depend on it.
		_ = os.RemoveAll(dir)
	}
}

// runHarness follows the harness steps: build, database, fake, configure, run, tests, stop, audit,
// teardown. Any failure is returned; the exit code of m.Run is returned when the harness itself held.
func runHarness(m *testing.M) (int, error) {
	ctx := context.Background()

	dirs, err := makeDirs()
	defer dirs.remove()
	if err != nil {
		return 0, err
	}

	bin, err := buildBinary(ctx, dirs.bin)
	if err != nil {
		return 0, err
	}

	b, err := newBackend(ctx, bin, dirs.cover)
	if err != nil {
		return 0, err
	}
	created.add("application snowflake", b.fake.ApplicationID())
	created.add("bot token", b.fake.Token())

	code, runErr := runAgainst(ctx, m, b, dirs)
	closeErr := b.close()
	fakeAuditErr := auditText("fake discord log", b.fakeLog.String(), &created)

	return code, errors.Join(runErr, closeErr, fakeAuditErr) //nolint:wrapcheck // joins this function's own errors
}

func runAgainst(ctx context.Context, m *testing.M, b *backend, dirs harnessDirs) (int, error) {
	seedSharedUCFixtures(b.fake)
	created.add("uc friend snowflake", sharedUCFriendID)

	err := b.configure(ctx)
	if err != nil {
		return 0, err
	}

	logPath := filepath.Join(dirs.logs, logFileName)
	p, err := b.start(logPath)
	if err != nil {
		return 0, err
	}

	err = awaitReady(ctx, b, p)
	if err != nil {
		if !p.hasExited() {
			p.kill()
		}

		return 0, err
	}

	shared = b
	code := m.Run()
	shared = nil

	stopErr := p.stopGracefully(stopTimeout)
	if stopErr != nil {
		return code, stopErr
	}

	auditErr := auditLog(logPath, &created)

	return code, auditErr
}

// awaitReady waits for the bot to connect and checks what it registered and asked for.
func awaitReady(ctx context.Context, b *backend, p *process) error {
	ctx, cancel := context.WithTimeout(ctx, readyTimeout)
	defer cancel()

	go func() {
		select {
		case <-p.done:
			cancel()
		case <-ctx.Done():
		}
	}()

	err := b.fake.WaitReady(ctx)
	if err != nil {
		if p.hasExited() {
			return fmt.Errorf("chatisimo exited with code %d before it was ready", p.exitCode())
		}

		return fmt.Errorf("wait for the bot: %w", err)
	}

	var problems []error
	intents, identified := b.fake.Intents()
	if !identified || intents != intentGuilds {
		problems = append(problems, fmt.Errorf("intents = %d (identified %v), want %d", intents, identified, intentGuilds))
	}

	names := map[string]bool{}
	for _, command := range b.fake.Commands() {
		names[command.Name] = true
	}
	for _, want := range []string{"chatisimo", "ge", "uc"} {
		if !names[want] {
			problems = append(problems, fmt.Errorf("command tree /%s is not registered", want))
		}
	}

	return errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
}

// auditLog fails when the captured stderr holds anything the run created that must stay out
// of logs. It names the kind of each finding, never the value.
func auditLog(path string, r *registry) error {
	//nolint:gosec // path is the log file the harness created
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read log: %w", err)
	}

	return auditText(path, string(raw), r)
}

// auditText fails when text holds a value registered in r. source names where text came from.
func auditText(source, text string, r *registry) error {
	found := r.findIn(text)
	if len(found) == 0 {
		return nil
	}

	return fmt.Errorf("log audit: %s holds %d value(s) that must not be logged: %s",
		source, len(found), strings.Join(found, ", "))
}
