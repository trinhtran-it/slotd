//go:build integration

// Package dbtest starts a throwaway Postgres for integration tests.
package dbtest

import (
	"context"
	"fmt"
	"io"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/trinhtran-it/slotd/internal/platform/db"
)

// Start runs Postgres 16 in a container, applies migrations and returns a pool.
// Call it once per package from TestMain and invoke cleanup when done.
func Start(ctx context.Context) (pool *pgxpool.Pool, cleanup func(), err error) {
	ctr, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("slotd"),
		postgres.WithUsername("slotd"),
		postgres.WithPassword("slotd"),
		postgres.BasicWaitStrategies(),
		testcontainers.WithLogger(log.New(io.Discard, "", 0)),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("start postgres container: %w", err)
	}
	terminate := func() { _ = testcontainers.TerminateContainer(ctr) }

	url, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		terminate()
		return nil, nil, fmt.Errorf("connection string: %w", err)
	}
	if err = db.Migrate(ctx, url); err != nil {
		terminate()
		return nil, nil, err
	}
	pool, err = db.NewPool(ctx, url)
	if err != nil {
		terminate()
		return nil, nil, err
	}
	return pool, func() { pool.Close(); terminate() }, nil
}
