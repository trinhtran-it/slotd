package httpx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"golang.org/x/sync/errgroup"
)

// Serve runs srv on ln until ctx is cancelled, then shuts down gracefully:
// it stops accepting connections and waits up to timeout for in-flight
// requests to finish. If they don't, remaining connections are closed and the
// deadline error is returned.
func Serve(ctx context.Context, srv *http.Server, ln net.Listener, timeout time.Duration) error {
	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http serve: %w", err)
		}
		return nil
	})

	g.Go(func() error {
		<-gctx.Done()
		// The parent ctx is already cancelled; keep its values but not its cancellation.
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			_ = srv.Close() // deadline hit: drop the stragglers
			return fmt.Errorf("http shutdown: %w", err)
		}
		return nil
	})

	return g.Wait()
}
