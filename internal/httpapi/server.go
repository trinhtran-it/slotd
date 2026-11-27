// Package httpapi is the HTTP transport for the booking service. Routes, path
// and query parameters, and request/response types are generated from
// api/openapi.yaml (api.gen.go); this package implements the handlers, maps
// domain errors to RFC 9457 problems, and serves health endpoints.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/trinhtran-it/slotd/internal/availability"
	"github.com/trinhtran-it/slotd/internal/booking"
	"github.com/trinhtran-it/slotd/internal/platform/httpx"
)

// Service is the subset of *booking.Service the handlers use.
type Service interface {
	CreateProvider(ctx context.Context, name, timezone string) (booking.Provider, error)
	SetAvailability(ctx context.Context, providerID uuid.UUID, rules []availability.Rule) ([]availability.Rule, error)
	ListSlots(ctx context.Context, providerID uuid.UUID, from, to time.Time, d time.Duration) (booking.SlotList, error)
	Hold(ctx context.Context, req booking.HoldRequest) (booking.Booking, error)
	GetBooking(ctx context.Context, id uuid.UUID) (booking.Booking, error)
}

// Pinger checks a dependency for readiness (*pgxpool.Pool implements it).
type Pinger interface {
	Ping(ctx context.Context) error
}

const maxBodyBytes = 1 << 20

// Server implements the generated ServerInterface.
type Server struct {
	svc    Service
	logger *slog.Logger
}

var _ ServerInterface = (*Server)(nil)

// NewHandler returns the complete HTTP handler: API routes, /healthz, /readyz
// and middleware.
func NewHandler(svc Service, db Pinger, logger *slog.Logger) http.Handler {
	s := &Server{svc: svc, logger: logger}
	mux := http.NewServeMux()

	// Liveness: the process is up. No dependencies, so a DB outage never
	// gets the process restarted.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
	})
	// Readiness: dependencies reachable, safe to route traffic here.
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := db.Ping(ctx); err != nil {
			logger.WarnContext(ctx, "readiness check failed", "err", err)
			httpx.WriteProblem(w, r, http.StatusServiceUnavailable, "unavailable", "database unreachable")
			return
		}
		httpx.WriteJSON(w, r, http.StatusOK, map[string]string{"status": "ready"})
	})

	HandlerWithOptions(s, StdHTTPServerOptions{
		BaseRouter: mux,
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			httpx.WriteProblem(w, r, http.StatusBadRequest, "bad_request", err.Error())
		},
	})

	return httpx.Chain(mux, httpx.RequestID, httpx.AccessLog(logger), httpx.Recover(logger))
}

// CreateProvider handles POST /v1/providers.
func (s *Server) CreateProvider(w http.ResponseWriter, r *http.Request) {
	var req CreateProviderRequest
	if err := decode(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	p, err := s.svc.CreateProvider(r.Context(), req.Name, req.Timezone)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusCreated, Provider{Id: p.ID, Name: p.Name, Timezone: p.Timezone, CreatedAt: p.CreatedAt.UTC()})
}

// SetAvailability handles PUT /v1/providers/{id}/availability.
func (s *Server) SetAvailability(w http.ResponseWriter, r *http.Request, id ID) {
	var req Availability
	if err := decode(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	rules, err := toDomainRules(req.Rules)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	stored, err := s.svc.SetAvailability(r.Context(), id, rules)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, Availability{Rules: toAPIRules(stored)})
}

// ListSlots handles GET /v1/providers/{id}/slots.
func (s *Server) ListSlots(w http.ResponseWriter, r *http.Request, id ID, params ListSlotsParams) {
	d, err := minutes(params.Duration)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	list, err := s.svc.ListSlots(r.Context(), id, params.From, params.To, d)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	resp := SlotList{
		ProviderId: list.Provider.ID,
		Timezone:   list.Provider.Timezone,
		Duration:   params.Duration,
		Slots:      make([]Slot, len(list.Slots)),
	}
	for i, sl := range list.Slots {
		resp.Slots[i] = Slot{Start: sl.Start.UTC(), End: sl.End.UTC()}
	}
	httpx.WriteJSON(w, r, http.StatusOK, resp)
}

// CreateBooking handles POST /v1/bookings: it creates a hold.
func (s *Server) CreateBooking(w http.ResponseWriter, r *http.Request) {
	var req CreateBookingRequest
	if err := decode(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	d, err := minutes(req.Duration)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	b, err := s.svc.Hold(r.Context(), booking.HoldRequest{
		ProviderID: req.ProviderId,
		CustomerID: req.CustomerId,
		Start:      req.Start,
		Duration:   d,
	})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	w.Header().Set("Location", "/v1/bookings/"+b.ID.String())
	httpx.WriteJSON(w, r, http.StatusCreated, toAPIBooking(b))
}

// GetBooking handles GET /v1/bookings/{id}.
func (s *Server) GetBooking(w http.ResponseWriter, r *http.Request, id ID) {
	b, err := s.svc.GetBooking(r.Context(), id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, toAPIBooking(b))
}

// ConfirmBooking handles POST /v1/bookings/{id}/confirm.
func (s *Server) ConfirmBooking(w http.ResponseWriter, r *http.Request, _ ID) {
	s.writeError(w, r, fmt.Errorf("confirm: %w", errNotImplemented))
}

// CancelBooking handles POST /v1/bookings/{id}/cancel.
func (s *Server) CancelBooking(w http.ResponseWriter, r *http.Request, _ ID) {
	s.writeError(w, r, fmt.Errorf("cancel: %w", errNotImplemented))
}

// decode reads exactly one JSON object, rejecting unknown fields and bodies over 1 MiB.
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: invalid JSON body: %w", errBadRequest, err)
	}
	if dec.More() {
		return fmt.Errorf("%w: body must contain a single JSON object", errBadRequest)
	}
	return nil
}

// minutes converts an API minute count, guarding against overflow; the service
// enforces the business range.
func minutes(m int) (time.Duration, error) {
	if m < 0 || m > 7*24*60 {
		return 0, fmt.Errorf("%w: duration %d minutes out of range", booking.ErrInvalidInput, m)
	}
	return time.Duration(m) * time.Minute, nil
}

var weekdays = map[Weekday]time.Weekday{
	Sunday: time.Sunday, Monday: time.Monday, Tuesday: time.Tuesday, Wednesday: time.Wednesday,
	Thursday: time.Thursday, Friday: time.Friday, Saturday: time.Saturday,
}

func toDomainRules(in []AvailabilityRule) ([]availability.Rule, error) {
	out := make([]availability.Rule, len(in))
	for i, r := range in {
		wd, ok := weekdays[r.Weekday]
		if !ok {
			return nil, fmt.Errorf("%w: rules[%d]: unknown weekday %q", booking.ErrInvalidInput, i, r.Weekday)
		}
		start, err := availability.ParseClock(r.Start)
		if err != nil {
			return nil, fmt.Errorf("%w: rules[%d].start: %w", booking.ErrInvalidInput, i, err)
		}
		end, err := availability.ParseClock(r.End)
		if err != nil {
			return nil, fmt.Errorf("%w: rules[%d].end: %w", booking.ErrInvalidInput, i, err)
		}
		out[i] = availability.Rule{Weekday: wd, Start: start, End: end}
	}
	return out, nil
}

func toAPIRules(in []availability.Rule) []AvailabilityRule {
	names := make(map[time.Weekday]Weekday, len(weekdays))
	for name, wd := range weekdays {
		names[wd] = name
	}
	out := make([]AvailabilityRule, len(in))
	for i, r := range in {
		out[i] = AvailabilityRule{Weekday: names[r.Weekday], Start: r.Start.String(), End: r.End.String()}
	}
	return out
}

func toAPIBooking(b booking.Booking) Booking {
	out := Booking{
		Id:         b.ID,
		ProviderId: b.ProviderID,
		CustomerId: b.CustomerID,
		Start:      b.Start.UTC(),
		End:        b.End.UTC(),
		Status:     BookingStatus(b.Status),
		CreatedAt:  b.CreatedAt.UTC(),
		UpdatedAt:  b.UpdatedAt.UTC(),
	}
	if b.HoldExpiresAt != nil {
		t := b.HoldExpiresAt.UTC()
		out.HoldExpiresAt = &t
	}
	return out
}

// Transport-level errors (domain errors live in package booking).
var (
	errBadRequest     = errors.New("bad request")
	errNotImplemented = errors.New("not implemented")
)
