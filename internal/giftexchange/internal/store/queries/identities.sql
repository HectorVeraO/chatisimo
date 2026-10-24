-- name: GetPersonByIdentity :one
SELECT person_id
FROM giftexchange.external_identities
WHERE provider = @provider AND subject = @subject;

-- name: InsertPerson :exec
INSERT INTO giftexchange.persons (id, created_at)
VALUES (@id, @created_at);

-- Two concurrent first sights of one identity both insert a person. The loser's identity insert
-- does nothing; the caller re-reads with GetPersonByIdentity and keeps the winner's person. The
-- loser's persons row stays orphaned: nothing references or lists it.
-- name: InsertIdentity :exec
INSERT INTO giftexchange.external_identities (provider, subject, person_id, created_at)
VALUES (@provider, @subject, @person_id, @created_at)
ON CONFLICT (provider, subject) DO NOTHING;

-- The no-op update makes RETURNING yield the existing row on conflict.
-- name: UpsertGroup :one
INSERT INTO giftexchange.groups (id, provider, subject, created_at)
VALUES (@id, @provider, @subject, @created_at)
ON CONFLICT (provider, subject) DO UPDATE SET provider = EXCLUDED.provider
RETURNING id;
