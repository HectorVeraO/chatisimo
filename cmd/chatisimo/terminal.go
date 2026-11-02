package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"

	"golang.org/x/term"
)

// errCancelled is returned when the user interrupts interactive input.
var errCancelled = errors.New("cancelled, nothing stored")

// terminal is the boundary for interactive console input.
type terminal interface {
	IsTerminal() bool
	ReadLine(ctx context.Context) (string, error)
	ReadPassword(ctx context.Context) (string, error)
}

// filer is implemented by os.File and types exposing an OS file descriptor.
type filer interface {
	Fd() uintptr
}

// isTerminalFile reports whether f is attached to an interactive terminal.
func isTerminalFile(f filer) bool {
	return term.IsTerminal(int(f.Fd()))
}

// osTerminal wraps an interactive OS terminal.
type osTerminal struct {
	fd int
	r  io.Reader
}

func newOSFileTerminal(f filer, r io.Reader) *osTerminal {
	return &osTerminal{
		fd: int(f.Fd()),
		r:  r,
	}
}

func (t *osTerminal) IsTerminal() bool {
	return term.IsTerminal(t.fd)
}

func (t *osTerminal) ReadPassword(ctx context.Context) (string, error) {
	type result struct {
		raw []byte
		err error
	}

	resCh := make(chan result, 1)
	go func() {
		raw, err := term.ReadPassword(t.fd)
		resCh <- result{raw: raw, err: err}
	}()

	select {
	case <-ctx.Done():
		return "", errCancelled
	case res := <-resCh:
		if res.err != nil {
			if errors.Is(res.err, io.EOF) && len(res.raw) == 0 {
				return "", errCancelled
			}

			return "", res.err
		}

		if strings.Contains(string(res.raw), "\x03") {
			return "", errCancelled
		}

		return string(res.raw), nil
	}
}

func (t *osTerminal) ReadLine(ctx context.Context) (string, error) {
	type result struct {
		line string
		err  error
	}

	resCh := make(chan result, 1)
	go func() {
		reader := bufio.NewReader(t.r)
		line, err := reader.ReadString('\n')
		resCh <- result{line: line, err: err}
	}()

	select {
	case <-ctx.Done():
		return "", errCancelled
	case res := <-resCh:
		if res.err != nil && !errors.Is(res.err, io.EOF) {
			return "", res.err
		}

		text := res.line
		if strings.Contains(text, "\x03") {
			return "", errCancelled
		}

		text = strings.TrimSuffix(text, "\r\n")
		text = strings.TrimSuffix(text, "\n")

		return text, nil
	}
}

// maskSecret returns secret with all but its last 4 characters masked with ****.
// If secret has 4 or fewer characters, it returns **** to avoid leaking short secrets.
func maskSecret(secret string) string {
	const (
		maskPrefix = "****"
		tailLen    = 4
	)

	if len(secret) <= tailLen {
		return maskPrefix
	}

	return maskPrefix + secret[len(secret)-tailLen:]
}
