package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/trinhtran-it/slotd/internal/booking"
	"github.com/trinhtran-it/slotd/internal/platform/httpx"
)

// errorMapping is the single place where errors become HTTP statuses and
// problem codes. The first matching entry (errors.Is) wins.
var errorMapping = []struct {
	target error
	status int
	code   string
}{
	{errBadRequest, http.StatusBadRequest, "bad_request"},
	{booking.ErrInvalidInput, http.StatusUnprocessableEntity, "invalid_input"},
	{booking.ErrOutsideAvailability, http.StatusUnprocessableEntity, "outside_availability"},
	{booking.ErrNotFound, http.StatusNotFound, "not_found"},
	{booking.ErrSlotTaken, http.StatusConflict, "slot_taken"},
	{booking.ErrHoldExpired, http.StatusConflict, "hold_expired"},
	{booking.ErrInvalidTransition, http.StatusConflict, "invalid_transition"},
	{errNotImplemented, http.StatusNotImplemented, "not_implemented"},
}

// classify returns the HTTP status and problem code for err; unknown errors are 500.
func classify(err error) (status int, code string) {
	for _, m := range errorMapping {
		if errors.Is(err, m.target) {
			return m.status, m.code
		}
	}
	return http.StatusInternalServerError, "internal"
}

// writeError writes err as a problem response. Internal errors are logged in
// full but their details never reach the client.
func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, code := classify(err)
	detail := err.Error()
	if status == http.StatusInternalServerError {
		detail = ""
		if errors.Is(err, context.Canceled) {
			s.logger.DebugContext(r.Context(), "request cancelled by client", "err", err)
		} else {
			s.logger.ErrorContext(r.Context(), "request failed", "err", err)
		}
	}
	httpx.WriteProblem(w, r, status, code, detail)
}
