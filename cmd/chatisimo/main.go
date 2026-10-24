// Command chatisimo is the Chatisimo binary.
package main

import (
	"context"
	"os"

	// The alpine images ship no zoneinfo and the Discord adapter reads America/Santiago.
	_ "time/tzdata"
)

// version is the build version, set at link time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	code := run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.LookupEnv)

	os.Exit(code)
}
