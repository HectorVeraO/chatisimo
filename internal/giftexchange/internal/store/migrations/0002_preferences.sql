-- +goose Up
ALTER TABLE giftexchange.participants
    ADD COLUMN likes    text NOT NULL DEFAULT '' CHECK (char_length(likes) <= 300),
    ADD COLUMN dislikes text NOT NULL DEFAULT '' CHECK (char_length(dislikes) <= 300),
    ADD COLUMN details  text NOT NULL DEFAULT '' CHECK (char_length(details) <= 300);

ALTER TABLE giftexchange.notifications
    DROP CONSTRAINT notifications_kind_check,
    ADD CONSTRAINT notifications_kind_check CHECK (kind IN (
        'drawn', 'redrawn', 'assignment_changed', 'round_voided', 'draw_failed', 'draw_soon',
        'reveal_soon', 'revealed', 'cancelled', 'question_received', 'answer_received',
        'preferences_updated'));

-- +goose Down
DELETE FROM giftexchange.notifications
WHERE kind = 'preferences_updated';

ALTER TABLE giftexchange.notifications
    DROP CONSTRAINT notifications_kind_check,
    ADD CONSTRAINT notifications_kind_check CHECK (kind IN (
        'drawn', 'redrawn', 'assignment_changed', 'round_voided', 'draw_failed', 'draw_soon',
        'reveal_soon', 'revealed', 'cancelled', 'question_received', 'answer_received'));

ALTER TABLE giftexchange.participants
    DROP COLUMN details,
    DROP COLUMN dislikes,
    DROP COLUMN likes;
