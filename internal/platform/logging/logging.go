// Package logging builds the process logger.
package logging

import (
	"io"
	"log/slog"

	"github.com/HectorVeraO/chatisimo/internal/platform/config"
)

// New returns the process logger: JSON at Info for production, text at Debug for local. Any
// environment other than config.EnvLocal gets the production logger.
func New(w io.Writer, env config.Environment) *slog.Logger {
	if w == nil {
		panic("logging.New: nil writer")
	}

	if env == config.EnvLocal {
		handler := slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug})

		return slog.New(handler)
	}

	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo})

	return slog.New(handler)
}
