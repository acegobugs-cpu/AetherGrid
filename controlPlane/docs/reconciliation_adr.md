# AETHER-GRID — Phase 3 Architecture Decisions

This document records the key design decisions made during Phase 3 (the
Reconciliation Engine) and the rationale behind them.

## Decision summary

| Decision                 | Choice                    | Reason                                        |
| ------------------------ | ------------------------- | --------------------------------------------- |
| Reconciliation model     | Controller loop           | Continuous convergence                        |
| State model              | Desired vs actual         | Declarative infrastructure                    |
| State comparison         | Structured differences    | Controller needs typed input to plan actions  |
| Triggering               | Event + periodic          | Fast response + eventual recovery             |
| Concurrency              | Bounded workers           | Prevent resource exhaustion                   |
| Per-node serialization   | Per-node lock             | Prevent conflicting actions on one node       |
| Retry                    | Exponential backoff       | Avoid retry storms                            |
| Actions                  | Idempotent / explicit     | Safe retries, never fake success              |
| Observer                 | Abstract interface        | Future infrastructure integrations            |
| Planner                  | Separate component        | Separate decision from execution              |
| Executor                 | Separate component        | Replaceable infrastructure actions            |
| Queue                    | In-memory, deduplicated   | Sufficient for a single control plane         |
| Broker                   | None                      | Avoid premature infrastructure                |
| HA                       | None                      | Outside Phase 3 scope                         |
| History                  | Lightweight table         | Observability without event sourcing          |
| Recovery action          | `RESTART_AGENT` command   | Real Phase 2 agent capability                 |
| Unsupported actions      | Explicit error            | Never claim a success that did not happen     |

---

## Reconciliation model — controller loop

The engine continuously loops over observe → compare → determine → execute →
observe. This is the Kubernetes-inspired controller model: the system always
converges toward desired state rather than reacting to one-off events.

**Alternatives rejected:** a purely event-driven system loses recovery when
events are dropped; a purely periodic system reacts slowly. The combination is
chosen (see Triggering below).

---

## State model — desired vs actual

- **Desired state** is declarative: the operator states what a node should be
  (`READY`, `kubernetes_enabled`, `wireguard_enabled`).
- **Actual state** is observed and is never mutated to satisfy desired state.

The two live on the same `Node` row today because the desired-state document is
still a subset of the node record; a later phase can split it into its own
document when the configuration surface grows.

---

## State comparison — structured differences

`CompareStates` returns one `Difference{field, desired, actual}` per differing
field. The planner needs this structure to decide actions; returning "states
are different" would force string parsing.

---

## Triggering — event + periodic

- **Periodic**: every `RECONCILIATION_INTERVAL` the engine sweeps the whole
  fleet. This guarantees eventual convergence even if every event is lost.
- **Event-driven**: registration, state reports, desired-state changes and
  heartbeats call `Notify`, which enqueues the node immediately through a
  deduplicating queue.

Periodic gives resilience; event-driven gives fast response. The work queue
coalesces bursts (for example heartbeat floods) so a node is only reconciled
once per pending slot.

---

## Concurrency — bounded workers

A configurable worker pool (`RECONCILIATION_WORKERS`) processes the queue.
Goroutine-per-node was rejected because an infrastructure control plane may
manage hundreds or thousands of nodes; unbounded goroutines would exhaust
memory and overwhelm the database.

## Per-node serialization

A reference-counted per-node lock guarantees two workers never reconcile the
same node simultaneously, preventing conflicting actions (for example two
`RESTART_AGENT` dispatches racing). Different nodes reconcile concurrently.
Entries are released when the last holder leaves, so no per-node entry leaks
for a large fleet.

---

## Retry — exponential backoff

Failed actions retry with exponential backoff (base 1s) capped at
`RECONCILIATION_MAX_BACKOFF` up to `RECONCILIATION_MAX_RETRIES`. Immediate
retries were rejected: they cause CPU waste, API pressure, log flooding and
cascading failures when infrastructure is genuinely down. Failures are recorded
as `RECONCILIATION_FAILED` with attempt count and error before the state is
considered terminal for that cycle.

`RECONCILIATION_MAX_RETRIES=0` enables **detect-only mode**: drift is reported
but never acted on, which is useful for observing the fleet before enabling
repairs.

---

## Actions — idempotent / explicit

- `RECOVER_NODE` dispatches a `RESTART_AGENT` command through the real Phase 2
  command pipeline. It is safe to re-dispatch: the agent processes pending
  commands.
- `ENABLE_KUBERNETES`, `DISABLE_KUBERNETES`, `ENABLE_WIREGUARD`,
  `DISABLE_WIREGUARD` have no execution path yet and fail with an explicit
  `UnsupportedActionError`. The system never claims an operation succeeded when
  it did not (spec §40).

## In-flight recovery deadline

A dispatched recovery records a deadline (`RECONCILIATION_RECOVERY_TIMEOUT`).
While the deadline is unexpired the engine reports `RECONCILING` and does not
re-dispatch, avoiding duplicate recovery commands for the same outage. Once the
node reports a fresh heartbeat matching desired state, the engine records
`RECONCILED` and returns to `IN_SYNC`.

---

## Observer / Planner / Executor separation

- **Observer** abstracts actual-state acquisition. Today it reads the node
  repository and derives staleness from `last_heartbeat`. Later it can observe
  Kubernetes, Terraform or cloud APIs without changing the engine.
- **Planner** turns differences into actions. It never executes.
- **Executor** executes planned actions against the command queue. It never
  decides what is necessary.

This keeps the engine stable while infrastructure integrations evolve.

---

## Heartbeat health model

A node is `OFFLINE` when its `last_heartbeat` is older than
`NODE_HEARTBEAT_TIMEOUT`. A single missed heartbeat does not mark a node
offline; a timeout window is required. Nodes that have never sent a heartbeat
keep their stored status (the heartbeat is the liveness signal, not the
status).

---

## Queue — in-memory

The work queue is an in-memory, deduplicating structure. It is sufficient for a
single control-plane process and avoids external infrastructure.

**Alternatives rejected:**

- **Message broker (Kafka/RabbitMQ/NATS):** unnecessary for a single-process
  control plane; adds operational complexity. A future distributed control
  plane could revisit this.
- **Redis:** an in-memory queue suffices.
- **Kubernetes Informers:** Kubernetes integration belongs to a later phase.
- **Temporal:** AETHER-GRID implements its own controller model for
  architectural understanding.
- **Full event sourcing:** current state plus lightweight history is sufficient.
- **Distributed leader election:** Phase 3 assumes a single control-plane
  instance; HA is out of scope.

---

## History — lightweight table

`reconciliation_events` stores an operational history row per non-`IN_SYNC`
cycle. It is observability/debugging aid only; the node's current
reconciliation metadata is authoritative. Steady-state `IN_SYNC` cycles are not
written to avoid flooding the table.

## Metadata persistence

Node reconciliation metadata is written through a dedicated
`UpdateReconciliation` repository method that touches only the reconciliation
columns. This prevents the engine from clobbering concurrent heartbeat/status
writes, and prevents a heartbeat write from clobbering reconciliation state
(race conditions spec §49, §55).

---

## API design

- `POST /nodes/{id}/reconcile` runs a full cycle synchronously and returns the
  structured result — useful for debugging. Automatic reconciliation stays
  asynchronous through the engine.
- `GET /nodes/{id}/reconciliation` exposes per-node metadata.
- `GET /nodes/{id}/reconciliation/history` exposes the operational history.
- `GET /reconciliation/status` exposes engine counters for observability.

---

## Metrics

Prometheus is NOT integrated (spec §35). The engine keeps explicit atomic
counters behind a `Metrics` type with clean instrumentation points so a
Prometheus exporter can be added later without reworking the engine.

---

## What remains for Phase 4+

- Real Kubernetes observation and provisioning (the `ENABLE_*`/`DISABLE_*`
  actions currently fail as unsupported).
- WireGuard networking.
- Infrastructure recovery beyond agent restart.
- Desired-state document separated from the node record (k8s/wg flags currently
  share fields with actual state, so no k8s/wg drift can occur yet).
- Distributed/HA control plane.
- Prometheus/Grafana integration.