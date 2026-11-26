-- +goose Up
-- btree_gist lets the bookings exclusion constraint (Phase 1) combine
-- equality on provider_id with range overlap on the booking period.
CREATE EXTENSION IF NOT EXISTS btree_gist;

-- +goose Down
DROP EXTENSION IF EXISTS btree_gist;
