package giftexchange

import (
	"fmt"

	"github.com/HectorVeraO/chatisimo/internal/giftexchange/internal/store"
	"github.com/HectorVeraO/chatisimo/internal/platform/db"
)

// MigrationSource returns the goose source of schema giftexchange for db.Migrate and pgtest.
func MigrationSource() (*db.GooseSource, error) {
	files, err := store.Migrations()
	if err != nil {
		return nil, fmt.Errorf("giftexchange migrations: %w", err)
	}

	source, err := db.NewGooseSource("giftexchange", files)
	if err != nil {
		return nil, fmt.Errorf("giftexchange migrations: %w", err)
	}

	return source, nil
}
