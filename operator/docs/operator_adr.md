# Decision Record — Kubernetes Operator (Phase 5)

Status: accepted

Context: AETHER-GRID needs to maintain Kubernetes-native desired state. Phase 4
established an agent that *observes* cluster state and reports it to the
control panel. Phase 5 introduces a separate component that *manages* cluster
resources: an operator that reconciles a declared desired state.

## Architectural relationship to Phase 4

Phase 4 and Phase 5 are related but separate responsibilities:

```
Phase 4 (Edge Agent):               Phase 5 (Operator):
  Agent                                Operator
    │                                    │
    ▼                                    ▼
  Kubernetes API                       Kubernetes API
    │                                    │
    ▼                                    ▼
  Observe state                        Manage resources
                                          │
                                          ▼
                                        Observe state
                                          │
                                          ▼
                                        Reconcile
```

The Edge Agent runs on edge infrastructure, observes machine state, connects to
Kubernetes and reports infrastructure state. The Operator runs inside
Kubernetes, watches Kubernetes resources and reconciles them. They are not one
giant process. This separation will matter for later phases (node
provisioning, networking, cluster lifecycle, autonomous recovery).

## Decisions

| Decision                 | Selected                                     | Reason                                                              |
| ------------------------ | -------------------------------------------- | ------------------------------------------------------------------- |
| Operator framework       | Kubebuilder                                  | Standard Kubernetes controller development workflow                 |
| Controller framework     | controller-runtime                           | Provides reconciliation, watches, caching, manager lifecycle        |
| Kubernetes client        | controller-runtime client / client-go underneath | Native Kubernetes API interaction                               |
| API version              | v1alpha1                                     | Initial experimental API                                            |
| Primary CR               | AetherCluster                                | Represents AETHER-GRID desired Kubernetes state                     |
| Initial managed resource | Deployment                                   | Meaningful reconciliation without excessive complexity              |
| Ownership                | OwnerReferences                              | Native Kubernetes ownership and garbage collection                  |
| Reconciliation           | Event-driven + controlled requeue (5s)       | Efficient and Kubernetes-native                                     |
| State storage            | Kubernetes API                               | Operator remains stateless                                          |
| Local cluster            | kind                                         | Reproducible local Kubernetes environment                           |
| RBAC                     | Least privilege                              | Security                                                            |
| Finalizer                | Not initially required                       | Owner references handle current cleanup                             |
| Terraform                | Not yet                                      | Infrastructure provisioning is a later layer                        |
| WireGuard                | Not yet                                      | Networking is a later layer                                         |
| Cluster provisioning     | Not yet                                      | Operator assumes Kubernetes already exists                          |
| Prometheus               | Not yet                                      | Observability is later                                              |
| Multi-cluster            | Not yet                                      | Keep controller scope focused                                       |

## Reconciliation algorithm

For each `AetherCluster`:

1. Fetch the CR; return quietly on not-found (deletion handled by GC).
2. Validate the spec; invalid specs → `Failed`, no resources created.
3. `buildDeployment(cr)` — a pure, unit-testable construction (no I/O).
4. Fetch the managed Deployment (name/namespace mirror the CR).
5. Create if missing; verify ownership before ever touching an existing
   Deployment (conflicting owners are never modified).
6. Compare only *owned* fields: replicas, container image, container name,
   ports, managed labels, pod labels, selector. Foreign fields
   (resourceVersion, UID, timestamps, managedFields, status, user labels) are
   ignored.
7. Update in place on owned-field drift; recreate only if the immutable
   selector changed (practically unreachable since it derives from the CR
   name).
8. Observe Deployment status; compute phase/conditions deterministically.
9. Write CR status only when changed (idempotency, no self-triggered loop).
10. Requeue after 5s while `Progressing`; watch events drive the rest.

## Failure semantics

```
Failure to reconcile  ≠  Operator process failure
```

On a Deployment API error the operator records the failure, keeps running and
returns the error so controller-runtime retries with backoff. It never exits.
It never reports `Ready` unless the Deployment actually satisfies the desired
conditions: creation and readiness are different states.

## No cross-layer automation yet

Phase 5 ends at Kubernetes resource reconciliation. The operator does not call
into the Edge Agent, Terraform or any VM creation path. No GitOps tooling
(Argo CD/Flux), Helm-based reconciliation, or cloud cluster provisioning.

## Phase 6 outlook

Later phases build on the clean separation established here: node
provisioning, networking (WireGuard), cluster lifecycle and autonomous recovery
operate across the agent/operator boundary established in Phase 5.