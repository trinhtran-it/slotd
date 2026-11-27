package booking

import "testing"

func TestStatus_CanTransitionTo(t *testing.T) {
	all := []Status{StatusHeld, StatusConfirmed, StatusCancelled, StatusExpired}
	allowed := map[[2]Status]bool{
		{StatusHeld, StatusConfirmed}:      true,
		{StatusHeld, StatusCancelled}:      true,
		{StatusHeld, StatusExpired}:        true,
		{StatusConfirmed, StatusCancelled}: true,
	}
	// Exhaustive: every pair not listed above must be rejected.
	for _, from := range all {
		for _, to := range all {
			want := allowed[[2]Status{from, to}]
			if got := from.CanTransitionTo(to); got != want {
				t.Errorf("%s -> %s = %v, want %v", from, to, got, want)
			}
		}
	}
}

func TestStatus_Active(t *testing.T) {
	for s, want := range map[Status]bool{
		StatusHeld: true, StatusConfirmed: true, StatusCancelled: false, StatusExpired: false,
	} {
		if got := s.Active(); got != want {
			t.Errorf("%s.Active() = %v, want %v", s, got, want)
		}
	}
}
