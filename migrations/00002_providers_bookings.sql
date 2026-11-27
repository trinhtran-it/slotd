-- +goose Up
CREATE TABLE providers (
  id         uuid PRIMARY KEY,
  name       text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
  timezone   text NOT NULL, -- IANA name, validated by the application
  created_at timestamptz NOT NULL DEFAULT now()
);

-- Weekly availability in the provider's local wall-clock time.
CREATE TABLE availability_rules (
  provider_id uuid     NOT NULL REFERENCES providers (id) ON DELETE CASCADE,
  weekday     smallint NOT NULL CHECK (weekday BETWEEN 0 AND 6),     -- 0 = Sunday (Go time.Weekday)
  start_min   smallint NOT NULL CHECK (start_min BETWEEN 0 AND 1439), -- minutes since local midnight
  end_min     smallint NOT NULL CHECK (end_min BETWEEN 1 AND 1440),
  CONSTRAINT availability_rules_order CHECK (end_min > start_min),
  PRIMARY KEY (provider_id, weekday, start_min)
);

CREATE TABLE bookings (
  id              uuid        PRIMARY KEY,
  provider_id     uuid        NOT NULL REFERENCES providers (id),
  customer_id     uuid        NOT NULL,
  during          tstzrange   NOT NULL,
  status          text        NOT NULL CHECK (status IN ('held', 'confirmed', 'cancelled', 'expired')),
  hold_expires_at timestamptz,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT bookings_during_half_open
    CHECK (NOT isempty(during) AND lower_inc(during) AND NOT upper_inc(during)),
  CONSTRAINT bookings_hold_has_expiry
    CHECK (status <> 'held' OR hold_expires_at IS NOT NULL),
  -- The double-booking guarantee: no two active bookings of one provider may overlap.
  -- Enforced by the database, so it holds under any concurrency.
  CONSTRAINT bookings_no_overlap
    EXCLUDE USING gist (provider_id WITH =, during WITH &&)
    WHERE (status IN ('held', 'confirmed'))
);

-- +goose Down
DROP TABLE bookings;
DROP TABLE availability_rules;
DROP TABLE providers;
