// Command api serves the slotd HTTP API.
//
// Phase 0 skeleton: it starts, waits for SIGINT/SIGTERM and exits cleanly.
// The HTTP server, config and graceful shutdown arrive in Phase 1.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, logger); err != nil {
		logger.Error("api exited with error", "err", err)
		stop()
		os.Exit(1)
	}
}

// run blocks until ctx is cancelled. It is split from main so it can be tested.
func run(ctx context.Context, logger *slog.Logger) error {
	logger.InfoContext(ctx, "api starting")
	<-ctx.Done()
	logger.InfoContext(ctx, "api stopped")
	return nil
}
