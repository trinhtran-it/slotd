// Command api serves the slotd HTTP API.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // embed the IANA database so provider time zones work in minimal images

	"github.com/trinhtran-it/slotd/internal/booking"
	"github.com/trinhtran-it/slotd/internal/httpapi"
	"github.com/trinhtran-it/slotd/internal/platform/config"
	"github.com/trinhtran-it/slotd/internal/platform/db"
	"github.com/trinhtran-it/slotd/internal/platform/httpx"
	"github.com/trinhtran-it/slotd/internal/platform/logging"
)

func main() {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	logger := logging.New(os.Stdout, cfg.LogLevel)
	slog.SetDefault(logger)

	if err := run(context.Background(), cfg, logger); err != nil {
		logger.Error("api exited with error", "err", err)
		os.Exit(1)
	}
}

// run wires dependencies and serves until SIGINT/SIGTERM, then drains
// in-flight requests for up to cfg.ShutdownTimeout before closing the pool.
func run(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close() // runs after Serve returns, i.e. after requests have drained

	svc := booking.NewService(booking.NewPostgresStore(pool), cfg.HoldTTL)

	srv := &http.Server{
		Handler:           httpapi.NewHandler(svc, pool, logger),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	// Bind before serving so "address in use" fails startup immediately.
	ln, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.HTTPAddr, err)
	}
	logger.InfoContext(ctx, "api listening", "addr", ln.Addr().String())

	err = httpx.Serve(ctx, srv, ln, cfg.ShutdownTimeout)
	logger.InfoContext(context.WithoutCancel(ctx), "api stopped")
	return err
}
