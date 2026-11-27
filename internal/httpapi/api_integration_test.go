//go:build integration

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/trinhtran-it/slotd/internal/booking"
	"github.com/trinhtran-it/slotd/internal/platform/db/dbtest"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	pool, cleanup, err := dbtest.Start(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, "integration setup:", err)
		os.Exit(1)
	}
	testPool = pool
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// TestAPI_ProviderSlotsHold walks the Phase 1 acceptance flow over real HTTP
// and a real database: create provider → set availability → list slots → hold
// → the held slot disappears and a second hold conflicts.
func TestAPI_ProviderSlotsHold(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := booking.NewService(booking.NewPostgresStore(testPool), 10*time.Minute)
	srv := httptest.NewServer(NewHandler(svc, testPool, logger))
	t.Cleanup(srv.Close)

	call := func(method, path string, body any, wantStatus int, out any) {
		t.Helper()
		var buf bytes.Buffer
		if body != nil {
			if err := json.NewEncoder(&buf).Encode(body); err != nil {
				t.Fatal(err)
			}
		}
		req, err := http.NewRequestWithContext(t.Context(), method, srv.URL+path, &buf)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != wantStatus {
			t.Fatalf("%s %s = %d, want %d; body %s", method, path, resp.StatusCode, wantStatus, raw)
		}
		if out != nil {
			if err := json.Unmarshal(raw, out); err != nil {
				t.Fatalf("decode %s: %v", raw, err)
			}
		}
	}

	var p Provider
	call("POST", "/v1/providers", map[string]string{"name": "Dr. Example", "timezone": "Europe/Berlin"}, 201, &p)

	rules := map[string]any{"rules": []map[string]string{
		{"weekday": "monday", "start": "09:00", "end": "12:00"},
	}}
	call("PUT", "/v1/providers/"+p.Id.String()+"/availability", rules, 200, nil)

	// Next Monday at least a week out, in Berlin local time.
	berlin, _ := time.LoadLocation("Europe/Berlin")
	day := time.Now().In(berlin).AddDate(0, 0, 7)
	for day.Weekday() != time.Monday {
		day = day.AddDate(0, 0, 1)
	}
	from := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, berlin)
	q := url.Values{
		"from":     {from.Format(time.RFC3339)},
		"to":       {from.AddDate(0, 0, 1).Format(time.RFC3339)},
		"duration": {"60"},
	}
	slotsPath := "/v1/providers/" + p.Id.String() + "/slots?" + q.Encode()

	var before SlotList
	call("GET", slotsPath, nil, 200, &before)
	if len(before.Slots) != 3 {
		t.Fatalf("want 3 one-hour slots in 09:00-12:00, got %+v", before.Slots)
	}
	if got := before.Slots[0].Start.In(berlin).Format("15:04"); got != "09:00" {
		t.Fatalf("first slot starts %s Berlin time, want 09:00", got)
	}

	target := before.Slots[1]
	holdReq := map[string]any{
		"provider_id": p.Id,
		"customer_id": uuid.New(),
		"start":       target.Start,
		"duration":    60,
	}
	var b Booking
	call("POST", "/v1/bookings", holdReq, 201, &b)
	if b.Status != "held" || !b.Start.Equal(target.Start) || b.HoldExpiresAt == nil {
		t.Fatalf("booking = %+v", b)
	}

	var after SlotList
	call("GET", slotsPath, nil, 200, &after)
	if len(after.Slots) != 2 {
		t.Fatalf("held slot still listed: %+v", after.Slots)
	}

	holdReq["customer_id"] = uuid.New()
	var conflict struct{ Code string }
	call("POST", "/v1/bookings", holdReq, 409, &conflict)
	if conflict.Code != "slot_taken" {
		t.Fatalf("code = %q, want slot_taken", conflict.Code)
	}

	var got Booking
	call("GET", "/v1/bookings/"+b.Id.String(), nil, 200, &got)
	if got.Id != b.Id {
		t.Fatalf("GET booking = %+v", got)
	}

	call("GET", "/readyz", nil, 200, nil)
}
