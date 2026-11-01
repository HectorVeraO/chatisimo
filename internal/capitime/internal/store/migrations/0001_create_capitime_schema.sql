-- +goose Up
CREATE SCHEMA IF NOT EXISTS capitime;

CREATE TABLE capitime.guild_config (
    guild_id            TEXT        PRIMARY KEY,
    target_user_id      TEXT        NOT NULL DEFAULT '',
    enabled_channel_ids TEXT[]      NOT NULL DEFAULT '{}',
    locale              TEXT        NOT NULL DEFAULT 'es-CL',
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE IF EXISTS capitime.guild_config;
DROP SCHEMA IF EXISTS capitime;
