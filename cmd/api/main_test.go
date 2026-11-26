package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestRun_ReturnsNilWhenContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	errc := make(chan error, 1)
	go func() { errc <- run(ctx, logger) }()

	cancel()

	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("run() error = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run() did not return after context cancellation")
	}
}
