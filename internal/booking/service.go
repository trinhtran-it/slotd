package booking

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/trinhtran-it/slotd/internal/availability"
)

// Input limits.
const (
	MaxNameLength = 200
	MinDuration   = 5 * time.Minute
	MaxDuration   = 12 * time.Hour
	MaxSlotRange  = 31 * 24 * time.Hour
)

// Store is the persistence the service needs. *PostgresStore implements it.
type Store interface {
	CreateProvider(ctx context.Context, p Provider) (Provider, error)
	GetProvider(ctx context.Context, id uuid.UUID) (Provider, error)
	ReplaceRules(ctx context.Context, providerID uuid.UUID, rules []availability.Rule) error
	Rules(ctx context.Context, providerID uuid.UUID) ([]availability.Rule, error)
	BusyIntervals(ctx context.Context, providerID uuid.UUID, from, to time.Time) ([]availability.Interval, error)
	InsertHold(ctx context.Context, b Booking, ttl time.Duration) (Booking, error)
	GetBooking(ctx context.Context, id uuid.UUID) (Booking, error)
}

// Service implements the booking use cases.
type Service struct {
	store   Store
	holdTTL time.Duration
	now     func() time.Time
}

// NewService returns a Service whose holds block their slot for holdTTL.
func NewService(store Store, holdTTL time.Duration) *Service {
	return &Service{store: store, holdTTL: holdTTL, now: time.Now}
}

// CreateProvider registers a provider in an IANA time zone.
func (s *Service) CreateProvider(ctx context.Context, name, timezone string) (Provider, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > MaxNameLength {
		return Provider{}, fmt.Errorf("%w: name must be 1-%d characters", ErrInvalidInput, MaxNameLength)
	}
	if _, err := loadLocation(timezone); err != nil {
		return Provider{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Provider{}, fmt.Errorf("generate id: %w", err)
	}
	return s.store.CreateProvider(ctx, Provider{ID: id, Name: name, Timezone: timezone})
}

// SetAvailability replaces all of a provider's weekly rules.
func (s *Service) SetAvailability(ctx context.Context, providerID uuid.UUID, rules []availability.Rule) ([]availability.Rule, error) {
	if err := availability.ValidateRules(rules); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidInput, err)
	}
	if err := s.store.ReplaceRules(ctx, providerID, rules); err != nil {
		return nil, err
	}
	return s.store.Rules(ctx, providerID)
}

// SlotList is the result of ListSlots.
type SlotList struct {
	Provider Provider
	Slots    []availability.Interval
}

// ListSlots returns free slots of length d within [from, to). Slots in the past
// are never offered.
func (s *Service) ListSlots(ctx context.Context, providerID uuid.UUID, from, to time.Time, d time.Duration) (SlotList, error) {
	if !from.Before(to) {
		return SlotList{}, fmt.Errorf("%w: from must be before to", ErrInvalidInput)
	}
	if to.Sub(from) > MaxSlotRange {
		return SlotList{}, fmt.Errorf("%w: range must not exceed %d days", ErrInvalidInput, int(MaxSlotRange.Hours()/24))
	}
	if err := validateDuration(d); err != nil {
		return SlotList{}, err
	}

	p, loc, err := s.provider(ctx, providerID)
	if err != nil {
		return SlotList{}, err
	}
	out := SlotList{Provider: p, Slots: []availability.Interval{}}

	if now := s.now(); from.Before(now) {
		from = now
	}
	if !from.Before(to) {
		return out, nil
	}

	rules, err := s.store.Rules(ctx, providerID)
	if err != nil {
		return SlotList{}, err
	}
	windows := availability.Windows(rules, loc, from, to)
	if len(windows) == 0 {
		return out, nil
	}
	busy, err := s.store.BusyIntervals(ctx, providerID, from, to)
	if err != nil {
		return SlotList{}, err
	}
	out.Slots = availability.Slots(windows, from, to, d, busy)
	return out, nil
}

// HoldRequest asks to hold [Start, Start+Duration) with a provider.
type HoldRequest struct {
	ProviderID uuid.UUID
	CustomerID uuid.UUID
	Start      time.Time
	Duration   time.Duration
}

// Hold reserves a time range for the hold TTL. The range must be in the future
// and inside the provider's availability. Overlap with another active booking
// is rejected by the database (ErrSlotTaken), which is what makes this safe
// under concurrency: there is no check-then-insert race.
func (s *Service) Hold(ctx context.Context, req HoldRequest) (Booking, error) {
	if req.CustomerID == uuid.Nil {
		return Booking{}, fmt.Errorf("%w: customer_id is required", ErrInvalidInput)
	}
	if err := validateDuration(req.Duration); err != nil {
		return Booking{}, err
	}
	if !req.Start.After(s.now()) {
		return Booking{}, fmt.Errorf("%w: start must be in the future", ErrInvalidInput)
	}

	_, loc, err := s.provider(ctx, req.ProviderID)
	if err != nil {
		return Booking{}, err
	}
	want := availability.Interval{Start: req.Start.UTC(), End: req.Start.Add(req.Duration).UTC()}

	rules, err := s.store.Rules(ctx, req.ProviderID)
	if err != nil {
		return Booking{}, err
	}
	windows := availability.Windows(rules, loc, want.Start, want.End)
	if !slices.ContainsFunc(windows, func(w availability.Interval) bool { return w.Contains(want) }) {
		return Booking{}, ErrOutsideAvailability
	}

	id, err := uuid.NewV7()
	if err != nil {
		return Booking{}, fmt.Errorf("generate id: %w", err)
	}
	return s.store.InsertHold(ctx, Booking{
		ID:         id,
		ProviderID: req.ProviderID,
		CustomerID: req.CustomerID,
		Start:      want.Start,
		End:        want.End,
		Status:     StatusHeld,
	}, s.holdTTL)
}

// GetBooking returns one booking.
func (s *Service) GetBooking(ctx context.Context, id uuid.UUID) (Booking, error) {
	return s.store.GetBooking(ctx, id)
}

func (s *Service) provider(ctx context.Context, id uuid.UUID) (Provider, *time.Location, error) {
	p, err := s.store.GetProvider(ctx, id)
	if err != nil {
		return Provider{}, nil, err
	}
	loc, err := time.LoadLocation(p.Timezone)
	if err != nil {
		return Provider{}, nil, fmt.Errorf("provider %s has unloadable timezone %q: %w", id, p.Timezone, err)
	}
	return p, loc, nil
}

func validateDuration(d time.Duration) error {
	if d < MinDuration || d > MaxDuration || d%time.Minute != 0 {
		return fmt.Errorf("%w: duration must be whole minutes between %s and %s", ErrInvalidInput, MinDuration, MaxDuration)
	}
	return nil
}

// loadLocation accepts only explicit IANA names: "" and "Local" would silently
// mean UTC or the server's zone.
func loadLocation(name string) (*time.Location, error) {
	if name == "" || name == "Local" {
		return nil, fmt.Errorf("%w: timezone must be an IANA name such as Europe/Berlin", ErrInvalidInput)
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("%w: unknown timezone %q", ErrInvalidInput, name)
	}
	return loc, nil
}
