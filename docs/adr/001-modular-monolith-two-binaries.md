# ADR-001: Modular monolith with two binaries

- **Status:** Accepted
- **Date:** 2026-11-26

## Context

slotd is a booking engine. Its hard problems are about **data correctness**: no double
bookings under concurrency, idempotent writes, reliable events (transactional outbox),
and delayed jobs (hold expiry, reminders). All of these depend on **one Postgres
transaction** covering the booking change, the outbox row and the scheduled job.

There are two kinds of work:

1. **Request/response** — HTTP API, latency-sensitive, scaled by request rate.
2. **Background** — outbox relay, job scheduler, event consumers; throughput-oriented,
   long-running loops, scaled by queue depth.

The team is one developer, so the operational budget is small.

## Decision

Build a **modular monolith**: one Go module, one repository, one database, with
**two binaries**:

| Binary | Responsibility |
|---|---|
| `cmd/api` | HTTP API: providers, availability, slots, bookings |
| `cmd/worker` | outbox relay → NATS JetStream, delayed-job scheduler, event consumers |

Module boundaries are enforced by package structure, not by the network:

- `internal/availability` is pure (no I/O) and depends on nothing else in the repo.
- `internal/booking`, `internal/outbox`, `internal/scheduler` own their own tables and
  expose small interfaces.
- `internal/platform` holds cross-cutting infrastructure (config, log, db, httpx, otel)
  and must not import domain packages.
- `cmd/*` only wire dependencies together.

## Alternatives considered

| Option | Pros | Cons |
|---|---|---|
| **Single binary** (API + background loops in one process) | Simplest deploy | Can't scale or restart API and workers independently; a stuck job loop competes with request latency; shutdown semantics get mixed |
| **Modular monolith, two binaries** (chosen) | Shared transactions and schema; independent scaling and lifecycle; one build, one CI pipeline | Both binaries share a DB schema, so migrations must stay backwards-compatible across a rolling deploy |
| **Microservices** (booking, availability, scheduler, notifier) | Independent deploy per team; tech diversity | Loses the single transaction that the outbox and hold-expiry rely on, so we'd need sagas/2PC; network failure modes everywhere; much more infra for a one-person project; no team-scaling benefit to pay for it |

## Consequences

- **Positive:** booking + outbox + job enqueue commit atomically. Local dev is
  `make up` plus two `go run` commands. Refactoring across modules is a compile-time
  change, not an API version bump.
- **Negative:** both binaries ship together and share a schema. Migrations follow
  expand/contract so old and new binaries can run side by side.
- **Escape hatch:** because modules talk through interfaces and events (NATS), one of
  them (e.g. notifications) can be extracted into a separate service later without
  redesigning it, if scale or team structure ever justify it.
