# AETHER-GRID

A software-controlled system for provisioning, connecting, deploying, monitoring, and managing Kubernetes nodes across distributed edge environments.

AETHER-GRID follows a **desired-state and reconciliation model**: an operator declares what the infrastructure should look like, and the system continuously compares that desired state against the actual state of the edge fleet, detecting (and repairing) drift.

This repository contains the **Control Plane** across four phases:

- **Phase 1** — control plane foundation: node registry, desired/actual state, heartbeats, drift detection.
- **Phase 2** — the Edge Node Agent (in `../nodeAgent`).
- **Phase 3** — the **Reconciliation Engine**: continuous observation, planning and corrective action execution.
- **Phase 4** — the **Kubernetes Integration** layer: cluster observation through the agent, Kubernetes desired/actual state and drift detection.

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

There is **no real edge node, Kubernetes, Terraform, WireGuard, or Prometheus** yet. Phase 3 reconciliation executes real corrective actions against the Phase 2 Edge Node Agent (dispatching `RESTART_AGENT` recovery commands); infrastructure provisioning arrives in later phases.

---

## Phase 3: Reconciliation Engine

Phase 3 turns AETHER-GRID from a system that merely records state into a system that **continuously reconciles** the edge fleet toward the desired state.

### Desired vs actual state

- **Desired state** is what the operator declares a node should converge to: a lifecycle status plus infrastructure flags (`kubernetes_enabled`, `wireguard_enabled`).
- **Actual state** is what the control plane observes: the reported status, the same infrastructure flags, and the last heartbeat time. It is never mutated to satisfy desired state; it only reflects observation.
- **Drift** is any structured difference between the two.

### The controller loop

The engine follows the classic controller pattern:

```text
Observe ──▶ Compare ──▶ Determine ──▶ Execute ──▶ Observe again
   │                                      │
   └──────────── desired == actual ◀──────┘
                        │
                        ▼
                     IN_SYNC
```

| Result                | Meaning                                                        |
| --------------------- | -------------------------------------------------------------- |
| `IN_SYNC`             | Desired and actual state match.                                |
| `DRIFT_DETECTED`      | Differences exist but no corrective action applies (for example a transitional status like `PROVISIONING`, or detect-only mode). |
| `RECONCILING`         | A corrective action was dispatched; the engine is awaiting convergence. |
| `RECONCILED`          | A dispatched recovery converged the node.                      |
| `RECONCILIATION_FAILED` | The corrective action failed or exhausted its retries.         |

### Observer / Planner / Executor

The engine is split so later phases can plug in new infrastructure integrations without touching the loop:

- **Observer** (`RepositoryObserver`) reads node state and flags nodes whose heartbeats are older than `NODE_HEARTBEAT_TIMEOUT` as effectively `OFFLINE`.
- **Planner** (`ReconciliationPlanner`) turns structured differences into actions. `OFFLINE`/`UNHEALTHY` plans `RECOVER_NODE`; transitional statuses plan nothing.
- **Executor** (`ReconciliationExecutor`) carries out actions. Today `RECOVER_NODE` dispatches a real `RESTART_AGENT` command to the edge agent. Actions without an execution path yet (for example `ENABLE_KUBERNETES`) fail explicitly as **unsupported** rather than silently succeeding.

### Triggering

- **Periodic**: every `RECONCILIATION_INTERVAL` the engine sweeps the fleet.
- **Event-driven**: node registration, state reports, desired-state changes and heartbeats notify the engine immediately (coalesced through a deduplicating work queue).

### Concurrency and retries

- A bounded worker pool (`RECONCILIATION_WORKERS`) reconciles nodes concurrently.
- Each node is serialized: two workers can never reconcile the same node at once.
- Failed actions retry with exponential backoff (base 1s, capped at `RECONCILIATION_MAX_BACKOFF`) up to `RECONCILIATION_MAX_RETRIES`; a failed cycle is recorded as `RECONCILIATION_FAILED` with the attempt count and error.
- A dispatched recovery tracks a deadline (`RECONCILIATION_RECOVERY_TIMEOUT`); while it is within its deadline the engine does not re-dispatch.

### Reconciliation metadata and history

Every node stores `last_reconciliation`, `last_successful_reconciliation`, `last_reconciliation_result`, `last_reconciliation_action`, `last_reconciliation_error`, `last_reconciliation_deadline` and `reconciliation_attempts`. Every non-`IN_SYNC` cycle also appends a lightweight row to the `reconciliation_events` table for observability (steady-state `IN_SYNC` cycles are not logged to avoid flooding).

### Configuration

| Variable                         | Default | Description                                  |
| -------------------------------- | ------- | -------------------------------------------- |
| `RECONCILIATION_INTERVAL`        | `10s`   | Periodic sweep interval                      |
| `RECONCILIATION_WORKERS`         | `4`     | Bounded worker pool size                     |
| `NODE_HEARTBEAT_TIMEOUT`         | `30s`   | Staleness threshold that marks a node OFFLINE |
| `RECONCILIATION_MAX_RETRIES`     | `3`     | Max execution attempts per drift resolution  |
| `RECONCILIATION_MAX_BACKOFF`     | `10s`   | Upper bound for exponential backoff          |
| `RECONCILIATION_RECOVERY_TIMEOUT`| `60s`   | How long a recovery may take to converge     |

Setting `RECONCILIATION_MAX_RETRIES=0` enables **detect-only mode**: drift is reported but never acted on.

---

## Phase 4: Kubernetes Integration

Phase 4 establishes the Kubernetes integration layer. AETHER-GRID does **not** become a Kubernetes operator and does **not** install or repair clusters; it observes an existing cluster through the edge agent and reconciles the fleet toward the declared expectation.

### Where the integration lives

```text
AETHER-GRID logic → Kubernetes abstraction → client-go → Kubernetes API
```

- The **node agent** owns all contact with Kubernetes via `client-go`, hidden behind a small interface (`kubernetes.KubernetesClient`). Command handlers and the reconciliation engine never call `client-go` directly.
- The **control plane** never talks to a Kubernetes cluster. It consumes the agent's reported Kubernetes state and the declared desired state, and detects drift.

### Kubernetes state model

- **Desired state** (per node): `kubernetes.enabled` and `kubernetes.minimum_ready_nodes`. When `enabled` is false, no Kubernetes expectations are enforced.
- **Actual state** (reported by the agent): `available`, `status` (`DISABLED`/`UNAVAILABLE`/`DEGRADED`/`READY`), `version`, `node_count`, `ready_nodes`, `not_ready_nodes`, and a `workload` summary (`total_pods`, `running_pods`, `failed_pods`).
- Kubernetes health is **separate** from node health: a node can be `READY` while its Kubernetes integration is `UNAVAILABLE`.

### Drift and reconciliation

The reconciliation engine compares desired vs observed Kubernetes state:

- Desired `enabled` while the cluster is unavailable → difference `kubernetes.available` (`desired: true`, `actual: false`).
- Cluster available but `ready_nodes < minimum_ready_nodes` → difference `kubernetes.ready_nodes`.

These differences surface as `DRIFT_DETECTED` in the structured reconcile result. Phase 4 has **no executable Kubernetes remediation** — the planner deliberately produces no corrective action for Kubernetes drift, and actions that would modify the cluster fail explicitly as unsupported rather than faking success. Node lifecycle recovery (`RESTART_AGENT`) is unaffected.

### Agent-side commands

| Command                    | Meaning                                            |
| -------------------------- | -------------------------------------------------- |
| `GET_KUBERNETES_STATUS`    | Report the observed Kubernetes state               |
| `LIST_KUBERNETES_NODES`    | List cluster nodes                                 |
| `LIST_KUBERNETES_PODS`     | List cluster pods (optional `namespace` parameter) |
| `CREATE_TEST_NAMESPACE`    | Create a dedicated test namespace                  |
| `DELETE_TEST_NAMESPACE`    | Delete a dedicated test namespace                  |

Every Kubernetes API call runs with a bounded context timeout; errors are translated to structured codes (`KUBERNETES_UNAVAILABLE`, `KUBERNETES_UNAUTHORIZED`, `KUBERNETES_FORBIDDEN`, `KUBERNETES_TIMEOUT`, `KUBERNETES_RESOURCE_NOT_FOUND`, `KUBERNETES_INVALID_CONFIGURATION`). Credentials are never logged and never included in state reports.

### Control-plane API

| Method | Path                                   | Description                                            |
| ------ | -------------------------------------- | ------------------------------------------------------ |
| GET    | `/nodes/{id}/kubernetes`               | Stored desired + last-reported Kubernetes state        |
| GET    | `/nodes/{id}/kubernetes/nodes`         | Dispatch `LIST_KUBERNETES_NODES` to the agent (`202`)  |
| GET    | `/nodes/{id}/kubernetes/pods`          | Dispatch `LIST_KUBERNETES_PODS` to the agent (`202`)   |

The `nodes`/`pods` endpoints answer `202 Accepted` with a `PENDING` command; poll `GET /nodes/{id}/commands` for the result.

### Agent configuration

| Variable                      | Default | Description                                          |
| ----------------------------- | ------- | ---------------------------------------------------- |
| `KUBERNETES_ENABLED`          | `false` | Turns the agent's Kubernetes integration on          |
| `KUBECONFIG`                  | (auto)  | Explicit kubeconfig path (falls back to standard loading rules, then in-cluster config) |
| `KUBERNETES_REQUEST_TIMEOUT`  | `10s`   | Timeout for each Kubernetes API call                 |

With `KUBERNETES_ENABLED=false` the agent reports `DISABLED`; with `KUBERNETES_ENABLED=true` and no reachable cluster it reports `UNAVAILABLE` and keeps running.

### Testing

Real-cluster tests run only when a development cluster (kind/minikube) is available:

```bash
INTEGRATION_KUBERNETES=true go test ./...
```

Without the variable (or without a cluster) those tests skip; the normal suite uses a mocked `KubernetesClient`.

---

## Architecture

```text
                         Operator / User
                               │
                               ▼
                         HTTP API (net/http)
                               │
              ┌────────────────┼───────────────────────────┐
              ▼                ▼                           ▼
       Node Service     Heartbeat Service      Reconciliation Service
              │                │                 (engine facade)
              └────────────────┼───────────────────┐
                               ▼                   ▼
                        NodeRepository      Reconciliation Engine
                          (interface)     ┌───────┼───────┐
                               │          │       │       │
                               ▼       Observer Planner Executor
                          SQLite          │       │       │
                                          └───────┼───────┘
                                                  ▼
                                              Edge Agent
```

Dependency direction is strictly one-way:

```text
HTTP  →  Service  →  Reconciler  →  Repository  →  Database
                              ↘ Observer / Planner / Executor
```

Domain models live in `internal/domain` and depend on neither HTTP nor SQLite.

### Project layout

```text
cmd/aether-grid/main.go                  Entry point, config, server lifecycle
internal/config/                         Environment-based configuration
internal/domain/                         Node, state, reconciliation models
internal/service/                        Business logic and engine facade
internal/reconcile/                      Reconciliation engine (observer, planner,
                                         executor, queue, locks, backoff, metrics)
internal/repository/                     Repository interface
internal/repository/sqlite/              SQLite implementation
internal/http/router.go                  Route wiring
internal/http/handlers/                  HTTP handlers (parse/validate/call services)
internal/http/middleware/                Logging and recovery middleware
migrations/                              Versioned SQL schema migrations
tests/                                   End-to-end integration tests
```

---

## Running Locally

Requirements: Go 1.22+ (tested with 1.26).

```bash
go run ./cmd/aether-grid
```

The server listens on `0.0.0.0:8080` by default and creates its SQLite database (and the `data/` directory) on first start. Migrations are applied automatically at startup.

### Configuration (environment variables)

| Variable                         | Default                | Description                        |
| -------------------------------- | ---------------------- | ---------------------------------- |
| `SERVER_HOST`                    | `0.0.0.0`              | Bind host                          |
| `SERVER_PORT`                    | `8080`                 | Bind port                          |
| `DATABASE_PATH`                  | `./data/aether-grid.db`| SQLite database file path          |
| `RECONCILIATION_INTERVAL`        | `10s`                  | Periodic sweep interval            |
| `RECONCILIATION_WORKERS`         | `4`                    | Worker pool size                   |
| `NODE_HEARTBEAT_TIMEOUT`         | `30s`                  | OFFLINE staleness threshold        |
| `RECONCILIATION_MAX_RETRIES`     | `3`                    | Max execution attempts             |
| `RECONCILIATION_MAX_BACKOFF`     | `10s`                  | Backoff upper bound                |
| `RECONCILIATION_RECOVERY_TIMEOUT`| `60s`                  | Recovery convergence window        |

Example with overrides:

```bash
SERVER_HOST=127.0.0.1 SERVER_PORT=9000 DATABASE_PATH=/tmp/aether-grid.db \
RECONCILIATION_INTERVAL=5s RECONCILIATION_WORKERS=8 go run ./cmd/aether-grid
```

---

## API

All endpoints accept and return JSON. Errors are returned consistently as `{"error": "..."}`.

| Method | Path                                   | Description                                      |
| ------ | -------------------------------------- | ------------------------------------------------ |
| POST   | `/nodes`                               | Register a node                                  |
| GET    | `/nodes`                               | List all nodes                                   |
| GET    | `/nodes/{id}`                          | Get one node                                     |
| DELETE | `/nodes/{id}`                          | Delete a node                                    |
| POST   | `/nodes/{id}/heartbeat`                | Record a heartbeat                               |
| PUT    | `/nodes/{id}/state`                    | Report actual state (`{"status":"READY"}`)       |
| GET    | `/nodes/{id}/state`                    | Get actual state                                 |
| GET    | `/nodes/{id}/desired-state`            | Get desired state (structured)                   |
| PUT    | `/nodes/{id}/desired-state`            | Set desired state (`{"status":"READY"}`)         |
| POST   | `/nodes/{id}/reconcile`                | Run a full reconciliation cycle synchronously     |
| GET    | `/nodes/{id}/reconciliation`           | Get a node's reconciliation metadata             |
| GET    | `/nodes/{id}/reconciliation/history`   | List a node's reconciliation history             |
| GET    | `/reconciliation/status`               | Controller status and metrics                    |
| POST   | `/nodes/{id}/commands`                 | Dispatch a command to an agent                   |
| GET    | `/nodes/{id}/commands`                 | List a node's commands                           |
| POST   | `/nodes/{id}/commands/{command_id}/result` | Report a command result                      |
| GET    | `/nodes/{id}/kubernetes`               | Get a node's Kubernetes desired + observed state |
| GET    | `/nodes/{id}/kubernetes/nodes`         | List cluster nodes via the agent (`202`)         |
| GET    | `/nodes/{id}/kubernetes/pods`          | List cluster pods via the agent (`202`)          |

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

Run reconciliation (returns a structured result such as `IN_SYNC`, `DRIFT_DETECTED`, `RECONCILED` or `RECONCILIATION_FAILED`):

```bash
curl -X POST http://localhost:8080/nodes/{id}/reconcile
```

Inspect reconciliation metadata and history:

```bash
curl http://localhost:8080/nodes/{id}/reconciliation
curl http://localhost:8080/nodes/{id}/reconciliation/history
curl http://localhost:8080/reconciliation/status
```

Set the desired state:

```bash
curl -X PUT http://localhost:8080/nodes/{id}/desired-state \
  -H "Content-Type: application/json" \
  -d '{"status":"READY","kubernetes":{"enabled":true,"minimum_ready_nodes":1}}'
```

Report actual state including the observed Kubernetes summary (as the agent does):

```bash
curl -X PUT http://localhost:8080/nodes/{id}/state \
  -H "Content-Type: application/json" \
  -d '{
    "status":"READY",
    "kubernetes":{
      "available":true,"status":"DEGRADED","version":"v1.31.0",
      "node_count":2,"ready_nodes":1,"not_ready_nodes":1,
      "workload":{"total_pods":5,"running_pods":4,"failed_pods":1}
    }
  }'
```

Inspect and query Kubernetes state through the control plane:

```bash
curl http://localhost:8080/nodes/{id}/kubernetes
curl -X GET http://localhost:8080/nodes/{id}/kubernetes/nodes
curl -X GET http://localhost:8080/nodes/{id}/kubernetes/pods?namespace=default
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
- **Reconciliation tests** for the observer, planner, executor, queue, backoff and the full engine (the five spec scenarios: IN_SYNC, DRIFT_DETECTED, RECONCILED, RECONCILIATION_FAILED and retry-then-RECONCILED, plus concurrency, staleness and cancellation cases)
- **HTTP tests** for every endpoint, covering success and failure cases
- **End-to-end integration tests** (`tests/`) covering the Phase 1 lifecycle and the Phase 3 reconciliation scenario (register → READY → IN_SYNC → heartbeats stop → OFFLINE → recovery dispatched → READY again → IN_SYNC)

---

## Design Decisions

See [docs/architecture.md](docs/architecture.md) for the full rationale, including why the major alternatives were not selected.