# AETHER-GRID

A software-controlled system for provisioning, connecting, deploying, monitoring, and managing Kubernetes nodes across distributed edge environments.

AETHER-GRID follows a **desired-state and reconciliation model**: an operator declares what the infrastructure should look like, and the system continuously compares that desired state against the actual state of the edge fleet, detecting (and eventually repairing) drift.

This repository contains **Phase 1**: the Control Plane foundation only.

---

## Phase 1 Scope

Phase 1 implements a standalone Go control plane that:

1. Starts as a single HTTP service
2. Accepts node registrations
3. Persists nodes in SQLite
4. Tracks node state (desired vs actual)
5. Receives heartbeats
6. Compares desired state with actual state
7. Detects drift
8. Exposes everything through a REST/JSON API
9. Survives a restart without losing registered nodes
10. Is covered by unit, repository, HTTP, and end-to-end tests

There is **no real edge node, Kubernetes, Terraform, WireGuard, or Prometheus** yet. Reconciliation in Phase 1 only *detects* differences; it never repairs anything. Those capabilities arrive in later phases.

---

## Architecture

```text
                         Operator / User
                               │
                               ▼
                         HTTP API (net/http)
                               │
              ┌────────────────┼────────────────┐
              ▼                ▼                ▼
       Node Service     Heartbeat Service   Reconciliation
                                               Service
              │                │                │
              └────────────────┼────────────────┘
                               ▼
                       NodeRepository
                         (interface)
                               │
                               ▼
                          SQLite
```

Dependency direction is strictly one-way:

```text
HTTP  →  Service  →  Repository  →  Database
```

Domain models live in `internal/domain` and depend on neither HTTP nor SQLite.

### Project layout

```text
cmd/aether-grid/main.go                  Entry point, config, server lifecycle
internal/config/                         Environment-based configuration
internal/domain/node.go                  Node model and statuses
internal/service/                        Business logic
internal/repository/                     Repository interface
internal/repository/sqlite/              SQLite implementation
internal/http/router.go                  Route wiring
internal/http/handlers/                  HTTP handlers (parse/validate/call services)
internal/http/middleware/                Logging and recovery middleware
migrations/                              Versioned SQL schema migrations
tests/                                   End-to-end integration test
```

---

## Running Locally

Requirements: Go 1.22+ (tested with 1.26).

```bash
go run ./cmd/aether-grid
```

The server listens on `0.0.0.0:8080` by default and creates its SQLite database (and the `data/` directory) on first start. Migrations are applied automatically at startup.

### Configuration (environment variables)

| Variable         | Default                | Description                        |
| ---------------- | ---------------------- | ---------------------------------- |
| `SERVER_HOST`    | `0.0.0.0`              | Bind host                          |
| `SERVER_PORT`    | `8080`                 | Bind port                          |
| `DATABASE_PATH`  | `./data/aether-grid.db`| SQLite database file path          |

Example with overrides:

```bash
SERVER_HOST=127.0.0.1 SERVER_PORT=9000 DATABASE_PATH=/tmp/aether-grid.db go run ./cmd/aether-grid
```

---

## API

All endpoints accept and return JSON. Errors are returned consistently as `{"error": "..."}`.

| Method | Path                        | Description                                      |
| ------ | --------------------------- | ------------------------------------------------ |
| POST   | `/nodes`                    | Register a node                                  |
| GET    | `/nodes`                    | List all nodes                                   |
| GET    | `/nodes/{id}`               | Get one node                                     |
| DELETE | `/nodes/{id}`               | Delete a node                                    |
| POST   | `/nodes/{id}/heartbeat`     | Record a heartbeat                               |
| GET    | `/nodes/{id}/state`         | Get actual state                                 |
| GET    | `/nodes/{id}/desired-state` | Get desired state                                |
| PUT    | `/nodes/{id}/desired-state` | Set desired state (`{"status":"READY"}`)         |
| POST   | `/nodes/{id}/reconcile`     | Compare desired vs actual state                  |

### curl examples

Register a node:

```bash
curl -X POST http://localhost:8080/nodes \
  -H "Content-Type: application/json" \
  -d '{
    "name": "edge-01",
    "location": "addis-01",
    "ip_address": "10.0.0.10",
    "kubernetes_enabled": true,
    "wireguard_enabled": true
  }'
```

List nodes:

```bash
curl http://localhost:8080/nodes
```

Get a node (replace `{id}` with the UUID returned above):

```bash
curl http://localhost:8080/nodes/{id}
```

Send a heartbeat:

```bash
curl -X POST http://localhost:8080/nodes/{id}/heartbeat
```

Check state and desired state:

```bash
curl http://localhost:8080/nodes/{id}/state
curl http://localhost:8080/nodes/{id}/desired-state
```

Run reconciliation (returns `IN_SYNC` or `DRIFT_DETECTED`):

```bash
curl -X POST http://localhost:8080/nodes/{id}/reconcile
```

Set the desired state:

```bash
curl -X PUT http://localhost:8080/nodes/{id}/desired-state \
  -H "Content-Type: application/json" \
  -d '{"status":"READY"}'
```

Delete a node:

```bash
curl -X DELETE http://localhost:8080/nodes/{id}
```

### Status codes

`201` Created, `200` OK, `204` No Content, `400` Bad Request, `404` Not Found, `409` Conflict, `500` Internal Server Error.

---

## Node lifecycle statuses

```text
PROVISIONING  PROVISIONED  CONNECTING  REGISTERED  CONFIGURING
READY         UNHEALTHY    OFFLINE     RECOVERING
```

New nodes are registered with actual status `PROVISIONING` and desired status `READY`. Heartbeats update the `last_heartbeat` timestamp without changing status or desired state.

---

## Testing

```bash
go test ./...
```

Run with the race detector:

```bash
go test -race ./...
```

The suite includes:

- **Unit tests** for node validation, node/heartbeat/reconciliation services (in-memory repository)
- **Repository tests** against a temporary SQLite database (create, get, list, update, delete, missing-node behavior, persistence across reopen)
- **HTTP tests** for every endpoint, covering success and failure cases
- **An end-to-end integration test** (`tests/`) that registers a node, verifies persistence, sends heartbeats, sets desired state, detects drift, simulates a restart, verifies the node survives, deletes it, and confirms a 404

---

## Design Decisions

See [docs/architecture.md](docs/architecture.md) for the full rationale, including why the major alternatives were not selected.