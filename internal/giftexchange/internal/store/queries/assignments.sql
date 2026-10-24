-- A COPY of the whole permutation. assignments_recipient_unique is not deferrable, so a caller
-- deletes any old rows of the exchange first.
-- name: InsertAssignments :copyfrom
INSERT INTO giftexchange.assignments (exchange_id, giver_id, recipient_id, round)
VALUES ($1, $2, $3, $4);

-- name: GetAssignmentOfGiver :one
SELECT recipient_id
FROM giftexchange.assignments
WHERE exchange_id = @exchange_id AND giver_id = @giver_id;
