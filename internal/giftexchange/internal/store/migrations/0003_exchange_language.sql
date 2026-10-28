-- +goose Up
ALTER TABLE giftexchange.exchanges
    ADD COLUMN language text NOT NULL DEFAULT 'en'
        CHECK (language IN ('en', 'es-419'));

-- +goose Down
ALTER TABLE giftexchange.exchanges
    DROP COLUMN language;
