-- name: InsertQuestion :exec
INSERT INTO giftexchange.questions (id, exchange_id, round, asker_id, recipient_id, text, asked_at)
VALUES (@id, @exchange_id, @round, @asker_id, @recipient_id, @text, @asked_at);

-- name: GetQuestion :one
SELECT *
FROM giftexchange.questions
WHERE id = @id;

-- Served by questions_asker_rate_idx. Callers run it under the exchange row lock.
-- name: CountQuestionsAskedSince :one
SELECT count(*)
FROM giftexchange.questions
WHERE exchange_id = @exchange_id AND asker_id = @asker_id AND asked_at > @since;

-- answer and answered_at are set together (questions_answer_iff_answered_at). The guards keep a
-- closed or answered question as it is.
-- name: AnswerQuestion :execrows
UPDATE giftexchange.questions
SET answer = @answer, answered_at = @answered_at
WHERE id = @id AND answer IS NULL AND closed_at IS NULL;

-- name: ListQuestionsAsked :many
SELECT *
FROM giftexchange.questions
WHERE exchange_id = @exchange_id AND asker_id = @asker_id
ORDER BY asked_at, id;

-- name: ListQuestionsReceived :many
SELECT *
FROM giftexchange.questions
WHERE exchange_id = @exchange_id AND recipient_id = @recipient_id
ORDER BY asked_at, id;

-- Answered questions are closed too: every question of a dead pair is closed.
-- name: CloseOpenQuestions :exec
UPDATE giftexchange.questions
SET closed_at = @closed_at
WHERE exchange_id = @exchange_id AND closed_at IS NULL;

-- name: CloseOpenQuestionsOfPerson :exec
UPDATE giftexchange.questions
SET closed_at = @closed_at
WHERE exchange_id = @exchange_id
    AND closed_at IS NULL
    AND (asker_id = @person_id OR recipient_id = @person_id);
