package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/trinhtran-it/slotd/internal/platform/logging"
)

func TestRequestID(t *testing.T) {
	tests := []struct {
		name     string
		incoming string
		wantSame bool
	}{
		{name: "generated when absent", incoming: ""},
		{name: "valid incoming id is reused", incoming: "abc-123.X_y", wantSame: true},
		{name: "unsafe incoming id is replaced", incoming: "evil\nline"},
		{name: "overlong incoming id is replaced", incoming: strings.Repeat("a", 129)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var seen string
			h := RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				seen = RequestIDFromContext(r.Context())
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.incoming != "" {
				req.Header.Set(RequestIDHeader, tt.incoming)
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			got := rr.Header().Get(RequestIDHeader)
			if got == "" || got != seen {
				t.Fatalf("response id %q, context id %q: want equal and non-empty", got, seen)
			}
			if tt.wantSame != (got == tt.incoming) {
				t.Fatalf("id = %q, incoming %q, wantSame %v", got, tt.incoming, tt.wantSame)
			}
		})
	}
}

func TestMiddlewareChain_LogsRequestIDRouteAndStatus(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.New(&buf, slog.LevelInfo)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /things/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	h := Chain(mux, RequestID, AccessLog(logger), Recover(logger))

	req := httptest.NewRequest(http.MethodGet, "/things/42", nil)
	req.Header.Set(RequestIDHeader, "req-1")
	h.ServeHTTP(httptest.NewRecorder(), req)

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("want one JSON log line, got %q", buf.String())
	}
	want := map[string]any{"request_id": "req-1", "route": "GET /things/{id}", "path": "/things/42", "status": float64(418)}
	for k, v := range want {
		if rec[k] != v {
			t.Errorf("log[%q] = %v, want %v", k, rec[k], v)
		}
	}
}

func TestRecover_WritesProblem(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.New(&buf, slog.LevelInfo)
	h := Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }), RequestID, Recover(logger))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/x", nil))

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q", ct)
	}
	var p Problem
	if err := json.Unmarshal(rr.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Status != 500 || p.Code != "internal" || p.Instance != "/x" || p.RequestID == "" || p.Detail != "" {
		t.Fatalf("problem = %+v", p)
	}
	if !strings.Contains(buf.String(), "handler panic") {
		t.Fatalf("panic not logged: %q", buf.String())
	}
}

func TestServe_DrainsInFlightRequestOnShutdown(t *testing.T) {
	started := make(chan struct{})
	srv := &http.Server{
		ReadHeaderTimeout: time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			close(started)
			time.Sleep(200 * time.Millisecond) // in-flight work outlives the shutdown signal
			_, _ = io.WriteString(w, "done")
		}),
	}
	ln := listen(t)
	ctx, cancel := context.WithCancel(t.Context())
	serveErr := make(chan error, 1)
	go func() { serveErr <- Serve(ctx, srv, ln, 5*time.Second) }()

	type result struct {
		body string
		err  error
	}
	resc := make(chan result, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String())
		if err != nil {
			resc <- result{err: err}
			return
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		resc <- result{body: string(b), err: err}
	}()

	<-started
	cancel() // SIGTERM arrives while the request is in flight

	res := <-resc
	if res.err != nil || res.body != "done" {
		t.Fatalf("in-flight request: body %q, err %v; want completed", res.body, res.err)
	}
	if err := <-serveErr; err != nil {
		t.Fatalf("Serve() = %v, want nil", err)
	}
	if _, err := net.DialTimeout("tcp", ln.Addr().String(), 200*time.Millisecond); err == nil {
		t.Fatal("listener still accepting connections after shutdown")
	}
}

func TestServe_ShutdownTimeoutReturnsError(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	srv := &http.Server{
		ReadHeaderTimeout: time.Second,
		Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			close(started)
			<-release // stuck handler
		}),
	}
	t.Cleanup(func() { close(release) })

	ln := listen(t)
	ctx, cancel := context.WithCancel(t.Context())
	serveErr := make(chan error, 1)
	go func() { serveErr <- Serve(ctx, srv, ln, 100*time.Millisecond) }()
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String())
		if err == nil {
			_ = resp.Body.Close()
		}
	}()

	<-started
	cancel()

	select {
	case err := <-serveErr:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Serve() = %v, want DeadlineExceeded", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve() did not return after shutdown timeout")
	}
}

func listen(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return ln
}
