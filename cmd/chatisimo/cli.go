package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/alecthomas/kong"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/HectorVeraO/chatisimo/internal/platform/config"
	"github.com/HectorVeraO/chatisimo/internal/platform/crypto"
)

const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

const redactedValue = "[redacted]"

// cli is the command tree. No flag ever carries a secret: values arrive on stdin.
type cli struct {
	Version versionCmd `cmd:"" help:"Print the build version."`
	Migrate migrateCmd `cmd:"" help:"Apply pending migrations and exit."`
	Config  struct {
		Set  configSetCmd  `cmd:"" help:"Set a setting; the value is read from stdin."`
		Get  configGetCmd  `cmd:"" help:"Print a setting; secrets print [redacted]."`
		List configListCmd `cmd:"" help:"List every setting."`
	} `cmd:"" help:"Read and write settings stored in the database."`
	Keygen keygenCmd `cmd:"" help:"Print a new random MASTER_KEY."`
	Run    runCmd    `cmd:"" help:"Serve Discord and run timed jobs until SIGINT or SIGTERM."`
}

// app is what a command needs from its process: its streams and its environment lookup.
type app struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	lookup func(string) (string, bool)
}

// usageError marks an error the caller caused by the way it invoked a command (exit code 2).
type usageError struct {
	err error
}

func (e *usageError) Error() string {
	return e.err.Error()
}

func (e *usageError) Unwrap() error {
	return e.err
}

type (
	versionCmd   struct{}
	migrateCmd   struct{}
	keygenCmd    struct{}
	runCmd       struct{}
	configSetCmd struct {
		Key string `arg:"" help:"Setting name, such as discord.token."`
	}
	configGetCmd struct {
		Key string `arg:"" help:"Setting name, such as discord.api_base_url."`
	}
	configListCmd struct{}
)

// run executes the CLI with the arguments after the program name and returns the exit code.
// Output goes only to stdout and stderr; the environment is read only through lookup.
func run(
	ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer,
	lookup func(string) (string, bool),
) int {
	var (
		tree     cli
		hasExit  bool
		exitCode int
	)

	process := &app{stdin: stdin, stdout: stdout, stderr: stderr, lookup: lookup}

	parser, err := kong.New(&tree,
		kong.Name("chatisimo"),
		kong.Writers(stdout, stderr),
		kong.Exit(func(code int) {
			hasExit = true
			exitCode = code
		}),
		kong.BindTo(ctx, (*context.Context)(nil)),
		kong.Bind(process),
	)
	if err != nil {
		reportError(stderr, err)

		return exitFailure
	}

	parsed, parseErr := parser.Parse(args)
	if hasExit {
		return exitCode
	}

	if parseErr != nil {
		reportError(stderr, parseErr)

		return exitUsage
	}

	runErr := parsed.Run()
	if runErr != nil {
		reportError(stderr, runErr)

		return exitCodeFor(runErr)
	}

	return exitOK
}

func exitCodeFor(err error) int {
	var usage *usageError
	if errors.As(err, &usage) {
		return exitUsage
	}

	return exitFailure
}

// reportError prints one "chatisimo: <line>" per line of err, which covers errors.Join.
func reportError(stderr io.Writer, err error) {
	for line := range strings.SplitSeq(err.Error(), "\n") {
		// A failed write to stderr cannot be reported anywhere else.
		_, _ = fmt.Fprintf(stderr, "chatisimo: %s\n", line)
	}
}

// Run prints the build version.
func (c *versionCmd) Run(a *app) error {
	_, err := fmt.Fprintln(a.stdout, version)
	if err != nil {
		return fmt.Errorf("print version: %w", err)
	}

	return nil
}

// Run prints a new random MASTER_KEY.
func (c *keygenCmd) Run(a *app) error {
	key, err := crypto.GenerateMasterKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate master key: %w", err)
	}

	_, err = fmt.Fprintln(a.stdout, key)
	if err != nil {
		return fmt.Errorf("print master key: %w", err)
	}

	return nil
}

// Run applies pending migrations.
func (c *migrateCmd) Run(ctx context.Context, a *app) error {
	return withDatabase(ctx, a.lookup, a.stderr, func(_ config.Env, _ *pgxpool.Pool) error {
		_, err := fmt.Fprintln(a.stdout, "migrations up to date")
		if err != nil {
			return fmt.Errorf("print result: %w", err)
		}

		return nil
	})
}

// unknownKeyError is the usage error for a name outside the registry.
func unknownKeyError(name string) error {
	return &usageError{err: fmt.Errorf("unknown setting %q", name)}
}

// Run reads the value from stdin and stores it. The key is checked before stdin is read.
func (c *configSetCmd) Run(ctx context.Context, a *app) error {
	key, isKnown := findSettingKey(c.Key)
	if !isKnown {
		return unknownKeyError(c.Key)
	}

	return withStore(ctx, a.lookup, a.stderr, func(store *config.Store) error {
		raw, err := io.ReadAll(io.LimitReader(a.stdin, config.MaxInputBytes+1))
		if err != nil {
			return fmt.Errorf("read value from stdin: %w", err)
		}

		if len(raw) > config.MaxInputBytes {
			return fmt.Errorf("value from stdin is longer than %d bytes", config.MaxInputBytes)
		}

		value, err := config.ParseInput(raw, key.Secret)
		if err != nil {
			return fmt.Errorf("setting %s: %w", key.Name, err)
		}

		err = store.Set(ctx, key.Name, value)
		if err != nil {
			return fmt.Errorf("set %s: %w", key.Name, err)
		}

		_, err = fmt.Fprintf(a.stdout, "%s set\n", key.Name)
		if err != nil {
			return fmt.Errorf("print result: %w", err)
		}

		return nil
	})
}

// Run prints a plain setting's value, or [redacted] for a secret that is set.
func (c *configGetCmd) Run(ctx context.Context, a *app) error {
	key, isKnown := findSettingKey(c.Key)
	if !isKnown {
		return unknownKeyError(c.Key)
	}

	return withStore(ctx, a.lookup, a.stderr, func(store *config.Store) error {
		value, err := readForDisplay(ctx, store, key)
		if err != nil {
			return err
		}

		_, err = fmt.Fprintln(a.stdout, value)
		if err != nil {
			return fmt.Errorf("print value: %w", err)
		}

		return nil
	})
}

// readForDisplay returns what get may print for key; a secret is never read, only its status.
func readForDisplay(ctx context.Context, store *config.Store, key config.Key) (string, error) {
	if !key.Secret {
		value, err := store.Get(ctx, key.Name)
		if err != nil {
			return "", fmt.Errorf("get %s: %w", key.Name, err)
		}

		return value, nil
	}

	statuses, err := store.List(ctx)
	if err != nil {
		return "", fmt.Errorf("get %s: %w", key.Name, err)
	}

	for _, status := range statuses {
		isSetTarget := status.Name == key.Name && status.Set
		if isSetTarget {
			return redactedValue, nil
		}
	}

	return "", fmt.Errorf("%s is not set", key.Name)
}

// Run prints one line per setting.
func (c *configListCmd) Run(ctx context.Context, a *app) error {
	return withStore(ctx, a.lookup, a.stderr, func(store *config.Store) error {
		statuses, err := store.List(ctx)
		if err != nil {
			return fmt.Errorf("list settings: %w", err)
		}

		for _, status := range statuses {
			_, err = fmt.Fprintln(a.stdout, formatStatus(status))
			if err != nil {
				return fmt.Errorf("print settings: %w", err)
			}
		}

		return nil
	})
}

// formatStatus renders name, kind, value and doc separated by tabs.
func formatStatus(status config.Status) string {
	kind := "plain"
	if status.Secret {
		kind = "secret"
	}

	value := "(unset)"

	switch {
	case status.Set && status.Secret:
		value = redactedValue
	case status.Set:
		value = status.Value
	case status.Default != "":
		value = status.Default + " (default)"
	}

	return strings.Join([]string{status.Name, kind, value, status.Doc}, "\t")
}

// Run serves Discord and the timed jobs until SIGINT, SIGTERM or the end of ctx.
func (c *runCmd) Run(ctx context.Context, a *app) error {
	signalCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	return serve(signalCtx, stop, a)
}
