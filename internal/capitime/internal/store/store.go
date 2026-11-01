// Package store persists Capi Time per-guild configuration in the capitime Postgres schema.
package store

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Migrations returns the embedded SQL migration files for the capitime schema.
func Migrations() (fs.FS, error) {
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("capitime store migrations sub-fs: %w", err)
	}

	return sub, nil
}

// GuildConfigRow represents a stored row in capitime.guild_config.
type GuildConfigRow struct {
	GuildID           string
	TargetUserID      string
	EnabledChannelIDs []string
	Locale            string
	UpdatedAt         time.Time
}

// GuildConfigPatch describes a partial update to capitime.guild_config.
type GuildConfigPatch struct {
	SetTargetUserID bool
	TargetUserID    string
	AddChannelID    string
	ClearChannels   bool
	SetLocale       bool
	Locale          string
}

func (p GuildConfigPatch) isEmpty() bool {
	return !p.SetTargetUserID && p.AddChannelID == "" && !p.ClearChannels && !p.SetLocale
}

func (p GuildConfigPatch) isClearAll() bool {
	return p.SetTargetUserID && p.TargetUserID == "" &&
		p.ClearChannels && p.AddChannelID == "" &&
		p.SetLocale && p.Locale == ""
}

// Store executes SQL queries against the capitime schema.
type Store struct {
	pool *pgxpool.Pool
}

// New creates a Store backed by pool.
func New(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("capitime store: pool is nil")
	}

	s := &Store{pool: pool}
	return s, nil
}

// GetGuildConfig returns the stored configuration for guildID, or a zero-value configuration with
// GuildID populated when the guild has no row.
func (s *Store) GetGuildConfig(ctx context.Context, guildID string) (GuildConfigRow, error) {
	var problems []error
	if ctx == nil {
		problems = append(problems, errors.New("capitime store: context is nil"))
	}
	if s == nil || s.pool == nil {
		problems = append(problems, errors.New("capitime store: pool is nil"))
	}
	if guildID == "" {
		problems = append(problems, errors.New("capitime store: guildID is empty"))
	}
	if len(problems) > 0 {
		return GuildConfigRow{}, errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
	}

	const query = `
SELECT guild_id, target_user_id, enabled_channel_ids, locale, updated_at
FROM capitime.guild_config
WHERE guild_id = $1`

	var cfg GuildConfigRow
	err := s.pool.QueryRow(ctx, query, guildID).Scan(
		&cfg.GuildID,
		&cfg.TargetUserID,
		&cfg.EnabledChannelIDs,
		&cfg.Locale,
		&cfg.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		empty := GuildConfigRow{
			GuildID:           guildID,
			EnabledChannelIDs: []string{},
		}
		return empty, nil
	}
	if err != nil {
		return GuildConfigRow{}, fmt.Errorf("capitime store: get guild config: %w", err)
	}
	if cfg.EnabledChannelIDs == nil {
		cfg.EnabledChannelIDs = []string{}
	}

	return cfg, nil
}

// ApplyGuildConfigPatch applies a partial update to guildID's configuration and deletes the row if
// all fields become empty.
func (s *Store) ApplyGuildConfigPatch(
	ctx context.Context,
	guildID string,
	patch GuildConfigPatch,
	updatedAt time.Time,
) (GuildConfigRow, error) {
	var problems []error
	if ctx == nil {
		problems = append(problems, errors.New("capitime store: context is nil"))
	}
	if s == nil || s.pool == nil {
		problems = append(problems, errors.New("capitime store: pool is nil"))
	}
	if guildID == "" {
		problems = append(problems, errors.New("capitime store: guildID is empty"))
	}
	if patch.isEmpty() {
		problems = append(problems, errors.New("capitime store: guild config patch is empty"))
	}
	if len(problems) > 0 {
		return GuildConfigRow{}, errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
	}

	if patch.isClearAll() {
		_, err := s.pool.Exec(ctx, "DELETE FROM capitime.guild_config WHERE guild_id = $1", guildID)
		if err != nil {
			return GuildConfigRow{}, fmt.Errorf("capitime store: delete guild config: %w", err)
		}

		empty := GuildConfigRow{
			GuildID:           guildID,
			EnabledChannelIDs: []string{},
		}
		return empty, nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return GuildConfigRow{}, fmt.Errorf("capitime store: begin guild config tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	current, found, err := loadGuildConfigForUpdate(ctx, tx, guildID)
	if err != nil {
		return GuildConfigRow{}, err
	}

	next := mergeGuildConfig(guildID, current, found, patch, updatedAt.UTC())
	if next.TargetUserID == "" && len(next.EnabledChannelIDs) == 0 && next.Locale == "" {
		if found {
			_, delErr := tx.Exec(ctx, "DELETE FROM capitime.guild_config WHERE guild_id = $1", guildID)
			if delErr != nil {
				return GuildConfigRow{}, fmt.Errorf("capitime store: delete empty guild config: %w", delErr)
			}
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return GuildConfigRow{}, fmt.Errorf("capitime store: commit empty guild config: %w", commitErr)
		}

		empty := GuildConfigRow{
			GuildID:           guildID,
			EnabledChannelIDs: []string{},
		}
		return empty, nil
	}

	const upsertQuery = `
INSERT INTO capitime.guild_config (guild_id, target_user_id, enabled_channel_ids, locale, updated_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (guild_id) DO UPDATE
SET target_user_id      = EXCLUDED.target_user_id,
    enabled_channel_ids = EXCLUDED.enabled_channel_ids,
    locale              = EXCLUDED.locale,
    updated_at          = EXCLUDED.updated_at
RETURNING guild_id, target_user_id, enabled_channel_ids, locale, updated_at`

	var saved GuildConfigRow
	err = tx.QueryRow(
		ctx,
		upsertQuery,
		next.GuildID,
		next.TargetUserID,
		next.EnabledChannelIDs,
		next.Locale,
		next.UpdatedAt,
	).Scan(
		&saved.GuildID,
		&saved.TargetUserID,
		&saved.EnabledChannelIDs,
		&saved.Locale,
		&saved.UpdatedAt,
	)
	if err != nil {
		return GuildConfigRow{}, fmt.Errorf("capitime store: upsert guild config: %w", err)
	}

	if commitErr := tx.Commit(ctx); commitErr != nil {
		return GuildConfigRow{}, fmt.Errorf("capitime store: commit guild config: %w", commitErr)
	}
	if saved.EnabledChannelIDs == nil {
		saved.EnabledChannelIDs = []string{}
	}

	return saved, nil
}

func loadGuildConfigForUpdate(ctx context.Context, tx pgx.Tx, guildID string) (GuildConfigRow, bool, error) {
	const selectQuery = `
SELECT guild_id, target_user_id, enabled_channel_ids, locale, updated_at
FROM capitime.guild_config
WHERE guild_id = $1
FOR UPDATE`

	var row GuildConfigRow
	err := tx.QueryRow(ctx, selectQuery, guildID).Scan(
		&row.GuildID,
		&row.TargetUserID,
		&row.EnabledChannelIDs,
		&row.Locale,
		&row.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return GuildConfigRow{GuildID: guildID, EnabledChannelIDs: []string{}}, false, nil
	}
	if err != nil {
		return GuildConfigRow{}, false, fmt.Errorf("capitime store: lock guild config: %w", err)
	}

	return row, true, nil
}

func mergeGuildConfig(
	guildID string,
	current GuildConfigRow,
	found bool,
	patch GuildConfigPatch,
	updatedAt time.Time,
) GuildConfigRow {
	next := GuildConfigRow{
		GuildID:           guildID,
		TargetUserID:      current.TargetUserID,
		EnabledChannelIDs: slices.Clone(current.EnabledChannelIDs),
		Locale:            current.Locale,
		UpdatedAt:         updatedAt,
	}
	if !found || next.EnabledChannelIDs == nil {
		next.EnabledChannelIDs = []string{}
	}

	if patch.SetTargetUserID {
		next.TargetUserID = patch.TargetUserID
	}
	if patch.ClearChannels {
		next.EnabledChannelIDs = []string{}
	}
	if patch.AddChannelID != "" && !slices.Contains(next.EnabledChannelIDs, patch.AddChannelID) {
		next.EnabledChannelIDs = append(next.EnabledChannelIDs, patch.AddChannelID)
	}
	if patch.SetLocale {
		next.Locale = patch.Locale
	}

	return next
}
