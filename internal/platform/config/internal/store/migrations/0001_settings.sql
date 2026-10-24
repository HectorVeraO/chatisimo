-- +goose Up
CREATE SCHEMA IF NOT EXISTS platform;

CREATE TABLE platform.setting (
    key        text        PRIMARY KEY
        CHECK (key ~ '^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$'),
    value      text,
    sealed     bytea,
    updated_at timestamptz NOT NULL,
    CONSTRAINT setting_one_form CHECK ((value IS NULL) <> (sealed IS NULL))
);

CREATE TABLE platform.key_check (
    id         boolean     PRIMARY KEY DEFAULT true CHECK (id),
    sealed     bytea       NOT NULL,
    created_at timestamptz NOT NULL
);

-- +goose Down
DROP TABLE platform.key_check;
DROP TABLE platform.setting;
