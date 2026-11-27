# slotd

[![ci](https://github.com/trinhtran-it/slotd/actions/workflows/ci.yml/badge.svg)](https://github.com/trinhtran-it/slotd/actions/workflows/ci.yml)

A booking engine in Go that guarantees no double bookings under concurrency, with a
transactional outbox, a Postgres-backed delayed-job scheduler and end-to-end observability.

> **Status:** Phase 1 (foundation): providers, weekly availability, slot listing and
> holds work end to end. Confirm/cancel, idempotency keys and events come next.

## Quickstart

Requirements: Go 1.26+, Docker, [golangci-lint](https://golangci-lint.run/) v2.

```sh
make up        # Postgres 16, NATS JetStream, Jaeger, Prometheus, Grafana
make migrate   # apply migrations
make api       # run the API on :8080
make help      # all targets
```

### Try it

```sh
# 1. Create a provider in Berlin
PID=$(curl -s -X POST localhost:8080/v1/providers -H 'Content-Type: application/json' \
  -d '{"name":"Dr. Example","timezone":"Europe/Berlin"}' | jq -r .id)

# 2. Mondays 09:00-12:00, Berlin wall-clock time
curl -s -X PUT localhost:8080/v1/providers/$PID/availability -H 'Content-Type: application/json' \
  -d '{"rules":[{"weekday":"monday","start":"09:00","end":"12:00"}]}'

# 3. Free one-hour slots on a Monday (use --data-urlencode: "+" in offsets must be escaped)
curl -s -G localhost:8080/v1/providers/$PID/slots \
  --data-urlencode from=2030-01-07T00:00:00+01:00 \
  --data-urlencode to=2030-01-08T00:00:00+01:00 -d duration=60 | jq

# 4. Hold 10:00 Berlin (09:00Z). Repeat it to get 409 slot_taken.
curl -s -X POST localhost:8080/v1/bookings -H 'Content-Type: application/json' \
  -d "{\"provider_id\":\"$PID\",\"customer_id\":\"$(uuidgen)\",\"start\":\"2030-01-07T09:00:00Z\",\"duration\":60}" | jq
```

| Service | URL |
|---|---|
| API | http://localhost:8080 (`/healthz`, `/readyz`) |
| Postgres | `postgres://slotd:slotd@localhost:5432/slotd` |
| NATS | `nats://localhost:4222` (monitor: http://localhost:8222) |
| Jaeger UI | http://localhost:16686 |
| Prometheus | http://localhost:9090 |
| Grafana | http://localhost:3000 |

Configuration is environment variables only, validated at startup (see `.env.example`).

## API

Contract: [`api/openapi.yaml`](api/openapi.yaml). Types and routing are generated from it with oapi-codegen.

| Method | Path | Purpose |
|---|---|---|
| POST | `/v1/providers` | create provider |
| PUT | `/v1/providers/{id}/availability` | replace weekly rules |
| GET | `/v1/providers/{id}/slots?from=&to=&duration=` | list free slots |
| POST | `/v1/bookings` | hold a time range |
| GET | `/v1/bookings/{id}` | read booking |
| POST | `/v1/bookings/{id}/confirm` · `/cancel` | *not implemented yet (501)* |

Errors are RFC 9457 `application/problem+json` with a stable `code`
(`invalid_input`, `not_found`, `slot_taken`, `outside_availability`, …) and the request ID.

## How it works

- **No double booking:** a Postgres exclusion constraint
  (`EXCLUDE USING gist (provider_id WITH =, during WITH &&) WHERE status IN ('held','confirmed')`)
  rejects overlapping active bookings, so concurrent holds can't both succeed. The violation
  (`23P01`) maps to `409 slot_taken`.
- **Slots are computed, never stored:** availability windows minus active bookings, in the
  pure package `internal/availability`.
- **DST-correct:** rules are wall-clock times in the provider's IANA zone. A 09:00–17:00 rule
  stays 09:00–17:00 local on transition days. Boundaries in a skipped hour snap to the
  transition, and a repeated hour is covered both times. Table-driven tests cover Europe/Berlin
  spring-forward and fall-back.
- **Production basics:** typed config with fail-fast validation, JSON `slog` with request IDs,
  graceful shutdown that drains in-flight requests, liveness vs readiness probes.

## Development

```sh
make lint       # golangci-lint
make test       # unit tests, race detector
make test-int   # + integration tests against real Postgres (testcontainers; needs Docker)
make gen        # regenerate sqlc + OpenAPI code after editing queries/ or api/openapi.yaml
make vuln       # govulncheck
```

## Layout

```
cmd/api/                  HTTP server binary
cmd/worker/               outbox relay + scheduler + consumers
internal/availability/    pure slot computation (no DB)
internal/booking/         domain, service, Postgres store
internal/httpapi/         HTTP handlers, error mapping (generated server in api.gen.go)
internal/outbox/          writer + relay
internal/scheduler/       job queue, worker pool, reaper
internal/platform/        config, logging, db (+ sqlc), httpx
migrations/               goose SQL migrations (embedded)
queries/                  sqlc query files
api/openapi.yaml          API contract
deploy/                   Prometheus config, Grafana provisioning and dashboards
docs/adr/                 architecture decision records
loadtest/                 k6 scripts
```

## Architecture decisions

- [ADR-001: Modular monolith with two binaries](docs/adr/001-modular-monolith-two-binaries.md)
