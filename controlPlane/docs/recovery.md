# Phase 9 — Autonomous Infrastructure Reconciliation & Recovery

AETHER-GRID does not merely deploy infrastructure. It continuously compares
what **should** exist against what **does** exist and takes bounded,
policy-controlled actions to bring the system back toward the desired state.

## Reconciliation Architecture

```
                 ┌──────────────────────┐
                 │    Desired State     │
                 └──────────┬───────────┘
                            ▼
                 ┌──────────────────────┐
                 │     Reconciler       │
                 └──────────┬───────────┘
                    ┌───────┴───────┐
                    ▼               ▼
              ┌───────────┐   ┌───────────┐
              │  Observer │   │  Planner  │
              └─────┬─────┘   └─────┬─────┘
                    ▼               ▼
              Actual State     Action Plan
                                    │
                                    ▼
                            ┌───────────────┐
                            │Action Executor│
                            └───────┬───────┘
                                    ▼
                        Provisioner / Network / K8s / Agent
                                    ▼
                              Observe Again
```

The engine keeps the Phase 3 observer/planner/executor split. Phase 9 adds a
recovery gate between planning and execution.

## State Model

Node lifecycle statuses: `UNKNOWN, PROVISIONING, PROVISIONED, BOOTSTRAPPING,
CONNECTING, REGISTERED, CONFIGURING, READY, DEGRADED, UNHEALTHY, OFFLINE,
UNREACHABLE, FAILED, RECOVERING, REMOVED`.

Cluster lifecycle states: `PENDING, BOOTSTRAPPING, CONTROL_PLANE_READY,
JOINING_WORKERS, VERIFYING, READY, DEGRADED, RECOVERING, FAILED, DESTROYED`.

Per-node recovery states (persisted): `NOT_REQUIRED → SUSPECTED →
CONFIRMED_FAILURE → RECOVERING → VERIFICATION → RECOVERED` or terminal
`RECOVERY_FAILED` / `RECOVERY_BLOCKED`.

## Failure Detection

Health is not binary and is never inferred from a single signal. Evidence
sources are heartbeat age, agent status, Kubernetes node status and stored
lifecycle status.

| Silence (heartbeat interval × k) | Interpretation |
|---|---|
| < 1× timeout | healthy |
| ≥ 1× timeout | `SUSPECTED` — no action |
| ≥ N× timeout (`FAILURE_CONFIRM_MULTIPLIER`, default 3) | failure confirmed |

An explicit terminal status (`FAILED`, `UNREACHABLE`, `REMOVED`) confirms
immediately.

## Recovery Workflow

```
Agent reports stale/failed        Operator calls POST /clusters/:id/reconcile
          │                                   │
          ▼                                   ▼
   ┌─────────────┐   sweep/event   ┌──────────────────┐
   │  Observe    │◄───────────────►│  Periodic Loop   │
   └──────┬──────┘                 └──────────────────┘
          │ drift + action planned
          ▼
   silence < confirm threshold? ──yes──► SUSPECTED (audit NODE_FAILURE_DETECTED)
          │ no                                (no replacement!)
          ▼
   preconditions ok? cluster managed? no conflicting op? under replacement cap?
          │ yes
          ▼
   policy allows this role? attempts remain? not flapping? not cooling down?
          │ yes
          ▼
   acquire recovery slot (max_concurrent_recoveries)
          │
          ▼
   escalate progressively:
     agent/network/kubernetes class → RESTART_AGENT
     infrastructure class on worker → PROVISION_REPLACEMENT (after retries)
     control-plane                  → never automatic (policy default off)
          │
          ▼
   execute → verify next cycle → RECOVERED (audit NODE_REJOINED /
   RECOVERY_COMPLETED) or retry with jittered exponential backoff or
   circuit breaker trips → RECOVERY_BLOCKED (audit RECOVERY_BLOCKED)
```

## Safety Boundaries

- One missed heartbeat never triggers replacement.
- Control-plane nodes are never automatically replaced.
- Destructive actions require: confirmed failure, managed-cluster membership,
  no conflicting cluster operation, policy enabled, attempts remaining.
- `MAX_REPLACEMENTS_PER_CLUSTER` caps runaway replacement loops.
- Circuit breaker blocks after `MAX_RECOVERY_ATTEMPTS` until an operator calls
  `POST /clusters/{id}/recovery/reset`. Reset re-enables evaluation only; it
  never executes recovery itself.
- Flapping nodes (≥3 exhausted recoveries without a later success) stay blocked.

## Retry Model

`delay = min(base · 2^(attempt-1), max)`, then halved-jitter so concurrent
failures do not synchronize retries.

## Failure Classification

`INFRASTRUCTURE` (machine/network path gone), `NETWORK` (silent but reporting),
`AGENT` (never reported), `KUBERNETES` (K8s layer unhealthy), plus
`CONFIGURATION`, `AUTHENTICATION`, `UNKNOWN` reserved by strategy. The class
selects the least destructive remediation path.

## Ownership

Recovery only touches nodes that belong to an AETHER-GRID-managed cluster
(declared in the cluster spec). Kubernetes-owned resources (pods, deployments,
scheduling) are never reconciled.

## API

| Endpoint | Purpose |
|---|---|
| `GET /clusters/{id}/health` | aggregate desired-vs-actual health |
| `GET /clusters/{id}/reconciliation` | per-member reconciliation view |
| `GET /clusters/{id}/recovery` | per-member recovery state |
| `POST /clusters/{id}/reconcile` | manual reconcile of every member |
| `POST /clusters/{id}/recovery/reset` | clear one node's circuit breaker |

## Configuration (defaults documented)

| Env var | Default | Meaning |
|---|---|---|
| `RECONCILIATION_INTERVAL` | 10s | periodic sweep period |
| `NODE_HEARTBEAT_TIMEOUT` | 30s | staleness threshold |
| `FAILURE_CONFIRM_MULTIPLIER` | 3 | confirmation = N× timeout |
| `WORKER_RECOVERY_ENABLED` | true | automatic worker recovery |
| `CONTROL_PLANE_RECOVERY_ENABLED` | false | automatic CP recovery |
| `MAX_RECOVERY_ATTEMPTS` | 3 | breaker trip point |
| `MAX_CONCURRENT_RECOVERIES` | 2 | fleet-wide recovery concurrency |
| `RECOVERY_COOLDOWN` | 30m | post-recovery cooldown |
| `RECOVERY_BACKOFF_BASE` / `_MAX` | 10s / 5m | backoff bounds |
| `RECOVERY_BACKOFF_JITTER` | true | de-synchronize retries |
| `MAX_REPLACEMENTS_PER_CLUSTER` | 2 | hard replacement cap |

## Metrics

`nodes_reconciled, cycles_in_sync/drifted/reconciled/failed`,
`node_failures_total`, `recoveries_started/attempts/succeeded/failed/blocked`,
`nodes_suspected_total`, `nodes_recovered_from_suspicion_total`.

## Audit Events

`NODE_FAILURE_DETECTED, NODE_FAILURE_CONFIRMED, RECOVERY_STARTED,
RECOVERY_ATTEMPT_FAILED, REPLACEMENT_PROVISIONED, NODE_REJOINED,
RECOVERY_COMPLETED, RECOVERY_BLOCKED, RECOVERY_RESET` — persisted through the
existing reconciliation history table (`result=AUDIT`).

## Architecture Decision Record

| Decision | Selected | Reason |
|---|---|---|
| Reconciliation model | Observe→Compare→Plan→Execute→Verify | separation of concerns |
| Failure handling | Progressive recovery | least destructive first |
| Worker recovery | Automatic | workers are disposable |
| Control-plane recovery | Disabled | HA/datastore complexity |
| Failure detection | Threshold-based | tolerate transient faults |
| Retry | Bounded exponential backoff + jitter | no infinite loops, no thundering herd |
| Recovery concurrency | Bounded semaphore | protect provider/control plane |
| Duplicate events | Deduplicated queue | no duplicate recoveries |
| Ownership | Cluster-spec membership | never touch unmanaged infra |
| Rollback | Limited | distributed ops are not atomic |
| Circuit breaker | Enabled | prevent resource-wasting loops |
| Flapping detection | Enabled | stop repeated replacement |
| Reconciliation | Event + periodic | fast reaction + eventual consistency |
