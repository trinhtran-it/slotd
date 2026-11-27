package booking

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/trinhtran-it/slotd/internal/availability"
)

// fakeStore is an in-memory Store for service unit tests. It does not enforce
// overlap; that guarantee belongs to Postgres and is covered by integration tests.
type fakeStore struct {
	providers map[uuid.UUID]Provider
	rules     map[uuid.UUID][]availability.Rule
	busy      []availability.Interval
	held      []Booking
	heldTTL   time.Duration
}

func newFakeStore() *fakeStore {
	return &fakeStore{providers: map[uuid.UUID]Provider{}, rules: map[uuid.UUID][]availability.Rule{}}
}

func (f *fakeStore) CreateProvider(_ context.Context, p Provider) (Provider, error) {
	f.providers[p.ID] = p
	return p, nil
}

func (f *fakeStore) GetProvider(_ context.Context, id uuid.UUID) (Provider, error) {
	p, ok := f.providers[id]
	if !ok {
		return Provider{}, ErrNotFound
	}
	return p, nil
}

func (f *fakeStore) ReplaceRules(_ context.Context, id uuid.UUID, rules []availability.Rule) error {
	if _, ok := f.providers[id]; !ok {
		return ErrNotFound
	}
	f.rules[id] = rules
	return nil
}

func (f *fakeStore) Rules(_ context.Context, id uuid.UUID) ([]availability.Rule, error) {
	return f.rules[id], nil
}

func (f *fakeStore) BusyIntervals(context.Context, uuid.UUID, time.Time, time.Time) ([]availability.Interval, error) {
	return f.busy, nil
}

func (f *fakeStore) InsertHold(_ context.Context, b Booking, ttl time.Duration) (Booking, error) {
	f.held = append(f.held, b)
	f.heldTTL = ttl
	return b, nil
}

func (f *fakeStore) GetBooking(context.Context, uuid.UUID) (Booking, error) {
	return Booking{}, ErrNotFound
}

// Monday 2026-01-12 08:00 UTC; the test provider is in Europe/Berlin (UTC+1).
var testNow = time.Date(2026, 1, 12, 8, 0, 0, 0, time.UTC)

func newTestService(t *testing.T) (*Service, *fakeStore, uuid.UUID) {
	t.Helper()
	store := newFakeStore()
	svc := NewService(store, 10*time.Minute)
	svc.now = func() time.Time { return testNow }

	pid := uuid.New()
	store.providers[pid] = Provider{ID: pid, Name: "Dr. Example", Timezone: "Europe/Berlin"}
	store.rules[pid] = []availability.Rule{{Weekday: time.Monday, Start: 9 * 60, End: 12 * 60}} // 09:00-12:00 local
	return svc, store, pid
}

func TestService_CreateProvider(t *testing.T) {
	tests := []struct {
		name, provider, tz string
		wantErr            error
	}{
		{name: "ok", provider: "Clinic", tz: "Europe/Berlin"},
		{name: "utc is explicit and allowed", provider: "Clinic", tz: "UTC"},
		{name: "blank name", provider: "   ", tz: "UTC", wantErr: ErrInvalidInput},
		{name: "unknown zone", provider: "Clinic", tz: "Mars/Olympus", wantErr: ErrInvalidInput},
		{name: "empty zone", provider: "Clinic", tz: "", wantErr: ErrInvalidInput},
		{name: "server-local zone rejected", provider: "Clinic", tz: "Local", wantErr: ErrInvalidInput},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _, _ := newTestService(t)
			p, err := svc.CreateProvider(t.Context(), tt.provider, tt.tz)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if err == nil && (p.ID.Version() != 7 || p.Timezone != tt.tz) {
				t.Fatalf("provider = %+v, want UUIDv7 id and timezone %q", p, tt.tz)
			}
		})
	}
}

func TestService_SetAvailability_RejectsOverlap(t *testing.T) {
	svc, _, pid := newTestService(t)
	_, err := svc.SetAvailability(t.Context(), pid, []availability.Rule{
		{Weekday: time.Monday, Start: 540, End: 720},
		{Weekday: time.Monday, Start: 600, End: 660},
	})
	if !errors.Is(err, ErrInvalidInput) || !errors.Is(err, availability.ErrInvalidRule) {
		t.Fatalf("error = %v, want ErrInvalidInput wrapping ErrInvalidRule", err)
	}
}

func TestService_ListSlots(t *testing.T) {
	day := func(h, m int) time.Time { return time.Date(2026, 1, 12, h, m, 0, 0, time.UTC) }

	tests := []struct {
		name      string
		from, to  time.Time
		duration  time.Duration
		busy      []availability.Interval
		wantErr   error
		wantStart []time.Time
	}{
		{
			name: "local 09:00-12:00 is 08:00-11:00 UTC",
			from: day(0, 0), to: day(23, 0), duration: time.Hour,
			wantStart: []time.Time{day(8, 0), day(9, 0), day(10, 0)},
		},
		{
			name: "busy interval removed",
			from: day(0, 0), to: day(23, 0), duration: time.Hour,
			busy:      []availability.Interval{{Start: day(9, 0), End: day(10, 0)}},
			wantStart: []time.Time{day(8, 0), day(10, 0)},
		},
		{
			name: "past slots are not offered",
			from: day(0, 0), to: day(23, 0), duration: 30 * time.Minute,
			// now is 08:00 UTC: the 08:00 slot is still offered (starts at now), none before.
			wantStart: []time.Time{day(8, 0), day(8, 30), day(9, 0), day(9, 30), day(10, 0), day(10, 30)},
		},
		{name: "from after to", from: day(12, 0), to: day(11, 0), duration: time.Hour, wantErr: ErrInvalidInput},
		{name: "range too long", from: day(0, 0), to: day(0, 0).AddDate(0, 0, 32), duration: time.Hour, wantErr: ErrInvalidInput},
		{name: "duration too short", from: day(0, 0), to: day(23, 0), duration: time.Minute, wantErr: ErrInvalidInput},
		{name: "duration not whole minutes", from: day(0, 0), to: day(23, 0), duration: 30*time.Minute + time.Second, wantErr: ErrInvalidInput},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, store, pid := newTestService(t)
			store.busy = tt.busy

			got, err := svc.ListSlots(t.Context(), pid, tt.from, tt.to, tt.duration)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if len(got.Slots) != len(tt.wantStart) {
				t.Fatalf("got %d slots %v, want %v", len(got.Slots), got.Slots, tt.wantStart)
			}
			for i, s := range got.Slots {
				if !s.Start.Equal(tt.wantStart[i]) {
					t.Errorf("slot %d = %s, want %s", i, s.Start, tt.wantStart[i])
				}
			}
		})
	}
}

func TestService_ListSlots_UnknownProvider(t *testing.T) {
	svc, _, _ := newTestService(t)
	_, err := svc.ListSlots(t.Context(), uuid.New(), testNow, testNow.Add(time.Hour), time.Hour)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

func TestService_Hold(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 1, 12, h, m, 0, 0, time.UTC) }
	customer := uuid.New()

	tests := []struct {
		name     string
		start    time.Time
		duration time.Duration
		customer uuid.UUID
		wantErr  error
	}{
		{name: "inside availability", start: at(9, 0), duration: time.Hour, customer: customer},
		{name: "any start inside the window", start: at(9, 15), duration: 45 * time.Minute, customer: customer},
		{name: "runs past window end", start: at(10, 30), duration: time.Hour, customer: customer, wantErr: ErrOutsideAvailability},
		{name: "before window", start: at(7, 0), duration: time.Hour, customer: customer, wantErr: ErrInvalidInput}, // also in the past
		{name: "in the past", start: at(7, 59), duration: time.Hour, customer: customer, wantErr: ErrInvalidInput},
		{name: "missing customer", start: at(9, 0), duration: time.Hour, wantErr: ErrInvalidInput},
		{name: "bad duration", start: at(9, 0), duration: 0, customer: customer, wantErr: ErrInvalidInput},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, store, pid := newTestService(t)
			b, err := svc.Hold(t.Context(), HoldRequest{ProviderID: pid, CustomerID: tt.customer, Start: tt.start, Duration: tt.duration})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if err != nil {
				if len(store.held) != 0 {
					t.Fatal("store written despite validation error")
				}
				return
			}
			if b.Status != StatusHeld || !b.Start.Equal(tt.start) || b.End.Sub(b.Start) != tt.duration || b.ID.Version() != 7 {
				t.Fatalf("booking = %+v", b)
			}
			if store.heldTTL != 10*time.Minute {
				t.Fatalf("hold ttl = %s, want 10m", store.heldTTL)
			}
		})
	}
}

func TestService_Hold_UnknownProvider(t *testing.T) {
	svc, _, _ := newTestService(t)
	_, err := svc.Hold(t.Context(), HoldRequest{ProviderID: uuid.New(), CustomerID: uuid.New(), Start: testNow.Add(time.Hour), Duration: time.Hour})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}
