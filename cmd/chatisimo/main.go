// Command chatisimo is the Chatisimo binary.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// version is the build version, set at link time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	code := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.LookupEnv)

	stop()
	os.Exit(code)
}
