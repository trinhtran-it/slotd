package booking

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/trinhtran-it/slotd/internal/availability"
	"github.com/trinhtran-it/slotd/internal/platform/db"
	"github.com/trinhtran-it/slotd/internal/platform/db/sqlc"
)

// PostgresStore is the Store backed by Postgres via sqlc-generated queries.
type PostgresStore struct {
	pool *pgxpool.Pool
	q    *sqlc.Queries
}

var _ Store = (*PostgresStore)(nil)

// NewPostgresStore returns a store using pool.
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool, q: sqlc.New(pool)}
}

// CreateProvider inserts p.
func (s *PostgresStore) CreateProvider(ctx context.Context, p Provider) (Provider, error) {
	row, err := s.q.CreateProvider(ctx, sqlc.CreateProviderParams{ID: p.ID, Name: p.Name, Timezone: p.Timezone})
	if err != nil {
		return Provider{}, fmt.Errorf("create provider: %w", err)
	}
	return toProvider(row), nil
}

// GetProvider returns ErrNotFound if id does not exist.
func (s *PostgresStore) GetProvider(ctx context.Context, id uuid.UUID) (Provider, error) {
	row, err := s.q.GetProvider(ctx, id)
	if err != nil {
		return Provider{}, mapErr(err, "provider", id)
	}
	return toProvider(row), nil
}

// ReplaceRules atomically swaps a provider's rules. The provider row is locked
// first so concurrent replacements serialize instead of interleaving.
func (s *PostgresStore) ReplaceRules(ctx context.Context, providerID uuid.UUID, rules []availability.Rule) error {
	return db.WithTx(ctx, s.pool, func(q *sqlc.Queries) error {
		if _, err := q.LockProvider(ctx, providerID); err != nil {
			return mapErr(err, "provider", providerID)
		}
		if err := q.DeleteAvailabilityRules(ctx, providerID); err != nil {
			return fmt.Errorf("delete rules: %w", err)
		}
		if len(rules) == 0 {
			return nil
		}
		params := make([]sqlc.InsertAvailabilityRulesParams, len(rules))
		for i, r := range rules {
			params[i] = sqlc.InsertAvailabilityRulesParams{
				ProviderID: providerID,
				Weekday:    smallint(int(r.Weekday)),
				StartMin:   smallint(int(r.Start)),
				EndMin:     smallint(int(r.End)),
			}
		}
		if _, err := q.InsertAvailabilityRules(ctx, params); err != nil {
			return fmt.Errorf("insert rules: %w", err)
		}
		return nil
	})
}

// Rules returns a provider's weekly rules ordered by weekday and start.
func (s *PostgresStore) Rules(ctx context.Context, providerID uuid.UUID) ([]availability.Rule, error) {
	rows, err := s.q.ListAvailabilityRules(ctx, providerID)
	if err != nil {
		return nil, fmt.Errorf("list rules: %w", err)
	}
	rules := make([]availability.Rule, len(rows))
	for i, r := range rows {
		rules[i] = availability.Rule{
			Weekday: time.Weekday(r.Weekday),
			Start:   availability.Clock(r.StartMin),
			End:     availability.Clock(r.EndMin),
		}
	}
	return rules, nil
}

// BusyIntervals returns active bookings overlapping [from, to), ordered by start.
func (s *PostgresStore) BusyIntervals(ctx context.Context, providerID uuid.UUID, from, to time.Time) ([]availability.Interval, error) {
	rows, err := s.q.ListBusyIntervals(ctx, sqlc.ListBusyIntervalsParams{ProviderID: providerID, FromAt: from, ToAt: to})
	if err != nil {
		return nil, fmt.Errorf("list busy intervals: %w", err)
	}
	out := make([]availability.Interval, len(rows))
	for i, r := range rows {
		out[i] = availability.Interval{Start: r.StartAt, End: r.EndAt}
	}
	return out, nil
}

// InsertHold inserts b as held, expiring ttl after the database's now().
// It returns ErrSlotTaken if an active booking overlaps.
func (s *PostgresStore) InsertHold(ctx context.Context, b Booking, ttl time.Duration) (Booking, error) {
	row, err := s.q.InsertHold(ctx, sqlc.InsertHoldParams{
		ID:             b.ID,
		ProviderID:     b.ProviderID,
		CustomerID:     b.CustomerID,
		StartAt:        b.Start,
		EndAt:          b.End,
		HoldTtlSeconds: ttl.Seconds(),
	})
	if err != nil {
		return Booking{}, mapErr(err, "provider", b.ProviderID)
	}
	// InsertHoldRow and GetBookingRow select the same columns, so they convert.
	return toBooking(sqlc.GetBookingRow(row)), nil
}

// GetBooking returns ErrNotFound if id does not exist.
func (s *PostgresStore) GetBooking(ctx context.Context, id uuid.UUID) (Booking, error) {
	row, err := s.q.GetBooking(ctx, id)
	if err != nil {
		return Booking{}, mapErr(err, "booking", id)
	}
	return toBooking(row), nil
}

// mapErr translates Postgres errors into domain errors.
func mapErr(err error, entity string, id uuid.UUID) error {
	var pgErr *pgconn.PgError
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("%s %s: %w", entity, id, ErrNotFound)
	case errors.As(err, &pgErr) && pgErr.Code == db.CodeExclusionViolation:
		return ErrSlotTaken
	case errors.As(err, &pgErr) && pgErr.Code == db.CodeForeignKeyViolation:
		return fmt.Errorf("%s %s: %w", entity, id, ErrNotFound)
	default:
		return fmt.Errorf("%s %s: %w", entity, id, err)
	}
}

func toBooking(r sqlc.GetBookingRow) Booking {
	return Booking{
		ID:            r.ID,
		ProviderID:    r.ProviderID,
		CustomerID:    r.CustomerID,
		Start:         r.StartAt.UTC(),
		End:           r.EndAt.UTC(),
		Status:        Status(r.Status),
		HoldExpiresAt: r.HoldExpiresAt,
		CreatedAt:     r.CreatedAt,
		UpdatedAt:     r.UpdatedAt,
	}
}

func toProvider(r sqlc.Provider) Provider {
	return Provider{ID: r.ID, Name: r.Name, Timezone: r.Timezone, CreatedAt: r.CreatedAt}
}

// smallint narrows values the domain has already range-checked
// (weekday 0-6, minutes 0-1440) to the smallint columns.
func smallint(v int) int16 {
	if v < 0 || v > 1440 {
		panic(fmt.Sprintf("smallint: %d out of validated range", v))
	}
	return int16(v) //nolint:gosec // range checked above
}
