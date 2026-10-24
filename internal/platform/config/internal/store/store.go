// Package store holds the migrations of schema platform.
package store

import (
	"embed"
	"fmt"
	"io/fs"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Migrations returns the goose migration files of schema platform, rooted at the directory that
// holds them.
func Migrations() (fs.FS, error) {
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("platform migrations: %w", err)
	}

	return sub, nil
}
