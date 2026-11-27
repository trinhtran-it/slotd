package booking

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Domain errors. The HTTP layer maps each to a status code in one place.
var (
	ErrNotFound            = errors.New("not found")
	ErrInvalidInput        = errors.New("invalid input")
	ErrSlotTaken           = errors.New("slot already taken")
	ErrOutsideAvailability = errors.New("outside provider availability")
	ErrHoldExpired         = errors.New("hold expired")
	ErrInvalidTransition   = errors.New("invalid status transition")
)

// Provider offers bookable time in its own time zone.
type Provider struct {
	ID        uuid.UUID
	Name      string
	Timezone  string // IANA name, e.g. "Europe/Berlin"
	CreatedAt time.Time
}

// Status is a booking's lifecycle state.
//
//	held ──confirm──▶ confirmed ──cancel──▶ cancelled
//	  ├──cancel───────────────────────────▶ cancelled
//	  └──(hold expires)──▶ expired
type Status string

// Booking statuses.
const (
	StatusHeld      Status = "held"
	StatusConfirmed Status = "confirmed"
	StatusCancelled Status = "cancelled"
	StatusExpired   Status = "expired"
)

var transitions = map[Status][]Status{
	StatusHeld:      {StatusConfirmed, StatusCancelled, StatusExpired},
	StatusConfirmed: {StatusCancelled},
}

// CanTransitionTo reports whether the state machine allows s → next.
func (s Status) CanTransitionTo(next Status) bool {
	for _, allowed := range transitions[s] {
		if allowed == next {
			return true
		}
	}
	return false
}

// Active reports whether a booking in this status occupies its time range.
// It must match the bookings_no_overlap constraint predicate.
func (s Status) Active() bool { return s == StatusHeld || s == StatusConfirmed }

// Booking is a reservation of [Start, End) with one provider.
type Booking struct {
	ID            uuid.UUID
	ProviderID    uuid.UUID
	CustomerID    uuid.UUID
	Start         time.Time
	End           time.Time
	Status        Status
	HoldExpiresAt *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
