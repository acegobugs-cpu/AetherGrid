# Edge Node Agent — Architecture Decision Record

This document records the key decisions for the Phase 2 Edge Node Agent, along with the alternatives that were considered and rejected.

## Decisions

| Decision | Choice | Reason |
|---|---|---|
| Language | Go | Single language across the project; small static binary, strong concurrency primitives, good fit for a system-level agent |
| Protocol | HTTP/JSON | Matches the Phase 1 control-plane API exactly; no new transport to build or debug |
| Identity | UUID assigned by control plane | A stable, globally unique identifier that the control plane owns; no collision risk between agents |
| Identity storage | Local file (`<data>/node-id`) | Minimal persistent data; a full local DB is unnecessary when the control plane is the system of record |
| State | In-memory | Runtime state does not need to survive a crash; on restart the agent reconnects and re-collects |
| Heartbeats | Periodic | Simple, standard liveness detection with a bounded payload |
| Retry | Exponential backoff | Resilient to temporary network failures and control-plane restarts without hammering the server |
| Concurrency | Goroutines + context | Three independent loops (heartbeat, state, commands) run safely and shut down together via context cancellation |
| Persistence | Identity only | Avoids the complexity of a local database while still preventing duplicate registrations |
| OS target | Linux | Edge infrastructure focus; state collection reads `/proc` |
| Command dispatch | HTTP polling | Simplest reliable delivery model; no outbound connections or listener on the control plane |

## Alternatives considered

### Identity

- **Agent generates its own UUID.** Rejected: nothing would stop two agents (e.g. a restored disk image) from generating the same or conflicting identity, and the control plane could not assign fleet semantics at registration time.
- **Mac-address-derived identity.** Rejected: unreliable on VMs/containers, and it leaks hardware info.
- **Operator-configured identity.** Rejected: it is still supported as an override (`NODE_ID`), but it must not be the only option because it does not scale to fleet bootstrapping.

### Identity storage

- **SQLite on the node.** Rejected: a single small identity value does not justify a database dependency on every edge node.
- **In-memory only.** Rejected: the agent would re-register on every restart, producing duplicate control-plane nodes and breaking the "survives restart" requirement.

### State persistence

- **Local database of reported states.** Rejected: the control plane is the system of record; the agent's own snapshot is only for local debugging via the debug API.

### Protocol

- **gRPC / WebSocket / MQTT.** Rejected: they add code generation or broker infrastructure that provides no benefit over the existing Phase 1 REST API. The control plane and agent both speak plain HTTP/JSON.

### Delivery of commands

- **Long-polling or server-push (SSE/WebSocket).** Rejected: polling at a short interval (default 5s) is simple, stateless, and trivially retryable; push adds connection-management complexity to both sides for marginal latency gain.
- **Agent-side listener invoked by control plane.** Rejected: edge nodes are often NAT'd; the agent must never require an inbound port from the fleet operator's network.

### Retry strategy

- **Fixed-interval retries.** Rejected: fixed intervals either hammer the control plane during an outage or delay recovery; exponential backoff bounds both.
- **Jittered backoff.** Deferred: pure exponential backoff already satisfies the requirement; jitter would be added if fleet-scale thundering-herd behavior is ever observed.

### Agent status model

- **Expose raw machine metrics to the control plane immediately.** Rejected: the Phase 2 control-plane state contract accepts only `status` and `ip_address` (matching the Phase 1 `state` model). Detailed machine state is collected and logged locally and served by the loopback debug API; richer reporting is a later phase.

### Command execution

- **Script execution by default.** Rejected for safety: commands are registry-dispatched to typed Go handlers (`GET_STATUS`, `RESTART_AGENT`), each with a bounded timeout and result reporting. Arbitrary script execution is deliberately out of scope for Phase 2.

## State reporting contract

The agent sends the control plane exactly:

```json
{ "status": "READY", "ip_address": "10.0.0.10" }
```

Everything else collected by the local collector is used for logging and the local debug API. This keeps the agent aligned with the Phase 1 control-plane state model.

## Lifecycle

```text
start
  └─ validate config (fail fast on error)
  └─ load identity (config > persisted > register)
  └─ verify identity against control plane; re-register on 404
  └─ start loopback debug API (optional; never fatal)
  └─ start heartbeat / state / command loops
  └─ on ctx cancel or RESTART_AGENT: stop loops, shut down API, exit 0
```