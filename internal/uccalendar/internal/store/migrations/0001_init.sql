-- +goose Up
CREATE SCHEMA IF NOT EXISTS uccalendar;

CREATE TABLE uccalendar.sent_alert (
    alert_key TEXT        PRIMARY KEY,
    sent_at   TIMESTAMPTZ NOT NULL
);

-- +goose Down
DROP TABLE uccalendar.sent_alert;
