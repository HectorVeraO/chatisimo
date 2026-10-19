// Command chatisimo is the Chatisimo bot platform binary.
package main

import (
	"fmt"
	"io"
	"os"
)

// version is the build version, set at link time with -ldflags "-X main.version=...".
var version = "dev"

const (
	exitOK    = 0
	exitUsage = 2
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
			return exitUsage
		}
		return exitOK
	default:
		return usage(stderr)
	}
}

func usage(stderr io.Writer) int {
	_, err := fmt.Fprintln(stderr, "usage: chatisimo <version>")
	if err != nil {
		return exitUsage
	}
	return exitUsage
}
