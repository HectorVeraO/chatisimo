//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
	"github.com/HectorVeraO/chatisimo/internal/platform/pgtest"
)

const (
	buildTimeout     = 5 * time.Minute
	configureTimeout = time.Minute
	readyTimeout     = 30 * time.Second
	stopTimeout      = 40 * time.Second
	closeTimeout     = 30 * time.Second
	callbackDeadline = 3 * time.Second
	masterKeyBytes   = 32
)

var errNoProcess = errors.New("e2e: no process to signal")

// syncBuffer is a bytes.Buffer safe for the writers and readers of different goroutines.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	n, err := b.buf.Write(p)
	if err != nil {
		return n, fmt.Errorf("write buffer: %w", err)
	}

	return n, nil
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// subprocessEnv is the whole environment of every chatisimo subprocess: the three variables
// the binary reads, GOCOVERDIR for the coverage build, and what the operating system needs to
// run a program. Nothing else of the test's environment is passed on.
func subprocessEnv(databaseURL, masterKey, coverDir string) []string {
	env := []string{
		"DATABASE_URL=" + databaseURL,
		"MASTER_KEY=" + masterKey,
		"CHATISIMO_ENV=local",
		"GOCOVERDIR=" + coverDir,
		"PATH=" + os.Getenv("PATH"),
	}
	if runtime.GOOS == "windows" {
		env = append(env, "SYSTEMROOT="+os.Getenv("SYSTEMROOT"))
	}

	return env
}

// moduleRoot returns the directory of go.mod, the parent of the package directory the tests
// run in.
func moduleRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}

	root := filepath.Dir(wd)
	_, err = os.Stat(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("find module root above %s: %w", wd, err)
	}

	return root, nil
}

// buildBinary builds cmd/chatisimo with coverage instrumentation into dir and returns its path.
func buildBinary(ctx context.Context, dir string) (string, error) {
	root, err := moduleRoot()
	if err != nil {
		return "", err
	}

	name := "chatisimo"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	out := filepath.Join(dir, name)

	ctx, cancel := context.WithTimeout(ctx, buildTimeout)
	defer cancel()

	// atomic matches the unit run, which -race forces to atomic; covdata merge rejects mixed modes.
	//nolint:gosec // fixed arguments; out is a path this harness chose
	cmd := exec.CommandContext(ctx, "go", "build", "-cover", "-covermode=atomic", "-coverpkg=./...", "-o", out, "./cmd/chatisimo")
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("go build -cover: %w\n%s", err, output)
	}

	return out, nil
}

// newMasterKey returns 32 random bytes as standard base64, like chatisimo keygen.
func newMasterKey() (string, error) {
	raw := make([]byte, masterKeyBytes)
	_, err := rand.Read(raw)
	if err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}

	return base64.StdEncoding.EncodeToString(raw), nil
}

// backend is what one binary runs against: its own database and its own fake Discord.
type backend struct {
	bin       string
	coverDir  string
	masterKey string
	db        *pgtest.Database
	fake      *fakediscord.Server
	fakeLog   *syncBuffer
}

// newBackend creates an empty database cloned from DATABASE_URL and a started fake Discord.
func newBackend(ctx context.Context, bin, coverDir string) (*backend, error) {
	masterKey, err := newMasterKey()
	if err != nil {
		return nil, err
	}

	db, err := pgtest.New(ctx, pgtest.Options{})
	if err != nil {
		return nil, fmt.Errorf("create database: %w", err)
	}

	fakeLog := &syncBuffer{}
	fake, err := fakediscord.New(fakediscord.Options{
		CallbackDeadline: callbackDeadline,
		Logger:           slog.New(slog.NewTextHandler(fakeLog, nil)),
	})
	if err != nil {
		return nil, errors.Join(fmt.Errorf("create fake discord: %w", err), db.Close(ctx)) //nolint:wrapcheck // joins this function's own errors
	}

	// The fake outlives any one context: close stops it.
	err = fake.Start(context.Background())
	if err != nil {
		return nil, errors.Join(fmt.Errorf("start fake discord: %w", err), db.Close(ctx)) //nolint:wrapcheck // joins this function's own errors
	}

	b := &backend{bin: bin, coverDir: coverDir, masterKey: masterKey, db: db, fake: fake, fakeLog: fakeLog}

	return b, nil
}

// close stops the fake and drops the database.
func (b *backend) close() error {
	ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()

	fakeErr := b.fake.Close()
	dbErr := b.db.Close(ctx)

	return errors.Join(fakeErr, dbErr) //nolint:wrapcheck // joins this function's own errors
}

func (b *backend) env() []string {
	return subprocessEnv(b.db.URL().Reveal(), b.masterKey, b.coverDir)
}

func (b *backend) command(ctx context.Context, args ...string) *exec.Cmd {
	//nolint:gosec // b.bin is the binary this harness built
	cmd := exec.CommandContext(ctx, b.bin, args...)
	cmd.Env = b.env()
	cmd.SysProcAttr = processAttributes()

	return cmd
}

// cliResult is what a finished chatisimo command printed.
type cliResult struct {
	stdout string
	stderr string
	code   int
}

// runCLI runs a chatisimo command to the end with stdin as its input.
func (b *backend) runCLI(ctx context.Context, stdin string, args ...string) (cliResult, error) {
	ctx, cancel := context.WithTimeout(ctx, configureTimeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	cmd := b.command(ctx, args...)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := cliResult{stdout: stdout.String(), stderr: stderr.String()}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.code = exitErr.ExitCode()

		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("run chatisimo %s: %w", strings.Join(args, " "), err)
	}

	return result, nil
}

const sharedUCFriendID = "199000000000000042"

// configure stores the fake's token and REST base URL through the CLI, which reads each from
// stdin. All commands must succeed and print nothing of the token.
func (b *backend) configure(ctx context.Context) error {
	settings := []struct{ key, value string }{
		{"discord.token", b.fake.Token()},
		{"discord.api_base_url", b.fake.RESTBase()},
		{"llm.api_key", b.fake.Token()},
		{"llm.base_url", b.fake.LLMBase()},
		{"uc.api_base_url", b.fake.UCBase()},
		{"uc.friend_user_id", sharedUCFriendID},
	}

	var problems []error
	for _, setting := range settings {
		result, err := b.runCLI(ctx, setting.value, "config", "set", setting.key)
		if err != nil {
			return err
		}

		if result.code != 0 {
			problems = append(problems, fmt.Errorf("config set %s exited %d: %s", setting.key, result.code, result.stderr))
		}

		printed := result.stdout + result.stderr
		if strings.Contains(printed, b.fake.Token()) {
			problems = append(problems, fmt.Errorf("config set %s printed the token", setting.key))
		}
	}

	return errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
}

// process is a chatisimo run started in the background, with its stderr in a file.
type process struct {
	cmd     *exec.Cmd
	logPath string
	done    chan struct{}
}

// start launches chatisimo run with stderr written to logPath.
func (b *backend) start(logPath string) (*process, error) {
	//nolint:gosec // logPath is chosen by the harness
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, fmt.Errorf("create log file: %w", err)
	}

	// The process is stopped by stopGracefully or kill, never by a context.
	cmd := b.command(context.Background(), "run")
	cmd.Stderr = logFile

	err = cmd.Start()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("start chatisimo run: %w", err), logFile.Close()) //nolint:wrapcheck // joins this function's own errors
	}

	p := &process{cmd: cmd, logPath: logPath, done: make(chan struct{})}
	go func() {
		// The exit code is read from ProcessState; Wait's error only repeats it.
		_ = cmd.Wait()
		// The child has exited, so nothing writes to the file any more.
		_ = logFile.Close()
		close(p.done)
	}()

	return p, nil
}

func (p *process) hasExited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// exitCode is the exit code of a finished process.
func (p *process) exitCode() int {
	return p.cmd.ProcessState.ExitCode()
}

// kill ends the process at once and waits for it.
func (p *process) kill() {
	// The process may have exited already; there is nothing to report either way.
	_ = p.cmd.Process.Kill()
	<-p.done
}

// waitExit waits for the process to exit on its own and returns its exit code.
func (p *process) waitExit(timeout time.Duration) (int, error) {
	select {
	case <-p.done:
		return p.exitCode(), nil
	case <-time.After(timeout):
		p.kill()

		return 0, fmt.Errorf("chatisimo did not exit within %s", timeout)
	}
}

// stopGracefully signals the process and requires exit code 0 within timeout. A process that
// is killed after the timeout has written no coverage, so that fails the run.
func (p *process) stopGracefully(timeout time.Duration) error {
	if p.hasExited() {
		return fmt.Errorf("chatisimo exited by itself with code %d", p.exitCode())
	}

	err := sendStop(p.cmd.Process)
	if err != nil {
		p.kill()

		return err
	}

	code, err := p.waitExit(timeout)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("chatisimo exited with code %d after the stop signal, want 0", code)
	}

	return nil
}
