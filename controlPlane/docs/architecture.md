# AETHER-GRID — Phase 1 Architecture Decisions

This document records the key design decisions made during Phase 1 and the
rationale behind them.

| Decision       | Selected                | Why                                                    |
| -------------- | ----------------------- | ------------------------------------------------------ |
| Language       | Go                      | Infrastructure/control-plane suitability               |
| HTTP           | `net/http`              | Small API; no framework required                       |
| API style      | REST/JSON               | Simple resource-oriented API                           |
| Database       | SQLite                  | Persistent local state without external infrastructure |
| DB abstraction | Repository interface    | Decouple business logic from storage                   |
| IDs            | UUID                    | Distributed-friendly identity                          |
| Architecture   | Layered                 | Separate transport/business/data concerns              |
| State model    | Desired vs actual       | Foundation for reconciliation                          |
| Logging        | Standard library        | Minimal dependencies                                   |
| Configuration  | Environment variables   | Simple deployment model                                |
| Migrations     | Versioned SQL           | Reproducible schema                                    |
| Testing        | Go testing + HTTP tests | Native ecosystem support                               |

---

## Language — Go

AETHER-GRID is infrastructure/control-plane software that will eventually run
long-lived reconciliation loops, network management, and Kubernetes operators.
Go's concurrency primitives, low runtime overhead, single-binary deployment,
and strong cloud-native ecosystem fit this profile well.

**Alternatives considered and rejected:**

- **Python** — excellent for scripting, but weaker fit for continuously
  running concurrent system software.
- **C++** — strong performance, but its complexity and manual memory
  management are not justified: AETHER-GRID is not CPU-bound enough to
  benefit.
- **Rust** — technically viable and memory-safe, but Go provides a simpler
  development model and a stronger Kubernetes ecosystem.

## HTTP — `net/http` standard library

The Phase 1 API is intentionally small (ten routes). The standard library
provides routing (including method-aware patterns), JSON encoding, status
codes, and middleware. A framework would add abstractions without meaningful
value yet. A framework may be adopted later if the API grows.

## API style — REST over HTTP + JSON

The API is resource-oriented (`nodes`, `node/{id}/heartbeat`, ...), simple to
inspect with `curl`, and sufficient for the current requirements.

- **GraphQL** rejected: not a query-heavy client application; would add
  schema/resolver complexity.
- **gRPC** rejected for Phase 1: there is no real Edge Agent yet, so it would
  solve a future problem prematurely. gRPC may return for high-frequency
  agent/control-plane traffic in a later phase.

## Database — SQLite

SQLite provides real persistence with zero external database server,
transactions, and easy testing — ideal for a local development/learning
environment. The DB file survives application restarts, satisfying the Phase 1
persistence requirement.

**PostgreSQL** is a stronger choice for a production distributed control
plane, but Phase 1 has no multi-server or high-concurrency needs. The
repository abstraction keeps PostgreSQL viable later without rewriting
business logic.

## Repository abstraction

The service layer depends on a `NodeRepository` interface
(`internal/repository/node_repository.go`), not on SQLite directly. SQLite is
one implementation (`internal/repository/sqlite`). This keeps business logic
decoupled from storage and enables a future PostgreSQL implementation and
mock-based unit tests.

## IDs — UUID

Nodes get a control-plane-generated UUID at registration. UUIDs are globally
unique without a centralized integer sequence, which matters when nodes are
distributed across edge locations. Auto-increment integers were rejected as
less suitable for distributed identities.

## Architecture — layered

Dependency direction is strictly:

```text
HTTP  →  Service  →  Repository  →  Database
```

- **Domain** (`internal/domain`) — plain models and typed statuses; depends on
  nothing internal.
- **Service** (`internal/service`) — business logic: registration, heartbeats,
  desired-state updates, comparison.
- **Repository** (`internal/repository`) — persistence abstraction and the
  SQLite implementation.
- **HTTP** (`internal/http`) — handlers parse/validate requests, call services,
  and map results to responses; middleware handles logging and panic recovery.

Handlers do not contain business logic, and database models are never exposed
directly from handlers.

## State model — desired vs actual

Each node carries both an actual `status` and a `desired_status`. On
registration the actual status is `PROVISIONING` and the desired status is
`READY`. Reconciliation compares the two and reports `IN_SYNC` or
`DRIFT_DETECTED`. Phase 1 deliberately does not repair drift; that is Phase 3.

## Statuses — strongly typed

`NodeStatus` is a defined string type with exported constants. Status values
are validated at the application boundary, so arbitrary status strings cannot
propagate through the codebase or into the database.

## Logging — standard library

`log` is used for startup, shutdown, registration, heartbeats, reconciliation,
and errors. Each log line includes enough context (e.g. node ID) to identify
the affected node. No logging framework was warranted.

## Configuration — environment variables

`SERVER_HOST`, `SERVER_PORT`, `DATABASE_PATH` are read from the environment
with sensible local defaults (`0.0.0.0:8080`, `./data/aether-grid.db`). No
config framework was introduced.

## Migrations — versioned SQL

The schema lives in `migrations/*.sql`, embedded into the binary and applied at
startup. Applied migrations are recorded in a `schema_migrations` table, so the
schema is reproducible and repeatable. Schema creation is not scattered through
application code.

## Testing — Go testing + HTTP tests

- **Unit tests** validate node inputs and service logic against an in-memory
  repository.
- **Repository tests** exercise the SQLite implementation against temporary
  databases, including reopen/persistence behavior.
- **HTTP tests** exercise every endpoint through `net/http/httptest` against a
  real SQLite database, covering success and failure paths.
- **Integration test** (`tests/`) runs the complete lifecycle, including a
  simulated application restart.

## External dependencies

Phase 1 adds only two:

- `github.com/google/uuid` — battle-tested UUID generation and parsing.
- `modernc.org/sqlite` — pure-Go SQLite driver (no CGO requirement).

Both solve problems the standard library does not reasonably cover.