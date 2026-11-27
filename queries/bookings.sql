-- name: InsertHold :one
INSERT INTO bookings (id, provider_id, customer_id, during, status, hold_expires_at)
VALUES (
  @id,
  @provider_id,
  @customer_id,
  tstzrange(@start_at::timestamptz, @end_at::timestamptz, '[)'),
  'held',
  now() + make_interval(secs => @hold_ttl_seconds::float8)
)
RETURNING id, provider_id, customer_id,
  lower(during)::timestamptz AS start_at,
  upper(during)::timestamptz AS end_at,
  status, hold_expires_at, created_at, updated_at;

-- name: GetBooking :one
SELECT id, provider_id, customer_id,
  lower(during)::timestamptz AS start_at,
  upper(during)::timestamptz AS end_at,
  status, hold_expires_at, created_at, updated_at
FROM bookings
WHERE id = @id;

-- Active bookings use the same predicate as the bookings_no_overlap constraint,
-- so slot listing and the constraint agree on what "taken" means.
-- name: ListBusyIntervals :many
SELECT lower(during)::timestamptz AS start_at,
       upper(during)::timestamptz AS end_at
FROM bookings
WHERE provider_id = @provider_id
  AND status IN ('held', 'confirmed')
  AND during && tstzrange(@from_at::timestamptz, @to_at::timestamptz, '[)')
ORDER BY lower(during);
