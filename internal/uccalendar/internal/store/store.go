// Package store holds the schema, goose migrations, per-guild configuration queries, and alert
// deduplication queries of the uccalendar module.
package store

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Migrations returns the goose migration files of schema uccalendar, rooted at the directory
// that holds them.
func Migrations() (fs.FS, error) {
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("uccalendar migrations: %w", err)
	}

	return sub, nil
}

// GuildConfig holds the persisted per-guild UC calendar configuration.
type GuildConfig struct {
	GuildID      string
	FriendUserID string
	ChannelID    string
	Locale       string
	UpdatedAt    time.Time
}

// GuildConfigPatch specifies a partial or full update to a guild's UC calendar configuration.
// Setting FriendUserID, ChannelID, or Locale to "" when the corresponding Set* flag is true clears
// that field; when all resulting fields are empty, the row is removed.
type GuildConfigPatch struct {
	SetFriendUserID bool
	FriendUserID    string
	SetChannelID    bool
	ChannelID       string
	SetLocale       bool
	Locale          string
}

// Store persists per-guild UC calendar configuration and proactive alert deduplication claims in
// schema uccalendar.
type Store struct {
	pool *pgxpool.Pool
}

// New constructs a Store backed by pool.
func New(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("uccalendar store: nil pool")
	}

	st := &Store{pool: pool}

	return st, nil
}

// GetGuildConfig returns the stored configuration for guildID, or a zero-value FriendUserID and
// ChannelID with GuildID populated when the guild has no stored configuration.
func (s *Store) GetGuildConfig(ctx context.Context, guildID string) (GuildConfig, error) {
	var problems []error
	if ctx == nil {
		problems = append(problems, errors.New("uccalendar store: nil context"))
	}

	if s == nil || s.pool == nil {
		problems = append(problems, errors.New("uccalendar store: nil pool"))
	}

	if guildID == "" {
		problems = append(problems, errors.New("uccalendar store: empty guild id"))
	}

	if len(problems) > 0 {
		return GuildConfig{}, errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
	}

	var cfg GuildConfig
	cfg.GuildID = guildID

	err := s.pool.QueryRow(
		ctx,
		`SELECT friend_user_id, channel_id, locale, updated_at
		 FROM uccalendar.guild_config
		 WHERE guild_id = $1`,
		guildID,
	).Scan(&cfg.FriendUserID, &cfg.ChannelID, &cfg.Locale, &cfg.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return GuildConfig{GuildID: guildID}, nil
		}

		return GuildConfig{}, fmt.Errorf("get guild config: %w", err)
	}

	return cfg, nil
}

// ApplyGuildConfigPatch atomically updates or clears the selected configuration fields for
// guildID and returns the resulting GuildConfig. If FriendUserID, ChannelID, and Locale all end
// up empty, the row is removed from uccalendar.guild_config.
func (s *Store) ApplyGuildConfigPatch(
	ctx context.Context,
	guildID string,
	patch GuildConfigPatch,
	updatedAt time.Time,
) (GuildConfig, error) {
	var problems []error
	if ctx == nil {
		problems = append(problems, errors.New("uccalendar store: nil context"))
	}

	if s == nil || s.pool == nil {
		problems = append(problems, errors.New("uccalendar store: nil pool"))
	}

	if guildID == "" {
		problems = append(problems, errors.New("uccalendar store: empty guild id"))
	}

	if !patch.SetFriendUserID && !patch.SetChannelID && !patch.SetLocale {
		problems = append(problems, errors.New("uccalendar store: empty guild config patch"))
	}

	if len(problems) > 0 {
		return GuildConfig{}, errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
	}

	ts := updatedAt.UTC()
	hasNonEmptyValue := (patch.SetFriendUserID && patch.FriendUserID != "") ||
		(patch.SetChannelID && patch.ChannelID != "") ||
		(patch.SetLocale && patch.Locale != "")

	if hasNonEmptyValue {
		return s.upsertGuildConfig(ctx, guildID, patch, ts)
	}

	clearsAll := patch.SetFriendUserID && patch.SetChannelID && patch.SetLocale
	if clearsAll {
		_, err := s.pool.Exec(
			ctx,
			`DELETE FROM uccalendar.guild_config WHERE guild_id = $1`,
			guildID,
		)
		if err != nil {
			return GuildConfig{}, fmt.Errorf("delete guild config: %w", err)
		}

		return GuildConfig{GuildID: guildID}, nil
	}

	return s.clearSingleGuildConfigField(ctx, guildID, patch, ts)
}

func (s *Store) upsertGuildConfig(
	ctx context.Context,
	guildID string,
	patch GuildConfigPatch,
	updatedAt time.Time,
) (GuildConfig, error) {
	insertFriend := ""
	if patch.SetFriendUserID {
		insertFriend = patch.FriendUserID
	}

	insertChannel := ""
	if patch.SetChannelID {
		insertChannel = patch.ChannelID
	}

	insertLocale := ""
	if patch.SetLocale {
		insertLocale = patch.Locale
	}

	cfg := GuildConfig{GuildID: guildID}
	err := s.pool.QueryRow(
		ctx,
		`INSERT INTO uccalendar.guild_config (guild_id, friend_user_id, channel_id, locale, updated_at)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (guild_id) DO UPDATE SET
		     friend_user_id = CASE
		         WHEN $6 THEN EXCLUDED.friend_user_id
		         ELSE uccalendar.guild_config.friend_user_id
		     END,
		     channel_id = CASE
		         WHEN $7 THEN EXCLUDED.channel_id
		         ELSE uccalendar.guild_config.channel_id
		     END,
		     locale = CASE
		         WHEN $8 THEN EXCLUDED.locale
		         ELSE uccalendar.guild_config.locale
		     END,
		     updated_at = EXCLUDED.updated_at
		 RETURNING friend_user_id, channel_id, locale, updated_at`,
		guildID,
		insertFriend,
		insertChannel,
		insertLocale,
		updatedAt,
		patch.SetFriendUserID,
		patch.SetChannelID,
		patch.SetLocale,
	).Scan(&cfg.FriendUserID, &cfg.ChannelID, &cfg.Locale, &cfg.UpdatedAt)
	if err != nil {
		return GuildConfig{}, fmt.Errorf("upsert guild config: %w", err)
	}

	return cfg, nil
}

func (s *Store) clearSingleGuildConfigField(
	ctx context.Context,
	guildID string,
	patch GuildConfigPatch,
	updatedAt time.Time,
) (GuildConfig, error) {
	cfg := GuildConfig{GuildID: guildID}
	err := s.pool.QueryRow(
		ctx,
		`UPDATE uccalendar.guild_config
		 SET friend_user_id = CASE WHEN $2 THEN '' ELSE friend_user_id END,
		     channel_id     = CASE WHEN $3 THEN '' ELSE channel_id END,
		     locale         = CASE WHEN $4 THEN '' ELSE locale END,
		     updated_at     = $5
		 WHERE guild_id = $1
		 RETURNING friend_user_id, channel_id, locale, updated_at`,
		guildID,
		patch.SetFriendUserID,
		patch.SetChannelID,
		patch.SetLocale,
		updatedAt,
	).Scan(&cfg.FriendUserID, &cfg.ChannelID, &cfg.Locale, &cfg.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return GuildConfig{GuildID: guildID}, nil
		}

		return GuildConfig{}, fmt.Errorf("clear guild config field: %w", err)
	}

	isNowEmpty := cfg.FriendUserID == "" && cfg.ChannelID == "" && cfg.Locale == ""
	if isNowEmpty {
		_, delErr := s.pool.Exec(
			ctx,
			`DELETE FROM uccalendar.guild_config
			 WHERE guild_id = $1 AND friend_user_id = '' AND channel_id = '' AND locale = ''`,
			guildID,
		)
		if delErr != nil {
			return GuildConfig{}, fmt.Errorf("prune empty guild config: %w", delErr)
		}

		return GuildConfig{GuildID: guildID}, nil
	}

	return cfg, nil
}

// ListAlertGuildConfigs returns all guild configurations with a non-empty proactive alert
// ChannelID, ordered deterministically by GuildID.
func (s *Store) ListAlertGuildConfigs(ctx context.Context) ([]GuildConfig, error) {
	var problems []error
	if ctx == nil {
		problems = append(problems, errors.New("uccalendar store: nil context"))
	}

	if s == nil || s.pool == nil {
		problems = append(problems, errors.New("uccalendar store: nil pool"))
	}

	if len(problems) > 0 {
		return nil, errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
	}

	rows, err := s.pool.Query(
		ctx,
		`SELECT guild_id, friend_user_id, channel_id, locale, updated_at
		 FROM uccalendar.guild_config
		 WHERE channel_id <> ''
		 ORDER BY guild_id`,
	)
	if err != nil {
		return nil, fmt.Errorf("list alert guild configs: %w", err)
	}
	defer rows.Close()

	var configs []GuildConfig
	for rows.Next() {
		var cfg GuildConfig
		scanErr := rows.Scan(&cfg.GuildID, &cfg.FriendUserID, &cfg.ChannelID, &cfg.Locale, &cfg.UpdatedAt)
		if scanErr != nil {
			return nil, fmt.Errorf("scan alert guild config: %w", scanErr)
		}

		configs = append(configs, cfg)
	}

	if rows.Err() != nil {
		return nil, fmt.Errorf("iterate alert guild configs: %w", rows.Err())
	}

	return configs, nil
}

// ClaimAlert atomically records alertKey as sent for guildID at sentAt. It returns true when this
// call won the claim, and false when (guildID, alertKey) was already present.
func (s *Store) ClaimAlert(ctx context.Context, guildID, alertKey string, sentAt time.Time) (bool, error) {
	var problems []error
	if ctx == nil {
		problems = append(problems, errors.New("uccalendar store: nil context"))
	}

	if s == nil || s.pool == nil {
		problems = append(problems, errors.New("uccalendar store: nil pool"))
	}

	if guildID == "" {
		problems = append(problems, errors.New("uccalendar store: empty guild id"))
	}

	if alertKey == "" {
		problems = append(problems, errors.New("uccalendar store: empty alert key"))
	}

	if len(problems) > 0 {
		return false, errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
	}

	tag, err := s.pool.Exec(
		ctx,
		`INSERT INTO uccalendar.sent_alert (guild_id, alert_key, sent_at)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (guild_id, alert_key) DO NOTHING`,
		guildID,
		alertKey,
		sentAt.UTC(),
	)
	if err != nil {
		return false, fmt.Errorf("claim alert %s: %w", alertKey, err)
	}

	claimed := tag.RowsAffected() == 1

	return claimed, nil
}

// ReleaseAlert removes (guildID, alertKey) so a failed notification delivery for that guild can
// be retried on a subsequent check without affecting other guilds.
func (s *Store) ReleaseAlert(ctx context.Context, guildID, alertKey string) error {
	var problems []error
	if ctx == nil {
		problems = append(problems, errors.New("uccalendar store: nil context"))
	}

	if s == nil || s.pool == nil {
		problems = append(problems, errors.New("uccalendar store: nil pool"))
	}

	if guildID == "" {
		problems = append(problems, errors.New("uccalendar store: empty guild id"))
	}

	if alertKey == "" {
		problems = append(problems, errors.New("uccalendar store: empty alert key"))
	}

	if len(problems) > 0 {
		return errors.Join(problems...) //nolint:wrapcheck // joins this function's own errors
	}

	_, err := s.pool.Exec(
		ctx,
		`DELETE FROM uccalendar.sent_alert
		 WHERE guild_id = $1 AND alert_key = $2`,
		guildID,
		alertKey,
	)
	if err != nil {
		return fmt.Errorf("release alert %s: %w", alertKey, err)
	}

	return nil
}
