// Package scheduler implements the Postgres-backed delayed-job queue:
// claiming with SKIP LOCKED, a worker pool, retries with backoff, and a reaper.
package scheduler
