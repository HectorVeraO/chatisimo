-- +goose Up
CREATE TABLE uccalendar.guild_config (
    guild_id       TEXT        PRIMARY KEY,
    friend_user_id TEXT        NOT NULL DEFAULT '',
    channel_id     TEXT        NOT NULL DEFAULT '',
    updated_at     TIMESTAMPTZ NOT NULL
);

CREATE INDEX guild_config_active_channel_idx
    ON uccalendar.guild_config (guild_id)
    WHERE channel_id <> '';

ALTER TABLE uccalendar.sent_alert
    ADD COLUMN guild_id TEXT NOT NULL DEFAULT '',
    DROP CONSTRAINT sent_alert_pkey,
    ADD PRIMARY KEY (guild_id, alert_key);

ALTER TABLE uccalendar.sent_alert
    ALTER COLUMN guild_id DROP DEFAULT;

-- +goose Down
DROP TABLE uccalendar.guild_config;

DELETE FROM uccalendar.sent_alert AS older
USING uccalendar.sent_alert AS newer
WHERE older.alert_key = newer.alert_key
  AND (older.sent_at < newer.sent_at
       OR (older.sent_at = newer.sent_at AND older.guild_id < newer.guild_id));

ALTER TABLE uccalendar.sent_alert
    DROP CONSTRAINT sent_alert_pkey,
    DROP COLUMN guild_id,
    ADD PRIMARY KEY (alert_key);
