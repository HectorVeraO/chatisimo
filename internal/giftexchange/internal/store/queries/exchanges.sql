-- name: GetGroup :one
SELECT id, provider, subject
FROM giftexchange.groups
WHERE id = @id;

-- name: InsertExchange :exec
INSERT INTO giftexchange.exchanges (
    id, group_id, organizer_id, name, state, round, reminder_lead_seconds, dropout_policy,
    avoid_last_round, questions_enabled, created_at
)
VALUES (
    @id, @group_id, @organizer_id, @name, 'open', 0, @reminder_lead_seconds, @dropout_policy,
    @avoid_last_round, @questions_enabled, @created_at
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

-- name: GetParticipant :one
SELECT exchange_id, person_id, joined_at, left_at
FROM giftexchange.participants
WHERE exchange_id = @exchange_id AND person_id = @person_id;

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

-- name: InsertNotification :exec
INSERT INTO giftexchange.notifications (id, kind, exchange_id, person_id, round, created_at)
VALUES (@id, @kind, @exchange_id, @person_id, @round, @created_at);
