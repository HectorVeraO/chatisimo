// Command chatisimo is the Chatisimo binary.
package main

import (
	"fmt"
	"io"
	"os"
)

// version is the build version, set at link time with -ldflags "-X main.version=...".
var version = "dev"

const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

func main() {
	code := run(os.Args[1:], os.Stdout, os.Stderr)
	os.Exit(code)
}

// run executes the CLI with the given arguments (without the program name)
// and returns the process exit code.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usage(stderr)
	}

	switch args[0] {
	case "version":
		_, err := fmt.Fprintln(stdout, version)
		if err != nil {
			return exitFailure
		}
		return exitOK
	default:
		return usage(stderr)
	}
}

// usage prints the command synopsis to stderr and returns the usage exit code.
func usage(stderr io.Writer) int {
	// The exit code already reports the failure, so a failed write to stderr changes nothing.
	_, _ = fmt.Fprintln(stderr, "usage: chatisimo version")
	return exitUsage
}
