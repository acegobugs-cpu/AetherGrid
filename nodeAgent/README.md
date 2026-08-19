# AETHER-GRID Edge Node Agent

A standalone Go agent that runs on each edge node, connects to the AETHER-GRID control plane, reports local state, and executes commands issued by the operator.

AETHER-GRID follows a **desired-state and reconciliation model**. The control plane (see `../controlPanel`) is the operator-facing side; this repository is the **Edge Node Agent** (Phase 2) that represents a single Linux node in the fleet. Phase 4 adds the **Kubernetes integration layer**: the agent observes an existing Kubernetes cluster through client-go and reports its health to the control plane.

---

## Overview

The agent is a separate executable (`cmd/aether-agent`). On first start it has no identity, so it registers with the control plane and persists the assigned node ID locally. On later starts it reuses the persisted identity and reconnects to the same control-plane node, so a restart never creates a duplicate registration.

Once connected, the agent runs three independent loops:

1. **Heartbeat loop** — proves liveness to the control plane at a fixed interval, with exponential backoff while the control plane is unreachable.
2. **State loop** — collects local machine state (hostname, OS, CPU, memory, uptime) and reports the agent's status, IP, and observed Kubernetes state to the control plane, then retrieves the authoritative desired state.
3. **Command loop** — polls for pending commands, executes them with a bounded timeout, and reports the outcome.

The agent is resilient: a temporary control-plane outage is retried with backoff, an unknown identity triggers automatic re-registration, and a `RESTART_AGENT` command shuts the process down cleanly (for a supervisor to restart). A Kubernetes outage degrades the Kubernetes report without affecting the agent's heartbeat or basic state reporting.

---

## Architecture

```text
          Control Plane
                │
                │ HTTP/JSON
                ▼
            Edge Agent
                │
                ▼
            Linux Node
```

Dependency direction inside the agent is strictly one-way:

```text
agent  →  client → control plane
   ↓
state/command/identity/config  (self-contained)
```

The agent talks to the control plane **only** through the `client` package; no raw HTTP lives outside it.

### Project layout

```text
cmd/aether-agent/main.go          Entry point, config, signal handling
internal/config/                  Environment-based configuration + validation
internal/identity/                Persisted node identity (data dir)
internal/state/                   Machine state collection (Linux)
internal/backoff/                 Exponential backoff schedule
internal/client/                  Control plane HTTP client (single interface)
internal/command/                 Command registry + handlers (incl. Kubernetes commands)
internal/kubernetes/              Kubernetes integration (client-go behind an interface)
internal/agent/                   Runtime: loops, local debug API, lifecycle
tests/                            End-to-end integration test vs mock control plane
docs/architecture.md              Architecture decision record
```

---

## Configuration

All configuration comes from environment variables. The agent fails to start on an invalid configuration (spec: "invalid configuration fails fast, temporary network failures retry").

| Variable                  | Default              | Description                                   |
| ------------------------- | -------------------- | --------------------------------------------- |
| `CONTROL_PLANE_URL`       | `http://localhost:8080` | Control plane base URL                     |
| `NODE_NAME`               | hostname             | Human-readable name sent at registration      |
| `NODE_LOCATION`           | `local`              | Location label sent at registration           |
| `NODE_ID`                 | *(empty)*            | Explicit identity override; otherwise persisted/registered |
| `AGENT_DATA_DIR`          | `./data`             | Directory for the persisted identity          |
| `HEARTBEAT_INTERVAL`      | `10s`                | Heartbeat period                              |
| `STATE_REPORT_INTERVAL`   | `30s`                | State report + desired-state refresh period   |
| `COMMAND_POLL_INTERVAL`   | `5s`                 | Pending-command poll period                   |
| `COMMAND_TIMEOUT`         | `30s`                | Maximum execution time of a single command    |
| `RETRY_INITIAL_BACKOFF`   | `1s`                 | First retry delay                             |
| `RETRY_MAX_BACKOFF`       | `30s`                 | Maximum backoff delay                         |
| `AGENT_LISTEN_ADDR`       | `127.0.0.1:9090`     | Local-only debug API address                  |
| `AGENT_VERSION`           | `dev`                | Version reported to the control plane         |
| `KUBERNETES_ENABLED`      | `false`              | Turns the Kubernetes integration on          |
| `KUBECONFIG`              | *(auto)*             | Explicit kubeconfig path (defaults to standard rules, then in-cluster config) |
| `KUBERNETES_REQUEST_TIMEOUT` | `10s`             | Timeout for each Kubernetes API call          |

### Kubernetes integration (Phase 4)

With `KUBERNETES_ENABLED=true` the agent connects to the cluster defined by the
kubeconfig and, on every state report, collects:

- cluster information (version, node count),
- node state (ready/not ready),
- a pod workload summary,
- an overall health state: `DISABLED`, `UNAVAILABLE`, `DEGRADED` or `READY`.

The observed summary is sent to the control plane inside the state report, so
the control plane can detect Kubernetes drift without contacting the cluster
itself. Kubernetes commands are registered with the command registry and are
issued by the control plane:

| Command                    | Meaning                                     |
| -------------------------- | ------------------------------------------- |
| `GET_KUBERNETES_STATUS`    | Report the observed Kubernetes state        |
| `LIST_KUBERNETES_NODES`    | List cluster nodes                          |
| `LIST_KUBERNETES_PODS`     | List cluster pods (optional `namespace`)    |
| `CREATE_TEST_NAMESPACE`    | Create the dedicated test namespace         |
| `DELETE_TEST_NAMESPACE`    | Delete the dedicated test namespace         |

With `KUBERNETES_ENABLED=false` the agent reports `DISABLED`. A Kubernetes
outage reports `UNAVAILABLE`; the agent keeps heartbeating and running
normally (spec: "a Kubernetes outage must not become an AETHER-GRID outage").

---

## Running

Requirements: Go 1.22+ (tested with 1.26), Linux.

Start the control plane first (see `../controlPanel/README.md`), then run the agent:

```bash
go run ./cmd/aether-agent
```

Example with overrides:

```bash
CONTROL_PLANE_URL=http://127.0.0.1:8080 \
NODE_NAME=edge-01 NODE_LOCATION=addis-01 \
go run ./cmd/aether-agent
```

### Local debug API

The agent exposes a loopback-only HTTP API for health checks and local debugging:

```bash
curl http://127.0.0.1:9090/health   # {"status":"READY","node_id":"..."} or 503
curl http://127.0.0.1:9090/status   # full runtime status
```

---

## Example Logs

First run (no persisted identity):

```text
[aether-agent] agent starting (version=dev)
[aether-agent] no local identity found, registering with control plane
[aether-agent] registration successful: node_id=<uuid> status=PROVISIONING
[aether-agent] identity ready: node_id=<uuid> ip=10.0.0.10
[aether-agent] heartbeat established: node_id=<uuid>
[aether-agent] state collected: node_id=<uuid> status=READY hostname=edge-01 os=linux/amd64 ...
[aether-agent] desired state retrieved: node_id=<uuid> desired_status=READY
```

Control plane temporarily unreachable:

```text
[aether-agent] heartbeat failed: node_id=<uuid> error=... retrying in 2s
[aether-agent] heartbeat failed: node_id=<uuid> error=... retrying in 4s
[aether-agent] heartbeat established: node_id=<uuid>
```

Restart with a persisted identity:

```text
[aether-agent] identity loaded: node_id=<uuid>
[aether-agent] reconnected to existing node: node_id=<uuid> ...
```

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

- **Unit tests** for configuration validation, identity persistence, state collection, backoff scheduling, the HTTP client, the command registry, and the Kubernetes package (client mapping, health calculation, error translation, service/collector via a mocked `KubernetesClient`)
- **Runtime tests** against a fake control-plane client: registration + heartbeat, identity reuse across restarts, survival during control-plane downtime, command execution, unknown-identity re-registration, `RESTART_AGENT` shutdown, the local health endpoint, and Kubernetes state reporting
- **End-to-end integration tests** (`tests/`) that run the real agent against an in-memory mock control plane over real HTTP: full lifecycle (register → heartbeat → state → desired-state → command → result), control-plane restart with automatic reconnection, and identity persistence across agent restarts
- **Kubernetes integration test** (`tests/kubernetes_integration_test.go`) that runs against a real development cluster and is gated by `INTEGRATION_KUBERNETES=true`; it skips when no cluster is reachable so ordinary tests never require one

---

## Design Decisions

See [docs/architecture.md](docs/architecture.md) for the full rationale, including why alternatives were not chosen.

- Language: **Go**
- Protocol: **HTTP/JSON** (matches the Phase 1 control-plane API)
- Identity: **UUID assigned by the control plane**, persisted as a local file
- State reporting: `status` + `ip_address` plus the observed Kubernetes summary (Phase 4) are sent to the control plane; detailed machine state stays local
- Retry: **exponential backoff** for all network operations
- Concurrency: **goroutines + context** for three independent loops
- Kubernetes: **client-go behind an interface**, polling observation, kubeconfig loading, structured error codes, per-call timeouts