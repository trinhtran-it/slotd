-- name: CreateProvider :one
INSERT INTO providers (id, name, timezone)
VALUES (@id, @name, @timezone)
RETURNING *;

-- name: GetProvider :one
SELECT * FROM providers WHERE id = @id;

-- LockProvider serializes concurrent writes to one provider's rules.
-- name: LockProvider :one
SELECT * FROM providers WHERE id = @id FOR UPDATE;

-- name: DeleteAvailabilityRules :exec
DELETE FROM availability_rules WHERE provider_id = @provider_id;

-- name: InsertAvailabilityRules :copyfrom
INSERT INTO availability_rules (provider_id, weekday, start_min, end_min)
VALUES (@provider_id, @weekday, @start_min, @end_min);

-- name: ListAvailabilityRules :many
SELECT weekday, start_min, end_min
FROM availability_rules
WHERE provider_id = @provider_id
ORDER BY weekday, start_min;
