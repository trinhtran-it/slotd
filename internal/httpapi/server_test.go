package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/trinhtran-it/slotd/internal/availability"
	"github.com/trinhtran-it/slotd/internal/booking"
	"github.com/trinhtran-it/slotd/internal/platform/httpx"
)

// fakeService returns canned results; err, when set, is returned by every method.
type fakeService struct {
	err      error
	gotRules []availability.Rule
	gotHold  booking.HoldRequest
}

func (f *fakeService) CreateProvider(_ context.Context, name, tz string) (booking.Provider, error) {
	return booking.Provider{ID: uuid.New(), Name: name, Timezone: tz, CreatedAt: time.Now()}, f.err
}

func (f *fakeService) SetAvailability(_ context.Context, _ uuid.UUID, rules []availability.Rule) ([]availability.Rule, error) {
	f.gotRules = rules
	return rules, f.err
}

func (f *fakeService) ListSlots(_ context.Context, id uuid.UUID, from, _ time.Time, d time.Duration) (booking.SlotList, error) {
	return booking.SlotList{
		Provider: booking.Provider{ID: id, Timezone: "UTC"},
		Slots:    []availability.Interval{{Start: from, End: from.Add(d)}},
	}, f.err
}

func (f *fakeService) Hold(_ context.Context, req booking.HoldRequest) (booking.Booking, error) {
	f.gotHold = req
	return booking.Booking{ID: uuid.New(), ProviderID: req.ProviderID, Start: req.Start, End: req.Start.Add(req.Duration), Status: booking.StatusHeld}, f.err
}

func (f *fakeService) GetBooking(_ context.Context, id uuid.UUID) (booking.Booking, error) {
	return booking.Booking{ID: id, Status: booking.StatusHeld}, f.err
}

type pinger struct{ err error }

func (p pinger) Ping(context.Context) error { return p.err }

func do(t *testing.T, h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func problem(t *testing.T, rr *httptest.ResponseRecorder) httpx.Problem {
	t.Helper()
	if ct := rr.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json (body %s)", ct, rr.Body)
	}
	var p httpx.Problem
	if err := json.Unmarshal(rr.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func newTestHandler(svc Service, db Pinger) http.Handler {
	return NewHandler(svc, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestClassify(t *testing.T) {
	tests := []struct {
		err        error
		wantStatus int
		wantCode   string
	}{
		{fmt.Errorf("x: %w", errBadRequest), 400, "bad_request"},
		{fmt.Errorf("%w: name", booking.ErrInvalidInput), 422, "invalid_input"},
		{booking.ErrOutsideAvailability, 422, "outside_availability"},
		{fmt.Errorf("provider 1: %w", booking.ErrNotFound), 404, "not_found"},
		{booking.ErrSlotTaken, 409, "slot_taken"},
		{booking.ErrHoldExpired, 409, "hold_expired"},
		{booking.ErrInvalidTransition, 409, "invalid_transition"},
		{errNotImplemented, 501, "not_implemented"},
		{errors.New("connection reset"), 500, "internal"},
	}
	for _, tt := range tests {
		t.Run(tt.wantCode, func(t *testing.T) {
			status, code := classify(tt.err)
			if status != tt.wantStatus || code != tt.wantCode {
				t.Fatalf("classify(%v) = %d %s, want %d %s", tt.err, status, code, tt.wantStatus, tt.wantCode)
			}
		})
	}
}

func TestHandlers_ErrorResponses(t *testing.T) {
	pid := uuid.New()
	tests := []struct {
		name       string
		svcErr     error
		method     string
		target     string
		body       string
		wantStatus int
		wantCode   string
		wantDetail string
	}{
		{name: "malformed json", method: "POST", target: "/v1/providers", body: `{`, wantStatus: 400, wantCode: "bad_request"},
		{name: "unknown field", method: "POST", target: "/v1/providers", body: `{"name":"a","timezone":"UTC","x":1}`, wantStatus: 400, wantCode: "bad_request"},
		{name: "two objects", method: "POST", target: "/v1/providers", body: `{"name":"a","timezone":"UTC"}{}`, wantStatus: 400, wantCode: "bad_request"},
		{name: "invalid path uuid", method: "GET", target: "/v1/bookings/not-a-uuid", wantStatus: 400, wantCode: "bad_request"},
		{name: "missing query param", method: "GET", target: "/v1/providers/" + pid.String() + "/slots?from=2030-01-01T00:00:00Z&duration=30", wantStatus: 400, wantCode: "bad_request"},
		{name: "unknown weekday", method: "PUT", target: "/v1/providers/" + pid.String() + "/availability", body: `{"rules":[{"weekday":"funday","start":"09:00","end":"10:00"}]}`, wantStatus: 422, wantCode: "invalid_input"},
		{name: "bad clock", method: "PUT", target: "/v1/providers/" + pid.String() + "/availability", body: `{"rules":[{"weekday":"monday","start":"9am","end":"10:00"}]}`, wantStatus: 422, wantCode: "invalid_input"},
		{name: "slot taken", svcErr: booking.ErrSlotTaken, method: "POST", target: "/v1/bookings", body: `{"provider_id":"` + pid.String() + `","customer_id":"` + pid.String() + `","start":"2030-01-01T09:00:00Z","duration":30}`, wantStatus: 409, wantCode: "slot_taken"},
		{name: "not found", svcErr: fmt.Errorf("booking x: %w", booking.ErrNotFound), method: "GET", target: "/v1/bookings/" + pid.String(), wantStatus: 404, wantCode: "not_found"},
		{name: "internal error hides detail", svcErr: errors.New("pq: password=hunter2"), method: "GET", target: "/v1/bookings/" + pid.String(), wantStatus: 500, wantCode: "internal", wantDetail: ""},
		{name: "confirm not implemented", method: "POST", target: "/v1/bookings/" + pid.String() + "/confirm", wantStatus: 501, wantCode: "not_implemented"},
		{name: "cancel not implemented", method: "POST", target: "/v1/bookings/" + pid.String() + "/cancel", wantStatus: 501, wantCode: "not_implemented"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHandler(&fakeService{err: tt.svcErr}, pinger{})
			rr := do(t, h, tt.method, tt.target, tt.body)
			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body %s", rr.Code, tt.wantStatus, rr.Body)
			}
			p := problem(t, rr)
			if p.Code != tt.wantCode || p.Status != tt.wantStatus || p.Type != "about:blank" || p.RequestID == "" {
				t.Fatalf("problem = %+v", p)
			}
			if tt.wantStatus == 500 && p.Detail != tt.wantDetail {
				t.Fatalf("internal detail leaked: %q", p.Detail)
			}
		})
	}
}

func TestHandlers_Success(t *testing.T) {
	pid := uuid.New()

	t.Run("create provider", func(t *testing.T) {
		rr := do(t, newTestHandler(&fakeService{}, pinger{}), "POST", "/v1/providers", `{"name":"Clinic","timezone":"Europe/Berlin"}`)
		if rr.Code != 201 {
			t.Fatalf("status = %d; body %s", rr.Code, rr.Body)
		}
		var p Provider
		if err := json.Unmarshal(rr.Body.Bytes(), &p); err != nil || p.Name != "Clinic" || p.Timezone != "Europe/Berlin" {
			t.Fatalf("provider = %+v, %v", p, err)
		}
	})

	t.Run("set availability converts weekdays and clocks", func(t *testing.T) {
		svc := &fakeService{}
		body := `{"rules":[{"weekday":"monday","start":"09:00","end":"17:00"},{"weekday":"sunday","start":"22:00","end":"24:00"}]}`
		rr := do(t, newTestHandler(svc, pinger{}), "PUT", "/v1/providers/"+pid.String()+"/availability", body)
		if rr.Code != 200 {
			t.Fatalf("status = %d; body %s", rr.Code, rr.Body)
		}
		want := []availability.Rule{{Weekday: time.Monday, Start: 540, End: 1020}, {Weekday: time.Sunday, Start: 1320, End: 1440}}
		if fmt.Sprint(svc.gotRules) != fmt.Sprint(want) {
			t.Fatalf("service got %v, want %v", svc.gotRules, want)
		}
		if !strings.Contains(rr.Body.String(), `"end":"24:00"`) || !strings.Contains(rr.Body.String(), `"weekday":"sunday"`) {
			t.Fatalf("response did not round-trip rules: %s", rr.Body)
		}
	})

	t.Run("list slots", func(t *testing.T) {
		rr := do(t, newTestHandler(&fakeService{}, pinger{}), "GET",
			"/v1/providers/"+pid.String()+"/slots?from=2030-01-07T09:00:00%2B01:00&to=2030-01-08T00:00:00Z&duration=30", "")
		if rr.Code != 200 {
			t.Fatalf("status = %d; body %s", rr.Code, rr.Body)
		}
		var sl SlotList
		if err := json.Unmarshal(rr.Body.Bytes(), &sl); err != nil {
			t.Fatal(err)
		}
		if sl.Duration != 30 || len(sl.Slots) != 1 || sl.Slots[0].Start.Format(time.RFC3339) != "2030-01-07T08:00:00Z" {
			t.Fatalf("slot list = %+v (want UTC output)", sl)
		}
	})

	t.Run("create booking sets Location", func(t *testing.T) {
		svc := &fakeService{}
		body := `{"provider_id":"` + pid.String() + `","customer_id":"` + uuid.NewString() + `","start":"2030-01-07T09:00:00Z","duration":45}`
		rr := do(t, newTestHandler(svc, pinger{}), "POST", "/v1/bookings", body)
		if rr.Code != 201 {
			t.Fatalf("status = %d; body %s", rr.Code, rr.Body)
		}
		if svc.gotHold.Duration != 45*time.Minute || svc.gotHold.ProviderID != pid {
			t.Fatalf("hold request = %+v", svc.gotHold)
		}
		if loc := rr.Header().Get("Location"); !strings.HasPrefix(loc, "/v1/bookings/") {
			t.Fatalf("Location = %q", loc)
		}
	})
}

func TestHealthEndpoints(t *testing.T) {
	tests := []struct {
		name       string
		target     string
		dbErr      error
		wantStatus int
	}{
		{name: "healthz ignores db", target: "/healthz", dbErr: errors.New("down"), wantStatus: 200},
		{name: "readyz ok", target: "/readyz", wantStatus: 200},
		{name: "readyz db down", target: "/readyz", dbErr: errors.New("down"), wantStatus: 503},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := do(t, newTestHandler(&fakeService{}, pinger{err: tt.dbErr}), "GET", tt.target, "")
			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
			if rr.Header().Get(httpx.RequestIDHeader) == "" {
				t.Fatal("missing X-Request-ID")
			}
		})
	}
}
