// Command fakediscord serves the fake Discord REST API and Gateway for the local end-to-end
// stack. It holds only synthetic data.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/HectorVeraO/chatisimo/internal/fakediscord"
)

const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2

	defaultListen            = "127.0.0.1:10010"
	defaultHeartbeatInterval = 41250 * time.Millisecond
	readHeaderTimeout        = 5 * time.Second
	shutdownTimeout          = 5 * time.Second
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run serves the fake until ctx ends and returns the process exit code. Log lines go to
// stderr and carry only the method, route pattern and status of each request.
func run(ctx context.Context, args []string, _, stderr io.Writer) int {
	flags := flag.NewFlagSet("fakediscord", flag.ContinueOnError)
	flags.SetOutput(stderr)
	listen := flags.String("listen", defaultListen, "address to listen on")
	heartbeat := flags.Duration("heartbeat-interval", defaultHeartbeatInterval, "Gateway heartbeat interval")
	err := flags.Parse(args)
	if errors.Is(err, flag.ErrHelp) {
		return exitOK
	}
	if err != nil {
		return exitUsage
	}
	if flags.NArg() > 0 {
		// If stderr is unwritable there is nowhere left to report it; the exit code still says usage.
		_, _ = fmt.Fprintf(stderr, "unexpected argument: %q\n", flags.Arg(0))
		return exitUsage
	}

	logger := slog.New(slog.NewTextHandler(stderr, nil))
	serveErr := serve(ctx, logger, *listen, *heartbeat)
	if serveErr != nil {
		logger.Error("fakediscord failed", "error", serveErr)
		return exitFailure
	}

	return exitOK
}

func serve(ctx context.Context, logger *slog.Logger, listen string, heartbeat time.Duration) error {
	fake, err := fakediscord.New(fakediscord.Options{HeartbeatInterval: heartbeat, Logger: logger})
	if err != nil {
		return fmt.Errorf("create fake discord: %w", err)
	}

	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "tcp", listen)
	if err != nil {
		return fmt.Errorf("listen on %q: %w", listen, err)
	}

	server := &http.Server{Handler: fake.Handler(), ReadHeaderTimeout: readHeaderTimeout}
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- server.Serve(listener)
	}()
	logger.Info("listening", "addr", listener.Addr().String())

	select {
	case err = <-serveDone:
		// The serve error is the one reported; a close error would only mask it.
		_ = fake.Close()
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	// The shutdown context is separate because ctx is already cancelled.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	shutdownErr := server.Shutdown(shutdownCtx)
	closeErr := fake.Close()
	serveErr := <-serveDone
	logger.Info("stopped")

	failures := []error{wrapIf("shutdown", shutdownErr), wrapIf("close", closeErr)}
	if !errors.Is(serveErr, http.ErrServerClosed) {
		failures = append(failures, wrapIf("serve", serveErr))
	}
	joined := errors.Join(failures...)
	if joined != nil {
		return fmt.Errorf("stop fake discord: %w", joined)
	}

	return nil
}

func wrapIf(what string, err error) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf("%s: %w", what, err)
}
