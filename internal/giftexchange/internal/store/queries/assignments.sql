-- A COPY of the whole permutation. assignments_recipient_unique is not deferrable, so a caller
-- deletes any old rows of the exchange first.
-- name: InsertAssignments :copyfrom
INSERT INTO giftexchange.assignments (exchange_id, giver_id, recipient_id, round)
VALUES ($1, $2, $3, $4);

-- name: GetAssignmentOfGiver :one
SELECT recipient_id
FROM giftexchange.assignments
WHERE exchange_id = @exchange_id AND giver_id = @giver_id;

-- name: GetAssignmentOfRecipient :one
SELECT giver_id
FROM giftexchange.assignments
WHERE exchange_id = @exchange_id AND recipient_id = @recipient_id;

-- name: DeleteAssignmentOfGiver :exec
DELETE FROM giftexchange.assignments
WHERE exchange_id = @exchange_id AND giver_id = @giver_id;

-- A patch frees the dropout's recipient with DeleteAssignmentOfGiver first, because
-- assignments_recipient_unique is not deferrable.
-- name: UpdateAssignmentRecipient :exec
UPDATE giftexchange.assignments
SET recipient_id = @recipient_id
WHERE exchange_id = @exchange_id AND giver_id = @giver_id;

-- name: ListAssignmentRecipients :many
SELECT recipient_id
FROM giftexchange.assignments
WHERE exchange_id = @exchange_id
ORDER BY recipient_id;

-- name: ListAssignmentGivers :many
SELECT giver_id
FROM giftexchange.assignments
WHERE exchange_id = @exchange_id
ORDER BY giver_id;
