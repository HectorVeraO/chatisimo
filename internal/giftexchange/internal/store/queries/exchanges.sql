-- name: GetGroup :one
SELECT id, provider, subject
FROM giftexchange.groups
WHERE id = @id;

-- name: InsertExchange :exec
INSERT INTO giftexchange.exchanges (
    id, group_id, organizer_id, name, state, round, draw_at, reveal_at, reminder_lead_seconds,
    dropout_policy, avoid_last_round, questions_enabled, language, created_at
)
VALUES (
    @id, @group_id, @organizer_id, @name, 'open', 0, @draw_at, @reveal_at, @reminder_lead_seconds,
    @dropout_policy, @avoid_last_round, @questions_enabled, @language, @created_at
);

-- The row lock serializes every state-changing call on one exchange.
-- name: LockExchange :one
SELECT *
FROM giftexchange.exchanges
WHERE id = @id
FOR UPDATE;

-- name: GetExchangeSummary :one
SELECT
    sqlc.embed(e),
    g.provider AS group_provider,
    g.subject AS group_subject,
    (SELECT count(*) FROM giftexchange.participants p
        WHERE p.exchange_id = e.id AND p.left_at IS NULL) AS participant_count,
    EXISTS (SELECT 1 FROM giftexchange.participants p
        WHERE p.exchange_id = e.id AND p.person_id = @caller_id AND p.left_at IS NULL)
        AS caller_is_participant
FROM giftexchange.exchanges e
JOIN giftexchange.groups g ON g.id = e.group_id
WHERE e.id = @id;

-- name: ListGroupExchanges :many
SELECT
    sqlc.embed(e),
    g.provider AS group_provider,
    g.subject AS group_subject,
    (SELECT count(*) FROM giftexchange.participants p
        WHERE p.exchange_id = e.id AND p.left_at IS NULL) AS participant_count,
    EXISTS (SELECT 1 FROM giftexchange.participants p
        WHERE p.exchange_id = e.id AND p.person_id = @caller_id AND p.left_at IS NULL)
        AS caller_is_participant
FROM giftexchange.exchanges e
JOIN giftexchange.groups g ON g.id = e.group_id
WHERE e.group_id = @group_id AND e.state <> 'cancelled'
ORDER BY e.created_at DESC, e.id DESC;

-- name: ListCallerExchanges :many
SELECT
    sqlc.embed(e),
    g.provider AS group_provider,
    g.subject AS group_subject,
    (SELECT count(*) FROM giftexchange.participants p
        WHERE p.exchange_id = e.id AND p.left_at IS NULL) AS participant_count,
    EXISTS (SELECT 1 FROM giftexchange.participants p
        WHERE p.exchange_id = e.id AND p.person_id = @caller_id AND p.left_at IS NULL)
        AS caller_is_participant
FROM giftexchange.exchanges e
JOIN giftexchange.groups g ON g.id = e.group_id
WHERE e.state <> 'cancelled'
    AND (e.organizer_id = @caller_id
        OR EXISTS (SELECT 1 FROM giftexchange.participants p
            WHERE p.exchange_id = e.id AND p.person_id = @caller_id AND p.left_at IS NULL))
ORDER BY e.created_at DESC, e.id DESC;

-- name: UpdateExchangeName :exec
UPDATE giftexchange.exchanges
SET name = @name
WHERE id = @id;

-- name: UpdateExchangeRules :exec
UPDATE giftexchange.exchanges
SET draw_at = @draw_at,
    reveal_at = @reveal_at,
    reminder_lead_seconds = @reminder_lead_seconds,
    dropout_policy = @dropout_policy,
    avoid_last_round = @avoid_last_round,
    questions_enabled = @questions_enabled,
    language = @language
WHERE id = @id;

-- draw_at is non-null only in state open, so a cancel clears it.
-- name: CancelExchange :exec
UPDATE giftexchange.exchanges
SET state = 'cancelled', cancelled_at = @cancelled_at, draw_at = NULL
WHERE id = @id;

-- name: DeleteAssignments :exec
DELETE FROM giftexchange.assignments
WHERE exchange_id = @exchange_id;

-- name: CountExclusions :one
SELECT count(*)
FROM giftexchange.exclusions
WHERE exchange_id = @exchange_id;

-- name: ListExclusions :many
SELECT giver_id, recipient_id
FROM giftexchange.exclusions
WHERE exchange_id = @exchange_id
ORDER BY created_at, giver_id, recipient_id;

-- name: InsertExclusion :exec
INSERT INTO giftexchange.exclusions (exchange_id, giver_id, recipient_id, created_at)
VALUES (@exchange_id, @giver_id, @recipient_id, @created_at)
ON CONFLICT (exchange_id, giver_id, recipient_id) DO NOTHING;

-- name: DeleteExclusion :exec
DELETE FROM giftexchange.exclusions
WHERE exchange_id = @exchange_id AND giver_id = @giver_id AND recipient_id = @recipient_id;

-- name: DeleteExclusionsOfPerson :exec
DELETE FROM giftexchange.exclusions
WHERE exchange_id = @exchange_id AND (giver_id = @person_id OR recipient_id = @person_id);

-- name: GetParticipant :one
SELECT exchange_id, person_id, joined_at, left_at, likes, dislikes, details
FROM giftexchange.participants
WHERE exchange_id = @exchange_id AND person_id = @person_id;

-- name: UpdateParticipantPreferences :exec
UPDATE giftexchange.participants
SET likes = @likes, dislikes = @dislikes, details = @details
WHERE exchange_id = @exchange_id AND person_id = @person_id AND left_at IS NULL;

-- name: CountActiveParticipants :one
SELECT count(*)
FROM giftexchange.participants
WHERE exchange_id = @exchange_id AND left_at IS NULL;

-- name: ListActiveParticipants :many
SELECT person_id, joined_at
FROM giftexchange.participants
WHERE exchange_id = @exchange_id AND left_at IS NULL
ORDER BY joined_at, person_id;

-- name: InsertParticipant :exec
INSERT INTO giftexchange.participants (exchange_id, person_id, joined_at)
VALUES (@exchange_id, @person_id, @joined_at);

-- (exchange_id, person_id) is the primary key, so a person who left rejoins by reviving the row.
-- name: ReviveParticipant :exec
UPDATE giftexchange.participants
SET joined_at = @joined_at, left_at = NULL
WHERE exchange_id = @exchange_id AND person_id = @person_id;

-- GREATEST keeps the participants_left_after_join check true if the clock reads earlier than
-- joined_at.
-- name: MarkParticipantLeft :exec
UPDATE giftexchange.participants
SET left_at = GREATEST(@left_at::timestamptz, joined_at)
WHERE exchange_id = @exchange_id AND person_id = @person_id AND left_at IS NULL;

-- name: ListIdentitiesOfPersons :many
SELECT person_id, provider, subject
FROM giftexchange.external_identities
WHERE person_id = ANY(@person_ids::uuid[])
ORDER BY person_id, provider, subject;

-- reason is set only for the kinds that carry one (notifications_reason_kinds), question_id only
-- for the question kinds (notifications_question_kinds).
-- name: InsertNotification :exec
INSERT INTO giftexchange.notifications (
    id, kind, exchange_id, person_id, round, question_id, reason, created_at
)
VALUES (
    @id, @kind, @exchange_id, @person_id, @round, sqlc.narg('question_id'), sqlc.narg('reason'),
    @created_at
);

-- One statement, because exchanges_drawn_has_round is checked per statement. draw_at is
-- non-null only in state open, so the draw clears it.
-- name: MarkExchangeDrawn :exec
UPDATE giftexchange.exchanges
SET state = 'drawn', round = @round, drawn_at = @drawn_at, draw_at = NULL
WHERE id = @id;

-- A void keeps round and reveal_at. draw_at is already NULL outside state open.
-- name: MarkExchangeVoided :exec
UPDATE giftexchange.exchanges
SET state = 'open', drawn_at = NULL
WHERE id = @id;

-- The pairs of the most recent other Revealed exchange of the same group: the "last round" that
-- avoid_last_round forbids. No such exchange gives no rows.
-- name: ListLastRoundAssignments :many
SELECT a.giver_id, a.recipient_id
FROM giftexchange.assignments a
WHERE a.exchange_id = (
    SELECT e.id
    FROM giftexchange.exchanges e
    WHERE e.group_id = @group_id AND e.id <> @exchange_id AND e.state = 'revealed'
    ORDER BY e.revealed_at DESC, e.id DESC
    LIMIT 1
);

-- One statement, because exchanges_revealed_at_iff_revealed is checked per statement. The
-- assignments stay: they are the last round that avoid_last_round reads.
-- name: MarkExchangeRevealed :exec
UPDATE giftexchange.exchanges
SET state = 'revealed', revealed_at = @revealed_at
WHERE id = @id;

-- The deadline draw failed: the exchange stays open without a deadline.
-- name: ClearDrawAt :exec
UPDATE giftexchange.exchanges
SET draw_at = NULL
WHERE id = @id;

-- A draw finds reveal_at already past: the reveal can no longer be scheduled.
-- name: ClearRevealAt :exec
UPDATE giftexchange.exchanges
SET reveal_at = NULL
WHERE id = @id;

-- The sent-marker closes the hole where draw_at set to A, then B, then A again leaves two
-- identical current reminder jobs.
-- name: MarkDrawReminderSent :exec
UPDATE giftexchange.exchanges
SET draw_reminder_sent_for = @sent_for
WHERE id = @id;

-- name: MarkRevealReminderSent :exec
UPDATE giftexchange.exchanges
SET reveal_reminder_sent_for = @sent_for
WHERE id = @id;
