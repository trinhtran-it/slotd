//go:build integration

package booking

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/trinhtran-it/slotd/internal/availability"
	"github.com/trinhtran-it/slotd/internal/platform/db/dbtest"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	ctx := context.Background()
	pool, cleanup, err := dbtest.Start(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "integration setup:", err)
		os.Exit(1)
	}
	testPool = pool
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// Each test creates its own provider, so tests share one database and run in parallel.
func newProvider(ctx context.Context, t *testing.T, s *PostgresStore) Provider {
	t.Helper()
	p, err := s.CreateProvider(ctx, Provider{ID: uuid.New(), Name: "Test", Timezone: "Europe/Berlin"})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	return p
}

func hold(providerID uuid.UUID, start time.Time, d time.Duration) Booking {
	return Booking{ID: uuid.New(), ProviderID: providerID, CustomerID: uuid.New(), Start: start, End: start.Add(d), Status: StatusHeld}
}

var base = time.Date(2030, 1, 7, 9, 0, 0, 0, time.UTC) // a Monday in the future

func TestPostgresStore_Provider(t *testing.T) {
	t.Parallel()
	s := NewPostgresStore(testPool)
	p := newProvider(t.Context(), t, s)

	got, err := s.GetProvider(t.Context(), p.ID)
	if err != nil || got.Name != "Test" || got.Timezone != "Europe/Berlin" || got.CreatedAt.IsZero() {
		t.Fatalf("GetProvider = %+v, %v", got, err)
	}
	if _, err := s.GetProvider(t.Context(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetProvider(unknown) error = %v, want ErrNotFound", err)
	}
}

func TestPostgresStore_ReplaceRules(t *testing.T) {
	t.Parallel()
	s := NewPostgresStore(testPool)
	p := newProvider(t.Context(), t, s)
	ctx := t.Context()

	first := []availability.Rule{
		{Weekday: time.Tuesday, Start: 540, End: 720},
		{Weekday: time.Monday, Start: 780, End: 1020},
		{Weekday: time.Monday, Start: 540, End: 720},
	}
	if err := s.ReplaceRules(ctx, p.ID, first); err != nil {
		t.Fatal(err)
	}
	got, err := s.Rules(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []availability.Rule{first[2], first[1], first[0]} // ordered by weekday, start
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("Rules = %v, want %v", got, want)
	}

	if err := s.ReplaceRules(ctx, p.ID, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Rules(ctx, p.ID); len(got) != 0 {
		t.Fatalf("Rules after clear = %v, want none", got)
	}

	if err := s.ReplaceRules(ctx, uuid.New(), first); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ReplaceRules(unknown) error = %v, want ErrNotFound", err)
	}
}

func TestPostgresStore_InsertHold(t *testing.T) {
	t.Parallel()
	s := NewPostgresStore(testPool)
	p := newProvider(t.Context(), t, s)
	ctx := t.Context()

	b, err := s.InsertHold(ctx, hold(p.ID, base, time.Hour), 10*time.Minute)
	if err != nil {
		t.Fatalf("InsertHold: %v", err)
	}
	if b.Status != StatusHeld || !b.Start.Equal(base) || !b.End.Equal(base.Add(time.Hour)) || b.HoldExpiresAt == nil {
		t.Fatalf("booking = %+v", b)
	}
	if ttl := b.HoldExpiresAt.Sub(b.CreatedAt); ttl != 10*time.Minute {
		t.Fatalf("hold_expires_at - created_at = %s, want 10m", ttl)
	}

	got, err := s.GetBooking(ctx, b.ID)
	if err != nil || got.ID != b.ID || !got.Start.Equal(b.Start) {
		t.Fatalf("GetBooking = %+v, %v", got, err)
	}
	if _, err := s.GetBooking(ctx, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetBooking(unknown) error = %v, want ErrNotFound", err)
	}
}

func TestPostgresStore_InsertHold_ExclusionConstraint(t *testing.T) {
	t.Parallel()
	s := NewPostgresStore(testPool)
	p := newProvider(t.Context(), t, s)
	other := newProvider(t.Context(), t, s)
	ctx := t.Context()

	if _, err := s.InsertHold(ctx, hold(p.ID, base, time.Hour), time.Minute); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		provider uuid.UUID
		start    time.Time
		d        time.Duration
		wantErr  error
	}{
		{name: "same range", provider: p.ID, start: base, d: time.Hour, wantErr: ErrSlotTaken},
		{name: "partial overlap", provider: p.ID, start: base.Add(30 * time.Minute), d: time.Hour, wantErr: ErrSlotTaken},
		{name: "enclosing", provider: p.ID, start: base.Add(-time.Hour), d: 3 * time.Hour, wantErr: ErrSlotTaken},
		{name: "adjacent after is free", provider: p.ID, start: base.Add(time.Hour), d: time.Hour},
		{name: "adjacent before is free", provider: p.ID, start: base.Add(-time.Hour), d: time.Hour},
		{name: "other provider same time", provider: other.ID, start: base, d: time.Hour},
		{name: "unknown provider", provider: uuid.New(), start: base, d: time.Hour, wantErr: ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.InsertHold(ctx, hold(tt.provider, tt.start, tt.d), time.Minute)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestPostgresStore_InactiveBookingsDoNotBlock(t *testing.T) {
	t.Parallel()
	s := NewPostgresStore(testPool)
	ctx := t.Context()

	for _, status := range []Status{StatusCancelled, StatusExpired} {
		t.Run(string(status), func(t *testing.T) {
			p := newProvider(ctx, t, s)
			b, err := s.InsertHold(ctx, hold(p.ID, base, time.Hour), time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			// Status transitions arrive in Phase 2; set it directly here.
			if _, err := testPool.Exec(ctx, `UPDATE bookings SET status = $1 WHERE id = $2`, string(status), b.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.InsertHold(ctx, hold(p.ID, base, time.Hour), time.Minute); err != nil {
				t.Fatalf("re-hold after %s: %v", status, err)
			}
		})
	}
}

func TestPostgresStore_BusyIntervals(t *testing.T) {
	t.Parallel()
	s := NewPostgresStore(testPool)
	p := newProvider(t.Context(), t, s)
	ctx := t.Context()

	h := func(offset time.Duration) Booking {
		b, err := s.InsertHold(ctx, hold(p.ID, base.Add(offset), time.Hour), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	late, early := h(3*time.Hour), h(0)
	cancelled := h(5 * time.Hour)
	_ = h(48 * time.Hour) // outside the queried range
	if _, err := testPool.Exec(ctx, `UPDATE bookings SET status = 'cancelled' WHERE id = $1`, cancelled.ID); err != nil {
		t.Fatal(err)
	}

	got, err := s.BusyIntervals(ctx, p.ID, base, base.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got[0].Start.Equal(early.Start) || !got[1].Start.Equal(late.Start) {
		t.Fatalf("BusyIntervals = %v, want [%s, %s] ordered", got, early.Start, late.Start)
	}
}
