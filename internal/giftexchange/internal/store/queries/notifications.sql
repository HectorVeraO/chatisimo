-- name: GetNotification :one
SELECT *
FROM giftexchange.notifications
WHERE id = @id;

-- The outcome IS NULL guard keeps the first recorded outcome when a duplicate delivery finishes
-- the same row.
-- name: FinishNotification :exec
UPDATE giftexchange.notifications
SET outcome = @outcome, finished_at = @finished_at
WHERE id = @id AND outcome IS NULL;
