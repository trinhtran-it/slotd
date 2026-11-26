# slotd

[![ci](https://github.com/trinhtran-it/slotd/actions/workflows/ci.yml/badge.svg)](https://github.com/trinhtran-it/slotd/actions/workflows/ci.yml)

A booking engine in Go that guarantees no double bookings under concurrency, with a
transactional outbox, a Postgres-backed delayed-job scheduler and end-to-end observability.

> **Status:** Phase 0 (project skeleton). Features land phase by phase.

## Quickstart

Requirements: Go 1.25+, Docker, [golangci-lint](https://golangci-lint.run/) v2.

```sh
make up        # Postgres 16, NATS JetStream, Jaeger, Prometheus, Grafana
make migrate   # apply migrations
make lint test # lint + unit tests (race detector)
make help      # all targets
```

| Service | URL |
|---|---|
| Postgres | `postgres://slotd:slotd@localhost:5432/slotd` |
| NATS | `nats://localhost:4222` (monitor: http://localhost:8222) |
| Jaeger UI | http://localhost:16686 |
| Prometheus | http://localhost:9090 |
| Grafana | http://localhost:3000 |

Ports can be overridden through env vars (see `docker-compose.yml`).

## Layout

```
cmd/api/                  HTTP server binary
cmd/worker/               outbox relay + scheduler + consumers
internal/booking/         domain, service, repository
internal/availability/    pure slot computation (no DB)
internal/outbox/          writer + relay
internal/scheduler/       job queue, worker pool, reaper
internal/platform/        config, log, db, httpx, otel
migrations/               goose SQL migrations
queries/                  sqlc query files
api/openapi.yaml          API contract
deploy/                   Prometheus config, Grafana provisioning and dashboards
docs/adr/                 architecture decision records
loadtest/                 k6 scripts
```

## Architecture decisions

- [ADR-001: Modular monolith with two binaries](docs/adr/001-modular-monolith-two-binaries.md)
