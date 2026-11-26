// Command worker runs the outbox relay, the delayed-job scheduler and event consumers.
//
// Phase 0 skeleton: it starts, waits for SIGINT/SIGTERM and exits cleanly.
// The relay arrives in Phase 3 and the scheduler in Phase 4.
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
		logger.Error("worker exited with error", "err", err)
		stop()
		os.Exit(1)
	}
}

// run blocks until ctx is cancelled. It is split from main so it can be tested.
func run(ctx context.Context, logger *slog.Logger) error {
	logger.InfoContext(ctx, "worker starting")
	<-ctx.Done()
	logger.InfoContext(ctx, "worker stopped")
	return nil
}
