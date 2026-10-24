// Package store holds the schema, the migrations and the sqlc queries of the giftexchange module.
// Generated row types stay in package db, inside this package; this package maps them to domain
// types.
package store

import (
	"embed"
	"fmt"
	"io/fs"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Migrations returns the goose migration files of schema giftexchange, rooted at the directory
// that holds them.
func Migrations() (fs.FS, error) {
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("giftexchange migrations: %w", err)
	}

	return sub, nil
}
