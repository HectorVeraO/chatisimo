-- +goose Up
CREATE SCHEMA IF NOT EXISTS giftexchange;

CREATE TABLE giftexchange.persons (
    id         uuid        PRIMARY KEY,
    created_at timestamptz NOT NULL
);

CREATE TABLE giftexchange.external_identities (
    provider   text        NOT NULL CHECK (provider IN ('discord')),
    subject    text        NOT NULL CHECK (char_length(subject) BETWEEN 1 AND 128),
    person_id  uuid        NOT NULL REFERENCES giftexchange.persons (id),
    created_at timestamptz NOT NULL,
    PRIMARY KEY (provider, subject)
);
CREATE INDEX external_identities_person_id_idx
    ON giftexchange.external_identities (person_id);

CREATE TABLE giftexchange.groups (
    id         uuid        PRIMARY KEY,
    provider   text        NOT NULL CHECK (provider IN ('discord')),
    subject    text        NOT NULL CHECK (char_length(subject) BETWEEN 1 AND 128),
    created_at timestamptz NOT NULL,
    UNIQUE (provider, subject)
);

CREATE TABLE giftexchange.exchanges (
    id                       uuid        PRIMARY KEY,
    group_id                 uuid        NOT NULL REFERENCES giftexchange.groups (id),
    organizer_id             uuid        NOT NULL REFERENCES giftexchange.persons (id),
    name                     text        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    state                    text        NOT NULL
        CHECK (state IN ('open', 'drawn', 'revealed', 'cancelled')),
    round                    integer     NOT NULL CHECK (round >= 0),
    draw_at                  timestamptz,
    reveal_at                timestamptz,
    reminder_lead_seconds    integer     NOT NULL
        CHECK (reminder_lead_seconds BETWEEN 0 AND 1209600),
    dropout_policy           text        NOT NULL CHECK (dropout_policy IN ('patch', 'redraw')),
    avoid_last_round         boolean     NOT NULL,
    questions_enabled        boolean     NOT NULL,
    draw_reminder_sent_for   timestamptz,
    reveal_reminder_sent_for timestamptz,
    created_at               timestamptz NOT NULL,
    drawn_at                 timestamptz,
    revealed_at              timestamptz,
    cancelled_at             timestamptz,
    CONSTRAINT exchanges_draw_at_only_open CHECK (draw_at IS NULL OR state = 'open'),
    CONSTRAINT exchanges_reveal_after_draw
        CHECK (draw_at IS NULL OR reveal_at IS NULL OR reveal_at > draw_at),
    CONSTRAINT exchanges_drawn_has_round
        CHECK (state NOT IN ('drawn', 'revealed') OR (round >= 1 AND drawn_at IS NOT NULL)),
    CONSTRAINT exchanges_revealed_at_iff_revealed
        CHECK ((state = 'revealed') = (revealed_at IS NOT NULL)),
    CONSTRAINT exchanges_cancelled_at_iff_cancelled
        CHECK ((state = 'cancelled') = (cancelled_at IS NOT NULL))
);
CREATE INDEX exchanges_group_id_created_at_idx
    ON giftexchange.exchanges (group_id, created_at DESC);
CREATE INDEX exchanges_organizer_id_idx
    ON giftexchange.exchanges (organizer_id);
CREATE INDEX exchanges_last_revealed_idx
    ON giftexchange.exchanges (group_id, revealed_at DESC, id DESC)
    WHERE state = 'revealed';

CREATE TABLE giftexchange.participants (
    exchange_id uuid        NOT NULL REFERENCES giftexchange.exchanges (id),
    person_id   uuid        NOT NULL REFERENCES giftexchange.persons (id),
    joined_at   timestamptz NOT NULL,
    left_at     timestamptz,
    PRIMARY KEY (exchange_id, person_id),
    CONSTRAINT participants_left_after_join CHECK (left_at IS NULL OR left_at >= joined_at)
);
CREATE INDEX participants_person_id_idx
    ON giftexchange.participants (person_id);

CREATE TABLE giftexchange.exclusions (
    exchange_id  uuid        NOT NULL,
    giver_id     uuid        NOT NULL,
    recipient_id uuid        NOT NULL,
    created_at   timestamptz NOT NULL,
    PRIMARY KEY (exchange_id, giver_id, recipient_id),
    CONSTRAINT exclusions_distinct CHECK (giver_id <> recipient_id),
    FOREIGN KEY (exchange_id, giver_id)
        REFERENCES giftexchange.participants (exchange_id, person_id),
    FOREIGN KEY (exchange_id, recipient_id)
        REFERENCES giftexchange.participants (exchange_id, person_id)
);

CREATE TABLE giftexchange.assignments (
    exchange_id  uuid    NOT NULL,
    giver_id     uuid    NOT NULL,
    recipient_id uuid    NOT NULL,
    round        integer NOT NULL CHECK (round >= 1),
    PRIMARY KEY (exchange_id, giver_id),
    CONSTRAINT assignments_recipient_unique UNIQUE (exchange_id, recipient_id),
    CONSTRAINT assignments_no_self CHECK (giver_id <> recipient_id),
    FOREIGN KEY (exchange_id, giver_id)
        REFERENCES giftexchange.participants (exchange_id, person_id),
    FOREIGN KEY (exchange_id, recipient_id)
        REFERENCES giftexchange.participants (exchange_id, person_id)
);

CREATE TABLE giftexchange.questions (
    id           uuid        PRIMARY KEY,
    exchange_id  uuid        NOT NULL REFERENCES giftexchange.exchanges (id),
    round        integer     NOT NULL CHECK (round >= 1),
    asker_id     uuid        NOT NULL,
    recipient_id uuid        NOT NULL,
    text         text        NOT NULL CHECK (char_length(text) BETWEEN 1 AND 1000),
    asked_at     timestamptz NOT NULL,
    answer       text        CHECK (answer IS NULL OR char_length(answer) BETWEEN 1 AND 1000),
    answered_at  timestamptz,
    closed_at    timestamptz,
    CONSTRAINT questions_distinct CHECK (asker_id <> recipient_id),
    CONSTRAINT questions_answer_iff_answered_at CHECK ((answer IS NULL) = (answered_at IS NULL)),
    FOREIGN KEY (exchange_id, asker_id)
        REFERENCES giftexchange.participants (exchange_id, person_id),
    FOREIGN KEY (exchange_id, recipient_id)
        REFERENCES giftexchange.participants (exchange_id, person_id)
);
CREATE INDEX questions_asker_rate_idx
    ON giftexchange.questions (exchange_id, asker_id, asked_at);
CREATE INDEX questions_recipient_idx
    ON giftexchange.questions (exchange_id, recipient_id);

CREATE TABLE giftexchange.notifications (
    id          uuid        PRIMARY KEY,
    kind        text        NOT NULL CHECK (kind IN (
        'drawn', 'redrawn', 'assignment_changed', 'round_voided', 'draw_failed', 'draw_soon',
        'reveal_soon', 'revealed', 'cancelled', 'question_received', 'answer_received')),
    exchange_id uuid        NOT NULL REFERENCES giftexchange.exchanges (id),
    person_id   uuid        NOT NULL REFERENCES giftexchange.persons (id),
    round       integer     NOT NULL CHECK (round >= 0),
    question_id uuid        REFERENCES giftexchange.questions (id),
    reason      text        CHECK (reason IN ('too_few_participants', 'no_valid_assignment')),
    created_at  timestamptz NOT NULL,
    outcome     text        CHECK (outcome IN ('delivered', 'stale', 'undeliverable')),
    finished_at timestamptz,
    CONSTRAINT notifications_reason_kinds
        CHECK ((kind IN ('round_voided', 'draw_failed')) = (reason IS NOT NULL)),
    CONSTRAINT notifications_question_kinds
        CHECK ((kind IN ('question_received', 'answer_received')) = (question_id IS NOT NULL)),
    CONSTRAINT notifications_outcome_iff_finished CHECK ((outcome IS NULL) = (finished_at IS NULL))
);
CREATE INDEX notifications_exchange_id_idx
    ON giftexchange.notifications (exchange_id);

-- +goose Down
DROP TABLE giftexchange.notifications;
DROP TABLE giftexchange.questions;
DROP TABLE giftexchange.assignments;
DROP TABLE giftexchange.exclusions;
DROP TABLE giftexchange.participants;
DROP TABLE giftexchange.exchanges;
DROP TABLE giftexchange.groups;
DROP TABLE giftexchange.external_identities;
DROP TABLE giftexchange.persons;
