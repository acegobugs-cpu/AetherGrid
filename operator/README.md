# AETHER-GRID Operator (Phase 5)

The Kubernetes Operator component of AETHER-GRID. It runs *inside* a
Kubernetes cluster, watches the `AetherCluster` custom resource, and
continuously reconciles a managed `Deployment` so the cluster matches the
declared desired state.

This is deliberately **separate from the Edge Agent** (Phase 4): the agent
observes edge infrastructure and reports state; the operator manages
Kubernetes-native desired state. See `docs/operator_adr.md` for the decisions.

## What it does

- **`AetherCluster` CRD** (`aether-grid.io/v1alpha1`) declares desired state:

  ```yaml
  apiVersion: aether-grid.io/v1alpha1
  kind: AetherCluster
  metadata:
    name: example
  spec:
    replicas: 2
    image: nginx:stable
    port: 80
  ```

  `replicas` defaults to `1` when omitted.

- **Reconciliation loop** for each `AetherCluster`:

  1. Fetch the CR (return quietly if deleted).
  2. Validate the spec; invalid specs become `Failed` without creating anything.
  3. Build the desired `Deployment` (name = CR name) and take ownership via an
     owner reference.
  4. Create the Deployment if missing.
  5. Detect drift against the Deployment — only *owned* fields are compared
     (replicas, image, container name, ports, managed labels, selector).
  6. Update the Deployment in place when owned fields drift; never overwrite
     user labels/annotations, status, `resourceVersion`, `UID`, timestamps or
     `managedFields`.
  7. Observe Deployment readiness and write the CR `status` (phase,
     `readyReplicas`, `desiredReplicas`, `observedGeneration`, conditions) —
     but only when it actually changed, keeping reconciliation idempotent.
  8. Requeue every 5s while progressing; rely on watch events when Ready.

- **Status lifecycle** (deterministic):

  ```
  Pending ──► Progressing ──► Ready
               │  │
               │  └────► Degraded
               └────► Failed
  ```

  The operator never reports `Ready` unless the Deployment actually runs the
  desired replicas (no fake success).

- **Cleanup**: deleting the `AetherCluster` lets Kubernetes garbage collection
  remove the owned Deployment (owner references).

## Architecture

```
┌──────────────────────────┐
│      AetherCluster       │
│       Custom Resource    │
└────────────┬─────────────┘
             │
             ▼
┌──────────────────────────┐
│   AETHER-GRID Operator   │
│                          │
│      Reconcile()         │
└────────────┬─────────────┘
             │
             ▼
┌──────────────────────────┐
│     Kubernetes API       │
└────────────┬─────────────┘
             │
             ▼
┌──────────────────────────┐
│       Deployment         │
└────────────┬─────────────┘
             │
             ▼
           Pods
             │
             └────── actual state
                      │
                      ▼
                 Reconcile
```

The operator is **stateless**: the Kubernetes API is the source of truth, so
restarts are harmless and multiple replicas can safely run behind leader
election (only the leader reconciles).

## Repository layout

```
operator/
  cmd/operator/                 main.go: manager, flags, health probes
  api/v1alpha1/                 AetherCluster API types (+ generated deepcopy)
  internal/controller/          reconciler + pure helpers (build/diff/status)
  config/
    crd/bases/                  generated CRD manifest
    rbac/                       least-privilege RBAC (+ leader-election role)
    manager/                    operator Deployment manifest
    samples/                    example AetherCluster
    default/                    kustomize entry point
  tests/                        gated real-cluster integration test
  Dockerfile                    operator image
  Makefile                      generation/build/test targets
```

## Local installation (kind)

```sh
# 1. Create the cluster.
kind create cluster --name aether-grid

# 2. Build and load the operator image.
docker build -t aether-grid-operator:latest .
kind load docker-image aether-grid-operator:latest --name aether-grid

# 3. Install the CRD, RBAC and operator.
kubectl apply -k config/default

# 4. Wait for the operator pod.
kubectl -n aether-grid-system rollout status deploy/aether-grid-operator

# 5. Apply the sample.
kubectl apply -k config/samples
```

## Drift demonstration

```sh
# Desired = 2. Operator creates the Deployment and reports Ready.
kubectl get aetherclusters            # example   Ready   2   2
kubectl get deployment                # example   2/2

# Manual replica drift: scale out-of-band.
kubectl scale deployment example --replicas=1

# The operator detects Desired=2 vs Actual=1 and corrects it.
kubectl get deployment                # example   2/2

# Image drift: change the Deployment image; the operator restores nginx:stable.
kubectl patch deployment example --type=json \
  -p='[{"op":"replace","path":"/spec/template/spec/containers/0/image","value":"nginx:latest"}]'
kubectl get deployment example -o jsonpath='{.spec.template.spec.containers[0].image}'  # nginx:stable

# Deletion garbage-collects the owned Deployment.
kubectl delete aethercluster example
kubectl get deployment                # (gone)
```

## Failure behavior

If a Deployment cannot be created or updated, the operator records the
situation, keeps running, and returns the error so controller-runtime retries
with backoff. A failed Deployment is reported as `Progressing`/`Degraded`,
never `Ready`, and recovers automatically once the Deployment heals or the
desired state is corrected.

## RBAC (least privilege)

Generated by controller-gen from markers on the reconciler:

- `aetherclusters`: get/list/watch/create/update/patch/delete
- `aetherclusters/status`: get/update/patch
- `deployments`: get/list/watch/create/update/patch/delete
- `deployments/status`: get/update/patch
- `events`: create/patch

Leader election uses a namespaced `coordination.k8s.io` `leases` Role; the
operator runs as a non-root service account.

## Flags

| Flag                      | Default                               | Purpose                            |
| ------------------------- | ------------------------------------- | ---------------------------------- |
| `--metrics-bind-address`  | `:8080`                               | Prometheus metrics endpoint        |
| `--health-probe-bind-address` | `:8081`                            | `/healthz`, `/readyz`              |
| `--leader-elect`          | `false`                               | Enable leader election             |
| `--leader-election-id`    | `aether-grid-operator.aether-grid.io` | Leader election lease ID           |

Health/readiness endpoints use controller-runtime's standard mechanisms; no
custom health server is built.

## Testing

- **Unit tests** cover spec validation, Deployment construction, labels, owner
  references, replica/image calculation, drift detection, status calculation
  and condition generation.
- **Controller tests** use controller-runtime's fake client and cover all spec
  scenarios: CR created, Deployment created/exists, drift detected/repaired
  (replicas and image), becoming ready/degraded, CR deleted, operator restart,
  API temporarily unavailable, and idempotency.
- **Integration test** (`tests/operator_integration_test.go`) drives the full
  Phase 5 demonstration against a real cluster. It is gated:

  ```sh
  INTEGRATION_KUBERNETES=true go test ./tests/ -count=1 -v
  ```

  Without a reachable cluster the test skips cleanly, so ordinary runs never
  require Kubernetes.

```sh
make generate    # regenerate deepcopy + CRD + RBAC (never hand-edit output)
make fmt vet test-race
```

## Known limitations (Phase 5)

- First managed resource is limited to a single `Deployment` per `AetherCluster`.
- No finalizer: owner references handle cleanup. A finalizer becomes necessary
  when a managed resource must survive deletion.
- No conversion webhooks; API stays at `v1alpha1`.
- No Terraform/cloud/VM/cluster-provisioning/networking automation (later phases).
- No custom metrics beyond what controller-runtime exposes.