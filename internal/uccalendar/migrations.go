package uccalendar

import (
	"fmt"

	"github.com/HectorVeraO/chatisimo/internal/platform/db"
	"github.com/HectorVeraO/chatisimo/internal/uccalendar/internal/store"
)

// MigrationSource returns the goose source of schema uccalendar for db.Migrate and pgtest.
func MigrationSource() (*db.GooseSource, error) {
	files, err := store.Migrations()
	if err != nil {
		return nil, fmt.Errorf("uccalendar migrations: %w", err)
	}

	source, err := db.NewGooseSource("uccalendar", files)
	if err != nil {
		return nil, fmt.Errorf("uccalendar migrations: %w", err)
	}

	return source, nil
}
