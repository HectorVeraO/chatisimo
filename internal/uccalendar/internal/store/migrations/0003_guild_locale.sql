-- +goose Up
ALTER TABLE uccalendar.guild_config
    ADD COLUMN locale TEXT NOT NULL DEFAULT '';

-- +goose Down
DELETE FROM uccalendar.guild_config
WHERE friend_user_id = '' AND channel_id = '';

ALTER TABLE uccalendar.guild_config
    DROP COLUMN locale;
