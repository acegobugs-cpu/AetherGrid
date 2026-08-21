# AETHER-GRID — Phase 4 Architecture Decisions

This document records the key design decisions made during Phase 4 (the
Kubernetes Integration layer) and the rationale behind them. It also records
the spec section 59 dependency decisions and the section 60 alternatives, as
required by the Definition of Done (spec §75).

## Decision summary

| Decision                  | Choice                          | Reason                                     |
| ------------------------- | ------------------------------- | ------------------------------------------ |
| Kubernetes client         | client-go                       | Official Go Kubernetes client              |
| Access location           | Edge Agent                      | Keeps cluster access local                 |
| Kubernetes protocol       | Kubernetes API                  | Native integration                         |
| Configuration             | kubeconfig                      | Standard development/admin mechanism       |
| Observation               | Polling                         | Simple and sufficient for Phase 4          |
| Kubernetes watches        | Not yet                         | Complexity belongs with Operator           |
| kubectl                   | Not used                        | Avoid CLI dependency                       |
| Cluster installation      | Not yet                         | Infrastructure provisioning is later       |
| Kubernetes Operator       | Not yet                         | Phase 5                                    |
| Prometheus                | Not yet                         | Observability phase                        |
| Cache                     | Minimal/in-memory               | Avoid unnecessary infrastructure           |
| Secrets                   | Never collected                 | Security boundary                          |
| Error translation         | Structured codes                | Handlers and API map without parsing       |
| Health model              | DISABLED/UNAVAILABLE/DEGRADED/READY | Separate from node lifecycle           |
| Kubernetes drift          | DRIFT_DETECTED, no action       | No auto-repair; never fake success         |
| Control-plane exposure    | Via agent commands (202)        | CP never contacts Kubernetes directly      |
| Desired-state model       | `kubernetes.enabled` + `minimum_ready_nodes` | Declarative and minimal          |
| Actual-state integration  | Nested `kubernetes` in state report | Matches the agent's observed shape     |
| Namespaces                | Dedicated test namespace only   | Never modify arbitrary user namespaces     |
| Test strategy             | Mocked client by default        | No real cluster required for unit tests    |

---

## Architecture — the Kubernetes abstraction layer

The integration follows the spec section 58 shape:

```text
AETHER-GRID logic → Kubernetes abstraction → client-go → Kubernetes API
```

All client-go code lives behind a small interface (`kubernetes.KubernetesClient`
in the agent). Command handlers, the state collector, and the reconciliation
engine depend on the abstraction, never on `client-go` types. This keeps the
integration replaceable (for example a future in-memory or remote stub) and
unit-testable without a cluster (spec §72).

**Alternatives rejected:** direct `client-go` calls from handlers — would couple
the command layer to Kubernetes internals and make testing require a real
cluster.

---

## Access location — the Edge Agent is the integration boundary

The agent owns all contact with the Kubernetes API. The control plane only
consumes what the agent reports and only queries the cluster by dispatching
commands to the agent (spec §52–54). This is the spec §59 "Access location —
Edge Agent" decision.

Consequences:

- `GET /nodes/{id}/kubernetes` returns the **stored, last-reported** Kubernetes
  summary plus the declared desired state.
- `GET /nodes/{id}/kubernetes/nodes` and `/kubernetes/pods` dispatch
  `LIST_KUBERNETES_NODES` / `LIST_KUBERNETES_PODS` and answer `202 Accepted`
  with the `PENDING` command; the caller polls
  `GET /nodes/{id}/commands` for the result. The command completes asynchronously
  on the agent.
- The agent reports Kubernetes state through the existing
  `PUT /nodes/{id}/state` endpoint, extended with an optional `kubernetes`
  object.

**Alternatives rejected:** direct Control Plane → Kubernetes (spec §60) — would
place cluster credentials on the control plane and duplicate the integration
boundary the agent already provides.

---

## Kubernetes client — client-go and kubeconfig loading

- client-go is the only Kubernetes dependency (spec §59).
- Configuration uses kubeconfig (spec §59). Loading order in
  `kubernetes.NewClient`:
  1. Explicit `KUBECONFIG` path when set.
  2. Standard loading rules (`KUBECONFIG` env + `~/.kube/config`).
  3. In-cluster configuration (`rest.InClusterConfig`) for agents running inside
     a cluster.
- A failure to build any configuration is translated to
  `KUBERNETES_INVALID_CONFIGURATION`; the agent keeps running and reports the
  Kubernetes integration as `UNAVAILABLE` (spec §74: a Kubernetes problem must
  never become an AETHER-GRID problem).

**Alternatives rejected:** `kubectl` and Helm (spec §60) — CLI dependency and
output parsing; not applicable this phase.

---

## Error translation

Every Kubernetes API call is wrapped so client-go and transport errors become
structured codes the command layer and control plane can handle without string
parsing:

| Code                              | Origin                                              |
| --------------------------------- | --------------------------------------------------- |
| `KUBERNETES_UNAUTHORIZED`         | client-go `IsUnauthorized`                          |
| `KUBERNETES_FORBIDDEN`            | client-go `IsForbidden`                             |
| `KUBERNETES_TIMEOUT`              | `IsTimeout`/`IsServerTimeout`, context deadline     |
| `KUBERNETES_RESOURCE_NOT_FOUND`   | client-go `IsNotFound`                              |
| `KUBERNETES_UNAVAILABLE`          | transport failures, `IsServiceUnavailable`          |
| `KUBERNETES_INVALID_CONFIGURATION`| configuration problems, validation failures         |

The underlying error text is preserved for logs, and `safeMessage` strips
sensitive material so credentials are never logged or reported (spec §50, §59
"Secrets — never collected", §75 "Credentials are never logged").

---

## Health model — independent of node lifecycle

Kubernetes health is computed from the observed cluster state and is **separate
from the agent's own status** (spec §16–19):

- Configuration disabled → `DISABLED`.
- API unreachable or an error occurs → `UNAVAILABLE`.
- API reachable but some nodes `NotReady` → `DEGRADED`.
- API reachable and all nodes `Ready` → `READY`.

`GetState` never fails: the state collector always produces a state, so a
Kubernetes outage degrades the Kubernetes report without failing the agent's
heartbeat or basic state reporting (spec §74).

---

## Kubernetes desired state and drift

Desired state is extended (spec §20, §22) with a nested `kubernetes` object:

```json
{
  "kubernetes": { "enabled": true, "minimum_ready_nodes": 1 }
}
```

`CompareStates` produces structured differences only when Kubernetes is
desired:

- desired `enabled` while the cluster is unavailable (or never reported) →
  `kubernetes.available` (`desired: true`, `actual: false`);
- cluster available but `ready_nodes < minimum_ready_nodes` →
  `kubernetes.ready_nodes`.

These differences surface as `DRIFT_DETECTED` in the reconciliation result
(spec §22, §33–38, §76). Phase 4 deliberately has **no executable Kubernetes
remediation**: the planner plans no action for Kubernetes drift, and actions
that would modify the cluster (`ENABLE_KUBERNETES` etc.) remain explicitly
unsupported in the executor. AETHER-GRID observes and reports Kubernetes drift;
it does not install, repair, or operate the cluster (spec §59 "Cluster
installation — not yet", §75 "No Operator/CRD functionality").

---

## Actual state — nested `kubernetes` in the state report

The agent's `StateReport` carries the observed Kubernetes summary under
`kubernetes` with the same shape the agent computes:

```json
{
  "status": "READY",
  "kubernetes": {
    "available": true,
    "status": "DEGRADED",
    "version": "v1.31.0",
    "node_count": 2,
    "ready_nodes": 1,
    "not_ready_nodes": 1,
    "workload": { "total_pods": 5, "running_pods": 4, "failed_pods": 1 }
  }
}
```

The control plane stores the observed state on the node row (migration
`004_kubernetes.sql`) and stamps `reported_at`. It never mutates it to satisfy
desired state.

---

## Namespaces and test safety

Pod listing accepts an optional `namespace`; an empty value means all
namespaces (spec §51). Namespace management commands
(`CREATE_TEST_NAMESPACE` / `DELETE_TEST_NAMESPACE`) only target the dedicated
`aether-grid-test` namespace (or an explicitly validated name); names that are
empty, over 63 characters, prefixed with `kube-`, or not valid DNS labels are
rejected. AETHER-GRID never modifies arbitrary user namespaces (spec §51, §73).

---

## Configuration

| Variable                     | Default | Description                            |
| ---------------------------- | ------- | -------------------------------------- |
| `KUBERNETES_ENABLED`         | `false` | Turns the agent's Kubernetes integration on |
| `KUBECONFIG`                 | (auto)  | Explicit kubeconfig path               |
| `KUBERNETES_REQUEST_TIMEOUT` | `10s`   | Per-call timeout for Kubernetes requests |

Every Kubernetes API call runs under `context.WithTimeout`, so a hung API never
blocks the agent (spec §75 "Kubernetes request timeouts work").

---

## Testing

- **Unit tests** use a mocked `KubernetesClient` interface (spec §72) — no real
  cluster required: cluster/node/pod mapping, health calculation, error
  translation, command handlers, collector, config, and agent wiring.
- **HTTP/CP tests** cover the new endpoints, the nested desired/actual payloads,
  and the spec §76 drift scenario through the reconcile API.
- **Integration tests** run only when a development cluster is reachable and
  are gated by `INTEGRATION_KUBERNETES=true`; without a cluster they skip
  cleanly so ordinary unit tests never depend on one (spec §45, §73).

## Verification checklist (spec §75)

- [x] client-go integrated
- [x] Kubernetes client abstraction exists
- [x] kubeconfig loading works (explicit → default rules → in-cluster)
- [x] Kubernetes can be disabled
- [x] Agent works without Kubernetes
- [x] Cluster information can be collected
- [x] Kubernetes node information can be collected
- [x] Basic pod information can be collected
- [x] Kubernetes health can be determined
- [x] Kubernetes state is integrated into actual state
- [x] Kubernetes desired state is represented
- [x] Kubernetes drift can be detected
- [x] Kubernetes commands work where implemented
- [x] Kubernetes errors are translated
- [x] Credentials are never logged
- [x] Kubernetes state does not block basic agent operation
- [x] Kubernetes request timeouts work
- [x] Unit tests pass
- [x] Mock client tests pass
- [x] Integration tests pass when enabled (skipped without a cluster)
- [x] Race detector passes
- [x] gofmt passes
- [x] go vet passes
- [x] README updated
- [x] Architecture decisions documented
- [x] No Operator/CRD functionality has been implemented