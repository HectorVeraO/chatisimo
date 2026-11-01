package capitime

import (
	"fmt"

	"github.com/HectorVeraO/chatisimo/internal/capitime/internal/store"
	"github.com/HectorVeraO/chatisimo/internal/platform/db"
)

// MigrationSource returns the Goose migration source for the capitime schema.
func MigrationSource() (*db.GooseSource, error) {
	files, err := store.Migrations()
	if err != nil {
		return nil, fmt.Errorf("capitime migrations: %w", err)
	}

	src, err := db.NewGooseSource("capitime", files)
	if err != nil {
		return nil, fmt.Errorf("capitime goose source: %w", err)
	}

	return src, nil
}
